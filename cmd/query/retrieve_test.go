package main

import (
	"context"
	"errors"
	"testing"

	"github.com/animeshroy/search-and-retrieval-system/internal/opensearch"
	"github.com/animeshroy/search-and-retrieval-system/internal/qdrant"
)

type unavailableLexicalStore struct{}

func (unavailableLexicalStore) Search(context.Context, string, map[string]string, int) ([]opensearch.Hit, error) {
	return nil, errors.New("opensearch unavailable")
}

func (unavailableLexicalStore) GetByIDs(context.Context, []string) (map[string]opensearch.Doc, error) {
	return nil, errors.New("opensearch unavailable")
}

type contentVectorStore struct {
	hits []qdrant.Hit
}

func (s contentVectorStore) Search(context.Context, []float32, map[string]string, int) ([]qdrant.Hit, error) {
	return s.hits, nil
}

type identityML struct{}

func (identityML) Embed(context.Context, []string) ([][]float32, string, error) {
	return [][]float32{{1, 0}}, "test", nil
}

func (identityML) Rerank(_ context.Context, _ string, docs []string) ([]float64, []int, error) {
	scores := make([]float64, len(docs))
	order := make([]int, len(docs))
	for i := range docs {
		scores[i] = 1
		order[i] = i
	}
	return scores, order, nil
}

func (identityML) Health(context.Context) error { return nil }

func TestRetrieveFallsBackToQdrantContentWhenOpenSearchIsUnavailable(t *testing.T) {
	s := &server{
		os: unavailableLexicalStore{},
		qd: contentVectorStore{hits: []qdrant.Hit{
			{
				DocID: "postgres:42", Source: "postgres", Title: "Vector-only",
				Body: "This content came from the Qdrant payload.", Score: 0.91,
			},
		}},
		ml: identityML{}, rrfK: 60, rerankN: 10, topK: 5,
	}

	sources, err := s.retrieve(context.Background(), "vector query", nil, 5, []float32{1, 0})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1", len(sources))
	}
	got := sources[0]
	if got.DocID != "postgres:42" || got.Origin != "postgres" {
		t.Fatalf("source identity = %#v", got)
	}
	wantText := "Vector-only. This content came from the Qdrant payload."
	if got.Text != wantText {
		t.Fatalf("text = %q, want %q", got.Text, wantText)
	}
}
