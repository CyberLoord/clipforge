package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/storage"
	"github.com/CyberLoord/clipforge/internal/store"
)

type fakeRepo struct {
	jobs               []domain.Job
	createError        error
	persistBeforeError bool
}

func (f *fakeRepo) Create(_ context.Context, j *domain.Job) error {
	if f.createError != nil {
		if f.persistBeforeError {
			f.jobs = append(f.jobs, *j)
		}
		return f.createError
	}
	f.jobs = append(f.jobs, *j)
	return nil
}
func (f *fakeRepo) List(context.Context) ([]domain.Job, error) { return f.jobs, nil }
func (f *fakeRepo) Get(context.Context, string) (domain.Job, error) {
	if len(f.jobs) == 0 {
		return domain.Job{}, store.ErrNotFound
	}
	return f.jobs[0], nil
}
func (f *fakeRepo) Cancel(context.Context, string) error                             { return nil }
func (f *fakeRepo) Retry(context.Context, string) error                              { return nil }
func (f *fakeRepo) Reexport(context.Context, string, string, float64, float64) error { return nil }

func TestUpload(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, bytes.Repeat([]byte{'x'}, 32)...)
	for _, tt := range []struct {
		name, filename string
		payload        []byte
		limit          int64
		status         int
		dbError        bool
	}{
		{"accepted", "lecture.mp4", mp4, 100, 202, false},
		{"wrong extension", "lecture.txt", mp4, 100, 415, false},
		{"bad container", "lecture.mp4", []byte("not an mp4 at all"), 100, 415, false},
		{"oversized", "lecture.mp4", mp4, 20, 413, false},
		{"db failure keeps uncertain source", "lecture.mp4", mp4, 100, 500, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files, err := storage.New(root)
			if err != nil {
				t.Fatal(err)
			}
			repo := &fakeRepo{}
			if tt.dbError {
				repo.createError = errors.New("unavailable")
			}
			server := New(repo, files, config.Config{Mode: "demo", MaxUploadBytes: tt.limit}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", tt.filename)
			if err != nil {
				t.Fatal(err)
			}
			part.Write(tt.payload)
			writer.Close()
			req := httptest.NewRequest(http.MethodPost, "/api/jobs", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
			}
			if tt.status == 202 {
				if len(repo.jobs) != 1 || repo.jobs[0].Status != "queued" {
					t.Fatal("job not queued")
				}
				data, err := os.ReadFile(repo.jobs[0].SourcePath)
				if err != nil || !bytes.Equal(data, tt.payload) {
					t.Fatalf("saved file mismatch: %v", err)
				}
				if strings.Contains(rec.Body.String(), root) {
					t.Fatal("response leaks source path")
				}
			} else {
				if len(repo.jobs) != 0 {
					t.Fatal("invalid upload created job")
				}
				filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						t.Fatal(err)
					}
					if !d.IsDir() && !tt.dbError {
						t.Errorf("leftover %s", path)
					}
					return nil
				})
			}
		})
	}
}
func TestCrossOriginWrite(t *testing.T) {
	files, _ := storage.New(t.TempDir())
	server := New(&fakeRepo{}, files, config.Config{}, slog.Default(), func(context.Context) error { return nil })
	req := httptest.NewRequest("POST", "http://localhost:8080/api/jobs", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestUploadWithLostInsertResponse(t *testing.T) {
	root := t.TempDir()
	files, _ := storage.New(root)
	repo := &fakeRepo{createError: errors.New("connection closed"), persistBeforeError: true}
	handler := New(repo, files, config.Config{Mode: "demo", MaxUploadBytes: 100}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "video.mp4")
	part.Write([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	writer.Close()
	req := httptest.NewRequest("POST", "/api/jobs", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 202 || len(repo.jobs) != 1 {
		t.Fatalf("status=%d jobs=%d", rec.Code, len(repo.jobs))
	}
	if _, err := os.Stat(repo.jobs[0].SourcePath); err != nil {
		t.Fatalf("committed job lost its source: %v", err)
	}
}

func TestConfigExposesClipLimits(t *testing.T) {
	files, _ := storage.New(t.TempDir())
	handler := New(&fakeRepo{}, files, config.Config{}, slog.Default(), func(context.Context) error { return nil })
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/config", nil))
	var limits struct {
		Min float64 `json:"min_clip_seconds"`
		Max float64 `json:"max_clip_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &limits); err != nil {
		t.Fatal(err)
	}
	if limits.Min != 15 || limits.Max != 60 {
		t.Fatalf("unexpected limits: %+v", limits)
	}
}
