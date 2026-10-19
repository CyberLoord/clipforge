package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestSampleFramesRejectsInvalidDuration(t *testing.T) {
	processor := New("missing-ffmpeg", "missing-ffprobe")
	for _, duration := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := processor.SampleFrames(context.Background(), "missing.mp4", duration); err == nil {
			t.Fatalf("accepted duration %v", duration)
		}
	}
}

func TestMontageSmoke(t *testing.T) {
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
	dir := filepath.Join(t.TempDir(), "montage's [source], : folder")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.mp4")
	err := run(ctx, ffmpeg, "", io.Discard,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=320x180:r=15:d=15[a];color=c=green:s=320x180:r=15:d=15[b];color=c=blue:s=320x180:r=15:d=15[c];color=c=yellow:s=320x180:r=15:d=15[d];[a][b][c][d]concat=n=4:v=1:a=0",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=60",
		"-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", source)
	if err != nil {
		t.Fatal(err)
	}
	processor := New(ffmpeg, ffprobe)
	t.Run("sample timestamps and bounds", func(t *testing.T) {
		for _, duration := range []float64{17.642667, 60} {
			frames, err := processor.SampleFrames(ctx, source, duration)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != int(math.Min(48, math.Ceil(duration))) {
				t.Fatalf("unexpected sample count: %d", len(frames))
			}
			previous := -1.0
			for _, frame := range frames {
				if frame.Time <= previous || frame.Time >= duration || math.Abs(frame.Time*15-math.Round(frame.Time*15)) > 0.001 {
					t.Fatalf("invalid presentation timestamp: %.9f after %.9f", frame.Time, previous)
				}
				previous = frame.Time
				config, err := jpeg.DecodeConfig(bytes.NewReader(frame.JPEG))
				if err != nil || config.Width > 640 || config.Height > 640 || len(frame.JPEG) > 256<<10 {
					t.Fatalf("invalid preview frame: %+v %d bytes %v", config, len(frame.JPEG), err)
				}
			}
		}
	})
	cuts := []domain.Cut{{Start: 31, End: 33}, {Start: 1, End: 3}, {Start: 46, End: 48}}
	for _, hasAudio := range []bool{true, false} {
		name := "silent"
		if hasAudio {
			name = "audio"
		}
		t.Run(name, func(t *testing.T) {
			input := source
			if !hasAudio {
				input = filepath.Join(dir, "silent.mp4")
				if err := run(ctx, ffmpeg, "", io.Discard, "-nostdin", "-v", "error", "-i", source, "-c:v", "copy", "-an", input); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(dir, name+"-montage.mp4")
			if err := processor.RenderMontage(ctx, input, output, cuts); err != nil {
				t.Fatal(err)
			}
			info, err := processor.Probe(ctx, output)
			if err != nil || info.Width != 540 || info.Height != 960 || math.Abs(info.Duration-6) > 0.15 {
				t.Fatalf("montage=%+v error=%v", info, err)
			}
			var outputProbe limitedBuffer
			outputProbe.limit = 64 << 10
			if err := run(ctx, ffprobe, "", &outputProbe, "-v", "error", "-show_entries", "stream=codec_name,pix_fmt", "-of", "json", output); err != nil {
				t.Fatal(err)
			}
			var streams struct {
				Streams []struct {
					Codec  string `json:"codec_name"`
					Format string `json:"pix_fmt"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(outputProbe.Bytes(), &streams); err != nil {
				t.Fatal(err)
			}
			expectedStreams := 1
			if hasAudio {
				expectedStreams = 2
			}
			if len(streams.Streams) != expectedStreams || streams.Streams[0].Codec != "h264" || streams.Streams[0].Format != "yuv420p" || (hasAudio && streams.Streams[1].Codec != "aac") {
				t.Fatalf("unexpected montage streams: %+v", streams)
			}
			for i, color := range []string{"blue", "red", "yellow"} {
				frame, err := processor.sampleFrame(ctx, output, float64(i*2+1))
				if err != nil {
					t.Fatal(err)
				}
				image, err := jpeg.Decode(bytes.NewReader(frame.JPEG))
				if err != nil {
					t.Fatal(err)
				}
				r, g, b, _ := image.At(image.Bounds().Dx()/2, image.Bounds().Dy()/2).RGBA()
				valid := false
				switch color {
				case "blue":
					valid = b > 50000 && r < 5000 && g < 5000
				case "red":
					valid = r > 50000 && b < 5000 && g < 5000
				case "yellow":
					valid = r > 50000 && g > 50000 && b < 5000
				}
				if !valid {
					t.Fatalf("cut %d should be %s, got RGB %d/%d/%d", i, color, r, g, b)
				}
			}
		})
	}
	t.Run("one second and fractional cuts", func(t *testing.T) {
		output := filepath.Join(dir, "short-montage.mp4")
		short := []domain.Cut{{Start: 31.1, End: 32.1}, {Start: 1.2, End: 2.5}, {Start: 46.3, End: 49}}
		if err := processor.RenderMontage(ctx, source, output, short); err != nil {
			t.Fatal(err)
		}
		info, err := processor.Probe(ctx, output)
		if err != nil || math.Abs(info.Duration-5) > 0.15 {
			t.Fatalf("short montage=%+v error=%v", info, err)
		}
	})
	t.Run("invalid cuts preserve output", func(t *testing.T) {
		output := filepath.Join(dir, "previous.mp4")
		if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range [][]domain.Cut{
			{{Start: 0, End: 6}},
			{{Start: 1, End: 4}, {Start: 3, End: 6}},
			{{Start: 1, End: 4}, {Start: 59, End: 62}},
		} {
			if err := processor.RenderMontage(ctx, source, output, invalid); err == nil {
				t.Fatalf("accepted invalid cuts: %+v", invalid)
			}
		}
		content, err := os.ReadFile(output)
		if err != nil || string(content) != "previous" {
			t.Fatalf("changed existing output: %q %v", content, err)
		}
	})
	t.Run("failed render and cancellation cleanup", func(t *testing.T) {
		output := filepath.Join(dir, "preserved.mp4")
		if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := New("missing-ffmpeg", ffprobe).RenderMontage(ctx, source, output, cuts); err == nil {
			t.Fatal("accepted failed render")
		}
		cancelled, stop := context.WithCancel(ctx)
		stop()
		if _, err := processor.SampleFrames(cancelled, source, 60); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancelled samples, got %v", err)
		}
		if err := processor.RenderMontage(cancelled, source, output, cuts); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancelled montage, got %v", err)
		}
		active, interrupt := context.WithTimeout(ctx, 5*time.Second)
		defer interrupt()
		interrupted := make(chan bool, 1)
		go func() {
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-active.Done():
					interrupted <- false
					return
				case <-ticker.C:
					entries, _ := os.ReadDir(dir)
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".montage-preserved.mp4-") {
							interrupt()
							interrupted <- true
							return
						}
					}
				}
			}
		}()
		if err := processor.RenderMontage(active, source, output, cuts); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected interrupted montage, got %v", err)
		}
		if !<-interrupted {
			t.Fatal("did not interrupt rendering after creating temporary directory")
		}
		content, err := os.ReadFile(output)
		if err != nil || string(content) != "previous" {
			t.Fatalf("changed existing output: %q %v", content, err)
		}
	})
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".montage-") || strings.HasPrefix(file.Name(), ".render-") {
			t.Fatalf("temporary montage file leaked: %s", file.Name())
		}
	}
}
