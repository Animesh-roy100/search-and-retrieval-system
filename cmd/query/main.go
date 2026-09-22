// Command query is the read path: hybrid retrieval (BM25 + vector + RRF + rerank + MMR)
// plus grounded RAG with citations, a faithfulness guardrail, and a semantic cache.
// It degrades gracefully when a dependency is down.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/animeshroy/search-and-retrieval-system/internal/cache"
	"github.com/animeshroy/search-and-retrieval-system/internal/config"
	"github.com/animeshroy/search-and-retrieval-system/internal/mlclient"
	"github.com/animeshroy/search-and-retrieval-system/internal/obs"
	"github.com/animeshroy/search-and-retrieval-system/internal/opensearch"
	"github.com/animeshroy/search-and-retrieval-system/internal/qdrant"
	"github.com/animeshroy/search-and-retrieval-system/internal/rag"
	"github.com/animeshroy/search-and-retrieval-system/internal/retrieve"
)

type server struct {
	os       *opensearch.Client
	qd       *qdrant.Client
	ml       *mlclient.Client
	cache    *cache.Cache
	llm      rag.Provider
	rrfK     int
	rerankN  int
	topK     int
	faithMin float64
}

func main() {
	addr := config.Str("QUERY_ADDR", ":8080")
	osc, err := opensearch.New(config.Str("OPENSEARCH_URL", "http://localhost:9200"))
	if err != nil {
		log.Fatalf("query: opensearch client: %v", err)
	}
	qdc, err := qdrant.New(config.Str("QDRANT_HOST", "localhost"), config.Int("QDRANT_PORT", 6334))
	if err != nil {
		log.Fatalf("query: qdrant client: %v", err)
	}
	mlc := mlclient.New(config.Str("ML_SERVICE_ADDR", "http://localhost:8000"))
	c := cache.New(config.Str("REDIS_ADDR", ""), config.Dur("CACHE_TTL", 5*time.Minute))

	s := &server{
		os: osc, qd: qdc, ml: mlc, cache: c, llm: rag.NewProvider(),
		rrfK:     config.Int("RRF_K", 60),
		rerankN:  config.Int("RERANK_TOP_N", 50),
		topK:     config.Int("RAG_TOP_K", 6),
		faithMin: config.Float("FAITHFULNESS_THRESHOLD", 0.9),
	}
	log.Printf("query: llm provider = %s", s.llm.Name())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var ready atomic.Bool
	ready.Store(true)
	go func() {
		if err := obs.Serve(ctx, config.Str("METRICS_ADDR", ":9102"), ready.Load); err != nil {
			log.Printf("query: metrics: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/ask", s.handleAsk)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	log.Printf("query: listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

type askReq struct {
	Query   string            `json:"query"`
	Filters map[string]string `json:"filters,omitempty"`
	TopK    int               `json:"top_k,omitempty"`
}

// candidate carries a doc through the pipeline.
type candidate struct {
	DocID  string
	Vector []float32
	Doc    opensearch.Doc
}

// retrieve runs hybrid retrieval and returns ordered doc ids + assembled docs.
func (s *server) retrieve(ctx context.Context, query string, filters map[string]string, topK int) ([]rag.Source, error) {
	// 1) embed query (vector lane). If ML is down we degrade to BM25-only.
	var qvec []float32
	t0 := time.Now()
	if vecs, _, err := s.ml.Embed(ctx, []string{query}); err == nil && len(vecs) > 0 {
		qvec = vecs[0]
	} else {
		log.Printf("query: embed failed, degrading to BM25-only: %v", err)
	}
	obs.SearchStageLatency.WithLabelValues("embed").Observe(time.Since(t0).Seconds())

	// 2) BM25 + vector in parallel.
	var bm25IDs, vecIDs []string
	t1 := time.Now()
	if hits, err := s.os.Search(ctx, query, filters, s.rerankN); err == nil {
		for _, h := range hits {
			bm25IDs = append(bm25IDs, h.DocID)
		}
	} else {
		log.Printf("query: opensearch down, vector-only: %v", err)
	}
	obs.SearchStageLatency.WithLabelValues("bm25").Observe(time.Since(t1).Seconds())

	if qvec != nil {
		t2 := time.Now()
		if hits, err := s.qd.Search(ctx, qvec, filters, s.rerankN); err == nil {
			for _, h := range hits {
				vecIDs = append(vecIDs, h.DocID)
			}
		} else {
			log.Printf("query: qdrant down, BM25-only: %v", err)
		}
		obs.SearchStageLatency.WithLabelValues("vector").Observe(time.Since(t2).Seconds())
	}

	// 3) RRF fuse.
	fused := retrieve.RRF([][]string{bm25IDs, vecIDs}, s.rrfK)
	if len(fused) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(fused))
	for _, f := range fused {
		ids = append(ids, f.DocID)
		if len(ids) >= s.rerankN {
			break
		}
	}

	// 4) fetch doc content for rerank + context.
	docs, err := s.os.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// keep only ids we actually have content for, in fused order
	var haveIDs []string
	var texts []string
	for _, id := range ids {
		if d, ok := docs[id]; ok {
			haveIDs = append(haveIDs, id)
			texts = append(texts, d.Title+" "+d.Body)
		}
	}
	if len(haveIDs) == 0 {
		return nil, nil
	}

	// 5) rerank via ML (skip gracefully if down).
	order := make([]int, len(haveIDs))
	for i := range order {
		order[i] = i
	}
	t3 := time.Now()
	if _, ord, err := s.ml.Rerank(ctx, query, texts); err == nil && len(ord) == len(haveIDs) {
		order = ord
	} else if err != nil {
		log.Printf("query: rerank skipped: %v", err)
	}
	obs.SearchStageLatency.WithLabelValues("rerank").Observe(time.Since(t3).Seconds())

	// 6) build ranked sources, cap at topK.
	if topK <= 0 {
		topK = s.topK
	}
	var sources []rag.Source
	for i, idx := range order {
		if i >= topK {
			break
		}
		id := haveIDs[idx]
		d := docs[id]
		sources = append(sources, rag.Source{
			Marker: markerFor(len(sources) + 1),
			DocID:  d.DocID, Origin: d.Source,
			Text: d.Title + ". " + d.Body,
		})
	}
	return sources, nil
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query required"})
		return
	}
	ctx := r.Context()
	start := time.Now()
	sources, err := s.retrieve(ctx, req.Query, req.Filters, req.TopK)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query": req.Query, "results": sources, "latency_ms": time.Since(start).Milliseconds(),
	})
}

func (s *server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query required"})
		return
	}
	ctx := r.Context()
	start := time.Now()

	key := cache.Key(req.Query, req.Filters)
	var cached map[string]any
	if s.cache.Get(ctx, key, &cached) {
		obs.CacheHits.Inc()
		cached["cached"] = true
		writeJSON(w, http.StatusOK, cached)
		return
	}
	obs.CacheMisses.Inc()

	sources, err := s.retrieve(ctx, req.Query, req.Filters, req.TopK)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if len(sources) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"answer": "Not found in sources.", "citations": []any{}, "faithfulness": 1.0,
		})
		return
	}

	tg := time.Now()
	ans, err := s.llm.Generate(ctx, req.Query, sources)
	obs.SearchStageLatency.WithLabelValues("generate").Observe(time.Since(tg).Seconds())
	if err != nil {
		// LLM down -> return retrieved docs, no generated answer (graceful).
		writeJSON(w, http.StatusOK, map[string]any{
			"answer": "", "degraded": "llm_unavailable", "results": sources,
		})
		return
	}

	f := rag.Faithfulness(ans.Text, sources)
	obs.RagFaithfulness.Observe(f.Score)

	resp := map[string]any{
		"answer":       ans.Text,
		"citations":    ans.Citations,
		"faithfulness": f.Score,
		"unsupported":  f.Unsupported,
		"model":        ans.Model,
		"latency_ms":   time.Since(start).Milliseconds(),
	}
	if f.Score < s.faithMin {
		resp["warning"] = "faithfulness below threshold"
	}
	s.cache.Set(ctx, key, resp)
	writeJSON(w, http.StatusOK, resp)
}

func markerFor(n int) string {
	return "doc_" + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var _ = candidate{}
