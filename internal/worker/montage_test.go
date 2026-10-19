package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
)

type fakeMontage struct {
	calls   int
	frames  []domain.Frame
	invalid bool
}

func montagePlans() []domain.Clip {
	return []domain.Clip{
		{Style: "dynamic", Title: "Первый", Reason: "Короткие сцены", Cuts: []domain.Cut{{Start: 10, End: 12}, {Start: 1, End: 3}, {Start: 6, End: 8}}},
		{Style: "cinematic", Title: "Второй", Reason: "Длинные сцены", Cuts: []domain.Cut{{Start: 1, End: 5}, {Start: 9, End: 14}}},
	}
}

func (m *fakeMontage) SelectMontages(_ context.Context, frames []domain.Frame, _ float64) ([]domain.Clip, error) {
	m.calls++
	m.frames = frames
	clips := montagePlans()
	if m.invalid {
		clips[0].Cuts[0].End = 9999
	}
	return clips, nil
}

func TestMontagePipeline(t *testing.T) {
	queue, media, provider := &fakeQueue{}, &fakeMedia{probeDuration: 15}, &fakeMontage{}
	w := testWorker(t, queue, media, &fakeAI{})
	w.montage = provider
	lease := testLease("probe")
	lease.Job.Mode = "montage"
	w.process(context.Background(), lease)
	if queue.failure != "" || !reflect.DeepEqual(queue.stages, []string{"select", "render"}) || len(queue.completed) != 2 {
		t.Fatalf("pipeline failed: %+v", queue)
	}
	if provider.calls != 1 || len(provider.frames) != 2 || media.samples != 1 || media.extracts != 0 || media.montages != 2 || media.renderedSegments != 0 {
		t.Fatalf("unexpected media path: media=%+v provider=%+v", media, provider)
	}
	for i, clip := range queue.completed {
		if !domain.ValidID(clip.ID) || clip.Path == "" || !reflect.DeepEqual(media.renderedCuts[i], montagePlans()[i].Cuts) {
			t.Fatalf("montage plan or output was lost: %+v", clip)
		}
	}
}

func TestMontageRenderResumesWithoutProvider(t *testing.T) {
	queue, media := &fakeQueue{}, &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	lease := testLease("render")
	lease.Job.Mode = "montage"
	lease.Job.Clips = montagePlans()
	for i := range lease.Job.Clips {
		lease.Job.Clips[i].ID = domain.NewID()
		lease.Job.Clips[i].Revision = 1
	}
	w.process(context.Background(), lease)
	if queue.failure != "" || len(queue.completed) != 2 || media.samples != 0 || media.montages != 2 {
		t.Fatalf("saved montage did not resume: queue=%+v media=%+v", queue, media)
	}
}

func TestMontageNeedsProviderBeforeSampling(t *testing.T) {
	queue, media := &fakeQueue{}, &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	lease := testLease("select")
	lease.Job.Mode = "montage"
	w.process(context.Background(), lease)
	if !strings.Contains(queue.failure, "OPENAI_API_KEY") || media.samples != 0 || queue.retry {
		t.Fatalf("missing provider was not rejected: %+v", queue)
	}
}

func TestMontageRejectsInvalidPlanBeforeRender(t *testing.T) {
	queue, media := &fakeQueue{}, &fakeMedia{}
	w := testWorker(t, queue, media, &fakeAI{})
	w.montage = &fakeMontage{invalid: true}
	lease := testLease("select")
	lease.Job.Mode = "montage"
	w.process(context.Background(), lease)
	if queue.failure == "" || media.renders != 0 || len(queue.completed) != 0 {
		t.Fatalf("invalid plan was rendered: %+v", queue)
	}
}
