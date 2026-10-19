package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const MinVideoSeconds = 15
const MinClipSeconds = 5
const MaxClipSeconds = 60

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Clip struct {
	ID       string  `json:"id"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Title    string  `json:"title"`
	Reason   string  `json:"reason"`
	Path     string  `json:"path"`
	Revision int     `json:"revision"`
}

type Job struct {
	ID         string    `json:"id"`
	Filename   string    `json:"filename"`
	Status     string    `json:"status"`
	Stage      string    `json:"stage"`
	Mode       string    `json:"mode"`
	Error      string    `json:"error"`
	SourcePath string    `json:"-"`
	Duration   float64   `json:"duration"`
	Transcript []Segment `json:"transcript"`
	Clips      []Clip    `json:"clips"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Info struct {
	Duration float64
	Width    int
	Height   int
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func ValidateSegments(segments []Segment, duration float64) error {
	if !finite(duration) || duration <= 0 || len(segments) == 0 || len(segments) > 20000 {
		return errors.New("invalid transcript")
	}
	previous := 0.0
	for _, s := range segments {
		if !finite(s.Start) || !finite(s.End) || s.Start < 0 || s.End > duration || s.Start >= s.End || s.Start < previous || strings.TrimSpace(s.Text) == "" || len(s.Text) > 10000 {
			return errors.New("invalid transcript segment")
		}
		previous = s.Start
	}
	return nil
}

func ValidateClip(clip Clip, duration float64) error {
	if !finite(duration) || !finite(clip.Start) || !finite(clip.End) || clip.Start < 0 || clip.End > duration || clip.Start >= clip.End {
		return errors.New("clip boundaries are outside the video")
	}
	length := clip.End - clip.Start
	if length < MinClipSeconds || length > MaxClipSeconds {
		return fmt.Errorf("clip duration must be between %d and %d seconds", MinClipSeconds, MaxClipSeconds)
	}
	return nil
}

func ValidateClips(clips []Clip, duration float64) error {
	if len(clips) == 0 || len(clips) > 3 {
		return errors.New("expected one to three clips")
	}
	for i, c := range clips {
		if err := ValidateClip(c, duration); err != nil {
			return fmt.Errorf("clip %d: %w", i+1, err)
		}
		if strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Reason) == "" || len(c.Title) > 300 || len(c.Reason) > 2000 {
			return errors.New("invalid clip title or reason")
		}
		for _, p := range clips[:i] {
			overlap := math.Min(c.End, p.End) - math.Max(c.Start, p.Start)
			if overlap > 0.5*math.Min(c.End-c.Start, p.End-p.Start) {
				return errors.New("clips overlap by more than half their duration")
			}
		}
	}
	return nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
