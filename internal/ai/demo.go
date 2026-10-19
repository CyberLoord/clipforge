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
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < domain.MinClipSeconds || duration > 24*60*60 {
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
	count := min(3, int(duration/domain.MinClipSeconds))
	if count < 1 {
		return nil, fmt.Errorf("video must last at least %d seconds", domain.MinClipSeconds)
	}
	length := math.Min(45, duration/float64(count))
	clips := make([]domain.Clip, 0, count)
	for i := 0; i < count; i++ {
		start := float64(i) * length
		if count > 1 {
			start = float64(i) * (duration - length) / float64(count-1)
		}
		clips = append(clips, domain.Clip{Start: start, End: math.Min(start+length, duration), Title: fmt.Sprintf("Демо-фрагмент %d", i+1), Reason: "Границы выбраны автоматически для проверки экспорта, без анализа содержания."})
	}
	return clips, domain.ValidateClips(clips, duration)
}
