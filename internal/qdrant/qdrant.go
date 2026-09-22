// Package qdrant wraps the official Qdrant Go client (github.com/qdrant/go-client),
// which talks gRPC on port 6334. This package exposes the small surface the indexer
// and query service need, keeping call sites decoupled from the SDK types.
package qdrant

import (
	"context"
	"fmt"

	qc "github.com/qdrant/go-client/qdrant"
)

const Collection = "documents"

type Client struct {
	c    *qc.Client
	coll string
}

// New connects to Qdrant over gRPC. host should be host:port (default port 6334).
func New(host string, port int) (*Client, error) {
	cl, err := qc.NewClient(&qc.Config{Host: host, Port: port})
	if err != nil {
		return nil, fmt.Errorf("qdrant: connect: %w", err)
	}
	return &Client{c: cl, coll: Collection}, nil
}

// EnsureCollection creates the collection with the given vector size if absent.
func (c *Client) EnsureCollection(ctx context.Context, dim int) error {
	exists, err := c.c.CollectionExists(ctx, c.coll)
	if err != nil {
		return fmt.Errorf("qdrant: collection exists: %w", err)
	}
	if exists {
		return nil
	}
	return c.c.CreateCollection(ctx, &qc.CreateCollection{
		CollectionName: c.coll,
		VectorsConfig: qc.NewVectorsConfig(&qc.VectorParams{
			Size:     uint64(dim),
			Distance: qc.Distance_Cosine,
		}),
	})
}

// Point is one vector + payload to upsert.
type Point struct {
	ID      string
	Vector  []float32
	Payload map[string]any
}

// Upsert writes points (idempotent — same UUID id overwrites).
func (c *Client) Upsert(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	ps := make([]*qc.PointStruct, 0, len(points))
	for _, p := range points {
		ps = append(ps, &qc.PointStruct{
			Id:      qc.NewIDUUID(p.ID),
			Vectors: qc.NewVectorsDense(p.Vector),
			Payload: qc.NewValueMap(p.Payload),
		})
	}
	wait := true
	_, err := c.c.Upsert(ctx, &qc.UpsertPoints{
		CollectionName: c.coll,
		Points:         ps,
		Wait:           &wait,
	})
	if err != nil {
		return fmt.Errorf("qdrant: upsert: %w", err)
	}
	return nil
}

// DeleteByDocIDs removes points whose payload.doc_id is in the list.
func (c *Client) DeleteByDocIDs(ctx context.Context, docIDs []string) error {
	if len(docIDs) == 0 {
		return nil
	}
	conds := make([]*qc.Condition, 0, len(docIDs))
	for _, id := range docIDs {
		conds = append(conds, qc.NewMatch("doc_id", id))
	}
	wait := true
	_, err := c.c.Delete(ctx, &qc.DeletePoints{
		CollectionName: c.coll,
		Points:         qc.NewPointsSelectorFilter(&qc.Filter{Should: conds}),
		Wait:           &wait,
	})
	if err != nil {
		return fmt.Errorf("qdrant: delete: %w", err)
	}
	return nil
}

type Hit struct {
	DocID string
	Score float64
}

// Search runs ANN search with optional payload filters (exact match).
func (c *Client) Search(ctx context.Context, vector []float32, filters map[string]string, limit int) ([]Hit, error) {
	req := &qc.QueryPoints{
		CollectionName: c.coll,
		Query:          qc.NewQuery(vector...),
		Limit:          ptrU64(uint64(limit)),
		WithPayload:    qc.NewWithPayload(true),
	}
	if len(filters) > 0 {
		var must []*qc.Condition
		for k, v := range filters {
			must = append(must, qc.NewMatch(k, v))
		}
		req.Filter = &qc.Filter{Must: must}
	}
	res, err := c.c.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("qdrant: query: %w", err)
	}
	hits := make([]Hit, 0, len(res))
	for _, p := range res {
		docID := ""
		if v, ok := p.GetPayload()["doc_id"]; ok {
			docID = v.GetStringValue()
		}
		hits = append(hits, Hit{DocID: docID, Score: float64(p.GetScore())})
	}
	return hits, nil
}

// Health verifies connectivity by listing collections.
func (c *Client) Health(ctx context.Context) error {
	if _, err := c.c.ListCollections(ctx); err != nil {
		return fmt.Errorf("qdrant: health: %w", err)
	}
	return nil
}

func ptrU64(v uint64) *uint64 { return &v }
