package stream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func integrationBroker(t *testing.T) (*Broker, context.Context) {
	t.Helper()
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("set KAFKA_BROKERS for Kafka integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	broker, err := New(Config{Brokers: strings.Split(brokers, ","), GroupPrefix: "test-" + domain.NewID(), ProcessingTimeout: 30 * time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); broker.Close() })
	if err := broker.EnsureTopics(ctx); err != nil {
		t.Fatal(err)
	}
	if err := broker.EnsureTopics(ctx); err != nil {
		t.Fatalf("topic creation is not idempotent: %v", err)
	}
	return broker, ctx
}

func startConsumer(t *testing.T, ctx context.Context, broker *Broker, topic, group string, handler func(context.Context, Record) error) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- broker.Consume(ctx, topic, group, 1, handler) }()
	stopped := false
	stop := func() {
		cancel()
		if stopped {
			return
		}
		stopped = true
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("consumer stopped: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("consumer did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func receiveRecord(t *testing.T, ctx context.Context, deliveries <-chan Record) Record {
	t.Helper()
	select {
	case record := <-deliveries:
		return record
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return Record{}
	}
}

func committedOffset(ctx context.Context, broker *Broker, suffix string, record Record) (int64, error) {
	request := kmsg.NewPtrOffsetFetchRequest()
	group := kmsg.NewOffsetFetchRequestGroup()
	group.Group = broker.cfg.GroupPrefix + "-" + suffix
	group.Topics = []kmsg.OffsetFetchRequestGroupTopic{{Topic: record.Topic, Partitions: []int32{record.Partition}}}
	request.Groups = []kmsg.OffsetFetchRequestGroup{group}
	response, err := request.RequestWith(ctx, broker.producer)
	if err != nil {
		return -1, err
	}
	for _, group := range response.Groups {
		if err := kerr.ErrorForCode(group.ErrorCode); err != nil {
			return -1, err
		}
		for _, topic := range group.Topics {
			for _, partition := range topic.Partitions {
				if err := kerr.ErrorForCode(partition.ErrorCode); err != nil {
					return -1, err
				}
				return partition.Offset, nil
			}
		}
	}
	return -1, nil
}

func awaitCommit(t *testing.T, ctx context.Context, broker *Broker, group string, record Record) {
	t.Helper()
	for ctx.Err() == nil {
		offset, err := committedOffset(ctx, broker, group, record)
		if err != nil {
			t.Fatal(err)
		}
		if offset > record.Offset {
			return
		}
		if !wait(ctx, 20*time.Millisecond) {
			break
		}
	}
	t.Fatal("record was not committed")
}

func TestKafkaOrderedDeliveryAndRetry(t *testing.T) {
	broker, ctx := integrationBroker(t)
	id := domain.NewID()
	for i := 0; i < 3; i++ {
		if err := broker.Publish(ctx, domain.OutboxMessage{Topic: domain.RenderTopic, Key: id, Payload: []byte(strconv.Itoa(i))}); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := make(chan Record, 3)
	var attempts atomic.Int32
	stop := startConsumer(t, ctx, broker, domain.RenderTopic, "ordered", func(ctx context.Context, record Record) error {
		if record.Key != id {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("database temporarily unavailable")
		}
		deliveries <- record
		return nil
	})
	var previous Record
	for i := 0; i < 3; i++ {
		record := receiveRecord(t, ctx, deliveries)
		if string(record.Value) != strconv.Itoa(i) || record.Key != id || i > 0 && (record.Partition != previous.Partition || record.Offset <= previous.Offset) {
			t.Fatalf("incorrect keyed order: %+v after %+v", record, previous)
		}
		previous = record
	}
	awaitCommit(t, ctx, broker, "ordered", previous)
	stop()
	if attempts.Load() != 4 {
		t.Fatalf("expected one transient retry, got %d calls", attempts.Load())
	}
}

func TestKafkaUncommittedDeliveryIsReplayed(t *testing.T) {
	broker, ctx := integrationBroker(t)
	id := domain.NewID()
	if err := broker.Publish(ctx, domain.OutboxMessage{Topic: domain.AnalysisTopic, Key: id, Payload: []byte(`{"job":"pending"}`)}); err != nil {
		t.Fatal(err)
	}
	deliveries := make(chan Record, 2)
	stop := startConsumer(t, ctx, broker, domain.AnalysisTopic, "replay", func(ctx context.Context, record Record) error {
		if record.Key != id {
			return nil
		}
		deliveries <- record
		<-ctx.Done()
		return ctx.Err()
	})
	first := receiveRecord(t, ctx, deliveries)
	offset, err := committedOffset(ctx, broker, "replay", first)
	if err != nil || offset > first.Offset {
		t.Fatalf("record committed before handler success: offset=%d err=%v", offset, err)
	}
	stop()
	stopAgain := startConsumer(t, ctx, broker, domain.AnalysisTopic, "replay", func(_ context.Context, record Record) error {
		if record.Key == id {
			deliveries <- record
		}
		return nil
	})
	replayed := receiveRecord(t, ctx, deliveries)
	if replayed.Partition != first.Partition || replayed.Offset != first.Offset {
		t.Fatalf("uncommitted record skipped: first=%+v replayed=%+v", first, replayed)
	}
	awaitCommit(t, ctx, broker, "replay", replayed)
	stopAgain()
}

func TestKafkaPoisonRecordGoesToDeadLetters(t *testing.T) {
	broker, ctx := integrationBroker(t)
	id := domain.NewID()
	payload := []byte(`not-json`)
	if err := broker.Publish(ctx, domain.OutboxMessage{Topic: domain.EventsTopic, Key: id, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	source := make(chan Record, 1)
	stop := startConsumer(t, ctx, broker, domain.EventsTopic, "poison", func(_ context.Context, record Record) error {
		if record.Key != id {
			return nil
		}
		select {
		case source <- record:
		default:
		}
		return Permanent(errors.New("invalid event envelope"))
	})
	letters := make(chan Record, 1)
	stopLetters := startConsumer(t, ctx, broker, domain.DeadLettersTopic, "letters", func(_ context.Context, record Record) error {
		if record.Key == id {
			letters <- record
		}
		return nil
	})
	original := receiveRecord(t, ctx, source)
	letterRecord := receiveRecord(t, ctx, letters)
	var letter domain.DeadLetter
	if err := json.Unmarshal(letterRecord.Value, &letter); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(letter.Payload)
	if err != nil || string(decoded) != string(payload) || letter.ID != fmt.Sprintf("%s:%d:%d", original.Topic, original.Partition, original.Offset) || letter.JobID != id || letter.SourceTopic != original.Topic || letter.Kind != "invalid_message" || letter.Error != "invalid event envelope" {
		t.Fatalf("invalid dead letter: %+v", letter)
	}
	awaitCommit(t, ctx, broker, "poison", original)
	awaitCommit(t, ctx, broker, "letters", letterRecord)
	stop()
	stopLetters()
}
