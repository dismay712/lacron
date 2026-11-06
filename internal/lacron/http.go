package lacron

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
)

//go:embed web/*
var webFiles embed.FS

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

func apiError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		problem(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrBusy), errors.Is(err, ErrStale):
		problem(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrClosing):
		problem(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrInvalid):
		problem(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("API: %v", err)
		problem(w, http.StatusInternalServerError, "请求未能完成，请查看服务日志")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, input any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("请求必须为单个 JSON 对象")
	}
	return nil
}

func (s *Service) handler(ctx context.Context, token string, cronAlive *atomic.Bool, system map[string]any) http.Handler {
	api := http.NewServeMux()
	auth := newAuth(s, token)
	api.HandleFunc("GET /api/variables", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Variables())
	})
	saveVariable := func(w http.ResponseWriter, r *http.Request) {
		if os.Geteuid() != 0 {
			problem(w, http.StatusForbidden, "环境变量隔离需要服务以容器内 root 运行（Podman --user 0:0）")
			return
		}
		var input VariableInput
		if err := decodeJSON(w, r, &input, 128<<10); err != nil {
			problem(w, http.StatusBadRequest, "JSON 格式无效或请求过大")
			return
		}
		v, err := s.SaveVariable(r.PathValue("id"), input)
		if err != nil {
			apiError(w, err)
			return
		}
		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
		respond(w, status, v)
	}
	api.HandleFunc("POST /api/variables", saveVariable)
	api.HandleFunc("PUT /api/variables/{id}", saveVariable)
	api.HandleFunc("DELETE /api/variables/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DeleteVariable(r.PathValue("id")); err != nil {
			apiError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{}
		for key, value := range system {
			data[key] = value
		}
		settings := s.Settings()
		data["cron_running"] = cronAlive.Load()
		data["max_concurrent"] = settings.MaxConcurrent
		data["max_runs"] = settings.MaxRuns
		data["max_log_kib"] = settings.MaxLogKiB
		data["encryption_configured"] = s.cipher != nil
		respond(w, http.StatusOK, data)
	})
	api.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Jobs())
	})
	api.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		var input JobInput
		if err := decodeJSON(w, r, &input, 16<<10); err != nil {
			problem(w, http.StatusBadRequest, "JSON 格式无效或请求过大")
			return
		}
		job, err := s.Save("", input)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusCreated, job)
	})
	api.HandleFunc("PUT /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input JobInput
		if err := decodeJSON(w, r, &input, 16<<10); err != nil {
			problem(w, http.StatusBadRequest, "JSON 格式无效或请求过大")
			return
		}
		job, err := s.Save(r.PathValue("id"), input)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusOK, job)
	})
	api.HandleFunc("DELETE /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Delete(r.PathValue("id")); err != nil {
			apiError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/jobs/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		run, err := s.Start(ctx, r.PathValue("id"), "manual")
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusAccepted, run)
	})
	api.HandleFunc("POST /api/jobs/{id}/copy", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name string `json:"name"`
		}
		if r.ContentLength != 0 && decodeJSON(w, r, &input, 4096) != nil {
			problem(w, http.StatusBadRequest, "JSON 格式无效或请求过大")
			return
		}
		job, err := s.CopyJob(r.PathValue("id"), input.Name)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusCreated, job)
	})
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		job, status, cursor := query.Get("job_id"), query.Get("status"), query.Get("cursor")
		if !query.Has("limit") && !query.Has("cursor") && !query.Has("status") {
			respond(w, http.StatusOK, s.Runs(job))
			return
		}
		limit := 50
		if query.Has("limit") {
			var err error
			limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil || limit < 1 || limit > 200 {
				problem(w, http.StatusBadRequest, "分页大小须在 1–200 之间")
				return
			}
		}
		page, err := s.RunsPage(job, status, cursor, limit)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusOK, page)
	})
	api.HandleFunc("POST /api/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := s.StopRun(r.PathValue("id")); err != nil {
			apiError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	api.HandleFunc("GET /api/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if !query.Has("offset") && !query.Has("limit") {
			data, err := s.Log(r.PathValue("id"))
			if err != nil {
				apiError(w, err)
				return
			}
			respond(w, http.StatusOK, map[string]string{"output": string(data)})
			return
		}
		var offset int64
		limit := 64 << 10
		var err error
		if query.Has("offset") {
			offset, err = strconv.ParseInt(query.Get("offset"), 10, 64)
			if err != nil || offset < 0 {
				problem(w, http.StatusBadRequest, "日志偏移量无效")
				return
			}
		}
		if query.Has("limit") {
			limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil || limit < 4 || limit > 128<<10 {
				problem(w, http.StatusBadRequest, "日志分块大小须在 4–131072 字节之间")
				return
			}
		}
		chunk, err := s.ReadLog(r.PathValue("id"), offset, limit)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusOK, chunk)
	})
	cleanup := func(preview bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var input CleanupRequest
			if decodeJSON(w, r, &input, 4096) != nil {
				problem(w, http.StatusBadRequest, "清理参数格式无效")
				return
			}
			result, err := s.CleanupLogs(input, preview)
			if err != nil {
				apiError(w, err)
				return
			}
			respond(w, http.StatusOK, result)
		}
	}
	api.HandleFunc("POST /api/runs/cleanup/preview", cleanup(true))
	api.HandleFunc("POST /api/runs/cleanup", cleanup(false))
	api.HandleFunc("GET /api/changes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Changes())
	})
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Settings())
	})
	api.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var input Settings
		if decodeJSON(w, r, &input, 4096) != nil {
			problem(w, http.StatusBadRequest, "设置格式无效")
			return
		}
		settings, err := s.UpdateSettings(input)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusOK, settings)
	})
	api.HandleFunc("GET /api/passkeys", auth.list)
	api.HandleFunc("POST /api/passkeys/begin", func(w http.ResponseWriter, r *http.Request) { auth.begin(w, r, true) })
	api.HandleFunc("POST /api/passkeys/finish", func(w http.ResponseWriter, r *http.Request) { auth.finish(w, r, true) })
	api.HandleFunc("DELETE /api/passkeys/{id}", auth.remove)
	mux := http.NewServeMux()
	mux.Handle("/api/", auth.protect(api))
	mux.HandleFunc("GET /api/auth/status", auth.status)
	mux.HandleFunc("POST /api/auth/token", auth.tokenLogin)
	mux.HandleFunc("POST /api/auth/logout", auth.logout)
	mux.HandleFunc("POST /api/auth/passkey/begin", func(w http.ResponseWriter, r *http.Request) { auth.begin(w, r, false) })
	mux.HandleFunc("POST /api/auth/passkey/finish", func(w http.ResponseWriter, r *http.Request) { auth.finish(w, r, false) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.CronEnabled && !cronAlive.Load() {
			problem(w, http.StatusServiceUnavailable, "cron 未运行")
			return
		}
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	static, _ := fs.Sub(webFiles, "web")
	files := http.FileServer(http.FS(static))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) triggerHandler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /trigger/{id}", func(w http.ResponseWriter, r *http.Request) {
		run, err := s.Start(ctx, r.PathValue("id"), "cron")
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusAccepted, run)
	})
	return mux
}
