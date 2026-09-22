package rag

import "regexp"

var citationMarker = regexp.MustCompile(`\[doc_\d+\]`)

// FaithfulnessResult reports how much of an answer is grounded in the sources.
type FaithfulnessResult struct {
	Score       float64  `json:"score"`
	Unsupported []string `json:"unsupported,omitempty"`
}

// Faithfulness scores an answer by the fraction of its sentences that are supported
// by at least one source chunk. A sentence is "supported" if a meaningful fraction
// of its content tokens appear in some source. Citation markers are stripped first.
//
// This is a deterministic NLI-lite check; a real deployment can swap in an LLM/NLI
// judge behind the same signature.
func Faithfulness(answer string, sources []Source) FaithfulnessResult {
	clean := citationMarker.ReplaceAllString(answer, "")
	sentences := splitSentences(clean)
	if len(sentences) == 0 {
		return FaithfulnessResult{Score: 1.0}
	}
	// "Not found" style abstentions are trivially faithful.
	srcToks := make([]map[string]bool, len(sources))
	for i, s := range sources {
		srcToks[i] = tokens(s.Text)
	}

	supported := 0
	var unsupported []string
	for _, sent := range sentences {
		st := contentTokens(sent)
		if len(st) == 0 {
			supported++ // no content to contradict
			continue
		}
		best := 0.0
		for _, src := range srcToks {
			ov := overlap(st, src)
			frac := float64(ov) / float64(len(st))
			if frac > best {
				best = frac
			}
		}
		if best >= 0.6 { // >=60% of content tokens found in a single source
			supported++
		} else {
			unsupported = append(unsupported, sent)
		}
	}
	return FaithfulnessResult{
		Score:       float64(supported) / float64(len(sentences)),
		Unsupported: unsupported,
	}
}

// contentTokens drops very common stopwords so scoring reflects substantive overlap.
func contentTokens(s string) map[string]bool {
	set := tokens(s)
	for w := range set {
		if stopwords[w] {
			delete(set, w)
		}
	}
	return set
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
	"to": true, "in": true, "is": true, "are": true, "it": true, "that": true,
	"this": true, "for": true, "on": true, "with": true, "as": true, "by": true,
	"be": true, "at": true, "from": true, "into": true, "so": true, "not": true,
}
