package qdrant

import (
	"testing"

	qc "github.com/qdrant/go-client/qdrant"
)

func TestPayloadContentExtraction(t *testing.T) {
	payload := qc.NewValueMap(map[string]any{
		"doc_id": "postgres:42", "source": "postgres", "title": "Title",
		"body": "Body", "category": "docs", "version": int64(9),
	})
	if got := payloadString(payload, "doc_id"); got != "postgres:42" {
		t.Fatalf("doc_id = %q", got)
	}
	if got := payloadString(payload, "title"); got != "Title" {
		t.Fatalf("title = %q", got)
	}
	if got := payloadString(payload, "body"); got != "Body" {
		t.Fatalf("body = %q", got)
	}
	if got := payloadInt(payload, "version"); got != 9 {
		t.Fatalf("version = %d", got)
	}
	if got := payloadString(payload, "missing"); got != "" {
		t.Fatalf("missing string = %q", got)
	}
}
