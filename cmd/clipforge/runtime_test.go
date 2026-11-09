package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/CyberLoord/clipforge/internal/stream"
)

type commandStub struct {
	calls int
	err   error
}

func (w *commandStub) Handle(context.Context, domain.Command) error { w.calls++; return w.err }

func TestCommandsCannotCrossProcessingLanes(t *testing.T) {
	id := domain.NewID()
	for _, tc := range []struct {
		stage, topic string
		valid        bool
	}{
		{"probe", domain.AnalysisTopic, true},
		{"select", domain.AnalysisTopic, true},
		{"render", domain.RenderTopic, true},
		{"render", domain.AnalysisTopic, false},
		{"probe", domain.RenderTopic, false},
		{"completed", domain.AnalysisTopic, false},
	} {
		t.Run(tc.stage+tc.topic, func(t *testing.T) {
			w := &commandStub{}
			value, _ := json.Marshal(domain.Command{ID: id + ":1", JobID: id, Generation: 1, Stage: tc.stage})
			err := commandHandler(w, tc.topic)(context.Background(), stream.Record{Topic: tc.topic, Key: id, Value: value})
			if (err == nil) != tc.valid || (w.calls == 1) != tc.valid {
				t.Fatalf("valid=%v calls=%d error=%v", tc.valid, w.calls, err)
			}
		})
	}
}

func TestMessageFailurePreservesRetrySignal(t *testing.T) {
	id := domain.NewID()
	databaseErr := errors.New("database unavailable")
	w := &commandStub{err: databaseErr}
	value, _ := json.Marshal(domain.Command{ID: id + ":1", JobID: id, Generation: 1, Stage: "probe"})
	err := commandHandler(w, domain.AnalysisTopic)(context.Background(), stream.Record{Key: id, Value: value})
	if !errors.Is(err, databaseErr) {
		t.Fatalf("handler error changed: %v", err)
	}
	for _, data := range [][]byte{[]byte("null"), []byte("{} {}"), []byte(`{"id":"unknown"}`), value} {
		w.calls = 0
		if err := commandHandler(w, domain.AnalysisTopic)(context.Background(), stream.Record{Key: "wrong-key", Value: data}); err == nil || w.calls != 0 {
			t.Fatal("malformed message reached worker")
		}
	}
}
