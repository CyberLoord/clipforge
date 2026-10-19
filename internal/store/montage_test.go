package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestMontagePlanSurvivesCheckpoint(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	job := createJob(t, s)
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Reprocess(ctx, job.ID, "montage"); err != nil {
		t.Fatal(err)
	}
	lease := claim(t, s, 3)
	lease.Job.Duration = 30
	lease.Job.Stage = "render"
	lease.Job.Clips = []domain.Clip{
		{ID: domain.NewID(), Style: "dynamic", Title: "Первый", Reason: "Жест и реакция", Revision: 1, Cuts: []domain.Cut{{Start: 10, End: 13}, {Start: 1, End: 4}}},
		{ID: domain.NewID(), Style: "cinematic", Title: "Второй", Reason: "Общий план", Revision: 1, Cuts: []domain.Cut{{Start: 2, End: 7}, {Start: 12, End: 17}}},
	}
	if err := s.Checkpoint(ctx, lease, lease.Job); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil || got.Mode != "montage" || got.Stage != "render" || !reflect.DeepEqual(got.Clips, lease.Job.Clips) {
		t.Fatalf("montage plan lost: %+v %v", got, err)
	}
	if err := s.Complete(ctx, lease, lease.Job.Clips); err != nil {
		t.Fatal(err)
	}
	if err := s.Reexport(ctx, job.ID, lease.Job.Clips[0].ID, 0, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("single-range edit accepted: %v", err)
	}
	if err := s.Reprocess(ctx, job.ID, "split"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, job.ID)
	if err != nil || got.Mode != "split" || len(got.Clips) != 0 || got.Stage != "probe" {
		t.Fatalf("montage did not reset: %+v %v", got, err)
	}
}
