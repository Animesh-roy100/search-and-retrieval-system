// Package idempotency implements the per-source version guard: a change is applied
// only if its version strictly exceeds the version already stored for that doc_id.
// Combined with deterministic ids in the sinks, this makes replay/reprocess safe.
package idempotency

import "sync"

// Guard tracks the highest applied version per doc_id.
// doc_id is already source-namespaced, so versions are never compared across sources.
type Guard struct {
	mu   sync.Mutex
	seen map[string]int64
}

func NewGuard() *Guard {
	return &Guard{seen: make(map[string]int64)}
}

// ShouldApply reports whether an incoming (docID, version) is newer than what we
// have applied. It does NOT record the version — call Commit after a successful sink.
func (g *Guard) ShouldApply(docID string, version int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	stored, ok := g.seen[docID]
	if !ok {
		return true
	}
	return version > stored
}

// Commit records that (docID, version) has been durably applied.
// It only advances the stored version, never regresses it.
func (g *Guard) Commit(docID string, version int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cur, ok := g.seen[docID]; !ok || version > cur {
		g.seen[docID] = version
	}
}

// Version returns the currently stored version for a doc (0, false if unknown).
func (g *Guard) Version(docID string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.seen[docID]
	return v, ok
}
