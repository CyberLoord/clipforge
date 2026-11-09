package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func pendingMessages(t *testing.T, s *Store) []domain.OutboxMessage {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT id, topic, message_key, payload FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var messages []domain.OutboxMessage
	for rows.Next() {
		var message domain.OutboxMessage
		if err := rows.Scan(&message.ID, &message.Topic, &message.Key, &message.Payload); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return messages
}

func commandFor(t *testing.T, s *Store, jobID, topic string) domain.Command {
	t.Helper()
	var payload []byte
	if err := s.pool.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE message_key = $1 AND topic = $2 ORDER BY id DESC LIMIT 1`, jobID, topic).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var command domain.Command
	if err := json.Unmarshal(payload, &command); err != nil {
		t.Fatal(err)
	}
	return command
}

func TestJobAndOutboxAtomicity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO jobs (id, filename, status, stage, mode, source_path) VALUES ($1, 'rollback.mp4', 'queued', 'probe', 'split', '/source.mp4')`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back job exists: %v", err)
	}
	if messages := pendingMessages(t, s); len(messages) != 0 {
		t.Fatalf("rolled back transaction emitted messages: %+v", messages)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE outbox ADD CONSTRAINT reject_command CHECK (topic <> 'clipforge.analysis.v1')`); err != nil {
		t.Fatal(err)
	}
	job := domain.Job{Filename: "reject.mp4", Mode: "split", SourcePath: "/source.mp4"}
	if err := s.Create(ctx, &job); err == nil {
		t.Fatal("job was accepted without its outbox command")
	}
	if _, err := s.Get(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed outbox left a job: %v", err)
	}
	if messages := pendingMessages(t, s); len(messages) != 0 {
		t.Fatalf("failed outbox left messages: %+v", messages)
	}
}

func TestCommandGenerationAndDelayedRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	first := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if first.Generation != 1 || first.Stage != "probe" || first.ID != job.ID+":1" {
		t.Fatalf("unexpected command: %+v", first)
	}
	lease, err := s.ClaimCommand(ctx, first, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCommand(ctx, first, time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate command claimed a running job: %v", err)
	}
	beforeHeartbeat := len(pendingMessages(t, s))
	if err := s.Renew(ctx, lease, time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(pendingMessages(t, s)) != beforeHeartbeat {
		t.Fatal("lease heartbeat emitted an event")
	}
	if err := s.Fail(ctx, lease, "provider unavailable", true, time.Hour, 3); err != nil {
		t.Fatal(err)
	}
	second := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if second.Generation != 2 {
		t.Fatalf("retry did not advance generation: %+v", second)
	}
	for _, command := range []domain.Command{first, second} {
		if _, err := s.ClaimCommand(ctx, command, time.Minute, 3); !errors.Is(err, ErrNotFound) {
			t.Fatalf("stale or delayed command accepted: %+v, %v", command, err)
		}
	}
	var published []domain.OutboxMessage
	if _, err := s.DispatchOutbox(ctx, func(_ context.Context, message domain.OutboxMessage) error {
		published = append(published, message)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, message := range published {
		if message.Topic == domain.AnalysisTopic {
			var command domain.Command
			if err := json.Unmarshal(message.Payload, &command); err != nil {
				t.Fatal(err)
			}
			if command.Generation == second.Generation {
				t.Fatal("delayed retry published before available_at")
			}
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET available_at = now() WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimCommand(ctx, second, time.Minute, 3)
	if err != nil || next.Job.Attempts != 2 || next.PreviousToken != lease.Token {
		t.Fatalf("retry lost attempt or fencing token: %+v %v", next, err)
	}
	if err := s.Complete(ctx, lease, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old lease completed a retried job: %v", err)
	}
}

func TestCommandClaimsAreExclusive(t *testing.T) {
	s := testStore(t)
	job := createJob(t, s)
	command := commandFor(t, s, job.ID, domain.AnalysisTopic)
	results := make(chan error, 12)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := s.ClaimCommand(context.Background(), command, time.Minute, 3)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	claims := 0
	for err := range results {
		if err == nil {
			claims++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if claims != 1 {
		t.Fatalf("command claimed %d times", claims)
	}
}

func TestRenderHandoffIsDurable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	command := commandFor(t, s, job.ID, domain.AnalysisTopic)
	lease, err := s.ClaimCommand(ctx, command, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	plan := lease.Job
	plan.Stage = "render"
	plan.Duration = 30
	plan.Transcript = []domain.Segment{{Start: 1, End: 8, Text: "hello"}}
	plan.Clips = []domain.Clip{{ID: domain.NewID(), Title: "One", Reason: "Moment", Start: 1, End: 8, Revision: 1}}
	if err := s.Handoff(ctx, lease, plan); err != nil {
		t.Fatal(err)
	}
	render := commandFor(t, s, job.ID, domain.RenderTopic)
	if render.Generation != 2 || render.Stage != "render" {
		t.Fatalf("wrong render command: %+v", render)
	}
	if err := s.Checkpoint(ctx, lease, plan); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("analysis lease survived handoff: %v", err)
	}
	if _, err := s.ClaimCommand(ctx, command, time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old analysis command survived handoff: %v", err)
	}
	renderLease, err := s.ClaimCommand(ctx, render, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	if renderLease.Job.Attempts != 1 || !reflect.DeepEqual(renderLease.Job.Clips, plan.Clips) || !reflect.DeepEqual(renderLease.Job.Transcript, plan.Transcript) {
		t.Fatalf("render checkpoint lost: %+v", renderLease.Job)
	}
	if err := s.Complete(ctx, renderLease, plan.Clips); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCommand(ctx, render, time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed render was claimed again: %v", err)
	}
}

func TestExpiredLeaseRecoveryAndDeadLetter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	first := commandFor(t, s, job.ID, domain.AnalysisTopic)
	lease, err := s.ClaimCommand(ctx, first, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	expire(t, s, job.ID)
	if err := s.RecoverExpired(ctx, 2); err != nil {
		t.Fatal(err)
	}
	second := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if second.Generation != 2 {
		t.Fatalf("expired lease did not queue another generation: %+v", second)
	}
	count := len(pendingMessages(t, s))
	if err := s.RecoverExpired(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if len(pendingMessages(t, s)) != count {
		t.Fatal("recovery emitted duplicate commands")
	}
	if err := s.Renew(ctx, lease, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired owner retained lease: %v", err)
	}
	if _, err := s.ClaimCommand(ctx, second, time.Minute, 2); err != nil {
		t.Fatal(err)
	}
	expire(t, s, job.ID)
	if err := s.RecoverExpired(ctx, 2); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Get(ctx, job.ID)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("retry exhaustion not terminal: %+v %v", failed, err)
	}
	letters := 0
	for _, message := range pendingMessages(t, s) {
		if message.Topic == domain.DeadLettersTopic {
			var letter domain.DeadLetter
			if err := json.Unmarshal(message.Payload, &letter); err != nil {
				t.Fatal(err)
			}
			if letter.JobID != job.ID || letter.Kind != "job_failed" || letter.Error != failed.Error || letter.SourceTopic != domain.AnalysisTopic || letter.Offset != -1 {
				t.Fatalf("invalid dead letter: %+v", letter)
			}
			letters++
		}
	}
	if letters != 1 {
		t.Fatalf("expected one terminal dead letter, got %d", letters)
	}
}

func TestOutboxPublishFailureRetainsBatch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	createJob(t, s)
	before := pendingMessages(t, s)
	calls := 0
	if count, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error {
		calls++
		if calls == 2 {
			return errors.New("broker unavailable")
		}
		return nil
	}); err == nil || count != 0 || calls != 2 {
		t.Fatalf("publish failure not returned: count=%d calls=%d err=%v", count, calls, err)
	}
	if after := pendingMessages(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("failed publish lost outbox messages")
	}
	var delivered []domain.OutboxMessage
	count, err := s.DispatchOutbox(ctx, func(_ context.Context, message domain.OutboxMessage) error {
		delivered = append(delivered, message)
		return nil
	})
	if err != nil || count != len(before) || !reflect.DeepEqual(before, delivered) || len(pendingMessages(t, s)) != 0 {
		t.Fatalf("retry did not deliver original batch: count=%d err=%v", count, err)
	}
}

func TestOutboxUsesOnePublisher(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	createJob(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error {
			once.Do(func() { close(entered) })
			<-release
			return nil
		})
		done <- err
	}()
	<-entered
	second, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	count, err := s.DispatchOutbox(second, func(context.Context, domain.OutboxMessage) error {
		t.Error("a second relay published out of order")
		return nil
	})
	close(release)
	if err != nil || count != 0 {
		t.Fatalf("second publisher did not yield: %d %v", count, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEventProjectionReplayAndOrdering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, sequence := range []int64{3, 1, 2, 3, 1} {
		event := domain.JobEvent{ID: fmt.Sprintf("%s:%d", job.ID, sequence), JobID: job.ID, Sequence: sequence, Type: "stage.changed", Status: "running", Stage: "render", Attempt: 1, OccurredAt: at}
		if err := s.RecordEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.Events(ctx, job.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("duplicate replay was not idempotent: %+v %v", events, err)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) || !event.OccurredAt.Equal(at) {
			t.Fatalf("projection not ordered: %+v", events)
		}
	}
	letter := domain.DeadLetter{ID: "invalid:analysis:0:7", Kind: "invalid_message", Error: "bad JSON", SourceTopic: domain.AnalysisTopic, Partition: 0, Offset: 7, OccurredAt: at}
	for range 2 {
		if err := s.RecordDeadLetter(ctx, letter); err != nil {
			t.Fatal(err)
		}
	}
	letters, err := s.DeadLetters(ctx)
	if err != nil || len(letters) != 1 || !reflect.DeepEqual(letters[0], letter) {
		t.Fatalf("dead letter replay was not idempotent: %+v %v", letters, err)
	}
}

func TestMigrationsDoNotRedispatchExistingWork(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	command := commandFor(t, s, job.ID, domain.AnalysisTopic)
	lease, err := s.ClaimCommand(ctx, command, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	before := pendingMessages(t, s)
	if err := MigrateAll(ctx, s.pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if after := pendingMessages(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("repeated startup emitted duplicate work")
	}
	if err := s.Renew(ctx, lease, time.Minute); err != nil {
		t.Fatalf("repeated migration revoked an active lease: %v", err)
	}
}

func TestMigrationQueuesLegacyJobsAndFencesOldWorker(t *testing.T) {
	s := testStoreVersion(t, true)
	ctx := context.Background()
	active := createJob(t, s)
	token := domain.NewID()
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET status = 'running', attempts = 1, lease_token = $2, lease_until = now() + interval '1 minute' WHERE id = $1`, active.ID, token); err != nil {
		t.Fatal(err)
	}
	lease := Lease{Job: active, Token: token}
	queued := createJob(t, s)
	if err := MigrateAll(ctx, s.pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err := s.Renew(ctx, lease, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("legacy worker retained its lease after upgrade: %v", err)
	}
	for _, job := range []domain.Job{active, queued} {
		command := commandFor(t, s, job.ID, domain.AnalysisTopic)
		if command.Generation != 1 {
			t.Fatalf("legacy job not dispatched exactly once: %+v", command)
		}
		current, err := s.ClaimCommand(ctx, command, time.Minute, 3)
		if err != nil {
			t.Fatal(err)
		}
		if job.ID == active.ID && current.PreviousToken != lease.Token {
			t.Fatalf("upgrade lost old token needed for cleanup: %+v", current)
		}
	}
}

func TestQueuedExhaustedCommandBecomesDeadLetter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	command := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET attempts = 3 WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCommand(ctx, command, time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("exhausted command was claimed: %v", err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("exhausted queued job was stranded: %+v %v", got, err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE topic = $1`, domain.DeadLettersTopic).Scan(&count); err != nil || count != 1 {
		t.Fatalf("exhaustion did not emit DLQ: count=%d %v", count, err)
	}
}

func TestLostQueuedCommandIsReconciled(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	original := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if count, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error { return nil }); err != nil || count != 2 {
		t.Fatalf("initial publication: %d %v", count, err)
	}
	var dispatched *time.Time
	var sequence int64
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at, event_sequence FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched, &sequence); err != nil || dispatched == nil || sequence != 1 {
		t.Fatalf("publish did not save dispatch time or emitted an event: time=%v sequence=%d err=%v", dispatched, sequence, err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil || !got.UpdatedAt.Equal(job.UpdatedAt) {
		t.Fatalf("publication changed job state: %+v %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET dispatched_at = now() - interval '16 minutes' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- s.RecoverExpired(ctx, 3) }()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	messages := pendingMessages(t, s)
	if len(messages) != 1 || messages[0].Topic != domain.AnalysisTopic {
		t.Fatalf("reconciliation must enqueue one command without events: %+v", messages)
	}
	recovered := commandFor(t, s, job.ID, domain.AnalysisTopic)
	if recovered != original {
		t.Fatalf("reconciliation invalidated the original command: original=%+v recovered=%+v", original, recovered)
	}
	if err := s.RecoverExpired(ctx, 3); err != nil || len(pendingMessages(t, s)) != 1 {
		t.Fatalf("repeat sweep duplicated pending command: %v", err)
	}
	if count, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error { return nil }); err != nil || count != 1 {
		t.Fatalf("recovered publication: %d %v", count, err)
	}
	if err := s.RecoverExpired(ctx, 3); err != nil || len(pendingMessages(t, s)) != 0 {
		t.Fatalf("recent publication was immediately reconciled again: %v", err)
	}
	lease, err := s.ClaimCommand(ctx, original, time.Minute, 3)
	if err != nil || lease.Job.Attempts != 1 {
		t.Fatalf("original delivery no longer valid: %+v %v", lease, err)
	}
	if _, err := s.ClaimCommand(ctx, recovered, time.Minute, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reconciled duplicate claimed running job: %v", err)
	}
}

func TestOldPublicationDoesNotStampNewGeneration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	_, err := s.DispatchOutbox(ctx, func(_ context.Context, message domain.OutboxMessage) error {
		if message.Topic != domain.AnalysisTopic {
			return nil
		}
		if err := s.Cancel(ctx, job.ID); err != nil {
			return err
		}
		return s.Retry(ctx, job.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var dispatched *time.Time
	var generation int64
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at, dispatch_generation FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched, &generation); err != nil || dispatched != nil || generation != 2 {
		t.Fatalf("old command stamped new generation: time=%v generation=%d err=%v", dispatched, generation, err)
	}
	if _, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched); err != nil || dispatched == nil {
		t.Fatalf("new command not stamped: %v %v", dispatched, err)
	}
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Retry(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched); err != nil || dispatched != nil {
		t.Fatalf("new generation retained stale dispatch time: %v %v", dispatched, err)
	}
}

func TestPublishRollbackDoesNotAdvanceDispatchTime(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first, second := createJob(t, s), createJob(t, s)
	_, err := s.DispatchOutbox(ctx, func(_ context.Context, message domain.OutboxMessage) error {
		if message.Key == second.ID {
			return errors.New("broker unavailable")
		}
		return nil
	})
	if err == nil {
		t.Fatal("failed publication succeeded")
	}
	var dispatched *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at FROM jobs WHERE id = $1`, first.ID).Scan(&dispatched); err != nil || dispatched != nil {
		t.Fatalf("rolled back publication retained dispatch time: %v %v", dispatched, err)
	}
	if messages := pendingMessages(t, s); len(messages) != 4 {
		t.Fatalf("failed publication dropped messages: %+v", messages)
	}
}

func TestReconciliationSkipsDelayedAndActiveJobs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pending, delayed, running := createJob(t, s), createJob(t, s), createJob(t, s)
	runningCommand := commandFor(t, s, running.ID, domain.AnalysisTopic)
	if _, err := s.DispatchOutbox(ctx, func(context.Context, domain.OutboxMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimCommand(ctx, runningCommand, time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET dispatched_at = now() - interval '16 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE jobs SET available_at = now() + interval '1 hour' WHERE id = $1`, delayed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO outbox (topic, message_key, payload, available_at) VALUES ($1, $2, jsonb_build_object('id', $2::text || ':1', 'job_id', $2::text, 'generation', 1, 'stage', 'probe'), now() + interval '1 hour')`, domain.AnalysisTopic, pending.ID); err != nil {
		t.Fatal(err)
	}
	before := pendingMessages(t, s)
	if err := s.RecoverExpired(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if after := pendingMessages(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("active, delayed, or pending work was reconciled: before=%+v after=%+v", before, after)
	}
}

func TestDispatchMigrationBackfillsAlreadyPublishedJobs(t *testing.T) {
	s := testStoreVersion(t, true)
	ctx := context.Background()
	if err := Migrate(ctx, s.pool, "../../migrations/002_streams.sql"); err != nil {
		t.Fatal(err)
	}
	job := createJob(t, s)
	if _, err := s.pool.Exec(ctx, `DELETE FROM outbox WHERE message_key = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAll(ctx, s.pool, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var dispatched *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched); err != nil || dispatched == nil {
		t.Fatalf("already-published job omitted from reconciliation: %v %v", dispatched, err)
	}
	if messages := pendingMessages(t, s); len(messages) != 0 {
		t.Fatalf("dispatch migration emitted lifecycle events: %+v", messages)
	}
}

func TestPublishingRunningCommandDoesNotBlockHeartbeat(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	job := createJob(t, s)
	command := commandFor(t, s, job.ID, domain.AnalysisTopic)
	lease, err := s.ClaimCommand(ctx, command, time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	done := make(chan error, 1)
	go func() {
		_, err := s.DispatchOutbox(ctx, func(ctx context.Context, message domain.OutboxMessage) error {
			if message.Topic != domain.EventsTopic {
				return nil
			}
			var event domain.JobEvent
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return err
			}
			if event.Type == "stage.started" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("relay stopped before later publication: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	renewCtx, stop := context.WithTimeout(ctx, time.Second)
	renewErr := s.Renew(renewCtx, lease, time.Minute)
	stop()
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if renewErr != nil {
		t.Fatalf("relay held running job lock while publishing later messages: %v", renewErr)
	}
	var dispatched *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT dispatched_at FROM jobs WHERE id = $1`, job.ID).Scan(&dispatched); err != nil || dispatched != nil {
		t.Fatalf("running job dispatch timestamp changed: %v %v", dispatched, err)
	}
}
