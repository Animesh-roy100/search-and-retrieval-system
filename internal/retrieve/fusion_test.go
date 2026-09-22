package retrieve

import "testing"

func TestRRFCombines(t *testing.T) {
	bm25 := []string{"a", "b", "c"}
	vec := []string{"c", "b", "d"}
	got := RRF([][]string{bm25, vec}, 60)
	// c appears rank2 in bm25 and rank0 in vec -> should be strong.
	// b appears rank1 in both -> also strong. a and d each appear once.
	if len(got) != 4 {
		t.Fatalf("expected 4 unique docs, got %d", len(got))
	}
	// Top result must be b or c (both appear in both lists).
	top := got[0].DocID
	if top != "b" && top != "c" {
		t.Fatalf("expected b or c on top, got %s", top)
	}
	// Docs appearing in both lists must outrank docs appearing once.
	rank := map[string]int{}
	for i, s := range got {
		rank[s.DocID] = i
	}
	if rank["b"] > rank["a"] || rank["c"] > rank["d"] {
		t.Fatalf("docs in both lists should outrank singletons: %+v", got)
	}
}

func TestRRFDeterministicTieBreak(t *testing.T) {
	// Two docs with identical single appearances -> alphabetical tie-break.
	got := RRF([][]string{{"z"}, {"a"}}, 60)
	if got[0].DocID != "a" {
		t.Fatalf("expected alphabetical tie-break 'a' first, got %s", got[0].DocID)
	}
}

func TestRRFEmpty(t *testing.T) {
	if got := RRF(nil, 60); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestMMRDiversifies(t *testing.T) {
	// a and b are near-identical vectors; c is orthogonal.
	vectors := map[string][]float32{
		"a": {1, 0, 0},
		"b": {0.99, 0.01, 0},
		"c": {0, 1, 0},
	}
	relevance := map[string]float64{"a": 1.0, "b": 0.95, "c": 0.9}
	// With lambda favoring diversity, after picking 'a' the next should be 'c', not 'b'.
	got := MMR([]string{"a", "b", "c"}, relevance, vectors, 0.5, 2)
	if got[0] != "a" {
		t.Fatalf("most relevant 'a' should be picked first, got %s", got[0])
	}
	if got[1] != "c" {
		t.Fatalf("MMR should diversify to 'c' over near-duplicate 'b', got %s", got[1])
	}
}

func TestMMRPureRelevance(t *testing.T) {
	vectors := map[string][]float32{"a": {1, 0}, "b": {0, 1}, "c": {1, 1}}
	relevance := map[string]float64{"a": 0.3, "b": 0.9, "c": 0.6}
	// lambda=1 -> pure relevance ordering.
	got := MMR([]string{"a", "b", "c"}, relevance, vectors, 1.0, 3)
	if got[0] != "b" || got[1] != "c" || got[2] != "a" {
		t.Fatalf("pure-relevance order wrong: %v", got)
	}
}
