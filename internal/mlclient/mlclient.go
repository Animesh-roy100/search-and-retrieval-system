// Package mlclient talks to an embedding + rerank backend. Two implementations
// satisfy the ML interface: the project's Python ML service (Client) and Hugging
// Face Text-Embeddings-Inference (TEI). FromEnv selects TEI when TEI_EMBED_URL is set.
package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ML is the backend-agnostic surface the indexer and query service depend on.
type ML interface {
	Embed(ctx context.Context, texts []string) ([][]float32, string, error)
	Rerank(ctx context.Context, query string, docs []string) ([]float64, []int, error)
	Health(ctx context.Context) error
}

// FromEnv returns a TEI-backed client when TEI_EMBED_URL is set (rerank optional),
// otherwise the Python ML service at pythonBase.
func FromEnv(pythonBase string) ML {
	if embed := os.Getenv("TEI_EMBED_URL"); embed != "" {
		return NewTEI(embed, os.Getenv("TEI_RERANK_URL"))
	}
	return New(pythonBase)
}

type Client struct {
	base string
	hc   *http.Client
}

func New(base string) *Client {
	// otelhttp transport creates a client span per call and injects the W3C
	// traceparent header, so ML-service spans join the caller's trace.
	return &Client{base: base, hc: &http.Client{
		Timeout:   30 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}}
}

type embedReq struct {
	Texts []string `json:"texts"`
}
type embedResp struct {
	Vectors      [][]float32 `json:"vectors"`
	ModelVersion string      `json:"model_version"`
	Dim          int         `json:"dim"`
}

// Embed returns one vector per input text plus the model version.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	var out embedResp
	if err := c.post(ctx, "/embed", embedReq{Texts: texts}, &out); err != nil {
		return nil, "", err
	}
	return out.Vectors, out.ModelVersion, nil
}

type rerankReq struct {
	Query string   `json:"query"`
	Docs  []string `json:"docs"`
}
type rerankResp struct {
	Scores       []float64 `json:"scores"`
	Order        []int     `json:"order"`
	ModelVersion string    `json:"model_version"`
}

// Rerank returns a relevance score per doc and the descending order.
func (c *Client) Rerank(ctx context.Context, query string, docs []string) ([]float64, []int, error) {
	var out rerankResp
	if err := c.post(ctx, "/rerank", rerankReq{Query: query, Docs: docs}, &out); err != nil {
		return nil, nil, err
	}
	return out.Scores, out.Order, nil
}

func (c *Client) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml health: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
