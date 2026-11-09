package httpapi

import (
	"net/http"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	job, ok := s.job(w, r)
	if !ok {
		return
	}
	events, err := s.repo.Events(r.Context(), job.ID)
	if err != nil {
		s.internal(w, err)
		return
	}
	if events == nil {
		events = []domain.JobEvent{}
	}
	s.json(w, http.StatusOK, events)
}

func (s *Server) deadLetters(w http.ResponseWriter, r *http.Request) {
	letters, err := s.repo.DeadLetters(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	if letters == nil {
		letters = []domain.DeadLetter{}
	}
	s.json(w, http.StatusOK, letters)
}
