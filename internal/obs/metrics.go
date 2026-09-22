// Package obs holds shared Prometheus metrics and a tiny metrics/health server.
package obs

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// FreshnessLag is the signature SLO metric: now - commit_ts at index time.
	FreshnessLag = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "doc_freshness_lag_seconds",
		Help:    "Seconds between source commit and document becoming searchable.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 300},
	}, []string{"source", "tier"})

	BulkWriteSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "indexer_bulk_write_seconds",
		Help:    "Latency of a bulk write to a sink.",
		Buckets: prometheus.DefBuckets,
	}, []string{"sink"})

	DLQEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_dlq_events_total",
		Help: "Events dead-lettered.",
	}, []string{"source", "reason"})

	DocsIndexed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_docs_indexed_total",
		Help: "Docs successfully applied to sinks.",
	}, []string{"source", "tier", "op"})

	DocsSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_docs_skipped_total",
		Help: "Docs skipped by the idempotency guard (stale version).",
	}, []string{"source"})

	SearchStageLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "search_stage_latency_seconds",
		Help:    "Per-stage latency in the read path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"stage"})

	RagFaithfulness = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "rag_faithfulness_score",
		Help:    "Faithfulness score of generated answers.",
		Buckets: []float64{0, 0.25, 0.5, 0.75, 0.9, 0.95, 1.0},
	})

	CacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "query_cache_hits_total", Help: "Semantic cache hits.",
	})
	CacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "query_cache_misses_total", Help: "Semantic cache misses.",
	})
)

// Serve starts a metrics + health HTTP server and blocks until ctx is done.
func Serve(ctx context.Context, addr string, ready func() bool) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if ready == nil || ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"starting"}`))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
