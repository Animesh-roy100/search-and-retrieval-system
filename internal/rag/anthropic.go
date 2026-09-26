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
	text, err := a.complete(ctx, system, user, 512)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Text: text, Model: a.Name(), Citations: citationsFromText(text, sources)}, nil
}

// JudgeFaithfulness asks the model to score how well the answer is grounded in the
// sources — a stronger check than lexical overlap because it understands paraphrase
// and entailment. Returns a score in [0,1] plus any unsupported sentences.
func (a *Anthropic) JudgeFaithfulness(ctx context.Context, answer string, sources []Source) (FaithfulnessResult, error) {
	var sb bytes.Buffer
	for _, s := range sources {
		sb.WriteString("[" + s.Marker + "] " + s.Text + "\n")
	}
	system := "You are a strict faithfulness judge for a RAG system. Given SOURCES and an " +
		"ANSWER, decide what fraction of the answer's factual claims are directly supported by " +
		"the sources. Respond ONLY with compact JSON: {\"score\": <0..1>, \"unsupported\": [\"<claim>\", ...]}. " +
		"No prose."
	user := "SOURCES:\n" + sb.String() + "\nANSWER:\n" + answer
	text, err := a.complete(ctx, system, user, 512)
	if err != nil {
		return FaithfulnessResult{}, err
	}
	// The model may wrap JSON in prose/fences; extract the first {...} object.
	raw := text
	if i, j := bytes.IndexByte([]byte(text), '{'), bytes.LastIndexByte([]byte(text), '}'); i >= 0 && j > i {
		raw = text[i : j+1]
	}
	var parsed FaithfulnessResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return FaithfulnessResult{}, fmt.Errorf("anthropic: judge parse: %w", err)
	}
	if parsed.Score < 0 {
		parsed.Score = 0
	} else if parsed.Score > 1 {
		parsed.Score = 1
	}
	return parsed, nil
}

// complete performs one Messages API call and returns the first text block.
func (a *Anthropic) complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	b, _ := json.Marshal(map[string]any{
		"model":      a.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   []any{map[string]any{"role": "user", "content": user}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := a.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Content) > 0 {
		return out.Content[0].Text, nil
	}
	return "", nil
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
