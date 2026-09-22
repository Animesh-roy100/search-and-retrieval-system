// Package kafka wraps franz-go with small helpers for producing/consuming
// canonical docs and creating topics. Redpanda speaks the Kafka API.
package kafka

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Brokers splits a comma-separated broker list.
func Brokers(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// NewProducer builds an idempotent producer.
func NewProducer(brokers []string) (*kgo.Client, error) {
	return kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
}

// NewConsumer builds a consumer-group client subscribed to the given topics.
func NewConsumer(brokers []string, group string, topics ...string) (*kgo.Client, error) {
	return kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		// Start from the earliest offset when the group has no committed offset,
		// so pre-existing messages (e.g. the CDC snapshot) are not skipped.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(), // we commit only after a successful sink write
		kgo.FetchMaxWait(100*time.Millisecond),
	)
}

// EnsureTopics creates topics (idempotently) with the given partitions/replication.
func EnsureTopics(ctx context.Context, brokers []string, partitions int32, replication int16, topics ...string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	resp, err := adm.CreateTopics(ctx, partitions, replication, nil, topics...)
	if err != nil {
		return err
	}
	for _, t := range resp {
		if t.Err != nil && !strings.Contains(t.Err.Error(), "already exists") {
			return fmt.Errorf("create topic %s: %w", t.Topic, t.Err)
		}
	}
	return nil
}
