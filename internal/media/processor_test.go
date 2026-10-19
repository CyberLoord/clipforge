package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestSubtitlesClipAndEscape(t *testing.T) {
	segments := []domain.Segment{
		{Start: 0, End: 4, Text: "before"},
		{Start: 8, End: 13, Text: "Привет {\\pos(0,0)}\\N"},
		{Start: 29, End: 40, Text: "last"},
		{Start: 45, End: 50, Text: "after"},
	}
	result := subtitles(segments, domain.Clip{Start: 10, End: 30})
	if strings.Contains(result, "before") || strings.Contains(result, "after") || strings.Contains(result, "\\pos") || strings.Contains(result, "\\N") {
		t.Fatalf("unexpected subtitle contents: %s", result)
	}
	if !strings.Contains(result, "Dialogue: 0,0:00:00.00,0:00:03.00") || !strings.Contains(result, "Dialogue: 0,0:00:19.00,0:00:20.00") || !strings.Contains(result, "Привет") {
		t.Fatalf("incorrect clipped timestamps: %s", result)
	}
}

func TestLongCaptionsAreSplit(t *testing.T) {
	text := strings.Repeat("длинная фраза ", 12)
	result := subtitles([]domain.Segment{{Start: 0, End: 20, Text: text}}, domain.Clip{Start: 0, End: 20})
	if strings.Count(result, "Dialogue: ") < 3 || !strings.Contains(result, "0:00:20.00") {
		t.Fatalf("caption was not split: %s", result)
	}
}

func TestSubtitlesKeepTimingAfterTrim(t *testing.T) {
	first := strings.Repeat("a", 40)
	second := strings.Repeat("b", 40)
	third := strings.Repeat("c", 40)
	segments := []domain.Segment{{Start: 0, End: 30, Text: first + " " + second + " " + third}}
	result := subtitles(segments, domain.Clip{Start: 10, End: 30})
	if strings.Contains(result, first) {
		t.Fatal("caption before clip start was moved into the clip")
	}
	if !strings.Contains(result, "0:00:00.00,0:00:10.00,Default,,0,0,0,,"+second) || !strings.Contains(result, "0:00:10.00,0:00:20.00,Default,,0,0,0,,"+third) {
		t.Fatalf("caption timing changed after trim: %s", result)
	}
}

func TestAtomicOutputFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("render failed")
	err := atomicOutput(path, ".mp4", func(temp string) error {
		if err := os.WriteFile(temp, []byte("partial output"), 0600); err != nil {
			t.Fatal(err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	files, _ := os.ReadDir(dir)
	if string(data) != "previous" || len(files) != 1 {
		t.Fatalf("existing output changed or temporary file leaked: %s %v", data, files)
	}
}

func TestCommandCancellation(t *testing.T) {
	t.Setenv("CLIPFORGE_TEST_PROCESS", "sleep")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = run(ctx, executable, "", io.Discard, "-test.run=TestMediaProcess")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation did not stop process: %v", err)
	}
}

func TestCommandFailureBoundsStderr(t *testing.T) {
	t.Setenv("CLIPFORGE_TEST_PROCESS", "failure")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), executable, "", io.Discard, "-test.run=TestMediaProcess")
	if err == nil {
		t.Fatal("expected process failure")
	}
	if !strings.Contains(err.Error(), "exit status 7") || len(err.Error()) > 9000 {
		t.Fatalf("unexpected stderr handling: length=%d error=%.100s", len(err.Error()), err)
	}
}

func TestMediaProcess(t *testing.T) {
	switch os.Getenv("CLIPFORGE_TEST_PROCESS") {
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "failure":
		_, _ = os.Stderr.WriteString(strings.Repeat("failure ", 20000))
		os.Exit(7)
	}
}

func TestInputRejectsProtocolsAndDirectories(t *testing.T) {
	for _, path := range []string{"https://example.com/video.mp4", "concat:https://example.com/video.mp4", t.TempDir()} {
		if _, err := New("ffmpeg", "ffprobe").Probe(context.Background(), path); err == nil {
			t.Fatalf("accepted nonlocal input %q", path)
		}
	}
}

func TestMediaSmoke(t *testing.T) {
	if os.Getenv("MEDIA_TEST") != "1" {
		t.Skip("set MEDIA_TEST=1 to run FFmpeg integration test")
	}
	ffmpeg := os.Getenv("FFMPEG_PATH")
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	ffprobe := os.Getenv("FFPROBE_PATH")
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	for _, binary := range []string{ffmpeg, ffprobe} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("required media binary: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "video's [source], : folder")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.mp4")
	err := run(ctx, ffmpeg, "", io.Discard,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0x506070:s=320x180:r=15:d=22",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=22",
		"-c:v", "libx264", "-threads", "2", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", source)
	if err != nil {
		t.Fatal(err)
	}
	processor := New(ffmpeg, ffprobe)
	info, err := processor.Probe(ctx, source)
	if err != nil || math.Abs(info.Duration-22) > 0.2 || info.Width != 320 || info.Height != 180 {
		t.Fatalf("source=%+v error=%v", info, err)
	}
	audio := filepath.Join(dir, "audio.mp3")
	if err := processor.Extract(ctx, source, audio); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(audio)
	if err != nil || stat.Size() < 100 || stat.Size() > 24<<20 {
		t.Fatalf("invalid audio: %v %v", stat, err)
	}
	output := filepath.Join(dir, "clip.mp4")
	segments := []domain.Segment{{Start: 0, End: 8, Text: "Проверка русских субтитров"}, {Start: 8, End: 22, Text: "Кадр полностью помещается в вертикальное видео."}}
	if err := processor.Render(ctx, source, output, segments, domain.Clip{Start: 1, End: 21}); err != nil {
		t.Fatal(err)
	}
	info, err = processor.Probe(ctx, output)
	if err != nil || info.Width != 540 || info.Height != 960 || math.Abs(info.Duration-20) > 0.15 {
		t.Fatalf("output=%+v error=%v", info, err)
	}
	var probeOutput limitedBuffer
	probeOutput.limit = 64 << 10
	if err := run(ctx, ffprobe, "", &probeOutput, "-v", "error", "-show_entries", "stream=codec_name,pix_fmt", "-of", "json", output); err != nil {
		t.Fatal(err)
	}
	var streams struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Format string `json:"pix_fmt"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(probeOutput.Bytes(), &streams); err != nil {
		t.Fatal(err)
	}
	if len(streams.Streams) != 2 || streams.Streams[0].Codec != "h264" || streams.Streams[0].Format != "yuv420p" || streams.Streams[1].Codec != "aac" {
		t.Fatalf("unexpected output codecs: %+v", streams)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 3 {
		t.Fatalf("temporary output leaked: %v %v", files, err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := processor.Render(cancelled, source, filepath.Join(dir, "cancelled.mp4"), segments, domain.Clip{Start: 1, End: 21}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled render, got %v", err)
	}
	files, _ = os.ReadDir(dir)
	if len(files) != 3 {
		t.Fatalf("cancelled output leaked: %v", files)
	}
	t.Run("without audio or transcript", func(t *testing.T) {
		dir := t.TempDir()
		silent := filepath.Join(dir, "silent.mp4")
		if err := run(ctx, ffmpeg, "", io.Discard, "-nostdin", "-v", "error", "-i", source, "-t", "15", "-c:v", "copy", "-an", silent); err != nil {
			t.Fatal(err)
		}
		if info, err := processor.Probe(ctx, silent); err != nil || math.Abs(info.Duration-15) > 0.15 {
			t.Fatalf("silent source=%+v error=%v", info, err)
		}
		if err := processor.Extract(ctx, silent, filepath.Join(dir, "audio.mp3")); err == nil {
			t.Fatal("extracted nonexistent audio")
		}
		output := filepath.Join(dir, "clip.mp4")
		if err := processor.Render(ctx, silent, output, nil, domain.Clip{Start: 5, End: 10}); err != nil {
			t.Fatal(err)
		}
		info, err := processor.Probe(ctx, output)
		if err != nil || info.Width != 540 || info.Height != 960 || math.Abs(info.Duration-5) > 0.15 {
			t.Fatalf("silent clip=%+v error=%v", info, err)
		}
		var streamOutput limitedBuffer
		streamOutput.limit = 64 << 10
		if err := run(ctx, ffprobe, "", &streamOutput, "-v", "error", "-select_streams", "a", "-show_entries", "stream=codec_type", "-of", "json", output); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(streamOutput.String(), "audio") {
			t.Fatal("silent clip contains fabricated audio")
		}
		var pixels limitedBuffer
		pixels.limit = 476 * 160
		if err := run(ctx, ffmpeg, "", &pixels, "-nostdin", "-v", "error", "-ss", "1", "-i", output, "-frames:v", "1", "-vf", "crop=476:160:32:740,format=gray", "-f", "rawvideo", "pipe:1"); err != nil {
			t.Fatal(err)
		}
		if len(pixels.Bytes()) != pixels.limit || pixels.truncated {
			t.Fatal("could not inspect caption area")
		}
		low, high := byte(255), byte(0)
		for _, pixel := range pixels.Bytes() {
			low, high = min(low, pixel), max(high, pixel)
		}
		if high-low > 3 {
			t.Fatalf("empty caption area is not uniform: %d..%d", low, high)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 2 {
			t.Fatalf("unexpected temporary files: %v", files)
		}
	})
}
