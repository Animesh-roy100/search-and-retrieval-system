package mlclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Proves the TEI adapter maps TEI's wire format back to the shared ML contract:
// /embed returns a bare array of vectors; /rerank returns [{index,score}] sorted by
// score, which we turn into (scores-in-input-order, descending-order).
func TestTEIEmbedAndRerankMapping(t *testing.T) {
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		_ = json.NewEncoder(w).Encode([][]float32{{0.1, 0.2}, {0.3, 0.4}})
	}))
	defer embed.Close()

	rerank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		// TEI returns results sorted by score desc, carrying the original index.
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"index": 2, "score": 0.9},
			{"index": 0, "score": 0.5},
			{"index": 1, "score": 0.1},
		})
	}))
	defer rerank.Close()

	c := NewTEI(embed.URL, rerank.URL)
	ctx := t.Context()

	vecs, ver, err := c.Embed(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][1] != 0.4 {
		t.Fatalf("unexpected vectors: %v", vecs)
	}
	if ver != "tei" {
		t.Fatalf("model version = %q", ver)
	}

	scores, order, err := c.Rerank(ctx, "q", []string{"d0", "d1", "d2"})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	// order is descending by score → original indices [2,0,1]
	want := []int{2, 0, 1}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	// scores are aligned to input order: d0=0.5, d1=0.1, d2=0.9
	if scores[0] != 0.5 || scores[1] != 0.1 || scores[2] != 0.9 {
		t.Fatalf("scores = %v", scores)
	}

	if err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
}
