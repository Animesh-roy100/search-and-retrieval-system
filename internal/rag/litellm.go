package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LiteLLM talks to a LiteLLM proxy (or any OpenAI-compatible endpoint: OpenAI,
// Ollama, vLLM). One interface over every provider — the proxy handles vendor
// routing/keys, so the app doesn't hand-write a client per vendor. Enabled with
// LLM_PROVIDER=litellm + LITELLM_URL (+ LITELLM_API_KEY, LLM_MODEL).
type LiteLLM struct {
	base   string // e.g. http://litellm:4000
	apiKey string
	model  string
	hc     *http.Client
}

func NewLiteLLM(base, apiKey, model string) *LiteLLM {
	if model == "" {
		model = "gemini-flash"
	}
	return &LiteLLM{base: base, apiKey: apiKey, model: model, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (l *LiteLLM) Name() string { return "litellm:" + l.model }

func (l *LiteLLM) Generate(ctx context.Context, query string, sources []Source) (Answer, error) {
	system, user := BuildPrompt(query, sources)
	text, err := l.complete(ctx, system, user, 2048)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Text: text, Model: l.Name(), Citations: citationsFromText(text, sources)}, nil
}

// JudgeFaithfulness scores answer grounding with the LLM (understands paraphrase).
func (l *LiteLLM) JudgeFaithfulness(ctx context.Context, answer string, sources []Source) (FaithfulnessResult, error) {
	var sb bytes.Buffer
	for _, s := range sources {
		sb.WriteString("[" + s.Marker + "] " + s.Text + "\n")
	}
	system := "You are a strict faithfulness judge for a RAG system. Given SOURCES and an " +
		"ANSWER, decide what fraction of the answer's factual claims are directly supported by " +
		"the sources. Respond ONLY with compact JSON: {\"score\": <0..1>, \"unsupported\": [\"<claim>\", ...]}. " +
		"No prose, no code fences."
	user := "SOURCES:\n" + sb.String() + "\nANSWER:\n" + answer
	text, err := l.complete(ctx, system, user, 2048)
	if err != nil {
		return FaithfulnessResult{}, err
	}
	raw := text
	if i, j := bytes.IndexByte([]byte(text), '{'), bytes.LastIndexByte([]byte(text), '}'); i >= 0 && j > i {
		raw = text[i : j+1]
	}
	var parsed FaithfulnessResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return FaithfulnessResult{}, fmt.Errorf("litellm: judge parse: %w", err)
	}
	if parsed.Score < 0 {
		parsed.Score = 0
	} else if parsed.Score > 1 {
		parsed.Score = 1
	}
	return parsed, nil
}

// complete performs one OpenAI-compatible /v1/chat/completions call.
func (l *LiteLLM) complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": l.model,
		"messages": []any{
			map[string]string{"role": "system", "content": system},
			map[string]string{"role": "user", "content": user},
		},
		"max_tokens":  maxTokens,
		"temperature": 0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}
	resp, err := l.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("litellm: status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) > 0 {
		return out.Choices[0].Message.Content, nil
	}
	return "", nil
}
