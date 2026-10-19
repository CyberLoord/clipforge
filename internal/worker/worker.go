package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CyberLoord/clipforge/internal/ai"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/store"
)

type Media interface {
	Probe(context.Context, string) (domain.Info, error)
	Extract(context.Context, string, string) error
	Render(context.Context, string, string, []domain.Segment, domain.Clip) error
}

type Queue interface {
	Claim(context.Context, time.Duration, int) (store.Lease, error)
	Renew(context.Context, store.Lease, time.Duration) error
	Checkpoint(context.Context, store.Lease, domain.Job) error
	Complete(context.Context, store.Lease, []domain.Clip) error
	Fail(context.Context, store.Lease, string, bool, time.Duration, int) error
}

type Config struct {
	Workers       int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	StageTimeout  time.Duration
	MaxAttempts   int
	MaxDuration   float64
	DataDir       string
	Mode          string
}

type Worker struct {
	queue       Queue
	media       Media
	transcriber ai.Transcriber
	selector    ai.Selector
	cfg         Config
	log         *slog.Logger
}

func New(queue Queue, media Media, transcriber ai.Transcriber, selector ai.Selector, cfg Config, logger *slog.Logger) *Worker {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.StageTimeout <= 0 {
		cfg.StageTimeout = 10 * time.Minute
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 3
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = 1800
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{queue: queue, media: media, transcriber: transcriber, selector: selector, cfg: cfg, log: logger}
}

func (w *Worker) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for range w.cfg.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			w.loop(ctx)
		}()
	}
	workers.Wait()
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		lease, err := w.queue.Claim(ctx, w.cfg.LeaseDuration, w.cfg.MaxAttempts)
		if err == nil {
			w.process(ctx, lease)
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			w.log.Error("claim failed", "error", err)
		}
		timer := time.NewTimer(w.cfg.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelFunc, lease store.Lease, done chan<- struct{}) {
	defer close(done)
	interval := max(w.cfg.LeaseDuration/3, time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, stop := context.WithTimeout(ctx, interval)
			err := w.queue.Renew(renewCtx, lease, w.cfg.LeaseDuration)
			stop()
			if err != nil {
				if ctx.Err() == nil {
					w.log.Warn("lease renewal failed", "job_id", lease.Job.ID, "error", err)
				}
				cancel()
				return
			}
		}
	}
}

func (w *Worker) process(parent context.Context, lease store.Lease) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go w.heartbeat(ctx, cancel, lease, done)
	defer func() {
		cancel()
		<-done
	}()
	job := lease.Job
	w.cleanupAbandoned(job, lease.Token)
	w.cleanupTemp(job.ID, lease.Token)
	outputs := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, path := range outputs {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					w.log.Warn("output cleanup failed", "job_id", job.ID, "error", err)
				}
			}
		}
	}()
	for ctx.Err() == nil {
		stage := job.Stage
		w.log.Info("stage started", "job_id", job.ID, "stage", stage, "attempt", job.Attempts)
		stageCtx, stop := context.WithTimeout(ctx, w.cfg.StageTimeout)
		err := w.runStage(stageCtx, &job, lease.Token, &outputs)
		stop()
		if err == nil {
			if job.Stage == "completed" {
				err = w.queue.Complete(ctx, lease, job.Clips)
				if err != nil && !errors.Is(err, store.ErrLeaseLost) {
					committed = true
				}
				if err == nil {
					committed = true
					w.log.Info("job completed", "job_id", job.ID)
					return
				}
			} else {
				err = w.queue.Checkpoint(ctx, lease, job)
				if err == nil {
					job.Attempts = 1
					continue
				}
			}
		}
		if ctx.Err() != nil || errors.Is(err, store.ErrLeaseLost) {
			return
		}
		retry := ai.IsTemporary(err)
		delay := time.Second * time.Duration(1<<min(max(job.Attempts-1, 0), 5))
		if failErr := w.queue.Fail(ctx, lease, fmt.Sprintf("%s: %v", stage, err), retry, delay, w.cfg.MaxAttempts); failErr != nil && !errors.Is(failErr, store.ErrLeaseLost) {
			w.log.Error("save failure failed", "job_id", job.ID, "error", failErr)
		}
		w.log.Warn("stage failed", "job_id", job.ID, "stage", stage, "retryable", retry, "error", err)
		return
	}
}

func (w *Worker) cleanupAbandoned(job domain.Job, token string) {
	dir := filepath.Join(w.cfg.DataDir, "jobs", job.ID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			w.log.Warn("read outputs failed", "job_id", job.ID, "error", err)
		}
		return
	}
	keep := map[string]bool{filepath.Clean(job.SourcePath): true}
	for _, clip := range job.Clips {
		if clip.Path != "" {
			keep[filepath.Join(w.cfg.DataDir, filepath.FromSlash(clip.Path))] = true
		}
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if strings.Contains(entry.Name(), "-"+token) {
			continue
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".subtitles-") {
			if err := os.RemoveAll(path); err != nil {
				w.log.Warn("abandoned subtitles cleanup failed", "job_id", job.ID, "error", err)
			}
			continue
		}
		if entry.IsDir() || keep[path] || !strings.HasSuffix(entry.Name(), ".mp4") || !strings.Contains(entry.Name(), "-") {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			w.log.Warn("abandoned output cleanup failed", "job_id", job.ID, "error", err)
		}
	}
}

func (w *Worker) cleanupTemp(id, token string) {
	dir := filepath.Join(w.cfg.DataDir, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), id+"-") && !strings.HasPrefix(entry.Name(), id+"-"+token+"-") {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				w.log.Warn("temporary audio cleanup failed", "job_id", id, "error", err)
			}
		}
	}
}

func (w *Worker) runStage(ctx context.Context, job *domain.Job, token string, outputs *[]string) error {
	if (job.Stage == "transcribe" || job.Stage == "select") && w.cfg.Mode != "" && job.Mode != w.cfg.Mode {
		return fmt.Errorf("job uses %s mode; restart the service in that mode before retrying", job.Mode)
	}
	switch job.Stage {
	case "probe":
		info, err := w.media.Probe(ctx, job.SourcePath)
		if err != nil {
			return err
		}
		if math.IsNaN(info.Duration) || math.IsInf(info.Duration, 0) || info.Duration < 20 || info.Duration > w.cfg.MaxDuration {
			return fmt.Errorf("video duration must be between 20 and %.0f seconds", w.cfg.MaxDuration)
		}
		job.Duration = info.Duration
		job.Stage = "transcribe"
	case "transcribe":
		tempDir := filepath.Join(w.cfg.DataDir, "tmp")
		if err := os.MkdirAll(tempDir, 0700); err != nil {
			return err
		}
		path, err := os.MkdirTemp(tempDir, job.ID+"-"+token+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(path)
		audio := filepath.Join(path, "audio.mp3")
		if err = w.media.Extract(ctx, job.SourcePath, audio); err != nil {
			return err
		}
		segments, err := w.transcriber.Transcribe(ctx, audio, job.Duration)
		if err != nil {
			return err
		}
		if err = domain.ValidateSegments(segments, job.Duration); err != nil {
			return err
		}
		job.Transcript = segments
		job.Stage = "select"
	case "select":
		clips, err := w.selector.Select(ctx, job.Transcript, job.Duration)
		if err != nil {
			return err
		}
		if err = domain.ValidateClips(clips, job.Duration); err != nil {
			return err
		}
		for i := range clips {
			clips[i].ID = domain.NewID()
			clips[i].Revision = 1
			clips[i].Path = ""
		}
		job.Clips = clips
		job.Stage = "render"
	case "render":
		if err := domain.ValidateClips(job.Clips, job.Duration); err != nil {
			return err
		}
		if err := domain.ValidateSegments(job.Transcript, job.Duration); err != nil {
			return err
		}
		for i := range job.Clips {
			clip := &job.Clips[i]
			if clip.Path != "" {
				continue
			}
			relative := filepath.Join("jobs", job.ID, fmt.Sprintf("%s-%d-%s.mp4", clip.ID, clip.Revision, token))
			path := filepath.Join(w.cfg.DataDir, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			*outputs = append(*outputs, path)
			if err := w.media.Render(ctx, job.SourcePath, path, job.Transcript, *clip); err != nil {
				return err
			}
			clip.Path = filepath.ToSlash(relative)
		}
		job.Stage = "completed"
	default:
		return fmt.Errorf("unknown job stage %q", job.Stage)
	}
	return nil
}
