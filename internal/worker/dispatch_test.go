package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/store"
)

func TestAnalysisHandsOffBeforeRendering(t *testing.T) {
	lease := testLease("probe")
	queue := &fakeQueue{claimLease: lease}
	media := &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	if err := w.Handle(context.Background(), domain.Command{JobID: lease.Job.ID}); err != nil {
		t.Fatal(err)
	}
	if queue.handoff == nil || queue.handoff.Stage != "render" || len(queue.handoff.Clips) == 0 || media.renders != 0 || len(queue.completed) != 0 {
		t.Fatalf("analysis did not hand off: queue=%+v media=%+v", queue, media)
	}
	queue.claimLease.Job = *queue.handoff
	if err := w.Handle(context.Background(), domain.Command{JobID: lease.Job.ID}); err != nil {
		t.Fatal(err)
	}
	if media.renders != 1 || len(queue.completed) != 1 {
		t.Fatal("render command did not finish saved plan")
	}
}

func TestDuplicateCommandDoesNoWork(t *testing.T) {
	queue, media := &fakeQueue{claimErr: store.ErrNotFound}, &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	if err := w.Handle(context.Background(), domain.Command{}); err != nil || media.probes != 0 || media.renders != 0 {
		t.Fatalf("duplicate executed: %v %+v", err, media)
	}
}

func TestFailedPersistenceDoesNotAcknowledge(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	for _, failure := range []string{"claim", "handoff", "failure", "complete"} {
		t.Run(failure, func(t *testing.T) {
			queue := &fakeQueue{claimLease: testLease("select")}
			provider := &fakeAI{}
			switch failure {
			case "claim":
				queue.claimErr = databaseErr
			case "handoff":
				queue.handoffErr = databaseErr
			case "failure":
				queue.failErr = databaseErr
				provider.err = errors.New("bad input")
			case "complete":
				queue.claimLease = testLease("render")
				queue.completeErr = databaseErr
			}
			w := testWorker(t, queue, &fakeMedia{}, provider)
			if err := w.Handle(context.Background(), domain.Command{}); !errors.Is(err, databaseErr) {
				t.Fatalf("persistence failure acknowledged: %v", err)
			}
		})
	}
}
