package domain

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

func ValidateMontages(clips []Clip, duration float64) error {
	if !finite(duration) || duration < MinVideoSeconds || len(clips) < 2 || len(clips) > 3 {
		return errors.New("expected two or three montage variants for a video of at least 15 seconds")
	}
	styles := map[string]bool{}
	for i, clip := range clips {
		switch clip.Style {
		case "dynamic", "cinematic", "story":
		default:
			return errors.New("unknown montage style")
		}
		if styles[clip.Style] {
			return errors.New("montage styles must be distinct")
		}
		styles[clip.Style] = true
		if strings.TrimSpace(clip.Title) == "" || strings.TrimSpace(clip.Reason) == "" || len(clip.Title) > 300 || len(clip.Reason) > 2000 {
			return errors.New("invalid montage title or reason")
		}
		if err := ValidateCuts(clip.Cuts, duration); err != nil {
			return fmt.Errorf("montage %d: %w", i+1, err)
		}
		for _, previous := range clips[:i] {
			if slices.EqualFunc(clip.Cuts, previous.Cuts, func(a, b Cut) bool {
				return math.Abs(a.Start-b.Start) < 0.1 && math.Abs(a.End-b.End) < 0.1
			}) {
				return errors.New("montage variants repeat the same cuts")
			}
		}
	}
	return nil
}

func ValidateCuts(cuts []Cut, duration float64) error {
	if !finite(duration) || duration < MinVideoSeconds || len(cuts) < 2 || len(cuts) > 12 {
		return errors.New("a montage needs two to twelve cuts")
	}
	total := 0.0
	for i, cut := range cuts {
		if !finite(cut.Start) || !finite(cut.End) || cut.Start < 0 || cut.End > duration || cut.End-cut.Start < 1-1e-6 || cut.End-cut.Start > 15+1e-6 {
			return errors.New("each cut must last 1 to 15 seconds and fit inside the source")
		}
		total += cut.End - cut.Start
		for _, previous := range cuts[:i] {
			if math.Min(cut.End, previous.End)-math.Max(cut.Start, previous.Start) > 1e-6 {
				return errors.New("montage cuts must not repeat source footage")
			}
		}
	}
	if total < MinClipSeconds-1e-6 || total > math.Min(MaxClipSeconds, duration*0.85)+1e-6 {
		return errors.New("montage must last 5 to 60 seconds and use at most 85% of the source")
	}
	return nil
}
