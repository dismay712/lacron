//go:build linux

package lacron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var Version = "dev"

type Config struct {
	DataDir       string
	Socket        string
	Addr          string
	Timezone      string
	Executable    string
	ScriptPath    string
	RunUID        uint32
	RunGID        uint32
	MaxConcurrent int
	CronEnabled   bool
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func ConfigFromEnv() (Config, error) {
	exe, err := os.Executable()
	if err != nil {
		// Also support restricted development environments without /proc.
		exe, err = exec.LookPath(os.Args[0])
		if err != nil {
			return Config{}, err
		}
		exe, err = filepath.Abs(exe)
		if err != nil {
			return Config{}, err
		}
	}
	cfg := Config{DataDir: env("LACRON_DATA_DIR", "/data"), Socket: env("LACRON_SOCKET", "/run/lacron/runner.sock"), Addr: env("LACRON_ADDR", ":8080"), Timezone: env("TZ", "UTC"), Executable: exe, ScriptPath: env("LACRON_SCRIPT_PATH", "/usr/local/bin:/usr/bin:/bin")}
	for _, path := range []string{cfg.DataDir, cfg.Socket} {
		if !filepath.IsAbs(path) {
			return cfg, fmt.Errorf("数据目录与 socket 必须使用绝对路径")
		}
	}
	uid, err := strconv.ParseUint(env("LACRON_RUN_UID", "10001"), 10, 32)
	if err != nil || uid == 0 {
		return cfg, fmt.Errorf("LACRON_RUN_UID 必须为非零 UID")
	}
	gid, err := strconv.ParseUint(env("LACRON_RUN_GID", "10001"), 10, 32)
	if err != nil || gid == 0 {
		return cfg, fmt.Errorf("LACRON_RUN_GID 必须为非零 GID")
	}
	cfg.RunUID, cfg.RunGID = uint32(uid), uint32(gid)
	cfg.MaxConcurrent, err = strconv.Atoi(env("LACRON_MAX_CONCURRENT", "4"))
	if err != nil || cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > 32 {
		return cfg, fmt.Errorf("LACRON_MAX_CONCURRENT 须在 1–32 之间")
	}
	cfg.CronEnabled, err = strconv.ParseBool(env("LACRON_CRON", "true"))
	if err != nil {
		return cfg, fmt.Errorf("LACRON_CRON 必须为布尔值")
	}
	return cfg, nil
}

func loadToken(cfg Config) (string, error) {
	token := os.Getenv("LACRON_TOKEN")
	if file := os.Getenv("LACRON_TOKEN_FILE"); file != "" {
		if token != "" {
			return "", errors.New("LACRON_TOKEN 与 LACRON_TOKEN_FILE 只能设置一个")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		token = strings.TrimSpace(string(b))
		if token == "" {
			return "", errors.New("令牌文件为空")
		}
	}
	if token == "" {
		path := filepath.Join(cfg.DataDir, "admin.token")
		b, err := os.ReadFile(path)
		if err == nil {
			token = strings.TrimSpace(string(b))
		} else if errors.Is(err, os.ErrNotExist) {
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				return "", err
			}
			token = hex.EncodeToString(random[:])
			if err := atomicWrite(path, []byte(token+"\n")); err != nil {
				return "", err
			}
			log.Printf("已生成管理令牌：%s（请通过 docker compose exec lacron cat %s 查看）", path, path)
		} else {
			return "", err
		}
	}
	if len(token) < 24 || strings.ContainsAny(token, "\r\n\t ") {
		return "", errors.New("访问令牌须至少 24 字节且不能包含空白字符")
	}
	return token, nil
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}

func Serve(parent context.Context, cfg Config) error {
	if cfg.CronEnabled && os.Geteuid() != 0 {
		return errors.New("BusyBox crond 需要 root；开发模式可设置 LACRON_CRON=false")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.DataDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("数据目录已被另一个 lacron 实例占用: %w", err)
	}
	s, err := Open(cfg)
	if err != nil {
		return err
	}
	token, err := loadToken(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var unix net.Listener
	if cfg.CronEnabled {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0700); err != nil {
			return err
		}
		if err := os.Remove(cfg.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		unix, err = net.Listen("unix", cfg.Socket)
		if err != nil {
			return err
		}
		defer unix.Close()
		defer os.Remove(cfg.Socket)
		if err := os.Chmod(cfg.Socket, 0600); err != nil {
			return err
		}
	}
	tcp, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	defer tcp.Close()
	errs := make(chan error, 3)
	var cronAlive atomic.Bool
	var cron *exec.Cmd
	var cronDone chan struct{}
	if cfg.CronEnabled {
		cron = exec.CommandContext(ctx, "crond", "-f", "-l", "8", "-L", "/dev/stderr", "-c", s.cronDir())
		cron.Env = append(os.Environ(), "TZ="+cfg.Timezone, "LACRON_SOCKET="+cfg.Socket)
		cron.Stdout, cron.Stderr = os.Stdout, os.Stderr
		if err := cron.Start(); err != nil {
			return fmt.Errorf("启动 crond: %w", err)
		}
		cronAlive.Store(true)
		cronDone = make(chan struct{})
		go func() {
			err := cron.Wait()
			cronAlive.Store(false)
			close(cronDone)
			errs <- fmt.Errorf("crond 已退出: %v", err)
		}()
	}
	nodeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	node, _ := exec.CommandContext(nodeCtx, "node", "--version").Output()
	stop()
	system := map[string]any{"version": Version, "go_version": runtime.Version(), "node_version": strings.TrimSpace(string(node)), "timezone": cfg.Timezone, "cron_enabled": cfg.CronEnabled, "max_concurrent": cfg.MaxConcurrent}
	public := newHTTPServer(s.handler(ctx, token, &cronAlive, system))
	private := newHTTPServer(s.triggerHandler(ctx))
	go func() { errs <- public.Serve(tcp) }()
	if unix != nil {
		go func() { errs <- private.Serve(unix) }()
	}
	log.Printf("lacron %s listening on %s; timezone=%s; script uid=%d gid=%d", Version, tcp.Addr(), cfg.Timezone, cfg.RunUID, cfg.RunGID)
	var result error
	select {
	case <-ctx.Done():
	case result = <-errs:
		if parent.Err() != nil {
			result = nil
		}
	}
	cancel()
	shutdownCtx, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	_ = public.Shutdown(shutdownCtx)
	_ = private.Shutdown(shutdownCtx)
	s.Close()
	if cronDone != nil {
		<-cronDone
	}
	return result
}

func Trigger(ctx context.Context, socket, id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("无效任务 ID")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/trigger/"+id, nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("trigger: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func Healthcheck() error {
	addr := env("LACRON_ADDR", ":8080")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: HTTP %d", resp.StatusCode)
	}
	return nil
}
