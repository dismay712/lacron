//go:build linux

package lacron

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const maxLogBytes = 256 << 10

// cappedLog drains both streams even after reaching the disk limit, so a chatty
// process cannot fill the data volume or deadlock waiting for its output pipe.
type cappedLog struct {
	mu        sync.Mutex
	f         *os.File
	written   int
	truncated bool
}

func (w *cappedLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := maxLogBytes - w.written
	if len(p) > remaining {
		p, w.truncated = p[:remaining], true
	}
	if len(p) > 0 {
		written, err := w.f.Write(p)
		w.written += written
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (s *Service) Start(ctx context.Context, id, source string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return Run{}, ErrClosing
	}
	var job Job
	for _, j := range s.jobs {
		if j.ID == id {
			job = j
			break
		}
	}
	if job.ID == "" {
		return Run{}, ErrNotFound
	}
	// Reject a stale cron trigger after a task has been paused or its schedule
	// changed. BusyBox reloads at the next minute boundary, not immediately.
	if source == "cron" {
		schedule, _ := ParseSchedule(job.Schedule)
		now := time.Now().In(s.location)
		if !job.Enabled || !schedule.fields[0][now.Minute()] || !schedule.fields[1][now.Hour()] || !schedule.fields[3][int(now.Month())] || !schedule.matchesDay(now) {
			return Run{}, ErrStale
		}
	}
	if s.active[id] || len(s.active) >= s.cfg.MaxConcurrent {
		return Run{}, ErrBusy
	}
	run := Run{ID: newID(), JobID: job.ID, JobName: job.Name, Source: source, Status: "running", StartedAt: time.Now().UTC()}
	f, err := os.OpenFile(filepath.Join(s.cfg.DataDir, "logs", run.ID+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Run{}, err
	}
	next := append(append([]Run{}, s.runs...), run)
	// Never prune an active run: its completion still needs its history entry.
	var removed []Run
	for len(next) > maxRuns {
		found := false
		for i, old := range next {
			if old.Status != "running" {
				removed = append(removed, old)
				next = append(next[:i], next[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			f.Close()
			os.Remove(f.Name())
			return Run{}, ErrBusy
		}
	}
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "runs.json"), next); err != nil {
		f.Close()
		os.Remove(f.Name())
		return Run{}, err
	}
	s.runs, s.active[id] = next, true
	for _, old := range removed {
		os.Remove(filepath.Join(s.cfg.DataDir, "logs", old.ID+".log"))
	}
	s.wg.Add(1)
	go s.execute(ctx, job, run, f)
	return run, nil
}

func (s *Service) execute(parent context.Context, job Job, run Run, f *os.File) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(parent, time.Duration(job.TimeoutSec)*time.Second)
	defer cancel()
	output := &cappedLog{f: f}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", job.Command)
	cmd.Dir = job.WorkingDir
	// Do not pass the admin token, cloud credentials, or server configuration to
	// scripts. Operators can explicitly source their own mounted secrets.
	cmd.Env = []string{"PATH=" + s.cfg.ScriptPath, "HOME=/tmp", "USER=lacron", "LOGNAME=lacron", "LANG=C.UTF-8", "TZ=" + s.cfg.Timezone}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if os.Geteuid() == 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: s.cfg.RunUID, Gid: s.cfg.RunGID, Groups: []uint32{}}
	}
	// Kill the process group, including Node/shell children, on timeout/shutdown.
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	// Also reap descendants left behind by commands that background a child.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	now, code := time.Now().UTC(), 0
	run.FinishedAt, run.ExitCode = &now, &code
	run.Status = "success"
	if err != nil {
		code, run.Status = -1, "failed"
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		fmt.Fprintf(output, "\n[lacron] %v\n", err)
	}
	if ctx.Err() != nil {
		if parent.Err() != nil {
			run.Status = "interrupted"
		} else {
			run.Status = "timeout"
		}
	}
	run.Truncated = output.truncated
	if run.Truncated {
		// The UI shows the truncation marker separately from the bounded log.
		log.Printf("run %s: output truncated at %d bytes", run.ID, maxLogBytes)
	}
	if err := f.Close(); err != nil {
		log.Printf("run %s close log: %v", run.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, job.ID)
	for i := range s.runs {
		if s.runs[i].ID == run.ID {
			s.runs[i] = run
			break
		}
	}
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "runs.json"), s.runs); err != nil {
		log.Printf("persist run %s: %v", run.ID, err)
	}
	log.Printf("run %s job=%s source=%s status=%s exit=%d", run.ID, job.ID, run.Source, run.Status, code)
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.wg.Wait()
}
