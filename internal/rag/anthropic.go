package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// Anthropic is the optional real-LLM provider. It is only used when
// LLM_PROVIDER=anthropic and ANTHROPIC_API_KEY is set.
type Anthropic struct {
	key   string
	model string
	hc    *http.Client
}

func NewAnthropic(key, model string) *Anthropic {
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	return &Anthropic{key: key, model: model, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (a *Anthropic) Name() string { return "anthropic:" + a.model }

func (a *Anthropic) Generate(ctx context.Context, query string, sources []Source) (Answer, error) {
	system, user := BuildPrompt(query, sources)
	reqBody := map[string]any{
		"model":      a.model,
		"max_tokens": 512,
		"system":     system,
		"messages":   []any{map[string]any{"role": "user", "content": user}},
	}
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	if err != nil {
		return Answer{}, err
	}
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		return Answer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Answer{}, fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Answer{}, err
	}
	text := ""
	if len(out.Content) > 0 {
		text = out.Content[0].Text
	}
	return Answer{Text: text, Model: a.Name(), Citations: citationsFromText(text, sources)}, nil
}

var markerRe = regexp.MustCompile(`doc_(\d+)`)

// citationsFromText maps [doc_N] markers found in a free-text answer back to sources.
func citationsFromText(text string, sources []Source) []Citation {
	byMarker := map[string]Source{}
	for _, s := range sources {
		byMarker[s.Marker] = s
	}
	seen := map[string]bool{}
	var cites []Citation
	for _, m := range markerRe.FindAllString(text, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		if s, ok := byMarker[m]; ok {
			cites = append(cites, Citation{Marker: m, DocID: s.DocID, Source: s.Origin, Score: s.Score})
		}
	}
	return cites
}
