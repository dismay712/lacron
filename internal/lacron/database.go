package lacron

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (seq INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS runs (seq INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, data BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS variables (seq INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, data BLOB NOT NULL, ciphertext BLOB NOT NULL);`

func openDatabase(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "lacron.sqlite3")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = file.Chmod(0600)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	// Keep pragmas on every replacement connection. Application reads use
	// snapshots, while one connection serializes SQLite transactions.
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	for _, pragma := range []string{"busy_timeout(5000)", "synchronous(FULL)", "temp_store(MEMORY)"} {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=WAL;" + schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type sqlWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func writeRow(db sqlWriter, statement, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(statement, id, data)
	return err
}

const saveJobRow = `INSERT INTO jobs(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`
const saveRunRow = `INSERT INTO runs(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`
const saveMetaRow = `INSERT INTO meta(key,data) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data`

func readMeta(db *sql.DB, key string, value any) error {
	var data []byte
	err := db.QueryRow(`SELECT data FROM meta WHERE key=?`, key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func readRows[T any](db *sql.DB, statement string) ([]T, error) {
	rows, err := db.Query(statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		var data []byte
		var value T
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Service) transaction(apply func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func variableCipher(key []byte) (cipher.AEAD, error) {
	if len(key) == 0 {
		return nil, nil
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("环境变量加密密钥解码后须为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func variableAAD(id string) []byte { return []byte("lacron-variable-v1/" + id) }

func (s *Service) writeVariable(db sqlWriter, variable Variable) error {
	if s.cipher == nil {
		return fmt.Errorf("%w：请先配置 LACRON_ENCRYPTION_KEY", ErrInvalid)
	}
	ciphertext := s.cipher.Seal(nil, nil, []byte(variable.Value), variableAAD(variable.ID))
	variable.Value = ""
	data, err := json.Marshal(variable)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO variables(id,data,ciphertext) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data, ciphertext=excluded.ciphertext`, variable.ID, data, ciphertext)
	return err
}

func (s *Service) readVariables(previous cipher.AEAD) (bool, error) {
	rows, err := s.db.Query(`SELECT id,data,ciphertext FROM variables ORDER BY seq`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	rotate := false
	for rows.Next() {
		var id string
		var data, ciphertext []byte
		if err := rows.Scan(&id, &data, &ciphertext); err != nil {
			return false, err
		}
		if s.cipher == nil {
			return false, fmt.Errorf("已有加密变量，请配置 LACRON_ENCRYPTION_KEY")
		}
		var v Variable
		if err := json.Unmarshal(data, &v); err != nil || id != v.ID {
			return false, fmt.Errorf("SQLite 环境变量记录无效")
		}
		value, err := s.cipher.Open(nil, nil, ciphertext, variableAAD(id))
		if err != nil && previous != nil {
			value, err = previous.Open(nil, nil, ciphertext, variableAAD(id))
			rotate = rotate || err == nil
		}
		if err != nil {
			return false, fmt.Errorf("无法解密环境变量，请检查 LACRON_ENCRYPTION_KEY 或轮换用的旧密钥")
		}
		v.Value = string(value)
		s.variables = append(s.variables, v)
	}
	return rotate, rows.Err()
}

func (s *Service) loadDatabase(previous cipher.AEAD) error {
	var migration string
	if err := readMeta(s.db, "legacy_import", &migration); err != nil {
		return err
	}
	if migration != "" && migration != "cleanup" && migration != "done" {
		return fmt.Errorf("SQLite 数据迁移状态无效")
	}
	legacy := migration == ""
	if err := readMeta(s.db, "settings", &s.settings); err != nil {
		return err
	}
	if err := validateSettings(s.settings); err != nil {
		return err
	}
	if err := readMeta(s.db, "passkeys", &s.passkeys); err != nil {
		return err
	}
	if err := readMeta(s.db, "passkey_user", &s.passkeyUser); err != nil {
		return err
	}
	if s.passkeyUser == "" {
		s.passkeyUser = newID()
		if err := writeRow(s.db, saveMetaRow, "passkey_user", s.passkeyUser); err != nil {
			return err
		}
	}
	legacyVars := filepath.Join(s.cfg.DataDir, "variables.json")
	rotate := false
	if legacy {
		if err := readJSON(filepath.Join(s.cfg.DataDir, "jobs.json"), &s.jobs); err != nil {
			return err
		}
		if err := readJSON(filepath.Join(s.cfg.DataDir, "runs.json"), &s.runs); err != nil {
			return err
		}
		if err := readJSON(legacyVars, &s.variables); err != nil {
			return err
		}
		if len(s.variables) != 0 && s.cipher == nil {
			return fmt.Errorf("迁移现有环境变量前请配置 LACRON_ENCRYPTION_KEY")
		}
	} else {
		var err error
		s.jobs, err = readRows[Job](s.db, `SELECT data FROM jobs ORDER BY seq`)
		if err != nil {
			return err
		}
		s.runs, err = readRows[Run](s.db, `SELECT data FROM runs ORDER BY seq`)
		if err != nil {
			return err
		}
		rotate, err = s.readVariables(previous)
		if err != nil {
			return err
		}
	}
	if err := s.validateLoadedData(); err != nil {
		return err
	}
	var interrupted []Run
	now := time.Now().UTC()
	for i := range s.runs {
		if s.runs[i].Status == "running" {
			s.runs[i].Status, s.runs[i].FinishedAt = "interrupted", &now
			interrupted = append(interrupted, s.runs[i])
		}
	}
	retained, removed := trimHistory(s.runs, s.settings.MaxRuns)
	s.runs = retained
	if legacy || rotate || len(interrupted) > 0 || len(removed) > 0 {
		err := s.transaction(func(tx *sql.Tx) error {
			if legacy {
				for _, job := range s.jobs {
					if err := writeRow(tx, saveJobRow, job.ID, job); err != nil {
						return err
					}
				}
				for _, run := range s.runs {
					if err := writeRow(tx, saveRunRow, run.ID, run); err != nil {
						return err
					}
				}
				if err := writeRow(tx, saveMetaRow, "legacy_import", "cleanup"); err != nil {
					return err
				}
			} else {
				for _, run := range interrupted {
					if err := writeRow(tx, saveRunRow, run.ID, run); err != nil {
						return err
					}
				}
				if err := deleteRunRows(tx, removed); err != nil {
					return err
				}
			}
			if legacy || rotate {
				for _, variable := range s.variables {
					if err := s.writeVariable(tx, variable); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if legacy || migration == "cleanup" {
		// Keep the marker until both the import and plaintext deletion are durable.
		if err := clearLegacyVariables(s.cfg.DataDir, legacyVars); err != nil {
			return fmt.Errorf("清理旧环境变量文件: %w", err)
		}
		if err := writeRow(s.db, saveMetaRow, "legacy_import", "done"); err != nil {
			return err
		}
	}
	s.reindexRuns()
	s.cleanOrphanLogs()
	return nil
}

func clearLegacyVariables(dir, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	// Old atomic writes may leave their root-level temp files after a crash.
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), ".lacron-")
		if ok && suffix != "" && strings.Trim(suffix, "0123456789") == "" && entry.Type().IsRegular() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return f.Sync()
}

func (s *Service) validateLoadedData() error {
	if len(s.jobs) > maxJobs || len(s.runs) > maxStoredRuns+32 || len(s.variables) > maxVariables {
		return fmt.Errorf("数据库记录数量超过上限")
	}
	for _, j := range s.jobs {
		if _, exists := s.schedules[j.ID]; !idPattern.MatchString(j.ID) || exists {
			return fmt.Errorf("任务记录包含重复或无效 ID")
		}
		if _, err := validateJob(j.input()); err != nil {
			return fmt.Errorf("任务记录: %w", err)
		}
		s.schedules[j.ID], _ = ParseSchedule(j.Schedule)
	}
	if err := s.migrateJobSlots(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range s.runs {
		if !idPattern.MatchString(r.ID) || !idPattern.MatchString(r.JobID) || seen[r.ID] {
			return fmt.Errorf("执行记录包含重复或无效 ID")
		}
		seen[r.ID] = true
	}
	seen = map[string]bool{}
	for _, v := range s.variables {
		in, err := validateVariable(VariableInput{v.Name, &v.Value, v.Description, v.JobIDs})
		if err != nil || in.Name != v.Name || !idPattern.MatchString(v.ID) || seen[v.ID] {
			return fmt.Errorf("环境变量记录无效")
		}
		seen[v.ID] = true
	}
	return checkEnvironmentSize(s.variables)
}
