package rag

import "regexp"

var markerRe = regexp.MustCompile(`doc_(\d+)`)

// citationsFromText maps [doc_N] markers found in a free-text answer back to sources.
// Shared by any provider that returns free-text with markers (e.g. LiteLLM).
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
