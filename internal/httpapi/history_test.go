package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/storage"
)

func historyHandler(t *testing.T, repo *fakeRepo) http.Handler {
	t.Helper()
	files, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(repo, files, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error { return nil })
}

func TestHistoryRequiresExistingJob(t *testing.T) {
	id := domain.NewID()
	repo := &fakeRepo{jobs: []domain.Job{{ID: id}}}
	handler := historyHandler(t, repo)
	for _, requested := range []string{"invalid", domain.NewID()} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+requested+"/events", nil))
		if rec.Code != http.StatusNotFound || repo.eventsRequested != "" {
			t.Fatalf("history read before checking job: status=%d requested=%q", rec.Code, repo.eventsRequested)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+id+"/events", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" || repo.eventsRequested != id {
		t.Fatalf("legacy job without history: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistoryIncludesRetryAndFailure(t *testing.T) {
	id := domain.NewID()
	repo := &fakeRepo{jobs: []domain.Job{{ID: id}}, events: []domain.JobEvent{
		{ID: id + ":3", JobID: id, Sequence: 3, Type: "job.queued", Status: "queued", Stage: "select", Attempt: 1, Error: "provider unavailable", OccurredAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)},
		{ID: id + ":4", JobID: id, Sequence: 4, Type: "stage.started", Status: "running", Stage: "select", Attempt: 2, OccurredAt: time.Date(2026, 11, 9, 10, 0, 2, 0, time.UTC)},
	}}
	rec := httptest.NewRecorder()
	historyHandler(t, repo).ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+id+"/events", nil))
	var events []domain.JobEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(events) != 2 || events[0].Error != "provider unavailable" || events[1].Attempt != 2 || events[0].Sequence >= events[1].Sequence {
		t.Fatalf("incomplete history: status=%d events=%+v", rec.Code, events)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("history can be stale in a browser cache")
	}
}

func TestHistoryProjectionErrors(t *testing.T) {
	id := domain.NewID()
	repo := &fakeRepo{jobs: []domain.Job{{ID: id}}, eventsError: errors.New("secret database detail"), deadLettersError: errors.New("secret broker detail")}
	handler := historyHandler(t, repo)
	for _, path := range []string{"/api/jobs/" + id + "/events", "/api/dead-letters"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("projection failure: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestDeadLetterDiagnostics(t *testing.T) {
	repo := &fakeRepo{}
	handler := historyHandler(t, repo)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/dead-letters", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty diagnostics: status=%d body=%s", rec.Code, rec.Body.String())
	}
	repo.deadLetters = []domain.DeadLetter{{ID: "analysis:1:12", Kind: "invalid_message", Error: "invalid command", SourceTopic: domain.AnalysisTopic, Partition: 1, Offset: 12}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/dead-letters", nil))
	var letters []domain.DeadLetter
	if err := json.Unmarshal(rec.Body.Bytes(), &letters); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(letters) != 1 || letters[0].Offset != 12 || letters[0].SourceTopic != domain.AnalysisTopic {
		t.Fatalf("missing diagnostic source: %+v", letters)
	}
}

func TestHealthReportsDependencyFailure(t *testing.T) {
	files, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	handler := New(&fakeRepo{}, files, config.Config{}, slog.Default(), func(ctx context.Context) error {
		checked = true
		if _, ok := ctx.Deadline(); !ok {
			t.Error("readiness check has no deadline")
		}
		return errors.New("Kafka broker address is private")
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if !checked || rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "dependencies unavailable") || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("readiness response: checked=%v status=%d body=%s", checked, rec.Code, rec.Body.String())
	}
}
