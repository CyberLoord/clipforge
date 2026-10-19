package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/ai"
	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/store"
)

type fakeQueue struct {
	stages      []string
	completed   []domain.Clip
	failure     string
	retry       bool
	renewErr    error
	completeErr error
}

func (q *fakeQueue) Claim(context.Context, time.Duration, int) (store.Lease, error) {
	return store.Lease{}, store.ErrNotFound
}

func (q *fakeQueue) Renew(context.Context, store.Lease, time.Duration) error {
	return q.renewErr
}

func (q *fakeQueue) Checkpoint(_ context.Context, _ store.Lease, job domain.Job) error {
	q.stages = append(q.stages, job.Stage)
	return nil
}

func (q *fakeQueue) Complete(_ context.Context, _ store.Lease, clips []domain.Clip) error {
	q.completed = append([]domain.Clip(nil), clips...)
	return q.completeErr
}

func (q *fakeQueue) Fail(_ context.Context, _ store.Lease, message string, retry bool, _ time.Duration, _ int) error {
	q.failure = message
	q.retry = retry
	return nil
}

type fakeMedia struct {
	probes        int
	extracts      int
	renders       int
	blockRender   bool
	probeDuration float64
	paths         []string
}

func (m *fakeMedia) Probe(context.Context, string) (domain.Info, error) {
	m.probes++
	duration := m.probeDuration
	if duration == 0 {
		duration = 90
	}
	return domain.Info{Duration: duration, Width: 1280, Height: 720}, nil
}

func (m *fakeMedia) Extract(_ context.Context, _, output string) error {
	m.extracts++
	return os.WriteFile(output, []byte("audio"), 0600)
}

func (m *fakeMedia) Render(ctx context.Context, _, output string, _ []domain.Segment, _ domain.Clip) error {
	m.renders++
	m.paths = append(m.paths, output)
	if err := os.WriteFile(output, []byte("video"), 0600); err != nil {
		return err
	}
	if m.blockRender {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

type fakeAI struct {
	transcribes int
	selects     int
	err         error
	block       bool
	invalid     bool
}

func (a *fakeAI) Transcribe(ctx context.Context, _ string, _ float64) ([]domain.Segment, error) {
	a.transcribes++
	if a.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if a.err != nil {
		return nil, a.err
	}
	if a.invalid {
		return []domain.Segment{{Start: 10, End: 5, Text: "invalid"}}, nil
	}
	return []domain.Segment{{Start: 0, End: 30, Text: "Example transcript."}}, nil
}

func (a *fakeAI) Select(context.Context, []domain.Segment, float64) ([]domain.Clip, error) {
	a.selects++
	if a.err != nil {
		return nil, a.err
	}
	return []domain.Clip{{Start: 0, End: 30, Title: "Example", Reason: "Complete thought."}}, nil
}

func testWorker(t *testing.T, queue *fakeQueue, media *fakeMedia, provider *fakeAI) *Worker {
	t.Helper()
	return New(queue, media, provider, provider, Config{
		DataDir: t.TempDir(), LeaseDuration: time.Second, StageTimeout: time.Second,
		MaxDuration: 1800, MaxAttempts: 3, Workers: 1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testLease(stage string) store.Lease {
	return store.Lease{Token: domain.NewID(), Job: domain.Job{
		ID: domain.NewID(), Stage: stage, Status: "running", Mode: "demo", Attempts: 1,
		SourcePath: "/source.mp4", Duration: 90,
		Transcript: []domain.Segment{{Start: 0, End: 30, Text: "Example transcript."}},
		Clips:      []domain.Clip{{ID: domain.NewID(), Start: 0, End: 30, Title: "Example", Reason: "Complete thought.", Revision: 1}},
	}}
}

func TestPipelineCheckpointsAndCreatesOutput(t *testing.T) {
	queue, media, provider := &fakeQueue{}, &fakeMedia{}, &fakeAI{}
	w := testWorker(t, queue, media, provider)
	w.process(context.Background(), testLease("probe"))
	if queue.failure != "" || !reflect.DeepEqual(queue.stages, []string{"transcribe", "select", "render"}) {
		t.Fatalf("stages = %v, failure = %q", queue.stages, queue.failure)
	}
	if media.probes != 1 || media.extracts != 1 || provider.transcribes != 1 || provider.selects != 1 || media.renders != 1 || len(queue.completed) != 1 {
		t.Fatalf("incomplete pipeline: media=%+v provider=%+v completed=%v", media, provider, queue.completed)
	}
	clip := queue.completed[0]
	if clip.ID == "" || clip.Revision != 1 || filepath.IsAbs(clip.Path) {
		t.Fatalf("invalid stored output: %+v", clip)
	}
	if _, err := os.Stat(filepath.Join(w.cfg.DataDir, clip.Path)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(w.cfg.DataDir, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary audio left behind: %v %v", entries, err)
	}
}

func TestTransientAndPermanentFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		provider  fakeAI
		wantRetry bool
	}{
		{name: "rate limit", provider: fakeAI{err: &ai.APIError{Status: 429}}, wantRetry: true},
		{name: "bad credentials", provider: fakeAI{err: &ai.APIError{Status: 401}}, wantRetry: false},
		{name: "invalid timestamps", provider: fakeAI{invalid: true}, wantRetry: false},
		{name: "timeout", provider: fakeAI{block: true}, wantRetry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			queue := &fakeQueue{}
			w := testWorker(t, queue, &fakeMedia{}, &test.provider)
			w.cfg.StageTimeout = 20 * time.Millisecond
			w.process(context.Background(), testLease("transcribe"))
			if queue.failure == "" || queue.retry != test.wantRetry || len(queue.completed) != 0 {
				t.Fatalf("failure=%q retry=%v completed=%v", queue.failure, queue.retry, queue.completed)
			}
		})
	}
}

func TestLeaseLossCancelsRenderAndRemovesOutput(t *testing.T) {
	queue := &fakeQueue{renewErr: store.ErrLeaseLost}
	media := &fakeMedia{blockRender: true}
	w := testWorker(t, queue, media, &fakeAI{})
	w.cfg.LeaseDuration = 30 * time.Millisecond
	w.process(context.Background(), testLease("render"))
	if queue.failure != "" || len(queue.completed) != 0 || len(media.paths) != 1 {
		t.Fatalf("cancelled attempt changed state: %+v", queue)
	}
	if _, err := os.Stat(media.paths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled output still exists: %v", err)
	}
}

func TestCompletionFencingAndUnknownCommit(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		wantFile bool
	}{
		{name: "stale lease", err: store.ErrLeaseLost, wantFile: false},
		{name: "unknown commit", err: errors.New("connection closed"), wantFile: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			queue := &fakeQueue{completeErr: test.err}
			media := &fakeMedia{}
			w := testWorker(t, queue, media, &fakeAI{})
			w.process(context.Background(), testLease("render"))
			_, err := os.Stat(media.paths[0])
			if (err == nil) != test.wantFile {
				t.Fatalf("output existence error = %v, wantFile = %v", err, test.wantFile)
			}
		})
	}
}

func TestRecoveryResumesAtSavedStage(t *testing.T) {
	queue, media, provider := &fakeQueue{}, &fakeMedia{}, &fakeAI{}
	w := testWorker(t, queue, media, provider)
	w.process(context.Background(), testLease("select"))
	if media.probes != 0 || media.extracts != 0 || provider.transcribes != 0 || provider.selects != 1 || media.renders != 1 || len(queue.completed) != 1 {
		t.Fatalf("recovery reran completed work: media=%+v provider=%+v", media, provider)
	}
}

func TestReexportPreservesPublishedFiles(t *testing.T) {
	queue, media := &fakeQueue{}, &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	lease := testLease("render")
	old := domain.Clip{ID: domain.NewID(), Start: 40, End: 65, Title: "Other", Reason: "Different thought.", Revision: 1, Path: filepath.Join("jobs", lease.Job.ID, "existing-1-lease.mp4")}
	lease.Job.Clips = append(lease.Job.Clips, old)
	oldPath := filepath.Join(w.cfg.DataDir, old.Path)
	if err := os.MkdirAll(filepath.Dir(oldPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(filepath.Dir(oldPath), "abandoned-1-lease.mp4")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	w.process(context.Background(), lease)
	if media.renders != 1 || len(queue.completed) != 2 || queue.completed[1] != old {
		t.Fatalf("reexport rerendered published clips: renders=%d, clips=%+v", media.renders, queue.completed)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("cleanup removed a file without a previous lease token: %v", err)
	}
}

func TestModeCannotChangeBetweenStages(t *testing.T) {
	queue := &fakeQueue{}
	provider := &fakeAI{}
	w := testWorker(t, queue, &fakeMedia{}, provider)
	w.cfg.Mode = "openai"
	w.process(context.Background(), testLease("transcribe"))
	if queue.failure == "" || queue.retry || provider.transcribes != 0 {
		t.Fatalf("mode mismatch accepted: %+v", queue)
	}
}

func TestCleanupKeepsCurrentLeaseAndPublishedOutput(t *testing.T) {
	w := testWorker(t, &fakeQueue{}, &fakeMedia{}, &fakeAI{})
	lease := testLease("render")
	lease.PreviousToken = domain.NewID()
	dir := filepath.Join(w.cfg.DataDir, "jobs", lease.Job.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	publishedName := "published-1-" + lease.PreviousToken + ".mp4"
	published := filepath.Join("jobs", lease.Job.ID, publishedName)
	lease.Job.Clips[0].Path = published
	for _, test := range []struct {
		name      string
		directory bool
		keep      bool
	}{
		{name: publishedName, keep: true},
		{name: ".render-clip-1-" + lease.PreviousToken + ".mp4-123.mp4"},
		{name: ".render-clip-1-" + lease.Token + ".mp4-123.mp4", keep: true},
		{name: ".subtitles-clip-1-" + lease.PreviousToken + ".mp4-123", directory: true},
		{name: ".subtitles-clip-1-" + lease.Token + ".mp4-123", directory: true, keep: true},
		{name: "unrelated", directory: true, keep: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name)
			var err error
			if test.directory {
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, []byte("data"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			w.cleanupAbandoned(lease.Job, lease.PreviousToken)
			_, err = os.Stat(path)
			if (err == nil) != test.keep {
				t.Fatalf("cleanup keep = %v, want %v, error %v", err == nil, test.keep, err)
			}
		})
	}
}

func TestStaleCleanupCannotRemoveNewerLeaseFiles(t *testing.T) {
	w := testWorker(t, &fakeQueue{}, &fakeMedia{}, &fakeAI{})
	stale := testLease("render")
	stale.PreviousToken = domain.NewID()
	newToken := domain.NewID()
	jobDir := filepath.Join(w.cfg.DataDir, "jobs", stale.Job.ID)
	audioDir := filepath.Join(w.cfg.DataDir, "tmp")
	newOutput := filepath.Join(jobDir, "clip-1-"+newToken+".mp4")
	newSubtitles := filepath.Join(jobDir, ".subtitles-clip-1-"+newToken+".mp4-random")
	newAudio := filepath.Join(audioDir, stale.Job.ID+"-"+newToken+"-random")
	oldAudio := filepath.Join(audioDir, stale.Job.ID+"-"+stale.PreviousToken+"-random")
	for _, dir := range []string{jobDir, newSubtitles, newAudio, oldAudio} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(newOutput, []byte("published after the stale claim"), 0600); err != nil {
		t.Fatal(err)
	}
	w.cleanupAbandoned(stale.Job, stale.PreviousToken)
	w.cleanupTemp(stale.Job.ID, stale.PreviousToken)
	for _, path := range []string{newOutput, newSubtitles, newAudio} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stale cleanup removed newer lease artifact: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(oldAudio); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous lease audio not cleaned: %v", err)
	}
}

func TestInvalidDurationIsPermanent(t *testing.T) {
	queue := &fakeQueue{}
	w := testWorker(t, queue, &fakeMedia{probeDuration: 1801}, &fakeAI{})
	w.process(context.Background(), testLease("probe"))
	if queue.failure == "" || queue.retry || len(queue.stages) != 0 {
		t.Fatalf("invalid input accepted: %+v", queue)
	}
}
