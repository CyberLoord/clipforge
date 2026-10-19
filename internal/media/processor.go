package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

type Processor struct {
	ffmpeg  string
	ffprobe string
}

func New(ffmpeg, ffprobe string) *Processor {
	return &Processor{ffmpeg: ffmpeg, ffprobe: ffprobe}
}

func (p *Processor) Probe(ctx context.Context, path string) (domain.Info, error) {
	input, err := localFile(path)
	if err != nil {
		return domain.Info{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var output limitedBuffer
	output.limit = 64 << 10
	err = run(ctx, p.ffprobe, "", &output,
		"-v", "error", "-protocol_whitelist", "file", "-f", "mov", "-i", input,
		"-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json")
	if err != nil {
		return domain.Info{}, err
	}
	if output.truncated {
		return domain.Info{}, errors.New("ffprobe output exceeds 64 KiB")
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return domain.Info{}, fmt.Errorf("decode ffprobe: %w", err)
	}
	duration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return domain.Info{}, errors.New("video has no valid duration")
	}
	info := domain.Info{Duration: duration}
	for _, stream := range result.Streams {
		if stream.Type == "video" && info.Width == 0 {
			info.Width, info.Height = stream.Width, stream.Height
		}
	}
	if info.Width <= 0 || info.Height <= 0 {
		return domain.Info{}, errors.New("file contains no video stream")
	}
	if info.Width > 8192 || info.Height > 8192 || int64(info.Width)*int64(info.Height) > 33554432 {
		return domain.Info{}, errors.New("video resolution exceeds 8K")
	}
	return info, nil
}

func (p *Processor) Extract(ctx context.Context, input, output string) error {
	input, err := localFile(input)
	if err != nil {
		return err
	}
	return atomicOutput(output, ".mp3", func(temp string) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		err := run(ctx, p.ffmpeg, "", io.Discard,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-threads", "2",
			"-protocol_whitelist", "file", "-f", "mov", "-i", input,
			"-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1",
			"-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "32k", "-f", "mp3", temp)
		if err != nil {
			return err
		}
		info, err := os.Stat(temp)
		if err != nil {
			return err
		}
		if info.Size() == 0 || info.Size() > 24<<20 {
			return errors.New("extracted audio must be nonempty and smaller than 24 MiB")
		}
		return nil
	})
}

func (p *Processor) Render(ctx context.Context, input, output string, segments []domain.Segment, clip domain.Clip) error {
	if err := domain.ValidateClip(clip, clip.End); err != nil {
		return err
	}
	input, err := localFile(input)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	dir := ""
	filter := "scale=w='max(2,trunc(min(540,960*dar)/2)*2)':h='max(2,trunc(min(960,540/dar)/2)*2)',setsar=1,pad=540:960:(ow-iw)/2:(oh-ih)/2:color=0x16171b"
	if len(segments) > 0 {
		dir, err = os.MkdirTemp(filepath.Dir(output), ".subtitles-"+filepath.Base(output)+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, "subtitles.ass"), []byte(subtitles(segments, clip)), 0600); err != nil {
			return fmt.Errorf("write subtitles: %w", err)
		}
		filter += ",ass=subtitles.ass"
	}
	return atomicOutput(output, ".mp4", func(temp string) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		return run(ctx, p.ffmpeg, dir, io.Discard,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-threads", "2",
			"-ss", seconds(clip.Start), "-protocol_whitelist", "file", "-f", "mov", "-i", input,
			"-t", seconds(clip.End-clip.Start), "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1",
			"-vf", filter, "-filter_threads", "1", "-c:v", "libx264", "-threads", "2", "-preset", "veryfast", "-crf", "23",
			"-pix_fmt", "yuv420p", "-r", "30", "-c:a", "aac", "-b:a", "96k", "-ac", "2",
			"-movflags", "+faststart", "-f", "mp4", temp)
	})
}

func localFile(path string) (string, error) {
	if strings.Contains(path, "://") || strings.ContainsRune(path, 0) {
		return "", errors.New("media input must be a local file")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("media input must be a regular file")
	}
	return path, nil
}

func atomicOutput(path, extension string, write func(string) error) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".render-"+filepath.Base(path)+"-*"+extension)
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Close(); err != nil {
		return err
	}
	if err := write(temp); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func run(ctx context.Context, binary, dir string, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Stdout = dir, stdout
	stderr := &limitedBuffer{limit: 8 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", filepath.Base(binary), ctx.Err())
		}
		return fmt.Errorf("%s: %w: %s", filepath.Base(binary), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if n > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}

func seconds(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}
