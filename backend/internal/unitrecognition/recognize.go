package unitrecognition

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type State string

const (
	Resolved   State = "resolved"
	Ambiguous  State = "ambiguous"
	Unresolved State = "unresolved"
)

// Mention retains accepted or rejected supported evidence. Unit is zero for
// conflicting identities; Alternatives then preserves the conflicting metadata.
type Mention struct {
	Unit
	Evidence     string
	Start, End   int
	Accepted     bool
	Reason       string
	Alternatives []Unit
}
type Result struct {
	Transcript string
	State      State
	Reason     string
	Mentions   []Mention
}
type token struct {
	text       string
	start, end int
}
type node struct {
	next  map[string]*node
	units []Unit
}

// Matcher is immutable after New and safe for concurrent Recognize calls.
type Matcher struct {
	units []Unit
	root  node
}

func New() (*Matcher, error) { return build(approved()) }
func build(units []Unit) (*Matcher, error) {
	if err := validate(units); err != nil {
		return nil, err
	}
	m := &Matcher{units: append([]Unit(nil), units...)}
	for _, u := range units {
		m.insert(u)
	}
	return m, nil
}
func (m *Matcher) insert(u Unit) {
	n := &m.root
	for _, t := range tokens(u.Phrase) {
		if n.next == nil {
			n.next = map[string]*node{}
		}
		if n.next[t.text] == nil {
			n.next[t.text] = &node{}
		}
		n = n.next[t.text]
	}
	n.units = append(n.units, u)
}
func unsafe(r rune) bool {
	return unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n' || unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029'
}
func delimiter(r rune) bool {
	return strings.ContainsRune(" \t,.;:-()\r\n\"'\u2018\u2019\u201c\u201d", r)
}
func tokens(s string) []token {
	var out []token
	start := -1
	for i, r := range s {
		if !delimiter(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, token{strings.ToLower(s[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, token{strings.ToLower(s[start:]), start, len(s)})
	}
	return out
}
func gap(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune(" \t,.;:-", r) }) < 0
}

// Recognize accepts exactly one unchanged transcript. A result establishes
// mentions only, never dispatch, assignment, sender identity, status, or an event.
func (m *Matcher) Recognize(s string) Result {
	r := Result{Transcript: s, State: Unresolved, Reason: "no_supported_evidence"}
	if !utf8.ValidString(s) || strings.IndexFunc(s, unsafe) >= 0 {
		r.Reason = "invalid_utf8_or_unsafe_controls"
		return r
	}
	ts := tokens(s)
	for i := range ts {
		n := &m.root
		var best []Unit
		end := 0
		for j := i; j < len(ts); j++ {
			if j > i && !gap(s[ts[j-1].end:ts[j].start]) {
				break
			}
			n = n.next[ts[j].text]
			if n == nil {
				break
			}
			if len(n.units) > 0 {
				best = n.units
				end = ts[j].end
			}
		}
		if len(best) == 0 {
			continue
		}
		v := Mention{Evidence: s[ts[i].start:end], Start: ts[i].start, End: end}
		if len(best) == 1 {
			v.Unit = best[0]
		} else {
			v.Alternatives = append([]Unit(nil), best...)
			v.Reason = "conflicting_identities"
		}
		r.Mentions = append(r.Mentions, v)
	}
	quoted := strings.ContainsAny(s, "\"'\u2018\u2019\u201c\u201d")
	// Per-line context accepts lists of complete mentions and approved separators
	// only. Removing spans is solely a context check, not a second phrase matcher.
	for k := range r.Mentions {
		v := &r.Mentions[k]
		if quoted {
			v.Reason = "quoted_or_apostrophe_text"
			continue
		}
		a, b := v.Start, v.End
		for a > 0 && s[a-1] != '\n' && s[a-1] != '\r' {
			a--
		}
		for b < len(s) && s[b] != '\n' && s[b] != '\r' {
			b++
		}
		cursor := a
		depth := 0
		valid := true
		check := func(part string) {
			for _, c := range part {
				switch c {
				case '(':
					depth++
					if depth > 1 {
						valid = false
					}
				case ')':
					depth--
					if depth < 0 {
						valid = false
					}
				default:
					if !strings.ContainsRune(" \t,.;:", c) {
						valid = false
					}
				}
			}
		}
		for _, other := range r.Mentions {
			if other.Start < a || other.End > b {
				continue
			}
			if other.Start < cursor {
				continue
			}
			check(s[cursor:other.Start])
			// Parentheses can enclose one complete mention, not a list.
			if depth == 1 {
				tail := strings.TrimLeft(s[other.End:b], " \t")
				if !strings.HasPrefix(tail, ")") {
					valid = false
				}
			}
			cursor = other.End
		}
		check(s[cursor:b])
		if !valid || depth != 0 {
			v.Reason = "unsupported_context"
			continue
		}
		if v.Reason == "conflicting_identities" {
			continue
		}
		v.Accepted = true
	}
	accepted, conflict := false, false
	for _, v := range r.Mentions {
		accepted = accepted || v.Accepted
		conflict = conflict || v.Reason == "conflicting_identities"
	}
	switch {
	case conflict:
		r.State = Ambiguous
		r.Reason = "conflicting_identities"
	case accepted:
		r.State = Resolved
		r.Reason = ""
	case len(r.Mentions) > 0:
		r.Reason = "all_supported_evidence_rejected"
	}
	return r
}
