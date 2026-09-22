package rag

import (
	"context"
	"strings"
	"testing"
)

func sampleSources() []Source {
	return []Source{
		{Marker: "doc_1", DocID: "postgres:documents:1", Origin: "postgres",
			Text: "Debezium reads the PostgreSQL write-ahead log using logical replication. Each committed row change becomes a change event."},
		{Marker: "doc_2", DocID: "postgres:documents:2", Origin: "postgres",
			Text: "Redpanda implements the Kafka API without a JVM. It buffers change events durably."},
	}
}

func TestExtractiveGroundedAndCited(t *testing.T) {
	p := &ExtractiveProvider{}
	ans, err := p.Generate(context.Background(), "How does Debezium read changes from the write-ahead log?", sampleSources())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ans.Text, "[doc_1]") {
		t.Fatalf("answer should cite doc_1: %q", ans.Text)
	}
	if len(ans.Citations) == 0 || ans.Citations[0].DocID != "postgres:documents:1" {
		t.Fatalf("expected citation to doc_1: %+v", ans.Citations)
	}
	// The extractive answer is drawn verbatim from sources -> perfectly faithful.
	f := Faithfulness(ans.Text, sampleSources())
	if f.Score < 0.99 {
		t.Fatalf("extractive answer should be fully faithful, got %.2f (unsupported=%v)", f.Score, f.Unsupported)
	}
}

func TestExtractiveAbstainsWhenNoMatch(t *testing.T) {
	p := &ExtractiveProvider{}
	ans, _ := p.Generate(context.Background(), "what is the airspeed velocity of a swallow", sampleSources())
	if ans.Text != "Not found in sources." {
		t.Fatalf("expected abstention, got %q", ans.Text)
	}
}

func TestFaithfulnessCatchesHallucination(t *testing.T) {
	hallucinated := "Debezium reads the write-ahead log [doc_1]. The system also mines bitcoin on weekends and orders pizza."
	f := Faithfulness(hallucinated, sampleSources())
	if f.Score >= 1.0 {
		t.Fatalf("hallucinated sentence should lower faithfulness, got %.2f", f.Score)
	}
	if len(f.Unsupported) == 0 {
		t.Fatal("expected at least one unsupported sentence")
	}
}

func TestBuildPromptContract(t *testing.T) {
	sys, user := BuildPrompt("q?", sampleSources())
	if !strings.Contains(sys, "ONLY using the numbered sources") {
		t.Fatalf("system prompt missing grounding contract: %q", sys)
	}
	if !strings.Contains(user, "[doc_1]") || !strings.Contains(user, "Question: q?") {
		t.Fatalf("user prompt malformed: %q", user)
	}
}

func TestProviderSelectionDefault(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	if got := NewProvider().Name(); got != "extractive-v1" {
		t.Fatalf("default provider should be extractive, got %s", got)
	}
	// anthropic without key falls back to extractive.
	t.Setenv("LLM_PROVIDER", "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if got := NewProvider().Name(); got != "extractive-v1" {
		t.Fatalf("anthropic without key should fall back to extractive, got %s", got)
	}
}
