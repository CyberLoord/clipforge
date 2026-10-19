package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

var frameTimestamp = regexp.MustCompile(`n:\s*0\s+pts:\s*-?\d+\s+pts_time:([\d.eE+-]+)`)

func (p *Processor) SampleFrames(ctx context.Context, input string, duration float64) ([]domain.Frame, error) {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return nil, errors.New("cannot sample video with invalid duration")
	}
	input, err := localFile(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	count := int(math.Min(48, math.Ceil(duration)))
	frames := make([]domain.Frame, 0, count)
	for i := range count {
		at := (float64(i) + 0.5) * duration / float64(count)
		frame, err := p.sampleFrame(ctx, input, at)
		if err != nil {
			return nil, fmt.Errorf("sample frame at %.3fs: %w", at, err)
		}
		if frame.Time >= duration || (len(frames) > 0 && frame.Time <= frames[len(frames)-1].Time) {
			continue
		}
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		return nil, errors.New("video contains no sampled frames")
	}
	return frames, nil
}

func (p *Processor) sampleFrame(ctx context.Context, input string, at float64) (domain.Frame, error) {
	seek := seconds(at)
	requested, _ := strconv.ParseFloat(seek, 64)
	for _, quality := range []string{"4", "8", "16", "31"} {
		output := &limitedBuffer{limit: 256 << 10}
		log, err := runWithLog(ctx, p.ffmpeg, "", output,
			"-nostdin", "-hide_banner", "-loglevel", "info", "-threads", "2",
			"-ss", seek, "-protocol_whitelist", "file", "-f", "mov", "-i", input,
			"-map", "0:v:0", "-an", "-sn", "-dn", "-map_metadata", "-1", "-frames:v", "1",
			"-vf", "scale=w='max(2,trunc(min(640,640*dar)/2)*2)':h='max(2,trunc(min(640,640/dar)/2)*2)',setsar=1,showinfo",
			"-filter_threads", "1", "-c:v", "mjpeg", "-threads", "1", "-q:v", quality, "-fps_mode", "passthrough", "-f", "image2pipe", "pipe:1")
		if err != nil {
			return domain.Frame{}, err
		}
		if output.truncated {
			continue
		}
		config, err := jpeg.DecodeConfig(bytes.NewReader(output.Bytes()))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 640 || config.Height > 640 {
			return domain.Frame{}, errors.New("FFmpeg returned an invalid preview frame")
		}
		match := frameTimestamp.FindStringSubmatch(log)
		if len(match) != 2 {
			return domain.Frame{}, errors.New("FFmpeg returned no preview timestamp")
		}
		offset, err := strconv.ParseFloat(match[1], 64)
		if err != nil || math.IsNaN(offset) || math.IsInf(offset, 0) || offset < 0 {
			return domain.Frame{}, errors.New("FFmpeg returned an invalid preview timestamp")
		}
		return domain.Frame{Time: requested + offset, JPEG: output.Bytes()}, nil
	}
	return domain.Frame{}, errors.New("preview frame exceeds 256 KiB")
}

func (p *Processor) RenderMontage(ctx context.Context, input, output string, cuts []domain.Cut) error {
	input, err := localFile(input)
	if err != nil {
		return err
	}
	info, err := p.Probe(ctx, input)
	if err != nil {
		return err
	}
	if err := domain.ValidateCuts(cuts, info.Duration); err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(filepath.Dir(output), ".montage-"+filepath.Base(output)+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var playlist strings.Builder
	for i, cut := range cuts {
		name := fmt.Sprintf("part-%02d.mp4", i)
		err := run(ctx, p.ffmpeg, "", io.Discard,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-threads", "2",
			"-ss", seconds(cut.Start), "-protocol_whitelist", "file", "-f", "mov", "-i", input,
			"-t", seconds(cut.End-cut.Start), "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1",
			"-vf", "scale=w='max(2,trunc(min(540,960*dar)/2)*2)':h='max(2,trunc(min(960,540/dar)/2)*2)',setsar=1,pad=540:960:(ow-iw)/2:(oh-ih)/2:color=0x16171b",
			"-filter_threads", "1", "-c:v", "libx264", "-threads", "2", "-preset", "veryfast", "-crf", "23",
			"-pix_fmt", "yuv420p", "-r", "30", "-video_track_timescale", "90000",
			"-c:a", "aac", "-b:a", "96k", "-ac", "2", "-ar", "48000", "-af", "apad=whole_dur="+seconds(cut.End-cut.Start),
			"-f", "mp4", filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("render cut %d: %w", i+1, err)
		}
		fmt.Fprintf(&playlist, "file '%s'\n", name)
	}
	if err := os.WriteFile(filepath.Join(dir, "parts.txt"), []byte(playlist.String()), 0600); err != nil {
		return err
	}
	return atomicOutput(output, ".mp4", func(temp string) error {
		return run(ctx, p.ffmpeg, dir, io.Discard,
			"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file",
			"-f", "concat", "-safe", "1", "-i", "parts.txt", "-map", "0:v:0", "-map", "0:a:0?",
			"-map_metadata", "-1", "-c", "copy", "-movflags", "+faststart", "-f", "mp4", temp)
	})
}
