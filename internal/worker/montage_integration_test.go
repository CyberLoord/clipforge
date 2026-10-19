package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/ai"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/media"
	"github.com/CyberLoord/clipforge/internal/store"
)

func TestMontagePipelineIntegration(t *testing.T) {
	if os.Getenv("MEDIA_TEST") != "1" {
		t.Skip("set MEDIA_TEST=1 to run FFmpeg integration test")
	}
	ffmpeg, ffprobe := os.Getenv("FFMPEG_PATH"), os.Getenv("FFPROBE_PATH")
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	source := filepath.Join(dataDir, "source.mp4")
	command := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=320x180:r=15:d=5[a];color=c=green:s=320x180:r=15:d=5[b];color=c=blue:s=320x180:r=15:d=5[c];[a][b][c]concat=n=3:v=1:a=0",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=15",
		"-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v: %s", err, output)
	}
	plans := []domain.Clip{
		{Title: "Смена цветов", Reason: "Короткие планы в обратном порядке", Style: "dynamic", Cuts: []domain.Cut{{Start: 10, End: 12}, {Start: 1, End: 3}, {Start: 6, End: 8}}},
		{Title: "Два цвета", Reason: "Два продолжительных плана", Style: "cinematic", Cuts: []domain.Cut{{Start: 0, End: 4}, {Start: 10, End: 14}}},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/chat/completions" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer integration-key" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "integration-vision" || len(payload.Messages) != 2 || payload.Messages[1].Role != "user" {
			t.Errorf("invalid vision request model=%s messages=%d", payload.Model, len(payload.Messages))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var parts []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Image struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(payload.Messages[1].Content, &parts); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(parts) != 31 {
			t.Errorf("expected 15 timestamped frames, got %d parts", len(parts))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		previous := -1.0
		colors := map[string]int{}
		for i := 1; i < len(parts); i += 2 {
			label, visual := parts[i], parts[i+1]
			timestamp, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(label.Text, "Frame at "), " seconds"), 64)
			if label.Type != "text" || err != nil || timestamp <= previous || timestamp >= 15 {
				t.Errorf("invalid frame timestamp: %q", label.Text)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			previous = timestamp
			encoded, ok := strings.CutPrefix(visual.Image.URL, "data:image/jpeg;base64,")
			frame, err := base64.StdEncoding.DecodeString(encoded)
			if visual.Type != "image_url" || !ok || err != nil || len(frame) > 256<<10 {
				t.Error("invalid encoded frame")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			picture, err := jpeg.Decode(bytes.NewReader(frame))
			if err != nil || picture.Bounds().Dx() > 640 || picture.Bounds().Dy() > 640 {
				t.Errorf("invalid JPEG frame: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r, g, b, _ := picture.At(picture.Bounds().Dx()/2, picture.Bounds().Dy()/2).RGBA()
			switch {
			case r > 50000 && g < 5000 && b < 5000:
				colors["red"]++
			case g > 20000 && r < 5000 && b < 5000:
				colors["green"]++
			case b > 50000 && r < 5000 && g < 5000:
				colors["blue"]++
			default:
				t.Errorf("unexpected preview color: %d/%d/%d", r, g, b)
			}
		}
		if colors["red"] != 5 || colors["green"] != 5 || colors["blue"] != 5 {
			t.Errorf("preview frames do not match source: %v", colors)
		}
		selected := make([]map[string]any, 0, len(plans))
		for _, plan := range plans {
			selected = append(selected, map[string]any{"title": plan.Title, "reason": plan.Reason, "style": plan.Style, "cuts": plan.Cuts})
		}
		content, err := json.Marshal(map[string]any{"clips": selected})
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	queue, unusedSpeech := &fakeQueue{}, &fakeAI{}
	processor := media.New(ffmpeg, ffprobe)
	provider := ai.NewOpenAI("integration-key", server.URL, "integration-vision")
	w := New(queue, processor, unusedSpeech, unusedSpeech, provider, Config{
		DataDir: dataDir, LeaseDuration: time.Minute, StageTimeout: time.Minute, Workers: 1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lease := store.Lease{Token: domain.NewID(), Job: domain.Job{
		ID: domain.NewID(), Stage: "probe", Status: "running", Mode: "montage", Attempts: 1, SourcePath: source,
	}}
	w.process(ctx, lease)
	if queue.failure != "" || !reflect.DeepEqual(queue.stages, []string{"select", "render"}) {
		t.Fatalf("stages=%v failure=%q", queue.stages, queue.failure)
	}
	if requests.Load() != 1 || unusedSpeech.transcribes != 0 || unusedSpeech.selects != 0 || len(queue.completed) != len(plans) {
		t.Fatalf("requests=%d speech=%+v outputs=%d", requests.Load(), unusedSpeech, len(queue.completed))
	}
	for i, clip := range queue.completed {
		plan := plans[i]
		if clip.Title != plan.Title || clip.Reason != plan.Reason || clip.Style != plan.Style || !reflect.DeepEqual(clip.Cuts, plan.Cuts) {
			t.Fatalf("saved plan changed: %+v", clip)
		}
		if !domain.ValidID(clip.ID) || clip.Revision != 1 || clip.Start != 0 || clip.End != 0 || filepath.IsAbs(clip.Path) || !strings.HasPrefix(clip.Path, "jobs/"+lease.Job.ID+"/") || !strings.HasSuffix(clip.Path, "-"+lease.Token+".mp4") {
			t.Fatalf("invalid saved output: %+v", clip)
		}
		info, err := processor.Probe(ctx, filepath.Join(dataDir, filepath.FromSlash(clip.Path)))
		var duration float64
		for _, cut := range plan.Cuts {
			duration += cut.End - cut.Start
		}
		if err != nil || info.Width != 540 || info.Height != 960 || math.Abs(info.Duration-duration) > 0.15 {
			t.Fatalf("output=%+v expected duration=%.3f error=%v", info, duration, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "jobs", lease.Job.ID))
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected output files: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("montage pipeline created audio extraction directory: %v", err)
	}
}
