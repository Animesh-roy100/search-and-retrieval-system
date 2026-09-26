// Package opensearch wraps the official OpenSearch Go client
// (github.com/opensearch-project/opensearch-go/v4) behind the small surface the
// indexer and query service need.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
)

const Index = "documents"

type Client struct {
	api *opensearchapi.Client
}

// New builds a client for the given address (e.g. http://opensearch:9200).
func New(addr string) (*Client, error) {
	api, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{strings.TrimRight(addr, "/")},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("opensearch: new client: %w", err)
	}
	return &Client{api: api}, nil
}

// EnsureIndex creates the documents index with the BM25 mapping if absent.
func (c *Client) EnsureIndex(ctx context.Context) error {
	body := `{
      "settings": { "index": { "number_of_shards": 1, "number_of_replicas": 0 } },
      "mappings": { "properties": {
        "doc_id":    { "type": "keyword" },
        "source":    { "type": "keyword" },
        "tenant_id": { "type": "keyword" },
        "title":     { "type": "text" },
        "body":      { "type": "text" },
        "category":  { "type": "keyword" },
        "version":   { "type": "long" },
        "commit_ts": { "type": "date" }
      }}
    }`
	_, err := c.api.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: Index,
		Body:  strings.NewReader(body),
	})
	if err != nil {
		var osErr *opensearch.StructError
		if errors.As(err, &osErr) && osErr.Err.Type == "resource_already_exists_exception" {
			return nil
		}
		return fmt.Errorf("opensearch: create index: %w", err)
	}
	return nil
}

// osDoc is the indexed shape.
type osDoc struct {
	DocID    string `json:"doc_id"`
	Source   string `json:"source"`
	TenantID string `json:"tenant_id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"`
	Version  int64  `json:"version"`
	CommitTS string `json:"commit_ts"`
}

// Bulk applies upserts and deletes in a single _bulk request. Both actions use
// external versioning so a stale write (version <= stored) is rejected with 409
// rather than clobbering fresher data — this is what makes replay/rebalance safe.
//
// It returns the set of doc_ids that were actually applied here: upserts that
// succeeded (2xx), and deletes that succeeded or found nothing to delete (2xx/404).
// A 409 means the store already holds an equal-or-newer version, so the doc is
// NOT in the applied set and callers must not propagate it to other sinks. Any
// other >=400 status is a hard error (the batch is retried, offsets uncommitted).
//
// OpenSearch is thus the single version authority; Qdrant is driven off this set.
func (c *Client) Bulk(ctx context.Context, docs []contract.CanonicalDoc) (map[string]bool, error) {
	if len(docs) == 0 {
		return map[string]bool{}, nil
	}
	// _bulk preserves item order; track the doc_id + action for each line.
	type line struct {
		docID    string
		isDelete bool
	}
	var order []line
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range docs {
		if d.Op == contract.OpDelete {
			// External version on delete too: a stale delete (e.g. replayed from the
			// DLQ) must not remove a doc that has since been re-created at a higher
			// version. OpenSearch rejects it with 409.
			_ = enc.Encode(map[string]any{"delete": map[string]any{
				"_index": Index, "_id": d.DocID,
				"version": d.Version, "version_type": "external",
			}})
			order = append(order, line{docID: d.DocID, isDelete: true})
			continue
		}
		_ = enc.Encode(map[string]any{"index": map[string]any{
			"_index": Index, "_id": d.DocID,
			"version": d.Version, "version_type": "external",
		}})
		_ = enc.Encode(osDoc{
			DocID: d.DocID, Source: d.Source, TenantID: fmt.Sprintf("%d", d.TenantID),
			Title: d.Title, Body: d.Body, Category: d.Metadata.Category,
			Version: d.Version, CommitTS: d.CommitTS.UTC().Format(time.RFC3339Nano),
		})
		order = append(order, line{docID: d.DocID, isDelete: false})
	}
	resp, err := c.api.Bulk(ctx, opensearchapi.BulkReq{
		Body:   bytes.NewReader(buf.Bytes()),
		Params: opensearchapi.BulkParams{Refresh: "true"},
	})
	if err != nil {
		return nil, fmt.Errorf("opensearch: bulk: %w", err)
	}

	applied := make(map[string]bool, len(order))
	for i, item := range resp.Items {
		if i >= len(order) {
			break
		}
		ln := order[i]
		for _, res := range item { // one entry per item, keyed by action
			switch {
			case res.Status < 300:
				applied[ln.docID] = true
			case res.Status == 409:
				// stale — store already has an equal/newer version; do not propagate.
			case res.Status == 404 && ln.isDelete:
				// nothing to delete; safe to propagate the delete to other sinks.
				applied[ln.docID] = true
			default:
				return nil, fmt.Errorf("opensearch: bulk item %q failed: status=%d", ln.docID, res.Status)
			}
		}
	}
	return applied, nil
}

// --- search ---

type Hit struct {
	DocID string
	Score float64
}

// Search runs a BM25 multi_match over title+body with optional keyword filters.
func (c *Client) Search(ctx context.Context, query string, filters map[string]string, size int) ([]Hit, error) {
	must := []any{map[string]any{"multi_match": map[string]any{
		"query": query, "fields": []string{"title^2", "body"},
	}}}
	var filter []any
	for k, v := range filters {
		filter = append(filter, map[string]any{"term": map[string]any{k: v}})
	}
	body := map[string]any{
		"size":    size,
		"query":   map[string]any{"bool": map[string]any{"must": must, "filter": filter}},
		"_source": []string{"doc_id"},
	}
	b, _ := json.Marshal(body)
	resp, err := c.api.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{Index},
		Body:    bytes.NewReader(b),
	})
	if err != nil {
		return nil, fmt.Errorf("opensearch: search: %w", err)
	}
	hits := make([]Hit, 0, len(resp.Hits.Hits))
	for _, h := range resp.Hits.Hits {
		var src struct {
			DocID string `json:"doc_id"`
		}
		_ = json.Unmarshal(h.Source, &src)
		hits = append(hits, Hit{DocID: src.DocID, Score: float64(h.Score)})
	}
	return hits, nil
}

// Doc is the stored content returned for context assembly.
type Doc struct {
	DocID    string
	Source   string
	Title    string
	Body     string
	Category string
}

// GetByIDs fetches stored documents by _id (used to assemble RAG context).
func (c *Client) GetByIDs(ctx context.Context, ids []string) (map[string]Doc, error) {
	out := map[string]Doc{}
	if len(ids) == 0 {
		return out, nil
	}
	body, _ := json.Marshal(map[string]any{"ids": ids})
	resp, err := c.api.MGet(ctx, opensearchapi.MGetReq{
		Index: Index,
		Body:  bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("opensearch: mget: %w", err)
	}
	for _, d := range resp.Docs {
		if !d.Found {
			continue
		}
		var s osDoc
		_ = json.Unmarshal(d.Source, &s)
		out[d.ID] = Doc{DocID: s.DocID, Source: s.Source, Title: s.Title, Body: s.Body, Category: s.Category}
	}
	return out, nil
}

// Health pings the cluster health endpoint.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.api.Cluster.Health(ctx, nil)
	if err != nil {
		return fmt.Errorf("opensearch: health: %w", err)
	}
	return nil
}
