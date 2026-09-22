// Package rag implements grounded generation: an LLM provider abstraction, a
// deterministic extractive default provider (no API key), a grounding prompt, and a
// faithfulness check that verifies every answer sentence is supported by a source.
package rag

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Source is one retrieved chunk offered to the generator, numbered doc_N.
type Source struct {
	Marker string // "doc_1"
	DocID  string
	Origin string // provenance ("postgres")
	Text   string
	Score  float64
}

// Answer is the generated result.
type Answer struct {
	Text     string
	Model    string
	Citations []Citation
}

// Citation links an answer marker to a source doc.
type Citation struct {
	Marker string  `json:"marker"`
	DocID  string  `json:"doc_id"`
	Source string  `json:"source"`
	Score  float64 `json:"score"`
}

// Provider generates a grounded answer from numbered sources.
type Provider interface {
	Generate(ctx context.Context, query string, sources []Source) (Answer, error)
	Name() string
}

// NewProvider selects a provider from env. Default is the deterministic extractive
// provider so the stack is self-contained; set LLM_PROVIDER=anthropic + ANTHROPIC_API_KEY
// to use a real model.
func NewProvider() Provider {
	switch strings.ToLower(os.Getenv("LLM_PROVIDER")) {
	case "anthropic":
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			return NewAnthropic(key, os.Getenv("LLM_MODEL"))
		}
		fallthrough
	default:
		return &ExtractiveProvider{}
	}
}

var sentenceSplit = regexp.MustCompile(`(?s)(.*?[.!?])(\s+|$)`)

func splitSentences(text string) []string {
	var out []string
	for _, m := range sentenceSplit.FindAllStringSubmatch(text, -1) {
		s := strings.TrimSpace(m[1])
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

func tokens(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range tokenRe.FindAllString(strings.ToLower(s), -1) {
		set[t] = true
	}
	return set
}

func overlap(a, b map[string]bool) int {
	n := 0
	for t := range a {
		if b[t] {
			n++
		}
	}
	return n
}

// ExtractiveProvider is a deterministic, grounded generator. It picks the source
// sentences most relevant to the query and emits them verbatim with citations, so
// the answer is faithful by construction. This lets the full RAG path (prompt →
// answer → citations → faithfulness) run and be tested without any external API.
type ExtractiveProvider struct{}

func (p *ExtractiveProvider) Name() string { return "extractive-v1" }

type scoredSentence struct {
	text   string
	marker string
	docID  string
	origin string
	score  float64
}

func (p *ExtractiveProvider) Generate(ctx context.Context, query string, sources []Source) (Answer, error) {
	q := contentTokens(query)
	var cands []scoredSentence
	for _, s := range sources {
		for _, sent := range splitSentences(s.Text) {
			ov := overlap(q, contentTokens(sent))
			if ov == 0 {
				continue
			}
			cands = append(cands, scoredSentence{
				text: sent, marker: s.Marker, docID: s.DocID, origin: s.Origin,
				score: float64(ov),
			})
		}
	}
	if len(cands) == 0 {
		return Answer{Text: "Not found in sources.", Model: p.Name()}, nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score == cands[j].score {
			return cands[i].marker < cands[j].marker
		}
		return cands[i].score > cands[j].score
	})
	// Take up to 3 best sentences, dedup by text.
	var parts []string
	seenCite := map[string]Citation{}
	seenText := map[string]bool{}
	for _, c := range cands {
		if len(parts) >= 3 {
			break
		}
		if seenText[c.text] {
			continue
		}
		seenText[c.text] = true
		parts = append(parts, c.text+" ["+c.marker+"]")
		if _, ok := seenCite[c.marker]; !ok {
			seenCite[c.marker] = Citation{Marker: c.marker, DocID: c.docID, Source: c.origin}
		}
	}
	// Deterministic citation order by marker.
	var cites []Citation
	for _, c := range seenCite {
		cites = append(cites, c)
	}
	sort.Slice(cites, func(i, j int) bool { return cites[i].Marker < cites[j].Marker })

	return Answer{Text: strings.Join(parts, " "), Model: p.Name(), Citations: cites}, nil
}

// BuildPrompt renders the grounding contract prompt (used by real LLM providers).
func BuildPrompt(query string, sources []Source) (system, user string) {
	system = "Answer ONLY using the numbered sources. Cite each claim as [doc_N]. " +
		"If the sources do not contain the answer, reply \"Not found in sources.\" " +
		"Do not use outside knowledge. Do not guess."
	var b strings.Builder
	b.WriteString("Sources:\n")
	for _, s := range sources {
		b.WriteString("[" + s.Marker + "] (source=" + s.Origin + ") " + s.Text + "\n")
	}
	b.WriteString("Question: " + query)
	return system, b.String()
}
