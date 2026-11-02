// Command indexer consumes docs.fast + docs.bulk, applies the per-source idempotency
// guard, embeds upserts via the ML service, and bulk-writes to OpenSearch + Qdrant.
// Freshness lag is observed per source+tier at write time. Poison events go to DLQ.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/animeshroy/search-and-retrieval-system/internal/cache"
	"github.com/animeshroy/search-and-retrieval-system/internal/config"
	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
	"github.com/animeshroy/search-and-retrieval-system/internal/idempotency"
	ikafka "github.com/animeshroy/search-and-retrieval-system/internal/kafka"
	"github.com/animeshroy/search-and-retrieval-system/internal/mlclient"
	"github.com/animeshroy/search-and-retrieval-system/internal/obs"
	"github.com/animeshroy/search-and-retrieval-system/internal/opensearch"
	"github.com/animeshroy/search-and-retrieval-system/internal/qdrant"
	"github.com/animeshroy/search-and-retrieval-system/internal/tracing"

	"go.opentelemetry.io/otel/attribute"
)

var tracer = tracing.Tracer("indexer")

type searchIndex interface {
	Bulk(context.Context, []contract.CanonicalDoc) (map[string]bool, error)
}

type vectorIndex interface {
	Upsert(context.Context, []qdrant.Point) error
	DeleteByDocIDs(context.Context, []string) error
}

type cacheInvalidator interface {
	BumpGeneration(context.Context, string)
}

type syncProducer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

type indexer struct {
	os    searchIndex
	qd    vectorIndex
	ml    mlclient.ML
	guard *idempotency.Guard
	cache cacheInvalidator // bumps per-tenant cache generation after a complete write
	dlq   string
	prod  syncProducer
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
	mlc := mlclient.FromEnv(config.Str("ML_SERVICE_ADDR", "http://localhost:8000"))
	guard := idempotency.NewGuard()
	invalidator := cache.New(config.Str("REDIS_ADDR", ""), 0) // no-op if REDIS_ADDR unset

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := tracing.Init(ctx, "indexer")
	if err != nil {
		log.Printf("indexer: tracing init: %v", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

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
	ix := &indexer{os: osc, qd: qdc, ml: mlc, guard: guard, cache: invalidator, dlq: dlqTopic, prod: prod}
	go ix.runLane(ctx, brokers, group+"-fast", fastTopic, fastFlush, "urgent")
	ix.runLane(ctx, brokers, group+"-bulk", bulkTopic, bulkFlush, "bulk")
}

type poisonRecord struct {
	original []byte
	reason   string
}

type pendingBatch struct {
	docs        []contract.CanonicalDoc
	poisons     []poisonRecord
	records     []*kgo.Record
	docsApplied bool
	nextPoison  int
}

func (b *pendingBatch) empty() bool { return len(b.records) == 0 }

type commitRecords func(context.Context, ...*kgo.Record) error

// flushPending completes every side effect for the pending records before committing
// their exact offsets. Progress is retained across transient failures so a commit retry
// does not repeat successful sink writes or DLQ publications within this process.
func (ix *indexer) flushPending(ctx context.Context, b *pendingBatch, tier string, commit commitRecords) error {
	if b.empty() {
		return nil
	}
	if !b.docsApplied {
		if err := ix.apply(ctx, b.docs, tier); err != nil {
			return fmt.Errorf("apply: %w", err)
		}
		b.docsApplied = true
	}
	for b.nextPoison < len(b.poisons) {
		p := b.poisons[b.nextPoison]
		if err := ix.toDLQ(ctx, p.original, p.reason); err != nil {
			return err
		}
		b.nextPoison++
	}
	if err := commit(ctx, b.records...); err != nil {
		return fmt.Errorf("commit records: %w", err)
	}
	return nil
}

func (ix *indexer) runLane(ctx context.Context, brokers []string, group, topic string, flush time.Duration, tier string) {
	cl, err := ikafka.NewConsumer(brokers, group, topic)
	if err != nil {
		log.Fatalf("indexer[%s]: consumer: %v", tier, err)
	}
	defer cl.Close()
	log.Printf("indexer[%s]: consuming %s (flush %s)", tier, topic, flush)

	var pending pendingBatch
	var batchStart time.Time
	flushBatch := func(flushCtx context.Context) bool {
		if pending.empty() {
			return true
		}
		if err := ix.flushPending(flushCtx, &pending, tier, cl.CommitRecords); err != nil {
			log.Printf("indexer[%s]: flush failed (will retry without polling): %v", tier, err)
			return false
		}
		pending = pendingBatch{}
		batchStart = time.Time{}
		return true
	}

	retryPending := false
	for {
		// Do not fetch beyond a failed batch. Retrying the same in-memory state keeps
		// partition offsets contiguous and avoids unbounded buffering during outages.
		if retryPending {
			select {
			case <-ctx.Done():
				flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = flushBatch(flushCtx)
				cancel()
				return
			case <-time.After(time.Second):
			}
			retryPending = !flushBatch(ctx)
			continue
		}

		// Bound each poll to the flush interval so a low-traffic batch, including a
		// poison-only tail batch, is never stranded while waiting for another record.
		pollCtx, cancel := context.WithTimeout(ctx, flush)
		fetches := cl.PollFetches(pollCtx)
		cancel()
		if ctx.Err() != nil {
			flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = flushBatch(flushCtx)
			flushCancel()
			return
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				if errors.Is(e.Err, context.DeadlineExceeded) {
					continue
				}
				log.Printf("indexer[%s]: fetch: %v", tier, e.Err)
			}
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if pending.empty() {
				batchStart = time.Now()
			}
			// Keep a stable copy until CommitRecords completes; franz-go may reuse poll
			// result storage after the next poll.
			record := *r
			pending.records = append(pending.records, &record)

			var d contract.CanonicalDoc
			if err := json.Unmarshal(r.Value, &d); err != nil {
				pending.poisons = append(pending.poisons, poisonRecord{
					original: append([]byte(nil), r.Value...), reason: "decode_error",
				})
				return
			}
			if err := d.Validate(); err != nil {
				pending.poisons = append(pending.poisons, poisonRecord{
					original: append([]byte(nil), r.Value...), reason: "invalid_doc",
				})
				return
			}
			pending.docs = append(pending.docs, d)
		})

		if len(pending.records) >= 256 || (!pending.empty() && time.Since(batchStart) >= flush) {
			retryPending = !flushBatch(ctx)
		}
	}
}

// apply runs the idempotency guard, embeds upserts, and bulk-writes both sinks.
func (ix *indexer) apply(ctx context.Context, docs []contract.CanonicalDoc, tier string) error {
	ctx, span := tracer.Start(ctx, "index.batch")
	defer span.End()
	span.SetAttributes(attribute.String("tier", tier), attribute.Int("batch.size", len(docs)))

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

	// 2) OpenSearch first — it is the version authority. external_gte rejects lower
	// versions but accepts an equal-version replay, allowing a prior OpenSearch-success /
	// Qdrant-failure attempt to reach Qdrant again. Only the safe returned set is
	// propagated, so a strictly stale event can never overwrite the vector store.
	osCtx, osSp := tracer.Start(ctx, "opensearch.bulk")
	t0 := time.Now()
	applied, err := ix.os.Bulk(osCtx, toApply)
	osSp.SetAttributes(attribute.Int("applied", len(applied)))
	osSp.End()
	if err != nil {
		return err
	}
	obs.BulkWriteSeconds.WithLabelValues("opensearch").Observe(time.Since(t0).Seconds())

	// 3) Split the applied docs into upserts/deletes; embed + write Qdrant.
	var upserts []contract.CanonicalDoc
	var deletes []string
	for _, d := range toApply {
		if !applied[d.DocID] {
			continue // stale per OpenSearch — do not touch the vector store
		}
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
					// Keep tenant_id a string to match query filters and OpenSearch's
					// keyword mapping. Title/body make vector-only degradation usable
					// when OpenSearch is unavailable.
					"tenant_id":     strconv.FormatInt(d.TenantID, 10),
					"title":         d.Title,
					"body":          d.Body,
					"category":      d.Metadata.Category,
					"version":       d.Version,
					"model_version": modelVer,
				},
			})
		}
	}

	qdCtx, qdSp := tracer.Start(ctx, "qdrant.write")
	qdSp.SetAttributes(attribute.Int("upserts", len(points)), attribute.Int("deletes", len(deletes)))
	t1 := time.Now()
	if len(points) > 0 {
		if err := ix.qd.Upsert(qdCtx, points); err != nil {
			qdSp.End()
			return err
		}
	}
	if len(deletes) > 0 {
		if err := ix.qd.DeleteByDocIDs(qdCtx, deletes); err != nil {
			qdSp.End()
			return err
		}
	}
	qdSp.End()
	obs.BulkWriteSeconds.WithLabelValues("qdrant").Observe(time.Since(t1).Seconds())

	// 4) Commit guard + observe freshness lag per applied doc, and note which
	// tenants changed so we can invalidate their cached answers.
	now := time.Now()
	tenantsChanged := map[string]struct{}{}
	for _, d := range toApply {
		if !applied[d.DocID] {
			continue
		}
		ix.guard.Commit(d.DocID, d.Version)
		tenantsChanged[strconv.FormatInt(d.TenantID, 10)] = struct{}{}
		lag := now.Sub(d.CommitTS).Seconds()
		if lag < 0 {
			lag = 0
		}
		obs.FreshnessLag.WithLabelValues(d.Source, string(d.Tier)).Observe(lag)
		obs.DocsIndexed.WithLabelValues(d.Source, string(d.Tier), string(d.Op)).Inc()
	}

	// 5) Bump each changed tenant's cache generation → their cached answers become
	// stale immediately (freshness-safe caching). Best-effort; a miss just re-fetches.
	for tenant := range tenantsChanged {
		ix.cache.BumpGeneration(ctx, tenant)
	}
	return nil
}

func (ix *indexer) toDLQ(ctx context.Context, original []byte, reason string) error {
	env := map[string]any{"error": reason, "stage": "indexer"}
	if json.Valid(original) {
		env["original"] = json.RawMessage(original)
	} else {
		// Arbitrary poison bytes cannot safely be embedded as RawMessage. Preserve
		// them losslessly for inspection/replay instead of emitting an empty record.
		env["original_b64"] = base64.StdEncoding.EncodeToString(original)
	}
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal dlq envelope: %w", err)
	}
	if err := ix.prod.ProduceSync(ctx, &kgo.Record{Topic: ix.dlq, Value: b}).FirstErr(); err != nil {
		return fmt.Errorf("publish dlq: %w", err)
	}
	obs.DLQEvents.WithLabelValues("postgres", reason).Inc()
	return nil
}

func probeDim(ctx context.Context, mlc mlclient.ML) int {
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
