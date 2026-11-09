package stream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
)

func TestInvalidBrokerConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{},
		{Brokers: []string{"http://localhost:9092"}},
		{Brokers: []string{"localhost"}},
		{Brokers: []string{"localhost:70000"}},
		{Brokers: []string{"localhost:9092"}, GroupPrefix: "a group"},
		{Brokers: []string{"localhost:9092"}, Partitions: -1},
		{Brokers: []string{"localhost:9092"}, ReplicationFactor: -1},
		{Brokers: []string{"localhost:9092"}, ProcessingTimeout: time.Millisecond},
	} {
		broker, err := New(cfg, nil)
		if err == nil {
			broker.Close()
			t.Fatalf("invalid configuration accepted: %+v", cfg)
		}
	}
}

func TestBrokerRejectsInvalidMessages(t *testing.T) {
	broker, err := New(Config{Brokers: []string{"127.0.0.1:1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for _, message := range []domain.OutboxMessage{
		{Topic: "arbitrary-topic", Payload: []byte(`{}`)},
		{Topic: domain.AnalysisTopic},
		{Topic: domain.EventsTopic, Payload: make([]byte, (512<<10)+1)},
	} {
		if err := broker.Publish(context.Background(), message); err == nil {
			t.Fatal("invalid message accepted")
		}
	}
}

func TestPermanentPreservesCause(t *testing.T) {
	cause := errors.New("invalid schema")
	var permanent permanentError
	if !errors.As(Permanent(cause), &permanent) || !errors.Is(permanent, cause) || Permanent(nil) != nil {
		t.Fatal("permanent error lost its cause")
	}
}
