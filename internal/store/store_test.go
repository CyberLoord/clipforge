package store

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "test_" + domain.NewID()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err := Migrate(ctx, pool, "../../migrations/001_jobs.sql"); err != nil {
		t.Fatal(err)
	}
	return New(pool)
}

func createJob(t *testing.T, s *Store) domain.Job {
	t.Helper()
	job := domain.Job{Filename: "video.mp4", SourcePath: "/data/source.mp4", Mode: "demo"}
	if err := s.Create(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func claim(t *testing.T, s *Store, attempts int) Lease {
	t.Helper()
	lease, err := s.Claim(context.Background(), time.Minute, attempts)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func expire(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentClaims(t *testing.T) {
	s := testStore(t)
	const count = 24
	for range count {
		createJob(t, s)
	}
	claimed := make(chan string, count)
	errorsFound := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				lease, err := s.Claim(context.Background(), time.Minute, 3)
				if errors.Is(err, ErrNotFound) {
					return
				}
				if err != nil {
					errorsFound <- err
					return
				}
				claimed <- lease.Job.ID
			}
		}()
	}
	workers.Wait()
	close(claimed)
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("job %s claimed twice", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("claimed %d jobs, want %d", len(seen), count)
	}
}

func TestExpiredLeaseIsFenced(t *testing.T) {
	s := testStore(t)
	job := createJob(t, s)
	old := claim(t, s, 3)
	expire(t, s, job.ID)
	current := claim(t, s, 3)
	if current.Token == old.Token || current.PreviousToken != old.Token || current.Job.Attempts != 2 {
		t.Fatalf("invalid recovered lease: %+v", current)
	}
	ctx := context.Background()
	for name, err := range map[string]error{
		"renew":      s.Renew(ctx, old, time.Minute),
		"checkpoint": s.Checkpoint(ctx, old, old.Job),
		"complete":   s.Complete(ctx, old, nil),
		"fail":       s.Fail(ctx, old, "stale", false, 0, 3),
	} {
		if !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s = %v, want lease lost", name, err)
		}
	}
	if err := s.Renew(ctx, current, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, current, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("complete cancelled job = %v", err)
	}
	if err := s.Retry(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if got := claim(t, s, 3); got.Job.Attempts != 1 || got.Job.Stage != "probe" || got.PreviousToken != current.Token {
		t.Fatalf("retry did not reset attempts: %+v", got.Job)
	}
}

func TestRecoveryHasBoundedAttempts(t *testing.T) {
	s := testStore(t)
	job := createJob(t, s)
	for attempt := 1; attempt <= 3; attempt++ {
		lease := claim(t, s, 3)
		if lease.Job.Attempts != attempt {
			t.Fatalf("attempt = %d, want %d", lease.Job.Attempts, attempt)
		}
		expire(t, s, job.ID)
	}
	if _, err := s.Claim(context.Background(), time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim exhausted job = %v", err)
	}
	got, err := s.Get(context.Background(), job.ID)
	if err != nil || got.Status != "failed" || got.Error == "" {
		t.Fatalf("job = %+v, error = %v", got, err)
	}
	if err := s.Retry(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if recovered := claim(t, s, 3); recovered.PreviousToken == "" {
		t.Fatal("crash recovery discarded previous lease token")
	}
}

func TestCheckpointAndDelayedRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	lease := claim(t, s, 2)
	lease.Job.Stage = "transcribe"
	lease.Job.Duration = 90
	if err := s.Checkpoint(ctx, lease, lease.Job); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(ctx, lease, "temporary", true, time.Minute, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx, time.Minute, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claimed before retry delay: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET available_at = now() WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	previousToken := lease.Token
	lease = claim(t, s, 2)
	if lease.PreviousToken != previousToken {
		t.Fatal("retry discarded previous lease token")
	}
	if lease.Job.Stage != "transcribe" || lease.Job.Duration != 90 || lease.Job.Attempts != 2 {
		t.Fatalf("lost checkpoint: %+v", lease.Job)
	}
	if err := s.Fail(ctx, lease, "temporary", true, 0, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("attempt limit not applied: %+v %v", got, err)
	}
	if err := s.Retry(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	lease = claim(t, s, 2)
	if lease.Job.Stage != "transcribe" || lease.Job.Duration != 90 || lease.Job.Attempts != 1 {
		t.Fatalf("manual retry lost checkpoint: %+v", lease.Job)
	}
}

func TestReexportPreservesOtherClips(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	lease := claim(t, s, 3)
	lease.Job.Duration = 90
	lease.Job.Stage = "render"
	clips := []domain.Clip{
		{ID: "one", Start: 0, End: 25, Title: "One", Reason: "Reason", Path: "one.mp4", Revision: 1},
		{ID: "two", Start: 40, End: 65, Title: "Two", Reason: "Reason", Path: "two.mp4", Revision: 1},
	}
	if err := s.Checkpoint(ctx, lease, lease.Job); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, lease, clips); err != nil {
		t.Fatal(err)
	}
	if err := s.Reexport(ctx, job.ID, "one", math.NaN(), 30); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid edit error = %v", err)
	}
	if err := s.Reexport(ctx, job.ID, "one", 10, 35); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "queued" || got.Stage != "render" || got.Clips[0].Start != 10 || got.Clips[0].End != 35 || got.Clips[0].Revision != 2 || got.Clips[0].Path != "" || !reflect.DeepEqual(got.Clips[1], clips[1]) {
		t.Fatalf("unexpected reexport: %+v", got)
	}
	if err := s.Reexport(ctx, job.ID, "two", 50, 75); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent edit error = %v", err)
	}
}

func TestReprocessResetsCompletedAndStoppedJobs(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			job := createJob(t, s)
			lease := claim(t, s, 3)
			lease.Job.Duration = 90
			lease.Job.Stage = "render"
			lease.Job.Transcript = []domain.Segment{{Start: 0, End: 30, Text: "saved transcript"}}
			lease.Job.Clips = []domain.Clip{{ID: "old", Start: 0, End: 30, Title: "Old clip", Reason: "Old reason", Path: "old.mp4", Revision: 2}}
			if err := s.Checkpoint(ctx, lease, lease.Job); err != nil {
				t.Fatal(err)
			}
			var err error
			switch status {
			case "completed":
				err = s.Complete(ctx, lease, lease.Job.Clips)
			case "failed":
				err = s.Fail(ctx, lease, "failed", false, 0, 3)
			case "cancelled":
				err = s.Cancel(ctx, job.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Reprocess(ctx, job.ID, "split"); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "queued" || got.Stage != "probe" || got.Mode != "split" || got.Duration != 0 || got.Attempts != 0 || got.Error != "" || len(got.Transcript) != 0 || len(got.Clips) != 0 {
				t.Fatalf("reprocess did not reset processing state: %+v", got)
			}
			if got.SourcePath != job.SourcePath || got.Filename != job.Filename || !got.CreatedAt.Equal(job.CreatedAt) {
				t.Fatalf("reprocess changed source metadata: %+v", got)
			}
			if err := s.Complete(ctx, lease, lease.Job.Clips); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("old lease can overwrite reprocessed job: %v", err)
			}
			fresh := claim(t, s, 3)
			if fresh.Job.Attempts != 1 || fresh.Job.Stage != "probe" || fresh.Token == lease.Token {
				t.Fatalf("invalid fresh lease: %+v", fresh)
			}
		})
	}
}

func TestReprocessRejectsActiveJobsAndInvalidModes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	if err := s.Reprocess(ctx, job.ID, "split"); !errors.Is(err, ErrConflict) {
		t.Fatalf("queued job accepted reprocess: %v", err)
	}
	lease := claim(t, s, 3)
	if err := s.Reprocess(ctx, job.ID, "openai"); !errors.Is(err, ErrConflict) {
		t.Fatalf("running job accepted reprocess: %v", err)
	}
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Reprocess(ctx, job.ID, "unknown"); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid mode accepted: %v", err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil || got.Mode != "demo" || got.Status != "cancelled" {
		t.Fatalf("invalid reprocess changed job: %+v %v", got, err)
	}
	if err := s.Reprocess(ctx, job.ID, "openai"); err != nil {
		t.Fatal(err)
	}
	fresh := claim(t, s, 3)
	if fresh.Job.Mode != "openai" || fresh.PreviousToken != lease.Token {
		t.Fatalf("mode or predecessor lost: %+v", fresh)
	}
	if err := s.Reprocess(ctx, domain.NewID(), "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing job error = %v", err)
	}
}

func TestConcurrentReprocessHasOneWinner(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, mode := range []string{"split", "demo"} {
		go func() { results <- s.Reprocess(ctx, job.ID, mode) }()
	}
	winners := 0
	for range 2 {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("reprocess winners = %d, want 1", winners)
	}
}
