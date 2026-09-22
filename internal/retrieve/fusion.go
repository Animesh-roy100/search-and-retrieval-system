// Package retrieve holds the source-agnostic fusion + diversity algorithms:
// Reciprocal Rank Fusion (combine BM25 + vector) and Maximal Marginal Relevance
// (diversify the reranked set).
package retrieve

import "sort"

// Scored is a doc id with a fusion/relevance score.
type Scored struct {
	DocID string
	Score float64
}

// RRF fuses ranked lists of doc ids with Reciprocal Rank Fusion.
// Each list contributes 1/(k+rank+1) to a doc's score (rank is 0-based).
// k=60 is the standard constant; it needs no score normalization.
func RRF(lists [][]string, k int) []Scored {
	if k <= 0 {
		k = 60
	}
	score := map[string]float64{}
	for _, list := range lists {
		for rank, id := range list {
			score[id] += 1.0 / float64(k+rank+1)
		}
	}
	out := make([]Scored, 0, len(score))
	for id, s := range score {
		out = append(out, Scored{DocID: id, Score: s})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].DocID < out[j].DocID // deterministic tie-break
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// MMR reorders candidates to balance relevance with diversity.
// relevance[id] is the first-stage/rerank score; sim(a,b) is cosine similarity of
// their vectors. lambda in [0,1]: 1 = pure relevance, 0 = pure diversity.
func MMR(candidates []string, relevance map[string]float64, vectors map[string][]float32, lambda float64, topK int) []string {
	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}
	selected := make([]string, 0, topK)
	remaining := append([]string(nil), candidates...)

	for len(selected) < topK && len(remaining) > 0 {
		bestIdx, bestScore := 0, -1e18
		for i, id := range remaining {
			rel := relevance[id]
			maxSim := 0.0
			for _, s := range selected {
				if sim := cosine(vectors[id], vectors[s]); sim > maxSim {
					maxSim = sim
				}
			}
			mmr := lambda*rel - (1-lambda)*maxSim
			if mmr > bestScore {
				bestScore, bestIdx = mmr, i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt(na) * sqrt(nb))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method — avoids importing math for one call in hot path tests.
	z := x
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
