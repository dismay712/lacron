package lacron

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxStoredRuns = 2000
const maxStoredLogKiB = 4096

// Settings are persisted independently of the initial container defaults.
type Settings struct {
	MaxConcurrent  int    `json:"max_concurrent"`
	MaxRuns        int    `json:"max_runs"`
	MaxLogKiB      int    `json:"max_log_kib"`
	PasskeyEnabled bool   `json:"passkey_enabled"`
	PasskeyOrigin  string `json:"passkey_origin"`
}

type Changes struct {
	Instance   string `json:"instance"`
	Jobs       uint64 `json:"jobs"`
	Runs       uint64 `json:"runs"`
	Variables  uint64 `json:"variables"`
	Settings   uint64 `json:"settings"`
	Now        int64  `json:"now"`
	Completed  int    `json:"completed"`
	Successful int    `json:"successful"`
}

func defaultSettings(cfg Config) Settings {
	concurrent := cfg.MaxConcurrent
	if concurrent == 0 {
		concurrent = 4
	}
	return Settings{MaxConcurrent: concurrent, MaxRuns: maxRuns, MaxLogKiB: maxLogBytes >> 10}
}

func parsePasskeyOrigin(value string) (origin, rpID string, err error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", "", fmt.Errorf("Passkey 站点地址须为不带路径的 HTTPS 域名或 http://localhost")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil || strings.HasSuffix(host, ".") || strings.ContainsAny(host, " /%\\") || (u.Scheme != "https" && !(u.Scheme == "http" && host == "localhost")) {
		return "", "", fmt.Errorf("Passkey 远程访问需要 HTTPS 域名，本机可以使用 http://localhost")
	}
	if host != "localhost" && !strings.Contains(host, ".") {
		return "", "", fmt.Errorf("Passkey 站点需要完整域名")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", fmt.Errorf("Passkey 域名无效")
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return "", "", fmt.Errorf("Passkey 域名须使用 ASCII 格式")
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("Passkey 站点端口无效")
		}
		port = strconv.Itoa(n)
	}
	origin = u.Scheme + "://" + host
	if port != "" && !(u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80") {
		origin += ":" + port
	}
	return origin, host, nil
}

func validateSettings(value Settings) error {
	if value.MaxConcurrent < 1 || value.MaxConcurrent > 32 {
		return fmt.Errorf("并发上限须在 1–32 之间")
	}
	if value.MaxRuns < 1 || value.MaxRuns > maxStoredRuns || value.MaxLogKiB < 1 || value.MaxLogKiB > maxStoredLogKiB {
		return fmt.Errorf("执行记录上限须在 1–%d 之间，单次日志须在 1–%d KiB 之间", maxStoredRuns, maxStoredLogKiB)
	}
	if value.PasskeyEnabled && value.PasskeyOrigin == "" {
		return fmt.Errorf("启用 Passkey 前请填写站点地址")
	}
	if value.PasskeyOrigin != "" {
		_, _, err := parsePasskeyOrigin(value.PasskeyOrigin)
		return err
	}
	return nil
}

func (s *Service) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Service) Changes() Changes {
	s.mu.RLock()
	defer s.mu.RUnlock()
	changes := s.changes
	changes.Now = time.Now().UnixMilli()
	changes.Completed, changes.Successful = s.completedRuns, s.successfulRuns
	return changes
}

func (s *Service) UpdateSettings(value Settings) (Settings, error) {
	value.PasskeyOrigin = strings.TrimSpace(value.PasskeyOrigin)
	if err := validateSettings(value); err != nil {
		return Settings{}, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if value.PasskeyOrigin != "" {
		value.PasskeyOrigin, _, _ = parsePasskeyOrigin(value.PasskeyOrigin)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closing {
		return Settings{}, ErrClosing
	}
	if len(s.passkeys) > 0 && s.settings.PasskeyOrigin != value.PasskeyOrigin {
		_, before, _ := parsePasskeyOrigin(s.settings.PasskeyOrigin)
		_, after, _ := parsePasskeyOrigin(value.PasskeyOrigin)
		if before != after {
			return Settings{}, fmt.Errorf("%w：更换 Passkey 域名前，请先移除旧域名的凭据", ErrInvalid)
		}
	}
	next, removed := trimHistory(s.runs, value.MaxRuns)
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := writeRow(tx, saveMetaRow, "settings", value); err != nil {
			return err
		}
		return deleteRunRows(tx, removed)
	}); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	if s.settings.PasskeyEnabled != value.PasskeyEnabled || s.settings.PasskeyOrigin != value.PasskeyOrigin {
		s.passkeyPolicy++
	}
	s.settings = value
	s.changes.Settings++
	if len(removed) > 0 {
		s.publishRuns(next)
	}
	s.mu.Unlock()
	s.removeLogs(removed)
	return value, nil
}
