// Command indexer consumes docs.fast + docs.bulk, applies the per-source idempotency
// guard, embeds upserts via the ML service, and bulk-writes to OpenSearch + Qdrant.
// Freshness lag is observed per source+tier at write time. Poison events go to DLQ.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/animeshroy/search-and-retrieval-system/internal/config"
	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
	"github.com/animeshroy/search-and-retrieval-system/internal/idempotency"
	ikafka "github.com/animeshroy/search-and-retrieval-system/internal/kafka"
	"github.com/animeshroy/search-and-retrieval-system/internal/mlclient"
	"github.com/animeshroy/search-and-retrieval-system/internal/obs"
	"github.com/animeshroy/search-and-retrieval-system/internal/opensearch"
	"github.com/animeshroy/search-and-retrieval-system/internal/qdrant"
)

type indexer struct {
	os     *opensearch.Client
	qd     *qdrant.Client
	ml     *mlclient.Client
	guard  *idempotency.Guard
	dlq    string
	prod   *kgo.Client
	tier   string
}

func main() {
	brokers := ikafka.Brokers(config.Str("REDPANDA_BROKERS", "localhost:9092"))
	fastTopic := config.Str("FAST_TOPIC", "docs.fast")
	bulkTopic := config.Str("BULK_TOPIC", "docs.bulk")
	dlqTopic := config.Str("DLQ_TOPIC", "docs.dlq")
	group := config.Str("INDEXER_GROUP", "indexer")
	metricsAddr := config.Str("METRICS_ADDR", ":9101")
	fastFlush := config.Dur("FAST_FLUSH_MS", 200*time.Millisecond)
	bulkFlush := config.Dur("BULK_FLUSH_MS", 2000*time.Millisecond)

	osc, err := opensearch.New(config.Str("OPENSEARCH_URL", "http://localhost:9200"))
	if err != nil {
		log.Fatalf("indexer: opensearch client: %v", err)
	}
	qdc, err := qdrant.New(config.Str("QDRANT_HOST", "localhost"), config.Int("QDRANT_PORT", 6334))
	if err != nil {
		log.Fatalf("indexer: qdrant client: %v", err)
	}
	mlc := mlclient.New(config.Str("ML_SERVICE_ADDR", "http://localhost:8000"))
	guard := idempotency.NewGuard()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wait for dependencies and set up schema (idempotent).
	mustReady(ctx, "opensearch", osc.Health)
	mustReady(ctx, "qdrant", qdc.Health)
	mustReady(ctx, "ml", mlc.Health)
	if err := osc.EnsureIndex(ctx); err != nil {
		log.Fatalf("indexer: ensure index: %v", err)
	}
	// Determine embedding dim from the ML service.
	dim := probeDim(ctx, mlc)
	if err := qdc.EnsureCollection(ctx, dim); err != nil {
		log.Fatalf("indexer: ensure collection: %v", err)
	}

	prod, err := ikafka.NewProducer(brokers)
	if err != nil {
		log.Fatalf("indexer: producer: %v", err)
	}
	defer prod.Close()

	var ready atomic.Bool
	ready.Store(true)
	go func() {
		if err := obs.Serve(ctx, metricsAddr, ready.Load); err != nil {
			log.Printf("indexer: metrics: %v", err)
		}
	}()

	// One consumer per lane so the fast lane is never blocked by a bulk backlog,
	// and each lane can use its own flush cadence.
	ix := &indexer{os: osc, qd: qdc, ml: mlc, guard: guard, dlq: dlqTopic, prod: prod}
	go ix.runLane(ctx, brokers, group+"-fast", fastTopic, fastFlush, "urgent")
	ix.runLane(ctx, brokers, group+"-bulk", bulkTopic, bulkFlush, "bulk")
}

func (ix *indexer) runLane(ctx context.Context, brokers []string, group, topic string, flush time.Duration, tier string) {
	cl, err := ikafka.NewConsumer(brokers, group, topic)
	if err != nil {
		log.Fatalf("indexer[%s]: consumer: %v", tier, err)
	}
	defer cl.Close()
	log.Printf("indexer[%s]: consuming %s (flush %s)", tier, topic, flush)

	var batch []contract.CanonicalDoc
	var raws [][]byte
	var batchStart time.Time // when the current batch's first record was buffered

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		if err := ix.apply(ctx, batch, tier); err != nil {
			log.Printf("indexer[%s]: apply failed (will retry): %v", tier, err)
			return // do not commit; messages re-delivered
		}
		if err := cl.CommitUncommittedOffsets(ctx); err != nil {
			log.Printf("indexer[%s]: commit: %v", tier, err)
		}
		batch = batch[:0]
		raws = raws[:0]
		batchStart = time.Time{}
	}

	for {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			flushBatch()
			return
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				log.Printf("indexer[%s]: fetch: %v", tier, e.Err)
			}
		}
		fetches.EachRecord(func(r *kgo.Record) {
			var d contract.CanonicalDoc
			if err := json.Unmarshal(r.Value, &d); err != nil {
				ix.toDLQ(ctx, r.Value, "decode_error")
				return
			}
			if err := d.Validate(); err != nil {
				ix.toDLQ(ctx, r.Value, "invalid_doc")
				return
			}
			if len(batch) == 0 {
				batchStart = time.Now()
			}
			batch = append(batch, d)
			raws = append(raws, r.Value)
		})

		// Flush when the batch is full (throughput) OR when the flush interval has
		// elapsed since the first buffered record (bounded latency). This is
		// deterministic under any poll cadence — no ticker race, no idle heuristic.
		if len(batch) >= 256 || (len(batch) > 0 && time.Since(batchStart) >= flush) {
			flushBatch()
		}
	}
}

// apply runs the idempotency guard, embeds upserts, and bulk-writes both sinks.
func (ix *indexer) apply(ctx context.Context, docs []contract.CanonicalDoc, tier string) error {
	// 1) Idempotency: keep only the newest version per doc, and only if newer than stored.
	newestByDoc := map[string]contract.CanonicalDoc{}
	for _, d := range docs {
		cur, ok := newestByDoc[d.DocID]
		if !ok || d.Version > cur.Version {
			newestByDoc[d.DocID] = d
		}
	}
	var toApply []contract.CanonicalDoc
	for _, d := range newestByDoc {
		if ix.guard.ShouldApply(d.DocID, d.Version) {
			toApply = append(toApply, d)
		} else {
			obs.DocsSkipped.WithLabelValues(d.Source).Inc()
		}
	}
	if len(toApply) == 0 {
		return nil
	}

	// 2) Split upserts/deletes; embed upserts.
	var upserts []contract.CanonicalDoc
	var deletes []string
	for _, d := range toApply {
		if d.Op == contract.OpDelete {
			deletes = append(deletes, d.DocID)
		} else {
			upserts = append(upserts, d)
		}
	}

	var points []qdrant.Point
	if len(upserts) > 0 {
		texts := make([]string, len(upserts))
		for i, d := range upserts {
			texts[i] = d.Text()
		}
		vecs, modelVer, err := ix.ml.Embed(ctx, texts)
		if err != nil {
			return err
		}
		for i, d := range upserts {
			points = append(points, qdrant.Point{
				ID:     d.PointID(),
				Vector: vecs[i],
				Payload: map[string]any{
					"doc_id": d.DocID, "source": d.Source,
					"tenant_id": d.TenantID, "category": d.Metadata.Category,
					"version": d.Version, "model_version": modelVer,
				},
			})
		}
	}

	// 3) Bulk write both sinks. OpenSearch first (BM25), then Qdrant (vectors).
	t0 := time.Now()
	if err := ix.os.Bulk(ctx, toApply); err != nil {
		return err
	}
	obs.BulkWriteSeconds.WithLabelValues("opensearch").Observe(time.Since(t0).Seconds())

	t1 := time.Now()
	if len(points) > 0 {
		if err := ix.qd.Upsert(ctx, points); err != nil {
			return err
		}
	}
	if len(deletes) > 0 {
		if err := ix.qd.DeleteByDocIDs(ctx, deletes); err != nil {
			return err
		}
	}
	obs.BulkWriteSeconds.WithLabelValues("qdrant").Observe(time.Since(t1).Seconds())

	// 4) Commit guard + observe freshness lag per doc.
	now := time.Now()
	for _, d := range toApply {
		ix.guard.Commit(d.DocID, d.Version)
		lag := now.Sub(d.CommitTS).Seconds()
		if lag < 0 {
			lag = 0
		}
		obs.FreshnessLag.WithLabelValues(d.Source, string(d.Tier)).Observe(lag)
		obs.DocsIndexed.WithLabelValues(d.Source, string(d.Tier), string(d.Op)).Inc()
	}
	return nil
}

func (ix *indexer) toDLQ(ctx context.Context, original []byte, reason string) {
	obs.DLQEvents.WithLabelValues("postgres", reason).Inc()
	env := map[string]any{"error": reason, "stage": "indexer", "original": json.RawMessage(original)}
	b, _ := json.Marshal(env)
	if err := ix.prod.ProduceSync(ctx, &kgo.Record{Topic: ix.dlq, Value: b}).FirstErr(); err != nil {
		log.Printf("indexer: dlq produce: %v", err)
	}
}

func probeDim(ctx context.Context, mlc *mlclient.Client) int {
	vecs, _, err := mlc.Embed(ctx, []string{"probe"})
	if err != nil || len(vecs) == 0 {
		log.Printf("indexer: dim probe failed (%v); defaulting to 768", err)
		return 768
	}
	return len(vecs[0])
}

func mustReady(ctx context.Context, name string, check func(context.Context) error) {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if err := check(ctx); err == nil {
			log.Printf("indexer: %s ready", name)
			return
		}
		if time.Now().After(deadline) {
			log.Fatalf("indexer: %s not ready after timeout", name)
		}
		select {
		case <-ctx.Done():
			log.Fatalf("indexer: canceled waiting for %s", name)
		case <-time.After(2 * time.Second):
		}
	}
}
