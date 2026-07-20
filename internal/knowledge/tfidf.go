// Package knowledge provides lightweight, dependency-free text analytics
// (currently TF-IDF term ranking) used by the context explorer.
package knowledge

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Term is a scored term produced by TopTerms.
type Term struct {
	Term   string  `json:"term"`
	Weight float64 `json:"weight"`
	Count  int     `json:"count"`
}

// minTokenLen is the shortest token length considered meaningful; shorter
// tokens (e.g. "a", "an", "is") are dropped before scoring.
const minTokenLen = 3

// stopwords is a generic English stopword list applied on top of the
// minTokenLen filter, so short function words as well as longer but
// low-signal words are excluded from term ranking.
var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "you": {}, "your": {}, "with": {}, "that": {}, "this": {},
	"are": {}, "was": {}, "from": {}, "have": {}, "has": {}, "not": {}, "but": {}, "can": {},
	"will": {}, "would": {}, "they": {}, "their": {}, "what": {}, "when": {}, "which": {},
	"there": {}, "here": {}, "been": {}, "were": {}, "into": {}, "over": {}, "then": {}, "than": {},
	"them": {}, "some": {}, "such": {}, "only": {}, "also": {}, "all": {}, "any": {}, "our": {},
	"out": {}, "who": {}, "how": {}, "its": {}, "his": {}, "her": {}, "she": {}, "him": {},
	"one": {}, "two": {}, "get": {}, "got": {}, "just": {}, "like": {}, "did": {}, "does": {},
	"doing": {}, "each": {}, "few": {}, "more": {}, "most": {}, "other": {}, "same": {}, "own": {},
	"had": {}, "having": {}, "because": {}, "while": {}, "these": {}, "those": {}, "about": {},
	"above": {}, "after": {}, "again": {}, "against": {}, "before": {}, "below": {}, "between": {},
	"both": {}, "during": {}, "further": {}, "off": {}, "once": {}, "under": {}, "until": {},
	"where": {}, "why": {}, "should": {}, "now": {}, "very": {}, "too": {}, "nor": {}, "yes": {},
}

// tokenize lowercases s, splits it into runs of letters (dropping digits and
// punctuation as separators), and filters out short tokens and stopwords.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
	out := make([]string, 0, len(fields))
	for _, t := range fields {
		if len(t) < minTokenLen {
			continue
		}
		if _, stop := stopwords[t]; stop {
			continue
		}
		out = append(out, t)
	}
	return out
}

// TopTerms ranks terms by summed TF-IDF across chunks (each chunk is treated
// as one document for IDF purposes), returning at most n terms. Ranking is
// deterministic: ties break by higher Count, then by term ascending.
func TopTerms(chunks []string, n int) []Term {
	if len(chunks) == 0 || n <= 0 {
		return nil
	}

	df := map[string]int{}
	tf := map[string]int{}
	seen := map[string]bool{}
	for _, c := range chunks {
		clear(seen)
		for _, tok := range tokenize(c) {
			tf[tok]++
			if !seen[tok] {
				seen[tok] = true
				df[tok]++
			}
		}
	}

	docCount := float64(len(chunks))
	terms := make([]Term, 0, len(tf))
	for term, count := range tf {
		// +1 smoothing so a term appearing in every chunk still ranks > 0.
		idf := math.Log(docCount/float64(df[term])) + 1
		terms = append(terms, Term{Term: term, Weight: float64(count) * idf, Count: count})
	}

	sort.Slice(terms, func(i, j int) bool {
		if terms[i].Weight != terms[j].Weight {
			return terms[i].Weight > terms[j].Weight
		}
		if terms[i].Count != terms[j].Count {
			return terms[i].Count > terms[j].Count
		}
		return terms[i].Term < terms[j].Term
	})

	if len(terms) > n {
		terms = terms[:n]
	}
	return terms
}
