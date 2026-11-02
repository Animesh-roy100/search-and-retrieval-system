package rag

import (
	"encoding/json"
	"testing"
)

// The web UI and API clients read these snake_case keys; keep them stable.
func TestSourceJSONContract(t *testing.T) {
	b, err := json.Marshal(Source{Marker: "doc_1", DocID: "postgres:1", Origin: "postgres", Text: "t", Score: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"marker", "doc_id", "origin", "text", "score"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing key %q in %s", k, b)
		}
	}
}
