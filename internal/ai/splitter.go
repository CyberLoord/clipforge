package ai

import (
	"context"
	"fmt"
	"math"

	"github.com/CyberLoord/clipforge/internal/domain"
)

type Splitter struct{}

func NewSplitter() *Splitter {
	return &Splitter{}
}

func (s *Splitter) Select(ctx context.Context, _ []domain.Segment, duration float64) ([]domain.Clip, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < domain.MinVideoSeconds {
		return nil, fmt.Errorf("video must last at least %d seconds", domain.MinVideoSeconds)
	}
	clips := make([]domain.Clip, 0, 3)
	for i := 0; i < 3; i++ {
		start := duration * float64(i) / 3
		end := duration * float64(i+1) / 3
		if duration > 135 {
			start = float64(i) / 2 * (duration - 45)
			end = start + 45
		}
		if i == 2 {
			end = duration
		}
		clips = append(clips, domain.Clip{
			Start: start, End: end, Title: fmt.Sprintf("Фрагмент %d", i+1),
			Reason: "Фрагмент выбран по времени, без анализа содержания.",
		})
	}
	return clips, domain.ValidateClips(clips, duration)
}
