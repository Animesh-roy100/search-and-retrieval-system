// Package mlclient is a thin HTTP client for the Python ML service.
package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(base string) *Client {
	return &Client{base: base, hc: &http.Client{Timeout: 30 * time.Second}}
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
