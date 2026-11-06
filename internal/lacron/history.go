package lacron

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) reindexRuns() {
	s.runIndex = make(map[string]int, len(s.runs))
	s.lastRunByJob = make(map[string]Run, len(s.jobs))
	s.completedRuns, s.successfulRuns = 0, 0
	for i, run := range s.runs {
		s.runIndex[run.ID] = i
		s.lastRunByJob[run.JobID] = run
		if run.Status != "running" {
			s.completedRuns++
		}
		if run.Status == "success" {
			s.successfulRuns++
		}
	}
}

// publishRuns is called with the snapshot lock after a durable update.
func (s *Service) publishRuns(next []Run) {
	s.runs = next
	s.reindexRuns()
	s.changes.Runs++
	s.changes.Jobs++
}

func trimHistory(runs []Run, limit int) ([]Run, []Run) {
	remaining := len(runs) - limit
	if remaining <= 0 {
		return runs, nil
	}
	keep := make([]Run, 0, len(runs))
	var removed []Run
	for _, run := range runs {
		if remaining > 0 && run.Status != "running" {
			removed = append(removed, run)
			remaining--
			continue
		}
		keep = append(keep, run)
	}
	return keep, removed
}

func deleteRunRows(db sqlWriter, removed []Run) error {
	for _, run := range removed {
		if _, err := db.Exec(`DELETE FROM runs WHERE id=?`, run.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) logPath(id string) string {
	return filepath.Join(s.cfg.DataDir, "logs", id+".log")
}

func (s *Service) removeLogs(runs []Run) int {
	failed := 0
	for _, run := range runs {
		if err := os.Remove(s.logPath(run.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed++
			log.Printf("remove log %s: %v", run.ID, err)
		}
	}
	return failed
}

// A committed deletion can outlive the process that unlinks its output file.
func (s *Service) cleanOrphanLogs() {
	entries, err := os.ReadDir(filepath.Join(s.cfg.DataDir, "logs"))
	if err != nil {
		log.Printf("scan logs: %v", err)
		return
	}
	var orphaned []Run
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".log")
		if !ok || !idPattern.MatchString(id) || !entry.Type().IsRegular() {
			continue
		}
		index, ok := s.runIndex[id]
		if !ok || s.runs[index].LogCleared {
			orphaned = append(orphaned, Run{ID: id})
		}
	}
	s.removeLogs(orphaned)
}

func (s *Service) Runs(jobID string) []Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := []Run{}
	for i := len(s.runs) - 1; i >= 0; i-- {
		if jobID == "" || s.runs[i].JobID == jobID {
			runs = append(runs, s.runs[i])
		}
	}
	return runs
}

type RunPage struct {
	Items      []Run  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (s *Service) RunsPage(jobID, status, cursor string, limit int) (RunPage, error) {
	if limit < 1 || limit > 200 {
		return RunPage{}, fmt.Errorf("%w：分页大小须在 1–200 之间", ErrInvalid)
	}
	switch status {
	case "", "running", "success", "failed", "timeout", "cancelled", "interrupted":
	default:
		return RunPage{}, fmt.Errorf("%w：未知执行状态", ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	start := len(s.runs)
	if cursor != "" {
		var ok bool
		start, ok = s.runIndex[cursor]
		if !ok {
			return RunPage{}, fmt.Errorf("%w：分页位置已过期，请刷新记录", ErrStale)
		}
	}
	page := RunPage{Items: make([]Run, 0, limit)}
	for i := start - 1; i >= 0; i-- {
		run := s.runs[i]
		if jobID != "" && run.JobID != jobID || status != "" && run.Status != status {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = page.Items[limit-1].ID
			break
		}
		page.Items = append(page.Items, run)
	}
	return page, nil
}

func (s *Service) Run(id string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.runIndex[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return s.runs[i], nil
}

func (s *Service) openLog(id string) (Run, *os.File, error) {
	run, err := s.Run(id)
	if err != nil || run.LogCleared {
		return run, nil, err
	}
	f, err := os.Open(s.logPath(run.ID))
	if errors.Is(err, os.ErrNotExist) {
		// Read the latest metadata if a concurrent cleanup removed the file.
		latest, lookupErr := s.Run(id)
		return latest, nil, lookupErr
	}
	return run, f, err
}

func (s *Service) Log(id string) ([]byte, error) {
	_, f, err := s.openLog(id)
	if err != nil || f == nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxStoredLogKiB<<10))
}

type LogChunk struct {
	Run        Run    `json:"run"`
	Output     string `json:"output"`
	NextOffset int64  `json:"next_offset"`
	More       bool   `json:"more"`
	Reset      bool   `json:"reset"`
}

func (s *Service) ReadLog(id string, offset int64, limit int) (LogChunk, error) {
	if offset < 0 || limit < utf8.UTFMax || limit > 128<<10 {
		return LogChunk{}, fmt.Errorf("%w：日志偏移量或分块大小无效", ErrInvalid)
	}
	run, f, err := s.openLog(id)
	chunk := LogChunk{Run: run, NextOffset: offset}
	if err != nil {
		return chunk, err
	}
	if f == nil {
		chunk.NextOffset, chunk.Reset = 0, offset != 0
		return chunk, nil
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return chunk, err
	}
	if offset > stat.Size() {
		offset, chunk.Reset = 0, true
	}
	data := make([]byte, min(int64(limit), stat.Size()-offset))
	n, err := f.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return chunk, err
	}
	more := offset+int64(n) < stat.Size()
	if n > 0 && (more || run.Status == "running") {
		start := n - 1
		for start > 0 && !utf8.RuneStart(data[start]) && n-start < utf8.UTFMax {
			start--
		}
		if !utf8.FullRune(data[start:n]) {
			n = start
		}
	}
	chunk.Output = string(data[:n])
	chunk.NextOffset, chunk.More = offset+int64(n), more
	return chunk, nil
}

type CleanupRequest struct {
	RunID  string    `json:"run_id,omitempty"`
	JobID  string    `json:"job_id,omitempty"`
	Before time.Time `json:"before,omitempty"`
	Cutoff time.Time `json:"cutoff,omitempty"`
	All    bool      `json:"all,omitempty"`
	Mode   string    `json:"mode"`
}

type CleanupResult struct {
	Matched int       `json:"matched"`
	Skipped int       `json:"skipped"`
	Pending int       `json:"pending"`
	Cutoff  time.Time `json:"cutoff"`
}

func (s *Service) CleanupLogs(in CleanupRequest, preview bool) (result CleanupResult, err error) {
	if in.Mode != "output" && in.Mode != "history" || in.RunID == "" && in.JobID == "" && in.Before.IsZero() && !in.All {
		return CleanupResult{}, fmt.Errorf("%w：请指定清理范围和方式", ErrInvalid)
	}
	var unlink []Run
	s.writeMu.Lock()
	defer func() {
		s.writeMu.Unlock()
		result.Pending = s.removeLogs(unlink)
	}()
	if s.closing {
		return CleanupResult{}, ErrClosing
	}
	if preview {
		in.Cutoff = time.Now().UTC()
	} else if in.Cutoff.IsZero() || in.Cutoff.After(time.Now()) {
		return CleanupResult{}, fmt.Errorf("%w：请先预览清理范围", ErrInvalid)
	}
	if _, exists := s.runIndex[in.RunID]; in.RunID != "" && !exists {
		return CleanupResult{}, ErrNotFound
	}
	result = CleanupResult{Cutoff: in.Cutoff}
	var selected []Run
	for _, run := range s.runs {
		if in.RunID != "" && run.ID != in.RunID || in.JobID != "" && run.JobID != in.JobID || !in.Before.IsZero() && !run.StartedAt.Before(in.Before) {
			continue
		}
		if run.Status == "running" || run.FinishedAt != nil && run.FinishedAt.After(in.Cutoff) || run.StartedAt.After(in.Cutoff) {
			result.Skipped++
			continue
		}
		if in.Mode == "output" && run.LogCleared {
			continue
		}
		selected = append(selected, run)
	}
	result.Matched = len(selected)
	if preview || len(selected) == 0 {
		return result, nil
	}
	selectedIDs := make(map[string]bool, len(selected))
	for _, run := range selected {
		selectedIDs[run.ID] = true
	}
	next := make([]Run, 0, len(s.runs))
	if err := s.transaction(func(tx *sql.Tx) error {
		if in.Mode == "history" {
			if err := deleteRunRows(tx, selected); err != nil {
				return err
			}
		}
		for _, run := range s.runs {
			if selectedIDs[run.ID] {
				if in.Mode == "history" {
					continue
				}
				run.LogCleared, run.Truncated = true, false
				if err := writeRow(tx, saveRunRow, run.ID, run); err != nil {
					return err
				}
			}
			next = append(next, run)
		}
		return nil
	}); err != nil {
		return result, err
	}
	s.mu.Lock()
	s.publishRuns(next)
	s.mu.Unlock()
	unlink = selected
	return result, nil
}
