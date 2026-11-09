package domain

import (
	"encoding/json"
	"time"
)

const (
	AnalysisTopic    = "clipforge.analysis.v1"
	RenderTopic      = "clipforge.render.v1"
	EventsTopic      = "clipforge.events.v1"
	DeadLettersTopic = "clipforge.dlq.v1"
)

type Command struct {
	ID         string `json:"id"`
	JobID      string `json:"job_id"`
	Generation int64  `json:"generation"`
	Stage      string `json:"stage"`
}

type JobEvent struct {
	ID         string    `json:"id"`
	JobID      string    `json:"job_id"`
	Sequence   int64     `json:"sequence"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	Stage      string    `json:"stage"`
	Attempt    int       `json:"attempt"`
	Error      string    `json:"error,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type DeadLetter struct {
	ID          string    `json:"id"`
	JobID       string    `json:"job_id,omitempty"`
	Kind        string    `json:"kind"`
	Error       string    `json:"error"`
	SourceTopic string    `json:"source_topic"`
	Partition   int32     `json:"partition"`
	Offset      int64     `json:"offset"`
	Payload     string    `json:"payload,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type OutboxMessage struct {
	ID      int64
	Topic   string
	Key     string
	Payload json.RawMessage
}
