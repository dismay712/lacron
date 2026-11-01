package lacron

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func valuePtr(s string) *string { return &s }

func TestVariablesGrantsPersistenceAndIsolation(t *testing.T) {
	s, _ := testService(t)
	a := saveTestJob(t, s, testInput())
	b := saveTestJob(t, s, testInput())
	if a.RunSlot == b.RunSlot {
		t.Fatal("tasks share an execution slot")
	}
	av, err := s.SaveVariable("", VariableInput{Name: "CHECKIN_COOKIE", Value: valuePtr("cookie-a-private"), JobIDs: []string{a.ID}})
	if err != nil {
		t.Fatal(err)
	}
	bv, err := s.SaveVariable("", VariableInput{Name: "CHECKIN_COOKIE", Value: valuePtr("cookie-b-private"), JobIDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	aenv, benv := s.jobEnvironment(a), s.jobEnvironment(b)
	if !slices.Contains(aenv, "CHECKIN_COOKIE=cookie-a-private") || slices.Contains(aenv, "CHECKIN_COOKIE=cookie-b-private") || !slices.Contains(benv, "CHECKIN_COOKIE=cookie-b-private") {
		t.Fatal("cross-task environment leak")
	}
	encoded, _ := json.Marshal(s.Variables())
	if strings.Contains(string(encoded), "cookie-a-private") || strings.Contains(string(encoded), `"value":`) {
		t.Fatal("secret exposed in variable view")
	}
	stat, err := os.Stat(filepath.Join(s.cfg.DataDir, "variables.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("secret file permissions: %v %v", stat, err)
	}
	reopened, err := Open(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(reopened.jobEnvironment(reopened.jobs[0]), "CHECKIN_COOKIE=cookie-a-private") || reopened.jobs[1].RunSlot != b.RunSlot {
		t.Fatal("grants, values or identities not persisted")
	}
	// Omitted value preserves the secret. Revocation changes subsequent snapshots.
	if _, err := s.SaveVariable(av.ID, VariableInput{Name: av.Name, Description: "updated", JobIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.jobEnvironment(a), "CHECKIN_COOKIE=cookie-a-private") || !slices.Contains(aenv, "CHECKIN_COOKIE=cookie-a-private") {
		t.Fatal("revocation did not preserve launch snapshot semantics")
	}
	if s.variables[0].Value != "cookie-a-private" {
		t.Fatal("metadata-only update erased value")
	}
	if err := s.DeleteVariable(bv.ID); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(s.jobEnvironment(b), "CHECKIN_COOKIE=cookie-b-private") {
		t.Fatal("deleted variable remains injected")
	}
}

func TestVariableValidationAndRollback(t *testing.T) {
	s, _ := testService(t)
	j := saveTestJob(t, s, testInput())
	for _, name := range []string{"", "1COOKIE", "bad-name", "X=Y", "PATH", "HOME", "TZ", "LACRON_TOKEN"} {
		if _, err := s.SaveVariable("", VariableInput{Name: name, Value: valuePtr("x")}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted reserved/invalid name %q: %v", name, err)
		}
	}
	for _, v := range []string{"a\x00b", strings.Repeat("x", maxVariableValue+1)} {
		if _, err := s.SaveVariable("", VariableInput{Name: "TEST", Value: &v}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid value accepted")
		}
	}
	if _, err := s.SaveVariable("", VariableInput{Name: "TEST", Value: valuePtr("x"), JobIDs: []string{newID()}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown task accepted")
	}
	v, err := s.SaveVariable("", VariableInput{Name: "TOKEN", Value: valuePtr("original"), JobIDs: []string{j.ID, j.ID}})
	if err != nil || len(v.JobIDs) != 1 {
		t.Fatalf("dedup grants: %v", err)
	}
	if _, err := s.SaveVariable("", VariableInput{Name: "TOKEN", Value: valuePtr("different"), JobIDs: []string{j.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambiguous same-name grants accepted")
	}
	path := filepath.Join(s.cfg.DataDir, "variables.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveVariable(v.ID, VariableInput{Name: "TOKEN", Value: valuePtr("changed")}); err == nil {
		t.Fatal("disk failure ignored")
	}
	if s.variables[0].Value != "original" || !slices.Contains(s.jobEnvironment(j), "TOKEN=original") {
		t.Fatal("failed save changed memory or grants")
	}
	if err := s.DeleteVariable(v.ID); err == nil || len(s.variables) != 1 {
		t.Fatal("failed delete changed memory")
	}
}

func TestVariableSizeAndDeletedTaskGrants(t *testing.T) {
	s, _ := testService(t)
	j := saveTestJob(t, s, testInput())
	for _, name := range []string{"A", "B", "C"} {
		if _, err := s.SaveVariable("", VariableInput{Name: name, Value: valuePtr(strings.Repeat("v", maxVariableValue)), JobIDs: []string{j.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveVariable("", VariableInput{Name: "D", Value: valuePtr(strings.Repeat("v", maxVariableValue)), JobIDs: []string{j.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized combined environment accepted")
	}
	if err := s.Delete(j.ID); err != nil {
		t.Fatal(err)
	}
	for _, v := range s.Variables() {
		if len(v.JobIDs) != 0 {
			t.Fatal("deleted task remains in grants view")
		}
	}
	replacement := saveTestJob(t, s, testInput())
	if replacement.RunSlot == j.RunSlot {
		t.Fatal("deleted task's UID reused in the same container lifetime")
	}
	if slices.Contains(s.jobEnvironment(replacement), "A="+strings.Repeat("v", maxVariableValue)) {
		t.Fatal("new task inherited deleted task grants")
	}
}

func TestJobIdentityMigration(t *testing.T) {
	s, _ := testService(t)
	a, b := saveTestJob(t, s, testInput()), saveTestJob(t, s, testInput())
	a.RunSlot, b.RunSlot = 0, 0
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "jobs.json"), []Job{a, b}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	uidA, _ := reopened.jobIdentity(reopened.jobs[0])
	uidB, _ := reopened.jobIdentity(reopened.jobs[1])
	if uidA != 10001 || uidB != 10002 {
		t.Fatalf("migration identities %d %d", uidA, uidB)
	}
	saved := reopened.jobs[1].RunSlot
	if _, err := reopened.Save(b.ID, testInput()); err != nil {
		t.Fatal(err)
	}
	if reopened.jobs[1].RunSlot != saved {
		t.Fatal("edit changed execution identity")
	}
}

func TestVariableHTTPAuthNoSecretEcho(t *testing.T) {
	s, ctx := testService(t)
	j := saveTestJob(t, s, testInput())
	var alive atomic.Bool
	token := strings.Repeat("a", 32)
	handler := s.handler(ctx, token, &alive, nil)
	request := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/variables", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated variable access")
	}
	payload := `{"name":"CHECKIN_COOKIE","value":"never-echo-this-cookie","job_ids":["` + j.ID + `"]}`
	w := request("POST", "/api/variables", payload, token)
	if os.Geteuid() != 0 {
		if w.Code != 403 {
			t.Fatal("non-root management must refuse secrets without identity isolation")
		}
		return
	}
	if w.Code != 201 || strings.Contains(w.Body.String(), "never-echo-this-cookie") {
		t.Fatalf("create status/secret echo: %d %s", w.Code, w.Body)
	}
	var view VariableView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/variables", "", token); w.Code != 200 || strings.Contains(w.Body.String(), "never-echo-this-cookie") {
		t.Fatal("GET echoes secret")
	}
	if w := request("DELETE", "/api/variables/"+view.ID, "", token); w.Code != 204 {
		t.Fatal("delete failed")
	}
}
