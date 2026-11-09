package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/CyberLoord/clipforge/internal/config"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/store"
	"github.com/CyberLoord/clipforge/internal/stream"
	"github.com/CyberLoord/clipforge/internal/worker"
	"golang.org/x/sync/errgroup"
)

func runBackground(ctx context.Context, cfg config.Config, repo *store.Store, broker *stream.Broker, jobs *worker.Worker, logger *slog.Logger) error {
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { <-ctx.Done(); return nil })
	enabled := func(role string) bool { return cfg.Role == "all" || cfg.Role == role }
	if enabled("dispatcher") {
		group.Go(func() error { return dispatch(ctx, repo, broker, cfg.MaxAttempts, logger) })
	}
	if enabled("analysis") {
		group.Go(func() error {
			return broker.Consume(ctx, domain.AnalysisTopic, "analysis", cfg.Workers, commandHandler(jobs, domain.AnalysisTopic))
		})
	}
	if enabled("render") {
		group.Go(func() error {
			return broker.Consume(ctx, domain.RenderTopic, "render", cfg.RenderWorkers, commandHandler(jobs, domain.RenderTopic))
		})
	}
	if enabled("events") {
		group.Go(func() error {
			return broker.Consume(ctx, domain.EventsTopic, "history", 1, func(ctx context.Context, record stream.Record) error {
				var event domain.JobEvent
				if err := decodeRecord(record.Value, &event); err != nil {
					return stream.Permanent(err)
				}
				if !domain.ValidID(event.JobID) || record.Key != event.JobID || event.Sequence < 1 || event.ID != event.JobID+":"+strconv.FormatInt(event.Sequence, 10) || event.OccurredAt.IsZero() || event.Attempt < 0 || !validStage(event.Stage) || !validEvent(event) {
					return stream.Permanent(errors.New("invalid job event"))
				}
				return repo.RecordEvent(ctx, event)
			})
		})
		group.Go(func() error {
			return broker.Consume(ctx, domain.DeadLettersTopic, "failures", 1, func(ctx context.Context, record stream.Record) error {
				var letter domain.DeadLetter
				if err := decodeRecord(record.Value, &letter); err != nil {
					return stream.Permanent(err)
				}
				if letter.ID == "" || len(letter.ID) > 256 || letter.Error == "" || letter.OccurredAt.IsZero() || (letter.JobID != "" && !domain.ValidID(letter.JobID)) || (letter.Kind != "job_failed" && letter.Kind != "invalid_message") {
					return stream.Permanent(errors.New("invalid dead letter"))
				}
				return repo.RecordDeadLetter(ctx, letter)
			})
		})
	}
	err := group.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func dispatch(ctx context.Context, repo *store.Store, broker *stream.Broker, maxAttempts int, logger *slog.Logger) error {
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	for ctx.Err() == nil {
		cycle, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := repo.RecoverExpired(cycle, maxAttempts); err != nil && ctx.Err() == nil {
			logger.Error("lease recovery failed", "error", err)
		}
		count, err := repo.DispatchOutbox(cycle, broker.Publish)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("outbox delivery deferred", "error", err)
		}
		if err == nil && count > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
	return nil
}

type commandWorker interface {
	Handle(context.Context, domain.Command) error
}

func commandHandler(jobs commandWorker, topic string) func(context.Context, stream.Record) error {
	return func(ctx context.Context, record stream.Record) error {
		var command domain.Command
		if err := decodeRecord(record.Value, &command); err != nil {
			return stream.Permanent(err)
		}
		if !domain.ValidID(command.JobID) || record.Key != command.JobID || command.Generation < 1 || command.ID != command.JobID+":"+strconv.FormatInt(command.Generation, 10) || !validStage(command.Stage) || command.Stage == "completed" || ((command.Stage == "render") != (topic == domain.RenderTopic)) {
			return stream.Permanent(errors.New("invalid processing command"))
		}
		return jobs.Handle(ctx, command)
	}
}

func decodeRecord(data []byte, destination any) error {
	if len(data) > 256<<10 {
		return errors.New("message exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON message: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func validStage(stage string) bool {
	switch stage {
	case "probe", "transcribe", "select", "render", "completed":
		return true
	default:
		return false
	}
}

func validEvent(event domain.JobEvent) bool {
	switch event.Type {
	case "job.queued":
		return event.Status == "queued"
	case "stage.started", "stage.changed":
		return event.Status == "running"
	case "job.completed":
		return event.Status == "completed" && event.Stage == "completed"
	case "job.failed":
		return event.Status == "failed"
	case "job.cancelled":
		return event.Status == "cancelled"
	default:
		return false
	}
}
