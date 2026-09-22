package cache

import "testing"

func TestKeyStableAndFilterOrderIndependent(t *testing.T) {
	k1 := Key("Hello World", map[string]string{"source": "postgres", "category": "rag"})
	k2 := Key("hello world  ", map[string]string{"category": "rag", "source": "postgres"})
	if k1 != k2 {
		t.Fatalf("keys should match regardless of case/whitespace/filter order: %s vs %s", k1, k2)
	}
	k3 := Key("different", nil)
	if k1 == k3 {
		t.Fatal("different queries must produce different keys")
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
