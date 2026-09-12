// Package addresscandidate extracts supported mentions from one transcript.
// It does not select an incident address or retain transmission state.
package addresscandidate

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"greenwich-fire-responder/backend/internal/addressdata"
)

type Kind string

const (
	Street     Kind = "street"
	AccessRoad Kind = "access road"
)

// Candidate offsets are zero-based UTF-8 byte offsets, with End exclusive.
// CanonicalStreet is the complete original dictionary entry, including the
// embedded house number for the two special access-road entries.
type Candidate struct {
	HouseNumber     string
	CanonicalStreet string
	DictionaryKind  Kind
	Evidence        string
	Start           int
	End             int
}

type entry struct {
	name string
	kind Kind
}
type node struct {
	children map[string]*node
	entries  []entry
}
type token struct {
	text       string
	start, end int
}

// Extractor is immutable after construction and safe for concurrent calls.
type Extractor struct{ streets, access node }

// New loads the existing embedded dictionary. It performs no external I/O.
func New() (*Extractor, error) {
	d, err := addressdata.Load()
	if err != nil {
		return nil, err
	}
	e := &Extractor{}
	for _, name := range d.Streets() {
		insert(&e.streets, name, Street)
	}
	for _, name := range d.AccessRoads() {
		insert(&e.access, name, AccessRoad)
	}
	return e, nil
}

func insert(root *node, name string, kind Kind) {
	tokens := tokenize(name)
	for _, t := range tokens {
		if root.children == nil {
			root.children = map[string]*node{}
		}
		if root.children[t.text] == nil {
			root.children[t.text] = &node{}
		}
		root = root.children[t.text]
	}
	root.entries = append(root.entries, entry{name, kind})
}

// Extract accepts exactly one transcript. It never combines calls, corrects
// names, or chooses a primary candidate. Invalid UTF-8 or unsafe text yields
// no candidates. Original spelling, punctuation, and byte offsets are retained.
func (e *Extractor) Extract(transcript string) []Candidate {
	if !utf8.ValidString(transcript) || strings.IndexFunc(transcript, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') || unicode.In(r, unicode.Cf)
	}) >= 0 {
		return nil
	}
	tokens := tokenize(transcript)
	var result []Candidate
	for i := 0; i < len(tokens); i++ {
		if !house(tokens[i].text) || !supportedNumber(transcript, tokens, i) {
			continue
		}
		var best []entry
		end := -1
		// Canonical street names follow the number; access entries contain it.
		for _, search := range []struct {
			root  *node
			start int
		}{{&e.streets, i + 1}, {&e.access, i}} {
			if search.start >= len(tokens) {
				continue
			}
			if search.start > i && !gap(transcript[tokens[i].end:tokens[search.start].start], false) {
				continue
			}
			n := search.root
			for j := search.start; j < len(tokens); j++ {
				if j > search.start && !gap(transcript[tokens[j-1].end:tokens[j].start], j > i+1) {
					break
				}
				n = n.children[tokens[j].text]
				if n == nil {
					break
				}
				if len(n.entries) > 0 {
					if j > end {
						end = j
						best = n.entries
					} else if j == end {
						best = append(append([]entry{}, best...), n.entries...)
					}
				}
			}
		}
		// A normalization collision at the longest match is not resolved by guess.
		if len(best) != 1 {
			continue
		}
		start, finish := tokens[i].start, tokens[end].end
		result = append(result, Candidate{tokens[i].text, best[0].name, best[0].kind, transcript[start:finish], start, finish})
		i = end
	}
	return result
}

func house(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func supportedNumber(text string, tokens []token, i int) bool {
	t := tokens[i]
	// Reject signed numbers and a trailing component of ranges, fractions,
	// decimals, comma-grouped numbers, or consecutive numeric tokens.
	if t.start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:t.start])
		if strings.ContainsRune("+-/−–—", r) {
			return false
		}
	}
	if i > 0 && house(tokens[i-1].text) {
		separator := text[tokens[i-1].end:t.start]
		if !strings.ContainsAny(separator, "\n\r!?;") {
			return false
		}
	}
	return true
}

// Commas, quotes and parentheses are ordinary separators. Sentence punctuation,
// newlines, symbols, and number-to-name hyphens are conservative barriers.
// A hyphen between street-name words is permitted (e.g. Doubling-Road).
func gap(s string, withinStreet bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || strings.ContainsRune(",\"'()“”‘’", r) {
			continue
		}
		if withinStreet && r == '-' {
			continue
		}
		return false
	}
	return true
}

func tokenize(text string) []token {
	var out []token
	start := -1
	for i, r := range text {
		// Keeping marks and numbers inside tokens prevents substring matches
		// inside alphanumeric words or visually similar accented spellings.
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, token{strings.ToLower(text[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, token{strings.ToLower(text[start:]), start, len(text)})
	}
	return out
}
