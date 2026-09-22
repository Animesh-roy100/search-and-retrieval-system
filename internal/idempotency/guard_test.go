package idempotency

import (
	"sync"
	"testing"
)

func TestFirstApplyAndMonotonic(t *testing.T) {
	g := NewGuard()
	if !g.ShouldApply("doc", 1) {
		t.Fatal("first version should apply")
	}
	g.Commit("doc", 1)
	if g.ShouldApply("doc", 1) {
		t.Fatal("same version should be skipped (replay)")
	}
	if g.ShouldApply("doc", 0) {
		t.Fatal("older version should be skipped (out of order)")
	}
	if !g.ShouldApply("doc", 2) {
		t.Fatal("newer version should apply")
	}
}

func TestCommitNeverRegresses(t *testing.T) {
	g := NewGuard()
	g.Commit("doc", 5)
	g.Commit("doc", 3) // stale, must not lower the watermark
	if v, _ := g.Version("doc"); v != 5 {
		t.Fatalf("expected watermark 5, got %d", v)
	}
}

func TestPerDocIsolation(t *testing.T) {
	g := NewGuard()
	g.Commit("a", 10)
	if !g.ShouldApply("b", 1) {
		t.Fatal("doc b should be independent of doc a")
	}
}

func TestConcurrentSafe(t *testing.T) {
	g := NewGuard()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			if g.ShouldApply("doc", v) {
				g.Commit("doc", v)
			}
		}(int64(i))
	}
	wg.Wait()
	// Highest version seen must win.
	if v, _ := g.Version("doc"); v != 99 {
		t.Fatalf("expected highest version 99, got %d", v)
	}
}
