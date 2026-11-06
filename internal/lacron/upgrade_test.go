package lacron

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncryptedLegacyVariablesAndRotation(t *testing.T) {
	cfg := testConfig(t.TempDir())
	now := time.Now().UTC()
	job := Job{ID: newID(), Name: "legacy", Schedule: "*/10 * * * * *", Command: "echo legacy", WorkingDir: "/", TimeoutSec: 30, Enabled: true, RunSlot: 5}
	run := Run{ID: newID(), JobID: job.ID, JobName: job.Name, Status: "success", StartedAt: now, FinishedAt: &now}
	secret := "legacy-cookie-plaintext-never-in-sqlite"
	variable := Variable{ID: newID(), Name: "CHECKIN_COOKIE", Value: secret, JobIDs: []string{job.ID}}
	for path, value := range map[string]any{"jobs.json": []Job{job}, "runs.json": []Run{run}, "variables.json": []Variable{variable}, ".lacron-12345": []Variable{variable}} {
		if err := writeJSON(filepath.Join(cfg.DataDir, path), value); err != nil {
			t.Fatal(err)
		}
	}
	missing := cfg
	missing.EncryptionKey = nil
	if service, err := Open(missing); err == nil {
		service.Close()
		t.Fatal("legacy variables accepted without a key")
	}
	legacyPath := filepath.Join(cfg.DataDir, "variables.json")
	if data, err := os.ReadFile(legacyPath); err != nil || !bytes.Contains(data, []byte(secret)) {
		t.Fatal("failed migration altered the source")
	}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if !slices.Contains(s.jobEnvironment(job), variable.Name+"="+secret) || s.Jobs()[0].RunSlot != job.RunSlot || s.Runs("")[0].ID != run.ID {
		t.Fatal("migration changed task, run, or grant data")
	}
	copy, err := s.CopyJob(job.ID, "")
	if err != nil || copy.Enabled || copy.RunSlot == job.RunSlot || copy.Command != job.Command || copy.Schedule != job.Schedule {
		t.Fatalf("copy did not preserve configuration with an independent identity: %+v %v", copy, err)
	}
	if slices.Contains(s.jobEnvironment(copy), variable.Name+"="+secret) {
		t.Fatal("copied task inherited the grant")
	}
	for _, path := range []string{legacyPath, filepath.Join(cfg.DataDir, ".lacron-12345")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy plaintext remains: %v", err)
		}
	}
	for _, name := range []string{"lacron.sqlite3", "lacron.sqlite3-wal"} {
		b, err := os.ReadFile(filepath.Join(cfg.DataDir, name))
		if err != nil || bytes.Contains(b, []byte(secret)) {
			t.Fatalf("SQLite storage check for %s: %v", name, err)
		}
	}
	s.Close()

	next := cfg
	next.EncryptionKey = []byte(strings.Repeat("b", 32))
	if service, err := Open(next); err == nil {
		service.Close()
		t.Fatal("wrong key accepted")
	}
	next.PreviousEncryptionKey = cfg.EncryptionKey
	rotated, err := Open(next)
	if err != nil {
		t.Fatal(err)
	}
	rotated.Close()
	next.PreviousEncryptionKey = nil
	reopened, err := Open(next)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Jobs()) != 2 || !slices.Contains(reopened.jobEnvironment(job), variable.Name+"="+secret) {
		t.Fatal("rotation or repeat migration changed data")
	}
}

func TestStopCleanupAndIncrementalHistory(t *testing.T) {
	s, ctx := testService(t)
	settings := s.Settings()
	settings.MaxConcurrent, settings.MaxRuns, settings.MaxLogKiB = 1, 3, 2
	if _, err := s.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	in := testInput()
	in.Command = "printf 'ab签Z'; sleep 30 & wait"
	j := saveTestJob(t, s, in)
	run, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	var chunk LogChunk
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		chunk, err = s.ReadLog(run.ID, 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		if chunk.More {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if chunk.Output != "ab" || chunk.NextOffset != 2 || !chunk.More {
		t.Fatalf("first chunk: %+v", chunk)
	}
	chunk, err = s.ReadLog(run.ID, chunk.NextOffset, 4)
	if err != nil || chunk.Output != "签Z" || chunk.NextOffset != 6 {
		t.Fatalf("second chunk: %+v %v", chunk, err)
	}
	cleanup := CleanupRequest{All: true, Mode: "output"}
	preview, err := s.CleanupLogs(cleanup, true)
	if err != nil || preview.Matched != 0 || preview.Skipped != 1 {
		t.Fatalf("preview active run: %+v %v", preview, err)
	}
	if err := s.StopRun(run.ID); err != nil {
		t.Fatal(err)
	}
	finished := waitRun(t, s, run.ID)
	if finished.Status != "cancelled" || finished.FinishedAt.Sub(run.StartedAt) > 6*time.Second {
		t.Fatalf("stop did not terminate process group: %+v", finished)
	}
	cleanup.Cutoff = preview.Cutoff
	if result, err := s.CleanupLogs(cleanup, false); err != nil || result.Matched != 0 || result.Skipped != 1 {
		t.Fatalf("cleanup acted on a run that was active at preview: %+v %v", result, err)
	}
	preview, err = s.CleanupLogs(cleanup, true)
	if err != nil || preview.Matched != 1 {
		t.Fatalf("preview finished run: %+v %v", preview, err)
	}
	cleanup.Cutoff = preview.Cutoff
	if result, err := s.CleanupLogs(cleanup, false); err != nil || result.Matched != 1 || result.Pending != 0 {
		t.Fatalf("cleanup output: %+v %v", result, err)
	}
	chunk, err = s.ReadLog(run.ID, 6, 4)
	if err != nil || chunk.Output != "" || !chunk.Reset || !chunk.Run.LogCleared {
		t.Fatalf("cleanup did not reset the log cursor: %+v %v", chunk, err)
	}
	if _, err := os.Stat(s.logPath(run.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output file remains: %v", err)
	}
	if _, err := s.Save(j.ID, testInput()); err != nil {
		t.Fatal(err)
	}
	newer, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, newer.ID)
	if err := s.StopRun(run.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("stale stop request: %v", err)
	}
	first, err := s.RunsPage(j.ID, "", "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != newer.ID || first.NextCursor != newer.ID {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := s.RunsPage(j.ID, "cancelled", first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != run.ID || second.NextCursor != "" {
		t.Fatalf("filtered page: %+v %v", second, err)
	}
	cleanup = CleanupRequest{RunID: newer.ID, Mode: "history"}
	preview, err = s.CleanupLogs(cleanup, true)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.Cutoff = preview.Cutoff
	if result, err := s.CleanupLogs(cleanup, false); err != nil || result.Matched != 1 {
		t.Fatalf("cleanup record: %+v %v", result, err)
	}
	s.Close()
	reopened, err := Open(testConfig(s.cfg.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runs := reopened.Runs("")
	if reopened.Settings() != settings || len(runs) != 1 || runs[0].ID != run.ID || !runs[0].LogCleared {
		t.Fatal("settings or cleanup did not persist across restart")
	}
}

func TestBrowserAuthCookieAndOneTimeChallenge(t *testing.T) {
	s, ctx := testService(t)
	settings := s.Settings()
	settings.PasskeyEnabled, settings.PasskeyOrigin = true, "http://localhost:8080"
	if _, err := s.UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	handler := s.handler(ctx, token, &atomic.Bool{}, nil)
	origin := settings.PasskeyOrigin
	var cookie *http.Cookie
	request := func(method, path, body, from string, bearer bool, session *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		if from != "" {
			r.Header.Set("Origin", from)
		}
		if bearer {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if session != nil {
			r.AddCookie(session)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("POST", "/api/auth/token", "", "https://evil.example", true, nil); w.Code != 403 {
		t.Fatal("cross-origin token exchange allowed")
	}
	w := request("POST", "/api/auth/token", "", origin, true, nil)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("token exchange: %d %s", w.Code, w.Body)
	}
	cookie = w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Value == token {
		t.Fatalf("browser cookie attributes: %+v", cookie)
	}
	if w := request("GET", "/api/settings", "", "", false, cookie); w.Code != 200 {
		t.Fatal("session cookie rejected")
	}
	if w := request("PUT", "/api/settings", "{}", "", false, cookie); w.Code != 403 {
		t.Fatal("cookie mutation did not require an origin")
	}
	if w := request("POST", "/api/passkeys/begin", `{"name":"Device"}`, "http://localhost:8081", true, nil); w.Code != 403 {
		t.Fatal("Passkey enrollment allowed a different origin")
	}
	w = request("POST", "/api/passkeys/begin", `{"name":"Device"}`, origin, false, cookie)
	var challenge struct {
		ID string `json:"challenge_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); w.Code != 200 || err != nil || challenge.ID == "" {
		t.Fatalf("registration begin: %d %s", w.Code, w.Body)
	}
	payload := `{"challenge_id":"` + challenge.ID + `","credential":{}}`
	if w := request("POST", "/api/passkeys/finish", payload, origin, true, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "已失效") {
		t.Fatal("other auth identity could access the pending ceremony")
	}
	w = request("POST", "/api/passkeys/finish", payload, origin, false, cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "注册响应无效") {
		t.Fatalf("unverified credential accepted: %d %s", w.Code, w.Body)
	}
	w = request("POST", "/api/passkeys/finish", payload, origin, false, cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "已失效") {
		t.Fatal("challenge replay accepted")
	}
	if w := request("POST", "/api/auth/logout", "", origin, false, cookie); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w := request("GET", "/api/settings", "", "", false, cookie); w.Code != 401 {
		t.Fatal("logged-out cookie remained usable")
	}
}
