package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestTranscribeMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		for field, want := range map[string]string{"model": "whisper-1", "response_format": "verbose_json", "timestamp_granularities[]": "segment"} {
			if got := r.FormValue(field); got != want {
				t.Errorf("%s: got %q, want %q", field, got, want)
			}
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if string(data) != "audio fixture" {
			t.Errorf("audio changed: %q", data)
		}
		_, _ = io.WriteString(w, `{"text":"Привет","segments":[{"id":0,"start":0,"end":5,"text":" Привет "}]}`)
	}))
	defer server.Close()
	segments, err := NewOpenAI("test-key", server.URL, "test-model").Transcribe(context.Background(), audioFixture(t), 30)
	if err != nil || len(segments) != 1 || segments[0].Text != "Привет" {
		t.Fatalf("segments=%v error=%v", segments, err)
	}
}

func TestTranscribeRejectsMalformedData(t *testing.T) {
	for _, response := range []string{
		`{"segments":[]}`, `not json`, `{"segments":[{"end":5,"text":"missing start"}]}`,
		`{"segments":[{"start":5,"end":2,"text":"backwards"}]}`,
		`{"segments":[{"start":0,"end":31,"text":"too long"}]}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := responseServer(response, http.StatusOK)
			defer server.Close()
			_, err := NewOpenAI("test", server.URL, "test").Transcribe(context.Background(), audioFixture(t), 30)
			if err == nil || IsTemporary(err) {
				t.Fatalf("expected permanent validation error, got %v", err)
			}
		})
	}
}

func TestSelectionRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model  string `json:"model"`
			Format struct {
				Type   string `json:"type"`
				Schema struct {
					Strict bool           `json:"strict"`
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/chat/completions" || payload.Model != "test-model" || payload.Format.Type != "json_schema" || !payload.Format.Schema.Strict || payload.Format.Schema.Schema["additionalProperties"] != false {
			t.Errorf("unexpected selection request: %+v", payload)
		}
		_, _ = io.WriteString(w, selectionResponse(`{"clips":[{"start":0,"end":25,"title":"Первый","reason":"Законченная мысль"}]}`))
	}))
	defer server.Close()
	clips, err := NewOpenAI("test", server.URL, "test-model").Select(context.Background(), transcriptFixture(), 60)
	if err != nil || len(clips) != 1 || clips[0].End != 25 || clips[0].Title != "Первый" {
		t.Fatalf("clips=%v error=%v", clips, err)
	}
}

func TestSelectionRejectsMalformedData(t *testing.T) {
	for _, response := range []string{
		`{}`, `{"choices":[]}`, `{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"refusal":"declined","content":"{}"}}]}`,
		selectionResponse(`{"clips":[{"start":0,"end":4.999,"title":"t","reason":"r"}]}`),
		selectionResponse(`{"clips":[{"start":0,"end":61,"title":"t","reason":"r"}]}`),
		selectionResponse(`{"clips":[{"end":25,"title":"t","reason":"r"}]}`),
		selectionResponse(`{"clips":[{"start":0,"end":25,"title":"t","reason":"r","extra":true}]}`),
		selectionResponse(`{"clips":[{"start":0,"end":25,"title":"t","reason":"r"},{"start":0,"end":25,"title":"t","reason":"r"}]}`),
		selectionResponse(`{"clips":[]} {"clips":[]}`),
	} {
		t.Run(response, func(t *testing.T) {
			server := responseServer(response, http.StatusOK)
			defer server.Close()
			_, err := NewOpenAI("test", server.URL, "test").Select(context.Background(), transcriptFixture(), 60)
			if err == nil || IsTemporary(err) {
				t.Fatalf("expected permanent validation error, got %v", err)
			}
		})
	}
}

func TestSelectionAllowsShortExcerpts(t *testing.T) {
	for _, content := range []string{
		`{"clips":[{"start":0,"end":6,"title":"Одна мысль","reason":"Законченный ответ"}]}`,
		`{"clips":[{"start":0,"end":5,"title":"Первая","reason":"Начало"},{"start":5,"end":10,"title":"Вторая","reason":"Пример"},{"start":10,"end":17.642667,"title":"Третья","reason":"Вывод"}]}`,
	} {
		server := responseServer(selectionResponse(content), http.StatusOK)
		defer server.Close()
		transcript := []domain.Segment{{Start: 0, End: 17.642667, Text: "Короткая расшифровка"}}
		clips, err := NewOpenAI("test", server.URL, "test").Select(context.Background(), transcript, 17.642667)
		if err != nil || len(clips) == 0 {
			t.Fatalf("short selection rejected: clips=%v error=%v", clips, err)
		}
	}
}

func TestAPIErrorClassification(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := responseServer("secret body", status)
			defer server.Close()
			_, err := NewOpenAI("test", server.URL, "test").Select(context.Background(), transcriptFixture(), 60)
			if err == nil || strings.Contains(err.Error(), "secret body") {
				t.Fatalf("unsafe API error: %v", err)
			}
			if IsTemporary(err) != (status == 429 || status >= 500) {
				t.Fatalf("unexpected retry classification: %v", err)
			}
		})
	}
}

func TestRequestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := NewOpenAI("test", server.URL, "test").Select(ctx, transcriptFixture(), 60)
	if !errors.Is(err, context.DeadlineExceeded) || !IsTemporary(err) {
		t.Fatalf("expected retryable timeout, got %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = NewOpenAI("test", server.URL, "test").Select(ctx, transcriptFixture(), 60)
	if !errors.Is(err, context.Canceled) || IsTemporary(err) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestDemoDurationBoundaries(t *testing.T) {
	for _, duration := range []float64{15, 17.642667, 20, 20.1, 29.9, 30, 39.9, 40, 44.9, 45, 59.9, 60, 1800} {
		demo := NewDemo()
		segments, err := demo.Transcribe(context.Background(), "", duration)
		if err != nil {
			t.Fatal(err)
		}
		clips, err := demo.Select(context.Background(), segments, duration)
		if err != nil {
			t.Fatalf("duration=%g: %v", duration, err)
		}
		if len(clips) != 3 || !strings.Contains(segments[0].Text, "Демо") {
			t.Fatalf("invalid demo: %v", clips)
		}
		if duration < 30 && (clips[0].Start != 0 || clips[2].End != duration || clips[0].End >= duration) {
			t.Fatalf("short demo should produce three excerpts: duration=%g clips=%v", duration, clips)
		}
	}
}

func TestDemoRejectsVideoBelowMinimum(t *testing.T) {
	demo := NewDemo()
	if _, err := demo.Transcribe(context.Background(), "", 14.999); err == nil {
		t.Fatal("accepted video shorter than 15 seconds")
	}
	segments := []domain.Segment{{Start: 0, End: 14.999, Text: "Демо"}}
	if _, err := demo.Select(context.Background(), segments, 14.999); err == nil {
		t.Fatal("accepted source video shorter than 15 seconds")
	}
}

func audioFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(path, []byte("audio fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func transcriptFixture() []domain.Segment {
	return []domain.Segment{{Start: 0, End: 30, Text: "Тестовая расшифровка"}}
}

func responseServer(response string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
}

func selectionResponse(content string) string {
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
	return string(data)
}
