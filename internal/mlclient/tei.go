package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TEI talks to Hugging Face Text-Embeddings-Inference servers — a high-throughput
// Rust inference runtime. Embeddings and reranking are served by separate TEI
// instances (one model each), so embedURL and rerankURL are distinct base URLs.
// Its wire format differs from the Python service, so this adapter maps it back to
// the shared ML interface.
type TEI struct {
	embedURL  string
	rerankURL string
	hc        *http.Client
}

func NewTEI(embedURL, rerankURL string) *TEI {
	return &TEI{embedURL: embedURL, rerankURL: rerankURL, hc: &http.Client{
		Timeout:   30 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}}
}

// Embed calls TEI POST /embed, which returns a bare array of vectors.
func (t *TEI) Embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	body, _ := json.Marshal(map[string]any{"inputs": texts, "normalize": true, "truncate": true})
	var vectors [][]float32
	if err := t.post(ctx, t.embedURL+"/embed", body, &vectors); err != nil {
		return nil, "", err
	}
	return vectors, "tei", nil
}

// Rerank calls TEI POST /rerank, which returns [{index, score}, ...] sorted by
// score desc. We map it back to (scores-in-input-order, descending-order).
func (t *TEI) Rerank(ctx context.Context, query string, docs []string) ([]float64, []int, error) {
	if t.rerankURL == "" {
		return nil, nil, fmt.Errorf("tei: rerank URL not configured")
	}
	body, _ := json.Marshal(map[string]any{"query": query, "texts": docs, "raw_scores": false})
	var res []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := t.post(ctx, t.rerankURL+"/rerank", body, &res); err != nil {
		return nil, nil, err
	}
	// TEI returns sorted desc; be defensive and re-sort in case that changes.
	sort.SliceStable(res, func(i, j int) bool { return res[i].Score > res[j].Score })
	scores := make([]float64, len(docs))
	order := make([]int, 0, len(res))
	for _, r := range res {
		if r.Index >= 0 && r.Index < len(scores) {
			scores[r.Index] = r.Score
			order = append(order, r.Index)
		}
	}
	return scores, order, nil
}

// Health checks the embed server (and rerank server if configured).
func (t *TEI) Health(ctx context.Context) error {
	if err := t.ping(ctx, t.embedURL); err != nil {
		return err
	}
	if t.rerankURL != "" {
		return t.ping(ctx, t.rerankURL)
	}
	return nil
}

func (t *TEI) ping(ctx context.Context, base string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tei health %s: status %d", base, resp.StatusCode)
	}
	return nil
}

func (t *TEI) post(ctx context.Context, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tei %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
