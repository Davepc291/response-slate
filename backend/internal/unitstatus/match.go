package unitstatus

import (
	"strings"
	"unicode/utf8"
)

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func valid(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 && c != '\t' && c != '\r' && c != '\n' {
			return false
		}
	}
	return true
}

// Only explicit separators delimit tokens. Hyphens, digits, Unicode letters,
// combining marks and symbols cannot manufacture a shorter phrase match.
func boundary(c rune) bool {
	return strings.ContainsRune(" \t\r\n,.;:!?()\"'\u2018\u2019\u201c\u201d", c)
}
func horizontal(c byte) bool { return c == ' ' || c == '\t' }

// matchAt compares ASCII literally, allowing only horizontal whitespace between
// phrase words. It returns original offsets without rewriting the transcript.
func matchAt(s string, start int, phrase string) (int, bool) {
	if start > 0 {
		c, _ := utf8.DecodeLastRuneInString(s[:start])
		if !boundary(c) {
			return 0, false
		}
	}
	i := start
	for n, word := range strings.Split(asciiLower(phrase), " ") {
		if n > 0 {
			before := i
			for i < len(s) && horizontal(s[i]) {
				i++
			}
			if before == i {
				return 0, false
			}
		}
		if len(s)-i < len(word) || asciiLower(s[i:i+len(word)]) != word {
			return 0, false
		}
		i += len(word)
	}
	if i < len(s) {
		c, _ := utf8.DecodeRuneInString(s[i:])
		if !boundary(c) {
			return 0, false
		}
	}
	return i, true
}

func statusEvidence(s string) []Evidence {
	var out []Evidence
	for start := range s {
		best := Evidence{}
		for _, p := range phrases() {
			end, ok := matchAt(s, start, p.text)
			if !ok || end <= best.End {
				continue
			}
			best = Evidence{Span: span(s, start, end), Canonical: p.status, Disposition: Accepted, Reason: "supported_phrase"}
			if p.status == "" {
				best.Disposition, best.Reason = Unresolved, "clear_unresolved"
			}
		}
		if best.End > start {
			out = append(out, best)
		}
	}
	return out
}

// clauses keeps adjacent terminal punctuation together so ...?, !? and ?!
// retain question meaning. CR/LF still end scope independently.
func clauses(s string) []Span {
	var out []Span
	start := 0
	for i := 0; i < len(s); i++ {
		if strings.ContainsRune(".;:!?\r\n", rune(s[i])) {
			end := i + 1
			if s[i] != '\r' && s[i] != '\n' {
				for end < len(s) && strings.ContainsRune(".;:!?", rune(s[end])) {
					end++
				}
			}
			out = append(out, span(s, start, end))
			start, i = end, end-1
		}
	}
	if start < len(s) {
		out = append(out, span(s, start, len(s)))
	}
	return out
}

func contentEnd(c Span) int {
	end := c.End
	for end > c.Start && strings.ContainsRune(".;:!?\r\n", rune(c.Text[end-c.Start-1])) {
		end--
	}
	return end
}

func (m *Matcher) unitEvidence(s string, c Span) []UnitEvidence {
	var out []UnitEvidence
	for offset := range s[c.Start:contentEnd(c)] {
		start := c.Start + offset
		var best []UnitEvidence
		maxEnd := 0
		for _, u := range m.units {
			end, ok := matchAt(s, start, u.Phrase)
			if !ok || end > contentEnd(c) || end < maxEnd {
				continue
			}
			if end > maxEnd {
				best = nil
				maxEnd = end
			}
			best = append(best, UnitEvidence{u, span(s, start, end)})
		}
		out = append(out, best...)
	}
	return out
}
