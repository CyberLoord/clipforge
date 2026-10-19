package ai

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/CyberLoord/clipforge/internal/domain"
)

type Transcriber interface {
	Transcribe(context.Context, string, float64) ([]domain.Segment, error)
}

type Selector interface {
	Select(context.Context, []domain.Segment, float64) ([]domain.Clip, error)
}

type MontageSelector interface {
	SelectMontages(context.Context, []domain.Frame, float64) ([]domain.Clip, error)
}

type APIError struct {
	Status int
}

func (e *APIError) Error() string {
	switch e.Status {
	case 401, 403:
		return "OpenAI rejected the API credentials"
	case 429:
		return "OpenAI rate or quota limit exceeded"
	default:
		return "OpenAI request failed: " + httpStatus(e.Status)
	}
}

func (e *APIError) Temporary() bool {
	return e.Status == 429 || e.Status >= 500
}

func IsTemporary(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}
	var network net.Error
	return errors.As(err, &network)
}
