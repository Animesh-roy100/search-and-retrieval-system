// Command normalizer consumes the Debezium CDC topic, converts each change event to
// a canonical doc, and routes it to docs.fast or docs.bulk by tier. Bad events go to DLQ.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/animeshroy/search-and-retrieval-system/internal/config"
	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
	ikafka "github.com/animeshroy/search-and-retrieval-system/internal/kafka"
	"github.com/animeshroy/search-and-retrieval-system/internal/normalizer"
	"github.com/animeshroy/search-and-retrieval-system/internal/obs"
)

func main() {
	brokers := ikafka.Brokers(config.Str("REDPANDA_BROKERS", "localhost:9092"))
	cdcTopic := config.Str("CDC_TOPIC", "cdc.public.documents")
	fastTopic := config.Str("FAST_TOPIC", "docs.fast")
	bulkTopic := config.Str("BULK_TOPIC", "docs.bulk")
	dlqTopic := config.Str("DLQ_TOPIC", "docs.dlq")
	group := config.Str("NORMALIZER_GROUP", "normalizer")
	metricsAddr := config.Str("METRICS_ADDR", ":9100")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Ensure downstream topics exist (single-broker dev => replication 1).
	if err := ikafka.EnsureTopics(ctx, brokers, 4, 1, fastTopic, bulkTopic, dlqTopic); err != nil {
		log.Fatalf("normalizer: ensure topics: %v", err)
	}

	producer, err := ikafka.NewProducer(brokers)
	if err != nil {
		log.Fatalf("normalizer: producer: %v", err)
	}
	defer producer.Close()

	consumer, err := ikafka.NewConsumer(brokers, group, cdcTopic)
	if err != nil {
		log.Fatalf("normalizer: consumer: %v", err)
	}
	defer consumer.Close()

	var ready atomic.Bool
	ready.Store(true)
	go func() {
		if err := obs.Serve(ctx, metricsAddr, ready.Load); err != nil {
			log.Printf("normalizer: metrics server: %v", err)
		}
	}()

	log.Printf("normalizer: consuming %s -> {%s,%s} (dlq %s)", cdcTopic, fastTopic, bulkTopic, dlqTopic)

	for {
		fetches := consumer.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				log.Printf("normalizer: fetch error topic=%s: %v", e.Topic, e.Err)
			}
			continue
		}

		var toProduce []*kgo.Record
		fetches.EachRecord(func(r *kgo.Record) {
			// Debezium tombstone (null value) — nothing to do.
			if len(r.Value) == 0 {
				return
			}
			doc, err := normalizer.FromDebezium(r.Value)
			if err == normalizer.ErrSkip {
				return
			}
			if err != nil {
				toProduce = append(toProduce, dlqRecord(dlqTopic, r.Value, err.Error()))
				obs.DLQEvents.WithLabelValues("postgres", "normalize_error").Inc()
				return
			}
			if err := doc.Validate(); err != nil {
				toProduce = append(toProduce, dlqRecord(dlqTopic, r.Value, err.Error()))
				obs.DLQEvents.WithLabelValues(doc.Source, "invalid_doc").Inc()
				return
			}
			payload, _ := json.Marshal(doc)
			topic := bulkTopic
			if doc.Tier == contract.TierUrgent {
				topic = fastTopic
			}
			toProduce = append(toProduce, &kgo.Record{
				Topic: topic,
				Key:   []byte(doc.DocID), // key by doc_id => per-doc ordering
				Value: payload,
			})
		})

		if len(toProduce) > 0 {
			if err := producer.ProduceSync(ctx, toProduce...).FirstErr(); err != nil {
				log.Printf("normalizer: produce error: %v", err)
				continue // do not commit; reprocess on next poll
			}
		}
		if err := consumer.CommitUncommittedOffsets(ctx); err != nil {
			log.Printf("normalizer: commit error: %v", err)
		}
	}
}

func dlqRecord(topic string, original []byte, reason string) *kgo.Record {
	env := map[string]any{"error": reason, "stage": "normalizer", "original": json.RawMessage(original)}
	b, _ := json.Marshal(env)
	return &kgo.Record{Topic: topic, Value: b}
}

var _ = os.Getenv
