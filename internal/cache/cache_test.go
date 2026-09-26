package cache

import "testing"

func TestKeyStableAndFilterOrderIndependent(t *testing.T) {
	k1 := Key("Hello World", map[string]string{"source": "postgres", "category": "rag"}, 0)
	k2 := Key("hello world  ", map[string]string{"category": "rag", "source": "postgres"}, 0)
	if k1 != k2 {
		t.Fatalf("keys should match regardless of case/whitespace/filter order: %s vs %s", k1, k2)
	}
	k3 := Key("different", nil, 0)
	if k1 == k3 {
		t.Fatal("different queries must produce different keys")
	}
}

func TestKeyGenerationInvalidates(t *testing.T) {
	filters := map[string]string{"tenant_id": "7"}
	if Key("q", filters, 1) == Key("q", filters, 2) {
		t.Fatal("different generations must produce different keys (invalidation)")
	}
}

func TestNilCacheIsSafe(t *testing.T) {
	c := New("", 0) // no redis
	var out map[string]any
	if c.Get(nil, "k", &out) {
		t.Fatal("nil-redis cache should always miss")
	}
	c.Set(nil, "k", map[string]any{"a": 1}) // must not panic
}
