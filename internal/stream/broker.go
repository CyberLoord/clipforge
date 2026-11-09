package stream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CyberLoord/clipforge/internal/domain"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type Config struct {
	Brokers           []string
	GroupPrefix       string
	Partitions        int
	ReplicationFactor int
	ProcessingTimeout time.Duration
}

type Record struct {
	Topic     string
	Key       string
	Value     []byte
	Partition int32
	Offset    int64
}

type Broker struct {
	cfg      Config
	producer *kgo.Client
	log      *slog.Logger
}

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

var groupName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func New(cfg Config, logger *slog.Logger) (*Broker, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("Kafka brokers are required")
	}
	for _, address := range cfg.Brokers {
		host, port, err := net.SplitHostPort(address)
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || host == "" || strings.TrimSpace(address) != address || number < 1 || number > 65535 {
			return nil, errors.New("Kafka brokers must use host:port")
		}
	}
	if cfg.GroupPrefix == "" {
		cfg.GroupPrefix = "clipforge"
	}
	if !groupName.MatchString(cfg.GroupPrefix) || len(cfg.GroupPrefix) > 200 {
		return nil, errors.New("invalid Kafka consumer group prefix")
	}
	if cfg.Partitions == 0 {
		cfg.Partitions = 3
	}
	if cfg.ReplicationFactor == 0 {
		cfg.ReplicationFactor = 1
	}
	if cfg.Partitions < 1 || cfg.Partitions > 128 || cfg.ReplicationFactor < 1 || cfg.ReplicationFactor > 7 {
		return nil, errors.New("invalid Kafka topic partition or replication count")
	}
	if cfg.ProcessingTimeout == 0 {
		cfg.ProcessingTimeout = 46 * time.Minute
	}
	if cfg.ProcessingTimeout < time.Second || cfg.ProcessingTimeout > 24*time.Hour {
		return nil, errors.New("invalid Kafka processing timeout")
	}
	if logger == nil {
		logger = slog.Default()
	}
	producer, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ClientID(cfg.GroupPrefix+"-publisher"), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		return nil, err
	}
	return &Broker{cfg: cfg, producer: producer, log: logger}, nil
}

func (b *Broker) Close()                         { b.producer.Close() }
func (b *Broker) Ping(ctx context.Context) error { return b.producer.Ping(ctx) }

func topicRetention(topic string) time.Duration {
	switch topic {
	case domain.AnalysisTopic, domain.RenderTopic:
		return 24 * time.Hour
	case domain.EventsTopic:
		return 7 * 24 * time.Hour
	case domain.DeadLettersTopic:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

func (b *Broker) EnsureTopics(ctx context.Context) error {
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 10000
	for _, name := range []string{domain.AnalysisTopic, domain.RenderTopic, domain.EventsTopic, domain.DeadLettersTopic} {
		topic := kmsg.NewCreateTopicsRequestTopic()
		topic.Topic = name
		topic.NumPartitions = int32(b.cfg.Partitions)
		topic.ReplicationFactor = int16(b.cfg.ReplicationFactor)
		minimum := 1
		if b.cfg.ReplicationFactor >= 3 {
			minimum = 2
		}
		for key, value := range map[string]string{"cleanup.policy": "delete", "retention.ms": strconv.FormatInt(topicRetention(name).Milliseconds(), 10), "min.insync.replicas": strconv.Itoa(minimum)} {
			config := kmsg.NewCreateTopicsRequestTopicConfig()
			config.Name, config.Value = key, &value
			topic.Configs = append(topic.Configs, config)
		}
		request.Topics = append(request.Topics, topic)
	}
	response, err := request.RequestWith(ctx, b.producer)
	if err != nil {
		return fmt.Errorf("create Kafka topics: %w", err)
	}
	for _, topic := range response.Topics {
		if err := kerr.ErrorForCode(topic.ErrorCode); err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("create Kafka topic %s: %w", topic.Topic, err)
		}
	}
	return nil
}

func (b *Broker) Publish(ctx context.Context, message domain.OutboxMessage) error {
	if topicRetention(message.Topic) == 0 || len(message.Payload) == 0 || len(message.Payload) > 512<<10 {
		return errors.New("invalid Kafka outbox message")
	}
	return b.producer.ProduceSync(ctx, &kgo.Record{Topic: message.Topic, Key: []byte(message.Key), Value: message.Payload}).FirstErr()
}

func (b *Broker) Consume(ctx context.Context, topic, groupSuffix string, workers int, handler func(context.Context, Record) error) error {
	if topicRetention(topic) == 0 || !groupName.MatchString(groupSuffix) || len(groupSuffix) > 100 || workers < 1 || workers > 128 || handler == nil {
		return errors.New("invalid Kafka consumer configuration")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	errorsFound := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := b.consumeWorker(ctx, topic, groupSuffix, handler); err != nil && ctx.Err() == nil {
				errorsFound <- err
				cancel()
			}
		}()
	}
	group.Wait()
	select {
	case err := <-errorsFound:
		return err
	default:
		return ctx.Err()
	}
}

func (b *Broker) consumeWorker(ctx context.Context, topic, groupSuffix string, handler func(context.Context, Record) error) error {
	for ctx.Err() == nil {
		client, err := kgo.NewClient(
			kgo.SeedBrokers(b.cfg.Brokers...), kgo.ClientID(b.cfg.GroupPrefix+"-"+groupSuffix),
			kgo.ConsumerGroup(b.cfg.GroupPrefix+"-"+groupSuffix), kgo.ConsumeTopics(topic),
			kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.Balancers(kgo.CooperativeStickyBalancer()),
			kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.RebalanceTimeout(b.cfg.ProcessingTimeout+time.Minute),
			kgo.FetchMaxBytes(1<<20), kgo.FetchMaxPartitionBytes(1<<20),
		)
		if err != nil {
			return err
		}
		err = b.consumeSession(ctx, client, topic, handler)
		client.CloseAllowingRebalance()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if topic == domain.DeadLettersTopic {
			var permanent permanentError
			if errors.As(err, &permanent) {
				return fmt.Errorf("invalid record in dead-letter topic: %w", err)
			}
		}
		b.log.Warn("Kafka consumer reconnecting", "topic", topic, "error", err)
		if !wait(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (b *Broker) consumeSession(ctx context.Context, client *kgo.Client, topic string, handler func(context.Context, Record) error) error {
	for ctx.Err() == nil {
		fetched := client.PollRecords(ctx, 1)
		if errs := fetched.Errors(); len(errs) > 0 {
			client.AllowRebalance()
			return errs[0].Err
		}
		for _, record := range fetched.Records() {
			err := b.processRecord(ctx, client, record, handler)
			client.AllowRebalance()
			if err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (b *Broker) processRecord(ctx context.Context, client *kgo.Client, record *kgo.Record, handler func(context.Context, Record) error) error {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.ProcessingTimeout)
	defer cancel()
	delivery := Record{Topic: record.Topic, Key: string(record.Key), Value: record.Value, Partition: record.Partition, Offset: record.Offset}
	delay := time.Second
	for ctx.Err() == nil {
		err := handler(ctx, delivery)
		var permanent permanentError
		if errors.As(err, &permanent) {
			if record.Topic == domain.DeadLettersTopic {
				return err
			}
			err = b.deadLetter(ctx, delivery, err)
		}
		if err == nil {
			commitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
			err = client.CommitRecords(commitCtx, record)
			stop()
			return err
		}
		b.log.Warn("Kafka record will be retried", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
		if !wait(ctx, delay) {
			return ctx.Err()
		}
		delay = min(delay*2, 10*time.Second)
	}
	return ctx.Err()
}

func (b *Broker) deadLetter(ctx context.Context, record Record, cause error) error {
	id := fmt.Sprintf("%s:%d:%d", record.Topic, record.Partition, record.Offset)
	message := domain.DeadLetter{
		ID: id, Kind: "invalid_message", Error: truncate(cause.Error(), 2048), SourceTopic: record.Topic,
		Partition: record.Partition, Offset: record.Offset,
		Payload: base64.StdEncoding.EncodeToString(record.Value[:min(len(record.Value), 16<<10)]), OccurredAt: time.Now().UTC(),
	}
	if domain.ValidID(record.Key) {
		message.JobID = record.Key
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	publishCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return b.Publish(publishCtx, domain.OutboxMessage{Topic: domain.DeadLettersTopic, Key: record.Key, Payload: payload})
}

func truncate(value string, maximum int) string {
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
