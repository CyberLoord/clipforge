package ai

import (
	"context"
	"fmt"
	"math"

	"github.com/CyberLoord/clipforge/internal/domain"
)

type Demo struct{}

func NewDemo() *Demo {
	return &Demo{}
}

func (d *Demo) Transcribe(ctx context.Context, _ string, duration float64) ([]domain.Segment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	phrases := []string{
		"Демо: это заранее подготовленная расшифровка.",
		"Она не связана с речью в загруженном видео.",
		"В этом режиме можно проверить создание клипов.",
		"Изображение вписывается в вертикальный кадр.",
		"Субтитры показываются по временным меткам.",
		"Границы клипа можно изменить перед повторным экспортом.",
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < domain.MinVideoSeconds || duration > 24*60*60 {
		return nil, fmt.Errorf("invalid demo duration: %g", duration)
	}
	segments := make([]domain.Segment, 0, int(duration/5)+1)
	for start := 0.0; start < duration; start += 5 {
		segments = append(segments, domain.Segment{Start: start, End: math.Min(start+5, duration), Text: phrases[len(segments)%len(phrases)]})
	}
	return segments, domain.ValidateSegments(segments, duration)
}

func (d *Demo) Select(ctx context.Context, segments []domain.Segment, duration float64) ([]domain.Clip, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidateSegments(segments, duration); err != nil {
		return nil, err
	}
	clips, err := NewSplitter().Select(ctx, nil, duration)
	if err != nil {
		return nil, err
	}
	for i := range clips {
		clips[i].Title = fmt.Sprintf("Демо-фрагмент %d", i+1)
		clips[i].Reason = "Границы выбраны автоматически для проверки экспорта, без анализа содержания."
	}
	return clips, nil
}
