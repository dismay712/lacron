package lacron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maxVariables = 200
const maxVariableValue = 16 << 10
const maxJobEnvBytes = 64 << 10

var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Variable values are stored only in a root-owned file, never in API responses.
type Variable struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	JobIDs      []string  `json:"job_ids"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type VariableInput struct {
	Name        string   `json:"name"`
	Value       *string  `json:"value,omitempty"`
	Description string   `json:"description"`
	JobIDs      []string `json:"job_ids"`
}

type VariableView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	JobIDs      []string  `json:"job_ids"`
	HasValue    bool      `json:"has_value"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func validateVariable(in VariableInput) (VariableInput, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if !variableNamePattern.MatchString(in.Name) {
		return in, fmt.Errorf("变量名须以字母或下划线开头，仅含字母、数字和下划线，最多 128 字节")
	}
	if strings.HasPrefix(in.Name, "LACRON_") || slices.Contains([]string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "TZ"}, in.Name) {
		return in, fmt.Errorf("变量名由系统保留，请使用其他名称")
	}
	if len(in.Description) > 500 || len(in.JobIDs) > maxJobs {
		return in, fmt.Errorf("描述或授权任务数量超过上限")
	}
	if in.Value != nil && (len(*in.Value) > maxVariableValue || strings.ContainsRune(*in.Value, 0)) {
		return in, fmt.Errorf("变量值不能包含 NUL，且最多 16 KiB")
	}
	ids := make([]string, 0, len(in.JobIDs))
	for _, id := range in.JobIDs {
		if !idPattern.MatchString(id) {
			return in, fmt.Errorf("授权任务 ID 无效")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	in.JobIDs = ids
	return in, nil
}

func (s *Service) loadVariables() error {
	s.variables = []Variable{}
	path := filepath.Join(s.cfg.DataDir, "variables.json")
	if err := readJSON(path, &s.variables); err != nil {
		return err
	}
	if len(s.variables) > maxVariables {
		return fmt.Errorf("环境变量超过 %d 个", maxVariables)
	}
	ids := map[string]bool{}
	for _, v := range s.variables {
		if !idPattern.MatchString(v.ID) || ids[v.ID] {
			return fmt.Errorf("variables.json 含重复名称或无效 ID")
		}
		in, err := validateVariable(VariableInput{v.Name, &v.Value, v.Description, v.JobIDs})
		if err != nil || in.Name != v.Name {
			return fmt.Errorf("variables.json 配置无效")
		}
		ids[v.ID] = true
	}
	if err := checkEnvironmentSize(s.variables); err != nil {
		return err
	}
	// Fix permissions even when a file was restored from a backup.
	if _, err := os.Stat(path); err == nil {
		return os.Chmod(path, 0600)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func checkEnvironmentSize(variables []Variable) error {
	encoded, err := json.MarshalIndent(variables, "", "  ")
	if err != nil || len(encoded) >= 8<<20 {
		return fmt.Errorf("环境变量存储总大小不能超过 8 MiB")
	}
	sizes := map[string]int{}
	names := map[string]map[string]bool{}
	for _, v := range variables {
		for _, id := range v.JobIDs {
			if names[id] == nil {
				names[id] = map[string]bool{}
			}
			if names[id][v.Name] {
				return fmt.Errorf("同一任务不能授权多个同名变量，请检查 %s 的授权", v.Name)
			}
			names[id][v.Name] = true
			sizes[id] += len(v.Name) + len(v.Value) + 2
			if sizes[id] > maxJobEnvBytes {
				return fmt.Errorf("单个任务的授权变量总大小不能超过 64 KiB")
			}
		}
	}
	return nil
}

func (s *Service) variableView(v Variable) VariableView {
	ids := []string{}
	for _, id := range v.JobIDs {
		if _, exists := s.schedules[id]; exists {
			ids = append(ids, id)
		}
	}
	return VariableView{ID: v.ID, Name: v.Name, Description: v.Description, JobIDs: ids, HasValue: true, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func (s *Service) Variables() []VariableView {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]VariableView, 0, len(s.variables))
	for _, v := range s.variables {
		result = append(result, s.variableView(v))
	}
	return result
}

func (s *Service) SaveVariable(id string, input VariableInput) (VariableView, error) {
	in, err := validateVariable(input)
	if err != nil {
		return VariableView{}, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return VariableView{}, ErrClosing
	}
	for _, jobID := range in.JobIDs {
		if _, exists := s.schedules[jobID]; !exists {
			return VariableView{}, fmt.Errorf("%w：授权任务不存在，请刷新后重试", ErrInvalid)
		}
	}
	now := time.Now().UTC()
	v := Variable{ID: id, Name: in.Name, Description: in.Description, JobIDs: in.JobIDs, CreatedAt: now, UpdatedAt: now}
	next := append([]Variable{}, s.variables...)
	if id == "" {
		if len(next) >= maxVariables || in.Value == nil {
			return VariableView{}, fmt.Errorf("%w：新建变量须设置值，最多支持 %d 个变量", ErrInvalid, maxVariables)
		}
		v.ID, v.Value = newID(), *in.Value
		next = append(next, v)
	} else {
		found := false
		for i, old := range next {
			if old.ID == id {
				v.CreatedAt, v.Value = old.CreatedAt, old.Value
				if in.Value != nil {
					v.Value = *in.Value
				}
				next[i], found = v, true
				break
			}
		}
		if !found {
			return VariableView{}, ErrNotFound
		}
	}
	if err := checkEnvironmentSize(next); err != nil {
		return VariableView{}, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if err := writeJSON(filepath.Join(s.cfg.DataDir, "variables.json"), next); err != nil {
		return VariableView{}, err
	}
	s.variables = next
	return s.variableView(v), nil
}

func (s *Service) DeleteVariable(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ErrClosing
	}
	for i, v := range s.variables {
		if v.ID == id {
			next := append([]Variable{}, s.variables[:i]...)
			next = append(next, s.variables[i+1:]...)
			if err := writeJSON(filepath.Join(s.cfg.DataDir, "variables.json"), next); err != nil {
				return err
			}
			s.variables = next
			return nil
		}
	}
	return ErrNotFound
}

// Called under s.mu. Return a fresh snapshot, never a shared global environment.
func (s *Service) jobEnvironment(job Job) []string {
	result := []string{"PATH=" + s.cfg.ScriptPath, "HOME=/tmp", "USER=lacron", "LOGNAME=lacron", "LANG=C.UTF-8", "TZ=" + s.cfg.Timezone}
	for _, v := range s.variables {
		if slices.Contains(v.JobIDs, job.ID) {
			result = append(result, v.Name+"="+v.Value)
		}
	}
	return result
}

func freeRunSlot(jobs []Job, retired map[uint32]bool) uint32 {
	used := [maxJobs + 1]bool{}
	for _, j := range jobs {
		if j.RunSlot <= maxJobs {
			used[j.RunSlot] = true
		}
	}
	for slot := uint32(1); slot <= maxJobs; slot++ {
		if !used[slot] && !retired[slot] {
			return slot
		}
	}
	return 0
}

func (s *Service) migrateJobSlots() error {
	used := map[uint32]bool{}
	for _, j := range s.jobs {
		if j.RunSlot > maxJobs || (j.RunSlot != 0 && used[j.RunSlot]) {
			return fmt.Errorf("jobs.json 任务运行身份无效或重复")
		}
		used[j.RunSlot] = true
	}
	changed := false
	for i := range s.jobs {
		if s.jobs[i].RunSlot == 0 {
			s.jobs[i].RunSlot = freeRunSlot(s.jobs, nil)
			changed = true
		}
	}
	if changed {
		return writeJSON(filepath.Join(s.cfg.DataDir, "jobs.json"), s.jobs)
	}
	return nil
}

func (s *Service) jobIdentity(job Job) (uint32, uint32) {
	return s.cfg.RunUID + job.RunSlot - 1, s.cfg.RunGID + job.RunSlot - 1
}
