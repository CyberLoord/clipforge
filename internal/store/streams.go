package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func (s *Store) ClaimCommand(ctx context.Context, command domain.Command, duration time.Duration, maxAttempts int) (Lease, error) {
	if !domain.ValidID(command.JobID) || command.Generation < 1 || command.ID != fmt.Sprintf("%s:%d", command.JobID, command.Generation) {
		return Lease{}, ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'failed', error = 'Worker stopped before the stage completed; retry limit reached.', lease_until = NULL, updated_at = now()
		WHERE id = $1 AND dispatch_generation = $2 AND stage = $3 AND status = 'queued' AND attempts >= $4`, command.JobID, command.Generation, command.Stage, maxAttempts); err != nil {
		return Lease{}, err
	}
	token := domain.NewID()
	var previousToken string
	returning := "jobs." + strings.ReplaceAll(columns, ", ", ", jobs.")
	job, err := scanJob(s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id, lease_token AS previous_token FROM jobs
		WHERE id = $1 AND dispatch_generation = $2 AND stage = $3 AND status = 'queued'
			AND available_at <= now() AND attempts < $6
		FOR UPDATE
	) UPDATE jobs SET status = 'running', attempts = jobs.attempts + 1, lease_token = $4,
		lease_until = now() + $5 * interval '1 second', updated_at = now()
		FROM candidate WHERE jobs.id = candidate.id RETURNING `+returning+`, candidate.previous_token`, command.JobID, command.Generation, command.Stage, token, duration.Seconds(), maxAttempts), &previousToken)
	return Lease{Job: job, Token: token, PreviousToken: previousToken}, err
}

func (s *Store) RecoverExpired(ctx context.Context, maxAttempts int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `WITH expired AS (
		SELECT id FROM jobs WHERE status = 'running' AND lease_until <= now()
		ORDER BY lease_until FOR UPDATE SKIP LOCKED LIMIT 100
	) UPDATE jobs SET status = CASE WHEN attempts < $1 THEN 'queued' ELSE 'failed' END,
		error = CASE WHEN attempts < $1 THEN 'Worker lease expired; processing will resume.' ELSE 'Worker stopped before the stage completed; retry limit reached.' END,
		lease_until = NULL, available_at = now(), updated_at = now()
		FROM expired WHERE jobs.id = expired.id`, maxAttempts)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH missing AS (
		SELECT id, stage, dispatch_generation FROM jobs j
		WHERE status = 'queued' AND available_at <= now() AND dispatched_at <= now() - interval '15 minutes'
			AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.message_key = j.id
				AND o.topic IN ('clipforge.analysis.v1', 'clipforge.render.v1')
				AND o.payload->>'generation' = j.dispatch_generation::text)
		ORDER BY dispatched_at FOR UPDATE OF j SKIP LOCKED LIMIT 100
	) INSERT INTO outbox (topic, message_key, payload)
		SELECT CASE WHEN stage = 'render' THEN 'clipforge.render.v1' ELSE 'clipforge.analysis.v1' END,
			id, jsonb_build_object('id', id || ':' || dispatch_generation, 'job_id', id,
				'generation', dispatch_generation, 'stage', stage)
		FROM missing ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Handoff(ctx context.Context, lease Lease, job domain.Job) error {
	if job.ID != lease.Job.ID || job.Stage != "render" {
		return ErrConflict
	}
	transcript, err := marshalSlice(job.Transcript)
	if err != nil {
		return err
	}
	clips, err := marshalSlice(job.Clips)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'queued', stage = 'render', duration = $3,
		transcript = $4, clips = $5, attempts = 0, error = '', lease_until = NULL,
		available_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'running' AND stage <> 'render' AND lease_token = $2 AND lease_until > now()`, lease.Job.ID, lease.Token, job.Duration, transcript, clips)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) DispatchOutbox(ctx context.Context, publish func(context.Context, domain.OutboxMessage) error) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(843783236)`).Scan(&locked); err != nil || !locked {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id, topic, message_key, payload FROM outbox WHERE available_at <= now() ORDER BY id LIMIT 20 FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	messages := []domain.OutboxMessage{}
	for rows.Next() {
		var message domain.OutboxMessage
		if err = rows.Scan(&message.ID, &message.Topic, &message.Key, &message.Payload); err != nil {
			rows.Close()
			return 0, err
		}
		messages = append(messages, message)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, message := range messages {
		if err = publish(ctx, message); err != nil {
			return 0, fmt.Errorf("publish outbox %d: %w", message.ID, err)
		}
		if message.Topic == domain.AnalysisTopic || message.Topic == domain.RenderTopic {
			if _, err = tx.Exec(ctx, `UPDATE jobs SET dispatched_at = now() WHERE id = $1 AND status = 'queued' AND dispatch_generation::text = ($2::jsonb->>'generation')`, message.Key, message.Payload); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, message.ID); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(messages), nil
}

func (s *Store) RecordEvent(ctx context.Context, event domain.JobEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO job_events (id, job_id, sequence, payload, occurred_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, event.ID, event.JobID, event.Sequence, payload, event.OccurredAt)
	return err
}

func (s *Store) Events(ctx context.Context, jobID string) ([]domain.JobEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT payload FROM (SELECT sequence, payload FROM job_events WHERE job_id = $1 ORDER BY sequence DESC LIMIT 100) recent ORDER BY sequence`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []domain.JobEvent{}
	for rows.Next() {
		var payload []byte
		var event domain.JobEvent
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) RecordDeadLetter(ctx context.Context, letter domain.DeadLetter) error {
	payload, err := json.Marshal(letter)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO dead_letters (id, job_id, payload, occurred_at) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, letter.ID, letter.JobID, payload, letter.OccurredAt)
	return err
}

func (s *Store) DeadLetters(ctx context.Context) ([]domain.DeadLetter, error) {
	rows, err := s.pool.Query(ctx, `SELECT payload FROM dead_letters ORDER BY occurred_at DESC, id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	letters := []domain.DeadLetter{}
	for rows.Next() {
		var payload []byte
		var letter domain.DeadLetter
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(payload, &letter); err != nil {
			return nil, err
		}
		letters = append(letters, letter)
	}
	return letters, rows.Err()
}
