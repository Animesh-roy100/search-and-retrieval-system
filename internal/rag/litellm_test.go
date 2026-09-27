package rag

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Proves the LiteLLM provider speaks the OpenAI /v1/chat/completions shape: sends
// model + system/user messages, parses choices[0].message.content, and that the
// judge extracts JSON from the reply.
func TestLiteLLMGenerateAndJudge(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role, Content string
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		reply := "Debezium reads the WAL [doc_1]."
		if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "faithfulness judge") {
			reply = "```json\n{\"score\": 1.0, \"unsupported\": []}\n```"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
		})
	}))
	defer srv.Close()

	c := NewLiteLLM(srv.URL, "sk-test", "gemini-flash")
	ctx := t.Context()

	ans, err := c.Generate(ctx, "how?", []Source{{Marker: "doc_1", Text: "x"}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(ans.Text, "[doc_1]") || ans.Model != "litellm:gemini-flash" {
		t.Fatalf("unexpected answer: %+v", ans)
	}
	if len(ans.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(ans.Citations))
	}
	if gotAuth != "Bearer sk-test" || gotModel != "gemini-flash" {
		t.Fatalf("request headers/model wrong: auth=%q model=%q", gotAuth, gotModel)
	}

	// Judge should parse JSON even when wrapped in code fences.
	res, err := c.JudgeFaithfulness(ctx, "Debezium reads the WAL.", []Source{{Marker: "doc_1", Text: "x"}})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if res.Score != 1.0 {
		t.Fatalf("judge score = %v", res.Score)
	}
}
