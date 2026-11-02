package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
	"github.com/animeshroy/search-and-retrieval-system/internal/idempotency"
	"github.com/animeshroy/search-and-retrieval-system/internal/qdrant"
)

type replaySearchIndex struct {
	calls int
}

func (s *replaySearchIndex) Bulk(_ context.Context, docs []contract.CanonicalDoc) (map[string]bool, error) {
	s.calls++
	applied := make(map[string]bool, len(docs))
	for _, d := range docs {
		applied[d.DocID] = true // external_gte accepts both initial and equal-version replay
	}
	return applied, nil
}

type flakyVectorIndex struct {
	upsertCalls int
	points      []qdrant.Point
}

func (v *flakyVectorIndex) Upsert(_ context.Context, points []qdrant.Point) error {
	v.upsertCalls++
	if v.upsertCalls == 1 {
		return errors.New("injected qdrant outage")
	}
	v.points = append(v.points, points...)
	return nil
}

func (*flakyVectorIndex) DeleteByDocIDs(context.Context, []string) error { return nil }

type indexerTestML struct{}

func (indexerTestML) Embed(_ context.Context, texts []string) ([][]float32, string, error) {
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = []float32{1, 0}
	}
	return vectors, "test-model", nil
}

func (indexerTestML) Rerank(context.Context, string, []string) ([]float64, []int, error) {
	return nil, nil, nil
}

func (indexerTestML) Health(context.Context) error { return nil }

type recordingInvalidator struct {
	tenants []string
}

func (c *recordingInvalidator) BumpGeneration(_ context.Context, tenant string) {
	c.tenants = append(c.tenants, tenant)
}

func TestApplyRetriesQdrantAfterOpenSearchSucceeded(t *testing.T) {
	osStore := &replaySearchIndex{}
	vectorStore := &flakyVectorIndex{}
	invalidator := &recordingInvalidator{}
	ix := &indexer{
		os: osStore, qd: vectorStore, ml: indexerTestML{},
		guard: idempotency.NewGuard(), cache: invalidator,
	}
	doc := contract.CanonicalDoc{
		Source: "postgres", SourceID: "42", DocID: "postgres:42", TenantID: 7,
		Op: contract.OpUpsert, Tier: contract.TierUrgent, Version: 12,
		CommitTS: time.Now().UTC(), Title: "Retry", Body: "Qdrant must converge.",
		Metadata: contract.Metadata{Category: "reliability"},
	}

	if err := ix.apply(context.Background(), []contract.CanonicalDoc{doc}, "urgent"); err == nil {
		t.Fatal("first apply should fail when Qdrant is unavailable")
	}
	if err := ix.apply(context.Background(), []contract.CanonicalDoc{doc}, "urgent"); err != nil {
		t.Fatalf("equal-version replay should converge: %v", err)
	}

	if osStore.calls != 2 {
		t.Fatalf("OpenSearch calls = %d, want 2 (initial + equal-version replay)", osStore.calls)
	}
	if vectorStore.upsertCalls != 2 || len(vectorStore.points) != 1 {
		t.Fatalf("Qdrant calls=%d stored points=%d, want 2 and 1", vectorStore.upsertCalls, len(vectorStore.points))
	}
	payload := vectorStore.points[0].Payload
	if payload["title"] != doc.Title || payload["body"] != doc.Body {
		t.Fatalf("Qdrant payload lacks vector-only content: %#v", payload)
	}
	if payload["tenant_id"] != "7" {
		t.Fatalf("tenant_id = %#v, want string %q", payload["tenant_id"], "7")
	}
	if len(invalidator.tenants) != 1 || invalidator.tenants[0] != "7" {
		t.Fatalf("invalidations = %#v, want tenant 7 once after convergence", invalidator.tenants)
	}
	if ix.guard.ShouldApply(doc.DocID, doc.Version) {
		t.Fatal("guard should commit only after both stores converge")
	}
}
