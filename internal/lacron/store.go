package lacron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxJobs = 500
const maxRuns = 200

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

var (
	ErrNotFound = errors.New("任务不存在")
	ErrBusy     = errors.New("任务正在执行，或已达到并发上限")
	ErrClosing  = errors.New("服务正在关闭")
	ErrInvalid  = errors.New("任务配置无效")
	ErrStale    = errors.New("已忽略过期的 cron 触发")
)

type Job struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Schedule   string    `json:"schedule"`
	Command    string    `json:"command"`
	WorkingDir string    `json:"working_dir"`
	TimeoutSec int       `json:"timeout_sec"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type JobInput struct {
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir"`
	TimeoutSec int    `json:"timeout_sec"`
	Enabled    bool   `json:"enabled"`
}

type JobView struct {
	Job
	Running bool       `json:"running"`
	NextRun *time.Time `json:"next_run"`
	LastRun *Run       `json:"last_run,omitempty"`
}

type Run struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	JobName    string     `json:"job_name"`
	Source     string     `json:"source"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Truncated  bool       `json:"truncated"`
}

type Service struct {
	mu       sync.Mutex
	cfg      Config
	jobs     []Job
	runs     []Run
	active   map[string]bool
	closing  bool
	wg       sync.WaitGroup
	location *time.Location
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func Open(cfg Config) (*Service, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("TZ: %w", err)
	}
	s := &Service{cfg: cfg, jobs: []Job{}, runs: []Run{}, active: map[string]bool{}, location: loc}
	for _, dir := range []string{cfg.DataDir, filepath.Join(cfg.DataDir, "logs"), s.cronDir()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, fmt.Errorf("保护数据目录 %s: %w", dir, err)
		}
	}
	if err := readJSON(filepath.Join(cfg.DataDir, "jobs.json"), &s.jobs); err != nil {
		return nil, err
	}
	if s.jobs == nil {
		s.jobs = []Job{}
	}
	if len(s.jobs) > maxJobs {
		return nil, fmt.Errorf("最多支持 %d 个任务", maxJobs)
	}
	seen := map[string]bool{}
	for _, j := range s.jobs {
		if !idPattern.MatchString(j.ID) || seen[j.ID] {
			return nil, fmt.Errorf("jobs.json 包含重复或无效 ID")
		}
		seen[j.ID] = true
		if _, err := validateJob(JobInput{j.Name, j.Schedule, j.Command, j.WorkingDir, j.TimeoutSec, j.Enabled}); err != nil {
			return nil, fmt.Errorf("jobs.json: %w", err)
		}
	}
	if err := readJSON(filepath.Join(cfg.DataDir, "runs.json"), &s.runs); err != nil {
		return nil, err
	}
	if s.runs == nil {
		s.runs = []Run{}
	}
	if len(s.runs) > maxRuns {
		return nil, fmt.Errorf("runs.json 超过保留上限")
	}
	for i := range s.runs {
		if !idPattern.MatchString(s.runs[i].ID) || !idPattern.MatchString(s.runs[i].JobID) {
			return nil, fmt.Errorf("runs.json 包含无效 ID")
		}
		if s.runs[i].Status == "running" {
			now := time.Now().UTC()
			s.runs[i].Status, s.runs[i].FinishedAt = "interrupted", &now
		}
	}
	if err := writeJSON(filepath.Join(cfg.DataDir, "runs.json"), s.runs); err != nil {
		return nil, err
	}
	if err := s.syncCron(s.jobs); err != nil {
		return nil, err
	}
	return s, nil
}

func readJSON(path string, target any) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%s 包含多余数据", path)
	}
	return nil
}

func atomicWrite(path string, content []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".lacron-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'))
}

func validateJob(in JobInput) (JobInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Command = strings.TrimSpace(in.Command)
	in.Schedule = strings.Join(strings.Fields(in.Schedule), " ")
	if in.Name == "" || len(in.Name) > 120 {
		return in, fmt.Errorf("任务名称不能为空，且不能超过 120 字节")
	}
	if in.Command == "" || len(in.Command) > 8192 || strings.ContainsRune(in.Command, 0) {
		return in, fmt.Errorf("命令不能为空，且不能超过 8192 字节或包含 NUL")
	}
	if _, err := ParseSchedule(in.Schedule); err != nil {
		return in, err
	}
	if in.WorkingDir == "" {
		in.WorkingDir = "/scripts"
	}
	if !filepath.IsAbs(in.WorkingDir) || len(in.WorkingDir) > 1024 || strings.ContainsRune(in.WorkingDir, 0) {
		return in, fmt.Errorf("工作目录必须为绝对路径")
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = 300
	}
	if in.TimeoutSec < 1 || in.TimeoutSec > 86400 {
		return in, fmt.Errorf("超时时间须在 1–86400 秒之间")
	}
	return in, nil
}

func (s *Service) cronDir() string { return filepath.Join(s.cfg.DataDir, "crontabs") }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func (s *Service) syncCron(jobs []Job) error {
	var b strings.Builder
	b.WriteString("# Generated by lacron. Edit jobs through the WebUI/API.\nSHELL=/bin/sh\nPATH=/usr/local/bin:/usr/bin:/bin\nMAILTO=\n")
	for _, j := range jobs {
		if j.Enabled {
			schedule, err := ParseSchedule(j.Schedule)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "%s %s trigger %s\n", schedule.Canonical(), shellQuote(s.cfg.Executable), j.ID)
		}
	}
	if err := atomicWrite(filepath.Join(s.cronDir(), "root"), []byte(b.String())); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.cronDir(), "cron.update"), []byte("root\n"))
}

func (s *Service) commitJobs(next []Job) error {
	if err := s.syncCron(next); err != nil {
		// syncCron can fail after replacing root but before notifying crond.
		return errors.Join(err, s.syncCron(s.jobs))
	}
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "jobs.json"), next); err != nil {
		return errors.Join(err, s.syncCron(s.jobs))
	}
	s.jobs = next
	return nil
}

func (s *Service) Save(id string, input JobInput) (Job, error) {
	in, err := validateJob(input)
	if err != nil {
		return Job{}, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return Job{}, ErrClosing
	}
	now := time.Now().UTC()
	next := append([]Job{}, s.jobs...)
	j := Job{ID: id, Name: in.Name, Schedule: in.Schedule, Command: in.Command, WorkingDir: in.WorkingDir, TimeoutSec: in.TimeoutSec, Enabled: in.Enabled, CreatedAt: now, UpdatedAt: now}
	if id == "" {
		if len(next) >= maxJobs {
			return Job{}, fmt.Errorf("%w：最多支持 %d 个任务", ErrInvalid, maxJobs)
		}
		j.ID = newID()
		next = append(next, j)
	} else {
		found := false
		for i := range next {
			if next[i].ID == id {
				j.CreatedAt = next[i].CreatedAt
				next[i], found = j, true
				break
			}
		}
		if !found {
			return Job{}, ErrNotFound
		}
	}
	if err := s.commitJobs(next); err != nil {
		return Job{}, fmt.Errorf("保存任务: %w", err)
	}
	return j, nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ErrClosing
	}
	if s.active[id] {
		return ErrBusy
	}
	for i, j := range s.jobs {
		if j.ID == id {
			next := append([]Job{}, s.jobs[:i]...)
			next = append(next, s.jobs[i+1:]...)
			return s.commitJobs(next)
		}
	}
	return ErrNotFound
}

func (s *Service) Jobs() []JobView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]JobView, 0, len(s.jobs))
	for _, j := range s.jobs {
		v := JobView{Job: j, Running: s.active[j.ID]}
		if j.Enabled {
			schedule, _ := ParseSchedule(j.Schedule)
			if next := schedule.Next(time.Now().In(s.location)); !next.IsZero() {
				v.NextRun = &next
			}
		}
		for i := len(s.runs) - 1; i >= 0; i-- {
			if s.runs[i].JobID == j.ID {
				run := s.runs[i]
				v.LastRun = &run
				break
			}
		}
		views = append(views, v)
	}
	return views
}

func (s *Service) Runs(jobID string) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := []Run{}
	for i := len(s.runs) - 1; i >= 0; i-- {
		if jobID == "" || s.runs[i].JobID == jobID {
			runs = append(runs, s.runs[i])
		}
	}
	return runs
}

func (s *Service) Log(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.ID == id {
			return os.ReadFile(filepath.Join(s.cfg.DataDir, "logs", id+".log"))
		}
	}
	return nil, ErrNotFound
}
