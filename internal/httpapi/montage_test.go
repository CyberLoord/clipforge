package httpapi

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/storage"
)

func TestMontageCannotBeEditedAsSingleRange(t *testing.T) {
	id := domain.NewID()
	repo := &fakeRepo{jobs: []domain.Job{{ID: id, Mode: "montage", Status: "completed", Duration: 30}}}
	files, _ := storage.New(t.TempDir())
	handler := New(repo, files, config.Config{}, slog.Default(), func(context.Context) error { return nil })
	req := httptest.NewRequest("PATCH", "/api/jobs/"+id+"/clips/"+domain.NewID(), strings.NewReader(`{"start":0,"end":10}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
