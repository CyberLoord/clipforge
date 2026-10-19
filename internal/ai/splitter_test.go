package ai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestSplitterIntervals(t *testing.T) {
	for _, duration := range []float64{15, 17.642667, 59.9, 65, 135, 135.1, 1800} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			clips, err := NewSplitter().Select(context.Background(), nil, duration)
			if err != nil || len(clips) != 3 {
				t.Fatalf("clips=%v error=%v", clips, err)
			}
			if err := domain.ValidateClips(clips, duration); err != nil {
				t.Fatal(err)
			}
			for i, clip := range clips {
				if clip.End-clip.Start > 45+1e-9 || clip.End-clip.Start < 5 || clip.End-clip.Start >= duration {
					t.Fatalf("invalid excerpt length: %+v", clip)
				}
				if clip.Title != fmt.Sprintf("Фрагмент %d", i+1) || !strings.Contains(clip.Reason, "без анализа содержания") {
					t.Fatalf("misleading metadata: %+v", clip)
				}
				if i > 0 && clip.Start < clips[i-1].End {
					t.Fatalf("clips overlap: %v", clips)
				}
				if duration <= 135 && i > 0 && clip.Start != clips[i-1].End {
					t.Fatalf("gap in short source: %v", clips)
				}
				if duration > 135 && (clip.End-clip.Start != 45 || clip.Start != float64(i)/2*(duration-45)) {
					t.Fatalf("long-source intervals are not evenly spaced: %v", clips)
				}
			}
			if clips[0].Start != 0 || clips[2].End != duration {
				t.Fatalf("source boundaries not preserved: %v", clips)
			}
		})
	}
}

func TestSplitterRejectsInvalidSource(t *testing.T) {
	for _, duration := range []float64{-1, 0, 5, 14.999, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := NewSplitter().Select(context.Background(), nil, duration); err == nil {
			t.Fatalf("accepted source duration %g", duration)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSplitter().Select(ctx, nil, 15); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
