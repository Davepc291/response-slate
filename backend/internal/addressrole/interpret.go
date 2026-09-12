// Package addressrole assigns conservative roles to exact evidence in one
// transcript. It neither retains transmission state nor creates incidents.
package addressrole

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"greenwich-fire-responder/backend/internal/addresscandidate"
	"greenwich-fire-responder/backend/internal/addressdata"
)

type State string

const (
	NoEvidence  State = "no evidence"
	Resolved    State = "resolved"
	Ambiguous   State = "ambiguous"
	Unsupported State = "unsupported"
)

// Evidence uses original UTF-8 byte offsets, Start inclusive and End exclusive.
type Evidence struct {
	Text       string
	Start, End int
}
type PrimaryMention struct {
	Address addresscandidate.Candidate
	Cue     Evidence
}
type Primary struct {
	HouseNumber, CanonicalStreet string
	DictionaryKind               addresscandidate.Kind
	Mentions                     []PrimaryMention
}
type CrossStreet struct {
	CanonicalStreet string
	Mentions        []Evidence
	Cues            []Evidence
}
type Result struct {
	State   State
	Primary *Primary
	// Alternatives contains all distinct supported addresses when ambiguous.
	Alternatives []Primary
	CrossStreets []CrossStreet
	Unresolved   []addresscandidate.Candidate
	Reason       string
}
type token struct {
	word       string
	start, end int
}
type streetNode struct {
	next  map[string]*streetNode
	names []string
}
type Interpreter struct {
	candidates *addresscandidate.Extractor
	streets    streetNode
}

func New() (*Interpreter, error) {
	d, e := addressdata.Load()
	if e != nil {
		return nil, e
	}
	c, e := addresscandidate.New()
	if e != nil {
		return nil, e
	}
	in := &Interpreter{candidates: c}
	for _, name := range d.Streets() {
		n := &in.streets
		for _, t := range tokenize(name) {
			if n.next == nil {
				n.next = map[string]*streetNode{}
			}
			if n.next[t.word] == nil {
				n.next[t.word] = &streetNode{}
			}
			n = n.next[t.word]
		}
		n.names = append(n.names, name)
	}
	return in, nil
}

func (in *Interpreter) Interpret(text string) Result {
	r := Result{State: NoEvidence}
	if !utf8.ValidString(text) || strings.IndexFunc(text, func(c rune) bool {
		return unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' || unicode.In(c, unicode.Cf)
	}) >= 0 {
		r.State = Unsupported
		r.Reason = "invalid transcript encoding or unsafe controls"
		return r
	}
	ts := tokenize(text)
	var primaries []Primary
	candidates := in.candidates.Extract(text)
	for _, candidate := range candidates {
		cue, ok := dispatchCue(text, ts, candidate.Start)
		if !ok {
			r.Unresolved = append(r.Unresolved, candidate)
			continue
		}
		index := -1
		for i, p := range primaries {
			if p.HouseNumber == candidate.HouseNumber && p.CanonicalStreet == candidate.CanonicalStreet && p.DictionaryKind == candidate.DictionaryKind {
				index = i
				break
			}
		}
		if index < 0 {
			primaries = append(primaries, Primary{HouseNumber: candidate.HouseNumber, CanonicalStreet: candidate.CanonicalStreet, DictionaryKind: candidate.DictionaryKind})
			index = len(primaries) - 1
		}
		primaries[index].Mentions = append(primaries[index].Mentions, PrimaryMention{candidate, cue})
	}
	cueSeen := false
	for i, t := range ts {
		if t.word == "respond" && clauseStart(text, ts, i) {
			cueSeen = true
		}
	}
	for i := range ts {
		// Cross patterns must begin a clause. This rejects quoted/negated/reported
		// patterns preceded by conversational words without trying to infer intent.
		allowed := clauseStart(text, ts, i)
		if !allowed && i > 0 {
			for _, c := range candidates {
				if c.End == ts[i-1].end && separator(text[c.End:ts[i].start], false) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			continue
		}
		length, pair := 0, false
		for _, pattern := range []struct {
			words []string
			pair  bool
		}{
			{[]string{"cross", "street", "is"}, false}, {[]string{"cross", "streets", "are"}, true}, {[]string{"runs", "between"}, true},
		} {
			if matches(text, ts, i, pattern.words) {
				length = len(pattern.words)
				pair = pattern.pair
				break
			}
		}
		if length == 0 {
			continue
		}
		cueSeen = true
		start := i + length
		if start >= len(ts) || !separator(text[ts[start-1].end:ts[start].start], false) {
			continue
		}
		name, end := in.street(text, ts, start)
		if name == "" {
			continue
		}
		type mention struct {
			name       string
			start, end int
		}
		found := []mention{{name, start, end}}
		if pair {
			and := end + 1
			if and+1 >= len(ts) || ts[and].word != "and" || !separator(text[ts[end].end:ts[and].start], false) || !separator(text[ts[and].end:ts[and+1].start], false) {
				continue
			}
			other, last := in.street(text, ts, and+1)
			if other == "" {
				continue
			}
			found = append(found, mention{other, and + 1, last})
		}
		cue := evidence(text, ts[i].start, ts[start-1].end)
		for _, m := range found {
			index := -1
			for j, c := range r.CrossStreets {
				if c.CanonicalStreet == m.name {
					index = j
					break
				}
			}
			if index < 0 {
				r.CrossStreets = append(r.CrossStreets, CrossStreet{CanonicalStreet: m.name})
				index = len(r.CrossStreets) - 1
			}
			r.CrossStreets[index].Mentions = append(r.CrossStreets[index].Mentions, evidence(text, ts[m.start].start, ts[m.end].end))
			r.CrossStreets[index].Cues = append(r.CrossStreets[index].Cues, cue)
		}
	}
	switch len(primaries) {
	case 0:
		if len(r.Unresolved) > 0 || len(r.CrossStreets) > 0 || cueSeen {
			r.State = Unsupported
			r.Reason = "no supported primary address"
		}
	case 1:
		r.State = Resolved
		r.Primary = &primaries[0]
	default:
		r.State = Ambiguous
		r.Alternatives = primaries
		r.Reason = "different explicitly supported primary addresses"
	}
	return r
}

func dispatchCue(text string, ts []token, addressStart int) (Evidence, bool) {
	end := 0
	for end < len(ts) && ts[end].start < addressStart {
		end++
	}
	for i := end - 1; i >= 0; i-- {
		if ts[i].word != "respond" {
			continue
		}
		if !clauseStart(text, ts, i) {
			// Also allow an explicit Engine <digits> addressee without a comma.
			if i < 2 || ts[i-2].word != "engine" || !digits(ts[i-1].word) || !clauseStart(text, ts, i-2) || !matches(text, ts, i-2, []string{"engine", ts[i-1].word, "respond"}) {
				continue
			}
		}
		words := []string{}
		valid := true
		for j := i + 1; j < end; j++ {
			if !separator(text[ts[j-1].end:ts[j].start], false) {
				valid = false
			}
			words = append(words, ts[j].word)
		}
		bridge := strings.Join(words, " ")
		if !valid || (bridge != "" && bridge != "to" && bridge != "to a residential alarm at" && bridge != "to residential alarm at") {
			continue
		}
		if end >= len(ts) || !separator(text[ts[end-1].end:addressStart], false) {
			continue
		}
		return evidence(text, ts[i].start, ts[end-1].end), true
	}
	return Evidence{}, false
}
func (in *Interpreter) street(text string, ts []token, start int) (string, int) {
	n := &in.streets
	var names []string
	end := -1
	for i := start; i < len(ts); i++ {
		if i > start && !separator(text[ts[i-1].end:ts[i].start], true) {
			break
		}
		n = n.next[ts[i].word]
		if n == nil {
			break
		}
		if len(n.names) > 0 {
			names = n.names
			end = i
		}
	}
	if len(names) != 1 {
		return "", -1
	}
	return names[0], end
}
func evidence(text string, start, end int) Evidence { return Evidence{text[start:end], start, end} }
func digits(s string) bool {
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
func clauseStart(text string, ts []token, i int) bool {
	if i == 0 {
		return true
	}
	return strings.ContainsAny(text[ts[i-1].end:ts[i].start], ",.;:!?\r\n\u2028\u2029")
}
func separator(s string, hyphen bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || strings.ContainsRune("\"'()“”‘’", r) || hyphen && r == '-' {
			continue
		}
		return false
	}
	return true
}
func matches(text string, ts []token, start int, words []string) bool {
	if start+len(words) > len(ts) {
		return false
	}
	for j, w := range words {
		if ts[start+j].word != w || j > 0 && !separator(text[ts[start+j-1].end:ts[start+j].start], false) {
			return false
		}
	}
	return true
}
func tokenize(text string) []token {
	var ts []token
	start := -1
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			ts = append(ts, token{strings.ToLower(text[start:i]), start, i})
			start = -1
		}
	}
	if start >= 0 {
		ts = append(ts, token{strings.ToLower(text[start:]), start, len(text)})
	}
	return ts
}
