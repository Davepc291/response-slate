// Package calltypecandidate extracts vocabulary evidence, not call-type decisions.
package calltypecandidate

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"greenwich-fire-responder/backend/internal/calltypedata"
)

// Candidate preserves catalog metadata and original zero-based byte offsets.
// End is exclusive; transcript[Start:End] is exactly Evidence.
type Candidate struct {
	calltypedata.Phrase
	Evidence string
	Start    int
	End      int
}

type token struct {
	text       string
	start, end int
}
type node struct {
	children map[string]*node
	phrase   *calltypedata.Phrase
}

// Extractor is immutable after New and safe for concurrent extraction.
type Extractor struct{ root node }

// New uses calltypedata as its only vocabulary source. No external I/O occurs.
func New() (*Extractor, error) {
	catalog, err := calltypedata.Load()
	if err != nil {
		return nil, err
	}
	e := &Extractor{}
	for _, phrase := range catalog.Phrases() {
		if err := insert(&e.root, phrase); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func insert(root *node, phrase calltypedata.Phrase) error {
	tokens := tokenize(phrase.Exact)
	if len(tokens) == 0 {
		return fmt.Errorf("calltypecandidate: empty catalog phrase")
	}
	for _, t := range tokens {
		if root.children == nil {
			root.children = make(map[string]*node)
		}
		if root.children[t.text] == nil {
			root.children[t.text] = &node{}
		}
		root = root.children[t.text]
	}
	if root.phrase != nil {
		return fmt.Errorf("calltypecandidate: normalized catalog collision")
	}
	root.phrase = &phrase
	return nil
}

// Extract accepts one transcript and retains no input state. Invalid UTF-8 or
// unsafe control/format characters reject the entire input with no evidence.
// Context flags are metadata only: even quoted, negated, or operational mentions
// can appear here and must not be treated as dispatch truth.
func (e *Extractor) Extract(transcript string) []Candidate {
	if !utf8.ValidString(transcript) || strings.IndexFunc(transcript, unsafe) >= 0 {
		return nil
	}
	tokens := tokenize(transcript)
	var result []Candidate
	for i := range tokens {
		n := &e.root
		var best *calltypedata.Phrase
		end := 0
		for j := i; j < len(tokens); j++ {
			n = n.children[tokens[j].text]
			if n == nil {
				break
			}
			if n.phrase != nil {
				best = n.phrase
				end = tokens[j].end
			}
		}
		if best != nil {
			start := tokens[i].start
			result = append(result, Candidate{Phrase: *best, Evidence: transcript[start:end], Start: start, End: end})
		}
	}
	return result
}

func unsafe(r rune) bool {
	return (unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n') ||
		unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029'
}

// Only ordinary punctuation and spacing delimit tokens. Other characters,
// including marks, underscores and symbols, stay in tokens and cannot be erased
// to manufacture an approved word.
func separator(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n' ||
		strings.ContainsRune(",.;:!?\"'()-\u2018\u2019\u201c\u201d", r)
}

func tokenize(s string) []token {
	var result []token
	start := -1
	for i, r := range s {
		if !separator(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			result = append(result, token{strings.ToLower(s[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		result = append(result, token{strings.ToLower(s[start:]), start, len(s)})
	}
	return result
}
