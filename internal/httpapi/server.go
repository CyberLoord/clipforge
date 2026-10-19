package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/storage"
	"github.com/CyberLoord/clipforge/internal/store"
)

type Repository interface {
	Create(context.Context, *domain.Job) error
	List(context.Context) ([]domain.Job, error)
	Get(context.Context, string) (domain.Job, error)
	Cancel(context.Context, string) error
	Retry(context.Context, string) error
	Reprocess(context.Context, string, string) error
	Reexport(context.Context, string, string, float64, float64) error
}

type Server struct {
	repo  Repository
	files storage.Storage
	cfg   config.Config
	log   *slog.Logger
	ready func(context.Context) error
}

func New(repo Repository, files storage.Storage, cfg config.Config, logger *slog.Logger, ready func(context.Context) error) http.Handler {
	s := &Server{repo, files, cfg, logger, ready}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.ready(ctx); err != nil {
			s.fail(w, 503, "database unavailable")
			return
		}
		s.json(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		s.json(w, 200, map[string]any{"mode": cfg.Mode, "available_modes": cfg.AvailableModes(), "min_video_seconds": domain.MinVideoSeconds, "max_upload_bytes": cfg.MaxUploadBytes, "max_duration_seconds": cfg.MaxDuration, "min_clip_seconds": domain.MinClipSeconds, "max_clip_seconds": domain.MaxClipSeconds})
	})
	mux.HandleFunc("GET /api/jobs", s.list)
	mux.HandleFunc("POST /api/jobs", s.upload)
	mux.HandleFunc("GET /api/jobs/{id}", s.get)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.cancel)
	mux.HandleFunc("POST /api/jobs/{id}/retry", s.retry)
	mux.HandleFunc("POST /api/jobs/{id}/reprocess", s.reprocess)
	mux.HandleFunc("PATCH /api/jobs/{id}/clips/{clipID}", s.reexport)
	mux.HandleFunc("GET /api/jobs/{id}/clips/{clipID}/video", s.video)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { s.fail(w, 404, "endpoint not found") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			s.fail(w, 405, "method not allowed")
			return
		}
		if _, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err != nil {
			s.fail(w, 503, "frontend is not built")
			return
		}
		http.FileServer(http.Dir(cfg.WebDir)).ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					s.fail(w, 403, "origin is not allowed")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.repo.List(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	if jobs == nil {
		jobs = []domain.Job{}
	}
	s.json(w, 200, jobs)
}
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	job, ok := s.job(w, r)
	if ok {
		s.json(w, 200, job)
	}
}
func (s *Server) job(w http.ResponseWriter, r *http.Request) (domain.Job, bool) {
	if !domain.ValidID(r.PathValue("id")) {
		s.fail(w, 404, "job not found")
		return domain.Job{}, false
	}
	job, err := s.repo.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.repoError(w, err)
		return job, false
	}
	return job, true
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+1024*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		s.fail(w, 400, "expected multipart file upload")
		return
	}
	mode := s.cfg.Mode
	if mode == "" {
		mode = "montage"
	}
	part, err := reader.NextPart()
	if err == nil && part.FormName() == "mode" && part.FileName() == "" {
		value, readErr := io.ReadAll(io.LimitReader(part, 33))
		if readErr != nil || len(value) > 32 {
			s.fail(w, 400, "invalid mode field")
			return
		}
		mode = string(value)
		part, err = reader.NextPart()
	}
	if !s.modeAllowed(w, mode) {
		return
	}
	if err != nil || part.FormName() != "file" {
		s.fail(w, 400, "file field is required")
		return
	}
	filename := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
	if !strings.EqualFold(filepath.Ext(filename), ".mp4") {
		s.fail(w, 415, "only MP4 files are accepted")
		return
	}
	filename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, filename)
	if len(filename) > 240 {
		s.fail(w, 400, "filename is too long")
		return
	}
	prefix := make([]byte, 12)
	if _, err := io.ReadFull(part, prefix); err != nil || !bytes.Equal(prefix[4:8], []byte("ftyp")) {
		s.fail(w, 415, "file is not an MP4 container")
		return
	}
	id := domain.NewID()
	key := filepath.Join("jobs", id, "source.mp4")
	source, err := s.files.Path(key)
	if err != nil {
		s.internal(w, err)
		return
	}
	saved := false
	defer func() {
		if !saved {
			s.files.Remove(key)
			os.Remove(filepath.Dir(source))
		}
	}()
	_, err = s.files.Save(r.Context(), key, io.MultiReader(bytes.NewReader(prefix), part), s.cfg.MaxUploadBytes)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.Is(err, storage.ErrTooLarge) || errors.As(err, &tooLarge) {
			s.fail(w, 413, "file exceeds upload limit")
			return
		}
		s.internal(w, err)
		return
	}
	if next, err := reader.NextPart(); err != io.EOF || next != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.fail(w, 413, "upload exceeds limit")
		} else {
			s.fail(w, 400, "upload exactly one file")
		}
		return
	}
	now := time.Now().UTC()
	job := domain.Job{ID: id, Filename: filename, Status: "queued", Stage: "probe", Mode: mode, SourcePath: source, CreatedAt: now, UpdatedAt: now, Transcript: []domain.Segment{}, Clips: []domain.Clip{}}
	if err := s.repo.Create(r.Context(), &job); err != nil {
		saved = true
		lookup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		persisted, lookupErr := s.repo.Get(lookup, job.ID)
		if lookupErr == nil {
			s.json(w, 202, persisted)
			return
		}
		s.internal(w, err)
		return
	}
	saved = true
	s.log.Info("job created", "job_id", id, "mode", job.Mode)
	s.json(w, 202, job)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.job(w, r); !ok {
		return
	}
	if err := s.repo.Cancel(r.Context(), r.PathValue("id")); err != nil {
		s.repoError(w, err)
		return
	}
	s.get(w, r)
}
func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.job(w, r); !ok {
		return
	}
	if err := s.repo.Retry(r.Context(), r.PathValue("id")); err != nil {
		s.repoError(w, err)
		return
	}
	s.get(w, r)
}
func (s *Server) modeAllowed(w http.ResponseWriter, mode string) bool {
	switch mode {
	case "split", "demo":
		return true
	case "openai", "montage":
		if s.cfg.APIKey != "" {
			return true
		}
		s.fail(w, 503, "Для анализа видео добавьте OPENAI_API_KEY в .env и перезапустите приложение.")
	default:
		s.fail(w, 400, "unknown processing mode")
	}
	return false
}

func (s *Server) reprocess(w http.ResponseWriter, r *http.Request) {
	job, ok := s.job(w, r)
	if !ok {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		s.fail(w, 415, "expected application/json")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		s.fail(w, 400, "invalid request body")
		return
	}
	if !s.modeAllowed(w, body.Mode) {
		return
	}
	if err := s.repo.Reprocess(r.Context(), job.ID, body.Mode); err != nil {
		s.repoError(w, err)
		return
	}
	job, err = s.repo.Get(r.Context(), job.ID)
	if err != nil {
		s.repoError(w, err)
		return
	}
	s.json(w, 202, job)
}

func (s *Server) reexport(w http.ResponseWriter, r *http.Request) {
	job, ok := s.job(w, r)
	if !ok {
		return
	}
	if job.Mode == "montage" {
		s.fail(w, 409, "Эдит состоит из нескольких сцен. Для нового варианта используйте повторную обработку.")
		return
	}
	var body struct {
		Start *float64 `json:"start"`
		End   *float64 `json:"end"`
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		s.fail(w, 415, "expected application/json")
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Start == nil || body.End == nil {
		s.fail(w, 400, "start and end must be numbers")
		return
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		s.fail(w, 400, "invalid request body")
		return
	}
	if err := domain.ValidateClip(domain.Clip{Start: *body.Start, End: *body.End}, job.Duration); err != nil {
		s.fail(w, 400, err.Error())
		return
	}
	if err := s.repo.Reexport(r.Context(), job.ID, r.PathValue("clipID"), *body.Start, *body.End); err != nil {
		s.repoError(w, err)
		return
	}
	job, err = s.repo.Get(r.Context(), job.ID)
	if err != nil {
		s.repoError(w, err)
		return
	}
	s.json(w, 202, job)
}
func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	job, ok := s.job(w, r)
	if !ok {
		return
	}
	for _, clip := range job.Clips {
		if clip.ID != r.PathValue("clipID") {
			continue
		}
		if clip.Path == "" {
			s.fail(w, 409, "clip is not ready")
			return
		}
		path, err := s.files.Path(clip.Path)
		if err != nil {
			s.internal(w, err)
			return
		}
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			s.fail(w, 404, "clip file not found")
			return
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			s.internal(w, err)
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"clip-%s.mp4\"", clip.ID))
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return
	}
	s.fail(w, 404, "clip not found")
}
func (s *Server) repoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.fail(w, 404, "job or clip not found")
	case errors.Is(err, store.ErrConflict):
		s.fail(w, 409, "operation is not available for this job or clip boundaries overlap")
	default:
		s.internal(w, err)
	}
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	s.fail(w, 500, "internal server error")
}
func (s *Server) fail(w http.ResponseWriter, status int, message string) {
	s.json(w, status, map[string]string{"error": message})
}
func (s *Server) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.log.Debug("response interrupted", "error", err)
	}
}
