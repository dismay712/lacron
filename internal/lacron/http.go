package lacron

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
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

func decodeJob(w http.ResponseWriter, r *http.Request) (JobInput, error) {
	var input JobInput
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return input, errors.New("请求必须为单个 JSON 对象")
	}
	return input, nil
}

func (s *Service) handler(ctx context.Context, token string, cronAlive *atomic.Bool, system map[string]any) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{}
		for key, value := range system {
			data[key] = value
		}
		data["cron_running"] = cronAlive.Load()
		respond(w, http.StatusOK, data)
	})
	api.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Jobs())
	})
	api.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		input, err := decodeJob(w, r)
		if err != nil {
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
		input, err := decodeJob(w, r)
		if err != nil {
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
	api.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, s.Runs(r.URL.Query().Get("job_id")))
	})
	api.HandleFunc("GET /api/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.Log(r.PathValue("id"))
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]string{"output": string(data)})
	})
	tokenHash := sha256.Sum256([]byte(token))
	authorized := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := ""
		if value := r.Header.Get("Authorization"); strings.HasPrefix(value, "Bearer ") {
			provided = strings.TrimPrefix(value, "Bearer ")
		}
		hash := sha256.Sum256([]byte(provided))
		if subtle.ConstantTimeCompare(hash[:], tokenHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, http.StatusUnauthorized, "访问令牌无效")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host {
					problem(w, http.StatusForbidden, "拒绝跨站请求")
					return
				}
			}
		}
		api.ServeHTTP(w, r)
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", authorized)
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
