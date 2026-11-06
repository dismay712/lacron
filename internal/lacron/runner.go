//go:build linux

package lacron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	limit     int
	written   int
	truncated bool
}

func (w *cappedLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := w.limit - w.written
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

var (
	errStopped  = errors.New("run stopped by administrator")
	errShutdown = errors.New("service shutdown")
	errTimeout  = errors.New("run timed out")
)

type activeRun struct {
	id     string
	cancel context.CancelCauseFunc
}

func (s *Service) Start(ctx context.Context, id, source string) (Run, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, job := range s.jobs {
		if job.ID == id {
			return s.startLocked(ctx, job, source, time.Now().In(s.location))
		}
	}
	return Run{}, ErrNotFound
}

func (s *Service) StopRun(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing {
		return ErrClosing
	}
	index, ok := s.runIndex[id]
	if !ok {
		return ErrNotFound
	}
	active, ok := s.active[s.runs[index].JobID]
	if !ok || active.id != id {
		return fmt.Errorf("%w：此执行已结束", ErrStale)
	}
	active.cancel(errStopped)
	return nil
}

// startLocked serializes admission, edits and persisted run transitions without
// blocking snapshots during a database write.
func (s *Service) startLocked(ctx context.Context, job Job, source string, now time.Time) (Run, error) {
	if s.closing || ctx.Err() != nil {
		return Run{}, ErrClosing
	}
	id := job.ID
	if source == "cron" {
		last, exists := s.lastScheduled[id]
		if !job.Enabled || !s.schedules[id].Matches(now) || (exists && last >= now.Unix()) {
			return Run{}, ErrStale
		}
	}
	if _, running := s.active[id]; running || len(s.active) >= s.settings.MaxConcurrent {
		return Run{}, ErrBusy
	}
	if len(s.variables) > 0 && os.Geteuid() != 0 {
		return Run{}, fmt.Errorf("%w：已配置环境变量，需要以容器内 root 运行服务，以隔离不同任务的运行身份", ErrInvalid)
	}
	environment := s.jobEnvironment(job)
	run := Run{ID: newID(), JobID: job.ID, JobName: job.Name, Source: source, Status: "running", StartedAt: time.Now().UTC()}
	f, err := os.OpenFile(s.logPath(run.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Run{}, err
	}
	// Appending only touches an element outside the readers' published slice.
	next, removed := trimHistory(append(s.runs, run), s.settings.MaxRuns)
	if err := s.persistRun(run, removed); err != nil {
		f.Close()
		os.Remove(f.Name())
		return Run{}, err
	}
	runContext, stop := context.WithCancelCause(ctx)
	s.mu.Lock()
	s.active[id] = activeRun{id: run.ID, cancel: stop}
	if source == "cron" {
		s.lastScheduled[id] = now.Unix()
	}
	if len(removed) == 0 {
		s.runIndex[run.ID] = len(s.runs)
		s.lastRunByJob[id] = run
		s.runs = next
		s.changes.Runs++
		s.changes.Jobs++
	} else {
		s.publishRuns(next)
	}
	s.mu.Unlock()
	s.removeLogs(removed)
	s.wg.Add(1)
	go s.execute(runContext, stop, job, run, f, environment, s.settings.MaxLogKiB<<10)
	return run, nil
}

// dispatchDue takes the writer lock so a deleted or edited job cannot race
// with admission. Missed ticks are skipped as before.
func (s *Service) dispatchDue(ctx context.Context, now time.Time) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now = now.In(s.location)
	for _, job := range s.jobs {
		if !job.Enabled || !s.schedules[job.ID].Matches(now) {
			continue
		}
		if _, err := s.startLocked(ctx, job, "cron", now); err != nil &&
			!errors.Is(err, ErrBusy) && !errors.Is(err, ErrStale) && !errors.Is(err, ErrClosing) {
			log.Printf("schedule job=%s: %v", job.ID, err)
		}
	}
}

func (s *Service) schedule(ctx context.Context) {
	// Re-align every tick so scheduler work and clock adjustments do not build
	// up drift. Read time.Now after waking instead of replaying an old timer time.
	for {
		now := time.Now()
		timer := time.NewTimer(time.Until(now.Truncate(time.Second).Add(time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.dispatchDue(ctx, time.Now())
		}
	}
}

func (s *Service) execute(parent context.Context, stop context.CancelCauseFunc, job Job, run Run, f *os.File, environment []string, logLimit int) {
	defer s.wg.Done()
	defer stop(nil)
	ctx, cancel := context.WithTimeoutCause(parent, time.Duration(job.TimeoutSec)*time.Second, errTimeout)
	defer cancel()
	output := &cappedLog{f: f, limit: logLimit}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", job.Command)
	cmd.Dir = job.WorkingDir
	// Do not pass the admin token, cloud credentials, or server configuration to
	// scripts. Operators can explicitly source their own mounted secrets.
	cmd.Env = environment
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if os.Geteuid() == 0 {
		uid, gid := s.jobIdentity(job)
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}
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
	cause := context.Cause(ctx)
	cancel()
	// Also reap descendants left behind by commands that background a child.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	code := 0
	run.ExitCode = &code
	run.Status = "success"
	if err != nil {
		code, run.Status = -1, "failed"
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		fmt.Fprintf(output, "\n[lacron] %v\n", err)
	}
	run.Truncated = output.truncated
	if run.Truncated {
		// The UI shows the truncation marker separately from the bounded log.
		log.Printf("run %s: output truncated at %d bytes", run.ID, logLimit)
	}
	if err := f.Close(); err != nil {
		log.Printf("run %s close log: %v", run.ID, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if cause == nil && context.Cause(parent) == errStopped {
		cause = errStopped
	}
	switch cause {
	case errStopped:
		run.Status = "cancelled"
	case errTimeout, context.DeadlineExceeded:
		run.Status = "timeout"
	case nil:
	default:
		run.Status = "interrupted"
	}
	now := time.Now().UTC()
	run.FinishedAt = &now
	index := s.runIndex[run.ID]
	var retained, removed []Run
	if len(s.runs) > s.settings.MaxRuns {
		next := append([]Run{}, s.runs...)
		next[index] = run
		retained, removed = trimHistory(next, s.settings.MaxRuns)
	}
	if err := s.persistRun(run, removed); err != nil {
		// The process has exited. Release its admission slot and keep the log
		// even if durable completion fails. Startup repairs the old running row.
		log.Printf("persist run %s: %v", run.ID, err)
		removed = nil
	}
	s.mu.Lock()
	delete(s.active, job.ID)
	if len(removed) == 0 {
		s.runs[index] = run
		s.lastRunByJob[job.ID] = run
		s.completedRuns++
		if run.Status == "success" {
			s.successfulRuns++
		}
		s.changes.Runs++
		s.changes.Jobs++
	} else {
		s.publishRuns(retained)
	}
	s.mu.Unlock()
	s.removeLogs(removed)
	log.Printf("run %s job=%s source=%s status=%s exit=%d", run.ID, job.ID, run.Source, run.Status, code)
}

func (s *Service) persistRun(run Run, removed []Run) error {
	if len(removed) == 0 {
		return writeRow(s.db, saveRunRow, run.ID, run)
	}
	return s.transaction(func(tx *sql.Tx) error {
		if err := writeRow(tx, saveRunRow, run.ID, run); err != nil {
			return err
		}
		return deleteRunRows(tx, removed)
	})
}

func (s *Service) Close() {
	s.closeOnce.Do(func() {
		s.writeMu.Lock()
		s.closing = true
		for _, active := range s.active {
			active.cancel(errShutdown)
		}
		s.writeMu.Unlock()
		s.wg.Wait()
		if err := s.db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	})
}
