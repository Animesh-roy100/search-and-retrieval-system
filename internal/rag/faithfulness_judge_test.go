package rag

import (
	"context"
	"errors"
	"testing"
)

// fakeJudge is a Provider that also implements Judge, for testing the dispatcher.
type fakeJudge struct {
	res    FaithfulnessResult
	err    error
	called bool
}

func (f *fakeJudge) Name() string { return "fake" }
func (f *fakeJudge) Generate(context.Context, string, []Source) (Answer, error) {
	return Answer{}, nil
}
func (f *fakeJudge) JudgeFaithfulness(context.Context, string, []Source) (FaithfulnessResult, error) {
	f.called = true
	return f.res, f.err
}

func TestScoreFaithfulness_UsesJudgeInLLMMode(t *testing.T) {
	j := &fakeJudge{res: FaithfulnessResult{Score: 0.42}}
	got := ScoreFaithfulness(context.Background(), "llm", j, "some answer", nil)
	if !j.called {
		t.Fatal("expected LLM judge to be called in llm mode")
	}
	if got.Score != 0.42 {
		t.Fatalf("expected judge score 0.42, got %v", got.Score)
	}
}

func TestScoreFaithfulness_FallsBackToLexicalOnJudgeError(t *testing.T) {
	j := &fakeJudge{err: errors.New("boom")}
	src := []Source{{Marker: "doc_1", Text: "debezium reads the write ahead log"}}
	got := ScoreFaithfulness(context.Background(), "llm", j, "Debezium reads the write ahead log.", src)
	// Falls back to lexical, which fully supports this verbatim answer.
	if got.Score < 0.9 {
		t.Fatalf("expected lexical fallback ~1.0, got %v", got.Score)
	}
}

func TestScoreFaithfulness_LexicalModeIgnoresJudge(t *testing.T) {
	j := &fakeJudge{res: FaithfulnessResult{Score: 0.42}}
	_ = ScoreFaithfulness(context.Background(), "lexical", j, "x", nil)
	if j.called {
		t.Fatal("lexical mode must not call the LLM judge")
	}
}
