package lacron

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	// On non-root developer machines, commands naturally run as the developer.
	cfg := Config{DataDir: t.TempDir(), Executable: "/usr/local/bin/lacron", Timezone: "Asia/Shanghai", MaxConcurrent: 4, ScriptPath: os.Getenv("PATH"), RunUID: 10001, RunGID: 10001}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.Close() })
	return s, ctx
}

func testInput() JobInput {
	return JobInput{Name: "test job", Schedule: "* * * * *", Command: "printf hello", WorkingDir: "/", TimeoutSec: 10, Enabled: true}
}

func saveTestJob(t *testing.T, s *Service, in JobInput) Job {
	t.Helper()
	j, err := s.Save("", in)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func waitRun(t *testing.T, s *Service, id string) Run {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, run := range s.Runs("") {
			if run.ID == id && run.Status != "running" {
				return run
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Run{}
}

func TestSchedule(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ expr, after, want string }{
		{"*/5 * * * *", "2026-10-28T09:01:45+08:00", "2026-10-28T09:05:00+08:00"},
		{"0 9 * * 1-5", "2026-10-30T09:00:00+08:00", "2026-11-02T09:00:00+08:00"},
		{"0 0 1 * 1", "2026-10-28T00:00:00+08:00", "2026-11-01T00:00:00+08:00"},
		{"0 0 29 2 *", "2026-10-28T00:00:00+08:00", "2028-02-29T00:00:00+08:00"},
		{"0 0 */2 * *", "2026-10-01T00:00:00+08:00", "2026-10-03T00:00:00+08:00"},
		{"0 0 * 1 *", "2026-01-31T00:01:00+08:00", "2027-01-01T00:00:00+08:00"},
	}
	for _, tc := range cases {
		t.Run(tc.expr+tc.after, func(t *testing.T) {
			schedule, err := ParseSchedule(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := time.Parse(time.RFC3339, tc.after)
			if got := schedule.Next(after.In(loc)).Format(time.RFC3339); got != tc.want {
				t.Fatalf("next=%s, want %s", got, tc.want)
			}
			canonical, err := ParseSchedule(schedule.Canonical())
			if err != nil || canonical.Next(after.In(loc)) != schedule.Next(after.In(loc)) {
				t.Fatalf("canonical changed schedule: %s", schedule.Canonical())
			}
		})
	}
	for _, expr := range []string{"", "* * * * * *", "60 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * 7", "*/0 * * * *", "10-2 * * * *", "*/9999999999 * * * *", "0 0 * * MON", "0 0 * * 1,", "1/2 * * * *", "*;sh * * * *", "+1 * * * *"} {
		if _, err := ParseSchedule(expr); err == nil {
			t.Errorf("accepted invalid expression %q", expr)
		}
	}
	schedule, _ := ParseSchedule("0 0 31 2 *")
	if next := schedule.Next(time.Now()); !next.IsZero() {
		t.Fatal("February 31 must not match")
	}
}

func TestScheduleDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	schedule, _ := ParseSchedule("30 2 * * *")
	after := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	next := schedule.Next(after)
	if next.Day() != 9 || next.Hour() != 2 || next.Minute() != 30 {
		t.Fatalf("nonexistent spring-forward time: %v", next)
	}
	schedule, _ = ParseSchedule("30 1 * * *")
	after, _ = time.Parse(time.RFC3339, "2026-11-01T01:31:00-04:00")
	next = schedule.Next(after.In(loc))
	if next.Format(time.RFC3339) != "2026-11-01T01:30:00-05:00" {
		t.Fatalf("repeated fall-back time: %v", next)
	}
}

func TestPersistenceAndCronIsolation(t *testing.T) {
	s, _ := testService(t)
	in := testInput()
	in.Command = "echo safe\n* * * * * malicious-command"
	in.Schedule = "0 0 */2 * *"
	j := saveTestJob(t, s, in)
	cronPath := filepath.Join(s.cronDir(), "root")
	cron, _ := os.ReadFile(cronPath)
	if strings.Contains(string(cron), "malicious-command") || !strings.Contains(string(cron), "trigger "+j.ID) || !strings.Contains(string(cron), "1,3,5,7") {
		t.Fatalf("unsafe or incorrect crontab: %s", cron)
	}
	stat, _ := os.Stat(cronPath)
	if stat.Mode().Perm() != 0600 {
		t.Fatalf("crontab mode = %v", stat.Mode())
	}
	reopened, err := Open(s.cfg)
	if err != nil || len(reopened.Jobs()) != 1 || reopened.Jobs()[0].Command != in.Command {
		t.Fatalf("persistence: %v", err)
	}
	in.Enabled = false
	if _, err := s.Save(j.ID, in); err != nil {
		t.Fatal(err)
	}
	cron, _ = os.ReadFile(cronPath)
	if strings.Contains(string(cron), j.ID) {
		t.Fatal("paused job remains scheduled")
	}
	if err := s.Delete(j.ID); err != nil || len(s.Jobs()) != 0 {
		t.Fatal(err)
	}
	if err := s.Delete(j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing job: %v", err)
	}
}

func TestFailedSaveRollsBackCron(t *testing.T) {
	s, _ := testService(t)
	j := saveTestJob(t, s, testInput())
	path := filepath.Join(s.cfg.DataDir, "jobs.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	in := testInput()
	in.Enabled = false
	if _, err := s.Save(j.ID, in); err == nil {
		t.Fatal("expected disk error")
	}
	cron, _ := os.ReadFile(filepath.Join(s.cronDir(), "root"))
	if !strings.Contains(string(cron), j.ID) || !s.Jobs()[0].Enabled {
		t.Fatal("memory and cron must roll back together")
	}
}

func TestRunnerOutputCredentialsAndExit(t *testing.T) {
	s, ctx := testService(t)
	t.Setenv("LACRON_TOKEN", "must-not-reach-scripts")
	in := testInput()
	in.Command = `printf 'token=%s\n' "$LACRON_TOKEN"; id -u; printf 'stderr\n' >&2; exit 7`
	j := saveTestJob(t, s, in)
	run, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	run = waitRun(t, s, run.ID)
	data, err := s.Log(run.ID)
	if err != nil || run.Status != "failed" || run.ExitCode == nil || *run.ExitCode != 7 {
		t.Fatalf("run: %+v, %v, %s", run, err, data)
	}
	if !strings.Contains(string(data), "token=\n") || strings.Contains(string(data), "must-not-reach") || !strings.Contains(string(data), "stderr") {
		t.Fatalf("unexpected output: %s", data)
	}
	if os.Geteuid() == 0 && !strings.Contains(string(data), "10001\n") {
		t.Fatalf("script did not drop privileges: %s", data)
	}
}

func TestRunnerNoOverlapAndCancellation(t *testing.T) {
	s, ctx := testService(t)
	in := testInput()
	in.Command = "sleep 30 & wait"
	in.TimeoutSec = 1
	j := saveTestJob(t, s, in)
	run, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, j.ID, "manual"); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlap was allowed: %v", err)
	}
	if err := s.Delete(j.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("deleted running job: %v", err)
	}
	if finished := waitRun(t, s, run.ID); finished.Status != "timeout" || finished.FinishedAt.Sub(run.StartedAt) > 5*time.Second {
		t.Fatalf("timeout did not kill process group: %+v", finished)
	}
	in.Enabled = false
	if _, err := s.Save(j.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, j.ID, "cron"); err == nil {
		t.Fatal("paused job accepted cron trigger")
	}
	manual, cancel := context.WithCancel(ctx)
	run, err = s.Start(manual, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if finished := waitRun(t, s, run.ID); finished.Status != "interrupted" {
		t.Fatalf("shutdown: %+v", finished)
	}
}

func TestRunnerLogLimitAndConcurrency(t *testing.T) {
	s, ctx := testService(t)
	s.cfg.MaxConcurrent = 1
	in := testInput()
	in.Command = "sleep 1; head -c 400000 /dev/zero"
	j := saveTestJob(t, s, in)
	j2 := saveTestJob(t, s, testInput())
	run, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, j2.ID, "manual"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrency limit: %v", err)
	}
	run = waitRun(t, s, run.ID)
	output, _ := s.Log(run.ID)
	if !run.Truncated || len(output) != maxLogBytes || run.Status != "success" {
		t.Fatalf("log cap: status=%s truncated=%t size=%d", run.Status, run.Truncated, len(output))
	}
}

func TestRecoveryAndRetention(t *testing.T) {
	s, ctx := testService(t)
	j := saveTestJob(t, s, testInput())
	for i := 0; i < maxRuns; i++ {
		s.runs = append(s.runs, Run{ID: newID(), JobID: j.ID, Status: "success"})
	}
	old := s.runs[0].ID
	if err := os.WriteFile(filepath.Join(s.cfg.DataDir, "logs", old+".log"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(ctx, j.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, run.ID)
	if len(s.Runs("")) != maxRuns {
		t.Fatal("history not bounded")
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "logs", old+".log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old log not pruned")
	}
	s.runs[0].Status = "running"
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "runs.json"), s.runs); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.cfg)
	if err != nil || reopened.runs[0].Status != "interrupted" {
		t.Fatalf("recover interrupted run: %v", err)
	}
	if _, err := s.Log("../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("log path traversal: %v", err)
	}
}

func TestHTTPAuthOriginCRUDAndHealth(t *testing.T) {
	s, ctx := testService(t)
	token := strings.Repeat("a", 32)
	var alive atomic.Bool
	alive.Store(true)
	s.cfg.CronEnabled = true
	handler := s.handler(ctx, token, &alive, map[string]any{"timezone": "UTC"})
	request := func(method, path, body, auth, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", "Bearer "+auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/jobs", "", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated access: %d", w.Code)
	}
	if w := request("GET", "/", "", "", ""); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("static UI/security headers: %d", w.Code)
	}
	if w := request("POST", "/api/jobs", `{}`, token, "http://evil.test"); w.Code != 403 {
		t.Fatalf("cross-site write: %d", w.Code)
	}
	b, _ := json.Marshal(testInput())
	w := request("POST", "/api/jobs", string(b), token, "http://localhost")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var j Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"unknown":true}`, `{`, strings.Repeat("x", 17000), `{"name":"bad","schedule":"60 * * * *","command":"echo x"}`} {
		if w := request("POST", "/api/jobs", body, token, ""); w.Code != 400 {
			t.Errorf("invalid request: %d %s", w.Code, w.Body)
		}
	}
	if w := request("GET", "/healthz", "", "", ""); w.Code != 200 {
		t.Fatal("healthy cron rejected")
	}
	alive.Store(false)
	if w := request("GET", "/healthz", "", "", ""); w.Code != 503 {
		t.Fatal("dead cron reported healthy")
	}
	if w := request("DELETE", "/api/jobs/"+j.ID, "", token, ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
}
