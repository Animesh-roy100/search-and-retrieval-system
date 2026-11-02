//go:build integration

// Replay-safety integration test. Proves the durable-idempotency guarantee at the
// version authority (OpenSearch): lower versions are rejected, while equal versions
// are accepted so a partial dual-write retry can reach Qdrant and converge. A stale
// upsert/delete from a DLQ replay or consumer-group rebalance never clobbers fresher
// data.
//
// Run against the live stack:
//
//	OPENSEARCH_URL=http://localhost:9200 go test -tags integration ./internal/opensearch/ -run Replay -v
package opensearch

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/animeshroy/search-and-retrieval-system/internal/contract"
)

func doc(id string, version int64, op contract.Op, body string) contract.CanonicalDoc {
	return contract.CanonicalDoc{
		Source: "itest", SourceID: id, DocID: id, TenantID: 999,
		Op: op, Tier: contract.TierUrgent, Version: version,
		CommitTS: time.Now().UTC(), Title: "replay probe", Body: body,
	}
}

func TestReplaySafety_StaleWritesRejected(t *testing.T) {
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		t.Skip("OPENSEARCH_URL not set; skipping integration test")
	}
	c, err := New(url)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx := context.Background()
	if err := c.EnsureIndex(ctx); err != nil {
		t.Fatalf("ensure index: %v", err)
	}
	id := fmt.Sprintf("itest:replay:%d", time.Now().UnixNano())

	// 1) Fresh upsert at v3 must apply.
	applied, err := c.Bulk(ctx, []contract.CanonicalDoc{doc(id, 3, contract.OpUpsert, "v3 body")})
	if err != nil {
		t.Fatalf("v3 upsert: %v", err)
	}
	if !applied[id] {
		t.Fatal("v3 upsert should be applied")
	}
	assertBody(t, c, ctx, id, "v3 body")

	// 2) Equal-version replay must be accepted so a prior Qdrant failure can recover.
	// The source+doc+version tuple is immutable, so replay content is identical.
	applied, err = c.Bulk(ctx, []contract.CanonicalDoc{doc(id, 3, contract.OpUpsert, "v3 body")})
	if err != nil {
		t.Fatalf("equal v3 replay: %v", err)
	}
	if !applied[id] {
		t.Fatal("equal v3 replay must be propagated for Qdrant convergence")
	}
	assertBody(t, c, ctx, id, "v3 body")

	// 3) Stale upsert at v2 (replayed) must NOT apply and must NOT overwrite v3.
	applied, err = c.Bulk(ctx, []contract.CanonicalDoc{doc(id, 2, contract.OpUpsert, "v2 body")})
	if err != nil {
		t.Fatalf("stale v2 upsert errored (should be tolerated): %v", err)
	}
	if applied[id] {
		t.Fatal("stale v2 upsert must NOT be in the applied set")
	}
	assertBody(t, c, ctx, id, "v3 body") // unchanged

	// 4) Stale delete at v1 (replayed) must NOT apply and must NOT remove the doc.
	applied, err = c.Bulk(ctx, []contract.CanonicalDoc{doc(id, 1, contract.OpDelete, "")})
	if err != nil {
		t.Fatalf("stale v1 delete errored (should be tolerated): %v", err)
	}
	if applied[id] {
		t.Fatal("stale v1 delete must NOT be in the applied set")
	}
	assertBody(t, c, ctx, id, "v3 body") // still present

	// 5) Newer delete at v4 must apply and remove the doc.
	applied, err = c.Bulk(ctx, []contract.CanonicalDoc{doc(id, 4, contract.OpDelete, "")})
	if err != nil {
		t.Fatalf("v4 delete: %v", err)
	}
	if !applied[id] {
		t.Fatal("v4 delete should be applied")
	}
	docs, _ := c.GetByIDs(ctx, []string{id})
	if _, found := docs[id]; found {
		t.Fatal("doc should be gone after v4 delete")
	}
}

func assertBody(t *testing.T, c *Client, ctx context.Context, id, want string) {
	t.Helper()
	docs, err := c.GetByIDs(ctx, []string{id})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	d, ok := docs[id]
	if !ok {
		t.Fatalf("doc %s not found (expected body %q)", id, want)
	}
	if d.Body != want {
		t.Fatalf("doc %s body = %q, want %q (stale write leaked!)", id, d.Body, want)
	}
}
