package lacron

import (
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
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
	"unicode/utf8"
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
	RunSlot    uint32    `json:"run_slot"`
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
	RunUID  uint32     `json:"run_uid"`
	RunGID  uint32     `json:"run_gid"`
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
	LogCleared bool       `json:"log_cleared,omitempty"`
}

type Service struct {
	// Writers commit to SQLite before publishing the new state. Readers hold
	// only the snapshot lock, so fsync never delays a list or log read.
	writeMu        sync.Mutex
	mu             sync.RWMutex
	closeOnce      sync.Once
	db             *sql.DB
	cipher         cipher.AEAD
	cfg            Config
	jobs           []Job
	runs           []Run
	runIndex       map[string]int
	lastRunByJob   map[string]Run
	completedRuns  int
	successfulRuns int
	active         map[string]activeRun
	closing        bool
	wg             sync.WaitGroup
	location       *time.Location
	schedules      map[string]Schedule
	lastScheduled  map[string]int64
	variables      []Variable
	retiredSlots   map[uint32]bool
	settings       Settings
	passkeys       []storedPasskey
	passkeyUser    string
	passkeyPolicy  uint64
	changes        Changes
}

func (j Job) input() JobInput {
	return JobInput{j.Name, j.Schedule, j.Command, j.WorkingDir, j.TimeoutSec, j.Enabled}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func Open(cfg Config) (*Service, error) {
	if cfg.RunUID == 0 || cfg.RunGID == 0 || cfg.RunUID > ^uint32(0)-maxJobs || cfg.RunGID > ^uint32(0)-maxJobs {
		return nil, fmt.Errorf("任务 UID/GID 起始值必须为非零值，且能容纳 %d 个隔离身份", maxJobs)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("TZ: %w", err)
	}
	current, err := variableCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	previous, err := variableCipher(cfg.PreviousEncryptionKey)
	if err != nil {
		return nil, err
	}
	if previous != nil && current == nil {
		return nil, fmt.Errorf("密钥轮换需要同时配置当前和旧加密密钥")
	}
	// Retain only the cipher in the running service.
	cfg.EncryptionKey, cfg.PreviousEncryptionKey = nil, nil
	s := &Service{
		cfg: cfg, cipher: current, jobs: []Job{}, runs: []Run{}, variables: []Variable{},
		active: map[string]activeRun{}, location: loc, schedules: map[string]Schedule{},
		lastScheduled: map[string]int64{}, retiredSlots: map[uint32]bool{},
		settings: defaultSettings(cfg), changes: Changes{Instance: newID()},
	}
	for _, dir := range []string{cfg.DataDir, filepath.Join(cfg.DataDir, "logs")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, fmt.Errorf("保护数据目录 %s: %w", dir, err)
		}
	}
	s.db, err = openDatabase(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	if err := s.loadDatabase(previous); err != nil {
		s.db.Close()
		return nil, err
	}
	if err := s.disableLegacyCron(); err != nil {
		s.db.Close()
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
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() > 8<<20 {
		return fmt.Errorf("旧数据文件 %s 超过 8 MiB", path)
	}
	decoder := json.NewDecoder(f)
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

// Remove managed entries left by minute-only versions, so an old crontab
// cannot double-trigger jobs after upgrading. New installations need no crontab.
func (s *Service) disableLegacyCron() error {
	if _, err := os.Stat(s.cronDir()); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.cronDir(), "root"), []byte("# Managed jobs now use lacron's built-in second-resolution scheduler.\n")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.cronDir(), "cron.update")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Service) Save(id string, input JobInput) (Job, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.saveJob(id, input)
}

// saveJob is called with the writer lock. The prior job configuration remains
// visible to readers until the replacement row is durable.
func (s *Service) saveJob(id string, input JobInput) (Job, error) {
	in, err := validateJob(input)
	if err != nil {
		return Job{}, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if s.closing {
		return Job{}, ErrClosing
	}
	now := time.Now().UTC()
	j := Job{ID: id, Name: in.Name, Schedule: in.Schedule, Command: in.Command, WorkingDir: in.WorkingDir, TimeoutSec: in.TimeoutSec, Enabled: in.Enabled, CreatedAt: now, UpdatedAt: now}
	index := -1
	if id == "" {
		if len(s.jobs) >= maxJobs {
			return Job{}, fmt.Errorf("%w：最多支持 %d 个任务", ErrInvalid, maxJobs)
		}
		j.ID = newID()
		j.RunSlot = freeRunSlot(s.jobs, s.retiredSlots)
		if j.RunSlot == 0 {
			return Job{}, fmt.Errorf("%w：本次容器运行的隔离身份已用尽，请重启容器释放已删除任务的身份", ErrBusy)
		}
	} else {
		for i, old := range s.jobs {
			if old.ID == id {
				j.CreatedAt, j.RunSlot, index = old.CreatedAt, old.RunSlot, i
				break
			}
		}
		if index == -1 {
			return Job{}, ErrNotFound
		}
	}
	schedule, _ := ParseSchedule(j.Schedule)
	if err := writeRow(s.db, saveJobRow, j.ID, j); err != nil {
		return Job{}, fmt.Errorf("保存任务: %w", err)
	}
	s.mu.Lock()
	if index == -1 {
		s.jobs = append(s.jobs, j)
	} else {
		s.jobs[index] = j
	}
	s.schedules[j.ID] = schedule
	s.changes.Jobs++
	s.mu.Unlock()
	return j, nil
}

func (s *Service) CopyJob(id, name string) (Job, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, job := range s.jobs {
		if job.ID == id {
			input := job.input()
			if strings.TrimSpace(name) == "" {
				const suffix = "（副本）"
				name = job.Name
				for len(name)+len(suffix) > 120 {
					_, width := utf8.DecodeLastRuneInString(name)
					name = name[:len(name)-width]
				}
				name += suffix
			}
			input.Name, input.Enabled = name, false
			return s.saveJob("", input)
		}
	}
	return Job{}, ErrNotFound
}

func (s *Service) Delete(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing {
		return ErrClosing
	}
	if _, running := s.active[id]; running {
		return ErrBusy
	}
	for i, job := range s.jobs {
		if job.ID != id {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id); err != nil {
			return err
		}
		s.mu.Lock()
		s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
		s.retiredSlots[job.RunSlot] = true
		delete(s.schedules, id)
		delete(s.lastScheduled, id)
		s.changes.Jobs++
		s.changes.Variables++
		s.mu.Unlock()
		return nil
	}
	return ErrNotFound
}

func (s *Service) Jobs() []JobView {
	s.mu.RLock()
	views := make([]JobView, 0, len(s.jobs))
	schedules := make([]Schedule, 0, len(s.jobs))
	for _, j := range s.jobs {
		uid, gid := s.jobIdentity(j)
		_, running := s.active[j.ID]
		view := JobView{Job: j, Running: running, RunUID: uid, RunGID: gid}
		if last, ok := s.lastRunByJob[j.ID]; ok {
			view.LastRun = &last
		}
		views = append(views, view)
		schedules = append(schedules, s.schedules[j.ID])
	}
	s.mu.RUnlock()
	now := time.Now().In(s.location)
	for i := range views {
		if views[i].Enabled {
			if next := schedules[i].Next(now); !next.IsZero() {
				views[i].NextRun = &next
			}
		}
	}
	return views
}
