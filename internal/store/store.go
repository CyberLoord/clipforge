package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("job not found")
	ErrConflict  = errors.New("job state does not allow this operation")
	ErrLeaseLost = errors.New("job lease lost")
)

type Store struct {
	pool *pgxpool.Pool
}

type Lease struct {
	Job           domain.Job
	Token         string
	PreviousToken string
}

const columns = `id, filename, status, stage, mode, error, source_path, duration, transcript, clips, attempts, created_at, updated_at`

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func Migrate(ctx context.Context, pool *pgxpool.Pool, path string) error {
	sql, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(843783235)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}
	return tx.Commit(ctx)
}

type scanner interface {
	Scan(...any) error
}

func scanJob(row scanner, extra ...any) (domain.Job, error) {
	var job domain.Job
	var transcript, clips []byte
	fields := []any{&job.ID, &job.Filename, &job.Status, &job.Stage, &job.Mode, &job.Error, &job.SourcePath, &job.Duration, &transcript, &clips, &job.Attempts, &job.CreatedAt, &job.UpdatedAt}
	err := row.Scan(append(fields, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, ErrNotFound
	}
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal(transcript, &job.Transcript); err != nil {
		return job, err
	}
	if err = json.Unmarshal(clips, &job.Clips); err != nil {
		return job, err
	}
	return job, nil
}

func marshalSlice[T any](values []T) ([]byte, error) {
	if values == nil {
		values = []T{}
	}
	return json.Marshal(values)
}

func (s *Store) Create(ctx context.Context, job *domain.Job) error {
	if job.ID == "" {
		job.ID = domain.NewID()
	}
	job.Status = "queued"
	job.Stage = "probe"
	err := s.pool.QueryRow(ctx, `INSERT INTO jobs (id, filename, status, stage, mode, source_path) VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at, updated_at`, job.ID, job.Filename, job.Status, job.Stage, job.Mode, job.SourcePath).Scan(&job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	job.Transcript = []domain.Segment{}
	job.Clips = []domain.Clip{}
	return nil
}

func (s *Store) List(ctx context.Context) ([]domain.Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM jobs ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []domain.Job{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (domain.Job, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id = $1`, id))
}

func (s *Store) mutationError(ctx context.Context, id string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrConflict
}

func (s *Store) Cancel(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'cancelled', error = '', lease_until = NULL, updated_at = now() WHERE id = $1 AND status IN ('queued', 'running')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.mutationError(ctx, id)
	}
	return nil
}

func (s *Store) Retry(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'queued', error = '', attempts = 0, lease_until = NULL, available_at = now(), updated_at = now() WHERE id = $1 AND status IN ('failed', 'cancelled')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.mutationError(ctx, id)
	}
	return nil
}

func (s *Store) Reprocess(ctx context.Context, id, mode string) error {
	switch mode {
	case "split", "demo", "openai":
	default:
		return fmt.Errorf("%w: unsupported processing mode", ErrConflict)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'queued', stage = 'probe', mode = $2, duration = 0, transcript = '[]', clips = '[]', attempts = 0, error = '', lease_until = NULL, available_at = now(), updated_at = now() WHERE id = $1 AND status IN ('completed', 'failed', 'cancelled')`, id, mode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.mutationError(ctx, id)
	}
	return nil
}

func (s *Store) Reexport(ctx context.Context, id, clipID string, start, end float64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM jobs WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if job.Status != "completed" {
		return ErrConflict
	}
	found := false
	for i := range job.Clips {
		if job.Clips[i].ID == clipID {
			job.Clips[i].Start = start
			job.Clips[i].End = end
			job.Clips[i].Path = ""
			job.Clips[i].Revision++
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	if err = domain.ValidateClips(job.Clips, job.Duration); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	clips, err := marshalSlice(job.Clips)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET status = 'queued', stage = 'render', clips = $2, attempts = 0, error = '', lease_token = '', lease_until = NULL, available_at = now(), updated_at = now() WHERE id = $1`, id, clips); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Claim(ctx context.Context, duration time.Duration, maxAttempts int) (Lease, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'failed', error = 'Worker stopped before the stage completed; retry limit reached.', lease_until = NULL, updated_at = now() WHERE status = 'running' AND lease_until <= now() AND attempts >= $1`, maxAttempts); err != nil {
		return Lease{}, err
	}
	token := domain.NewID()
	var previousToken string
	returning := "jobs." + strings.ReplaceAll(columns, ", ", ", jobs.")
	job, err := scanJob(s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id, lease_token AS previous_token FROM jobs
		WHERE (status = 'queued' AND available_at <= now()) OR (status = 'running' AND lease_until <= now() AND attempts < $3)
		ORDER BY available_at, created_at
		FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE jobs SET status = 'running', attempts = jobs.attempts + 1, lease_token = $1,
		lease_until = now() + $2 * interval '1 second', updated_at = now()
		FROM candidate WHERE jobs.id = candidate.id RETURNING `+returning+`, candidate.previous_token`, token, duration.Seconds(), maxAttempts), &previousToken)
	return Lease{Job: job, Token: token, PreviousToken: previousToken}, err
}

func (s *Store) Renew(ctx context.Context, lease Lease, duration time.Duration) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET lease_until = now() + $3 * interval '1 second' WHERE id = $1 AND status = 'running' AND lease_token = $2 AND lease_until > now()`, lease.Job.ID, lease.Token, duration.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) Checkpoint(ctx context.Context, lease Lease, job domain.Job) error {
	transcript, err := marshalSlice(job.Transcript)
	if err != nil {
		return err
	}
	clips, err := marshalSlice(job.Clips)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET stage = $3, duration = $4, transcript = $5, clips = $6, attempts = 1, error = '', updated_at = now() WHERE id = $1 AND status = 'running' AND lease_token = $2 AND lease_until > now()`, lease.Job.ID, lease.Token, job.Stage, job.Duration, transcript, clips)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) Complete(ctx context.Context, lease Lease, clips []domain.Clip) error {
	data, err := marshalSlice(clips)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'completed', stage = 'completed', clips = $3, attempts = 0, error = '', lease_token = '', lease_until = NULL, updated_at = now() WHERE id = $1 AND status = 'running' AND lease_token = $2 AND lease_until > now()`, lease.Job.ID, lease.Token, data)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) Fail(ctx context.Context, lease Lease, message string, retry bool, delay time.Duration, maxAttempts int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status = CASE WHEN $4 AND attempts < $6 THEN 'queued' ELSE 'failed' END, error = $3, available_at = now() + $5 * interval '1 second', lease_until = NULL, updated_at = now() WHERE id = $1 AND status = 'running' AND lease_token = $2 AND lease_until > now()`, lease.Job.ID, lease.Token, message, retry, delay.Seconds(), maxAttempts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}
