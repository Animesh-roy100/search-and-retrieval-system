package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Gemini is a real-LLM provider backed by the Google Generative Language API.
// Enabled with LLM_PROVIDER=gemini and GEMINI_API_KEY set.
type Gemini struct {
	key   string
	model string
	hc    *http.Client
}

func NewGemini(key, model string) *Gemini {
	if model == "" {
		model = "gemini-3.8-flash"
	}
	return &Gemini{key: key, model: model, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (g *Gemini) Name() string { return "gemini:" + g.model }

func (g *Gemini) Generate(ctx context.Context, query string, sources []Source) (Answer, error) {
	system, user := BuildPrompt(query, sources)
	// Gemini 3.x are thinking models — "thought" tokens count against the output
	// budget, so give ample room or the answer text comes back empty.
	text, err := g.complete(ctx, system, user, 2048)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Text: text, Model: g.Name(), Citations: citationsFromText(text, sources)}, nil
}

// JudgeFaithfulness scores answer grounding with the LLM (understands paraphrase).
func (g *Gemini) JudgeFaithfulness(ctx context.Context, answer string, sources []Source) (FaithfulnessResult, error) {
	var sb bytes.Buffer
	for _, s := range sources {
		sb.WriteString("[" + s.Marker + "] " + s.Text + "\n")
	}
	system := "You are a strict faithfulness judge for a RAG system. Given SOURCES and an " +
		"ANSWER, decide what fraction of the answer's factual claims are directly supported by " +
		"the sources. Respond ONLY with compact JSON: {\"score\": <0..1>, \"unsupported\": [\"<claim>\", ...]}. " +
		"No prose, no code fences."
	user := "SOURCES:\n" + sb.String() + "\nANSWER:\n" + answer
	text, err := g.complete(ctx, system, user, 2048)
	if err != nil {
		return FaithfulnessResult{}, err
	}
	raw := text
	if i, j := bytes.IndexByte([]byte(text), '{'), bytes.LastIndexByte([]byte(text), '}'); i >= 0 && j > i {
		raw = text[i : j+1]
	}
	var parsed FaithfulnessResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return FaithfulnessResult{}, fmt.Errorf("gemini: judge parse: %w", err)
	}
	if parsed.Score < 0 {
		parsed.Score = 0
	} else if parsed.Score > 1 {
		parsed.Score = 1
	}
	return parsed, nil
}

// complete performs one generateContent call and returns the first text part.
func (g *Gemini) complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": system}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": user}}}},
		"generationConfig":  map[string]any{"maxOutputTokens": maxTokens, "temperature": 0},
	})
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", g.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-goog-api-key", g.key) // key in header, not URL — keeps it out of logs
	resp, err := g.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gemini: status %d", resp.StatusCode)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Candidates) > 0 && len(out.Candidates[0].Content.Parts) > 0 {
		return out.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", nil
}
