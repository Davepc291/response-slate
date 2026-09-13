// Package calltypeinterpret provides offline syntactic interpretation, not dispatch truth.
package calltypeinterpret

import (
	"strings"

	"greenwich-fire-responder/backend/internal/calltypecandidate"
	"greenwich-fire-responder/backend/internal/calltypedata"
)

type State string

const (
	Resolved   State = "resolved"
	Ambiguous  State = "ambiguous"
	Unresolved State = "unresolved"
)

// Evidence includes rejected as well as accepted original candidate metadata.
type Evidence struct {
	calltypecandidate.Candidate
	Accepted           bool
	RejectionReason    string
	AssociatedCallType string
}

// Result owns its collections. Alternatives are distinct accepted types in
// first-evidence order, without priority. CallType is set only when resolved.
type Result struct {
	Transcript   string
	State        State
	CallType     string
	Alternatives []string
	CallTypes    []Evidence
	AlarmLevels  []Evidence
	Qualifiers   []Evidence
}

// Interpreter is immutable and safe for concurrent calls after New.
type Interpreter struct{ extractor *calltypecandidate.Extractor }

func New() (*Interpreter, error) {
	e, err := calltypecandidate.New()
	if err != nil {
		return nil, err
	}
	return &Interpreter{extractor: e}, nil
}

func bounds(s string, start, end int) (int, int) {
	a, b := start, end
	for a > 0 && !strings.ContainsRune(".;!?\r\n", rune(s[a-1])) {
		a--
	}
	for b < len(s) && !strings.ContainsRune(".;!?\r\n", rune(s[b])) {
		b++
	}
	return a, b
}
func plain(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
func cue(s string) bool {
	switch plain(s) {
	case "respond", "respond to", "respond to a":
		return true
	}
	return false
}
func alarm(value string) bool { return value == "ALARM RESD" || value == "ALARM COMM" }

// Interpret uses only candidate extraction for phrase evidence. Unsupported
// grammar is intentionally rejected, rather than guessed from surrounding words.
func (i *Interpreter) Interpret(transcript string) Result {
	r := Result{Transcript: transcript, State: Unresolved}
	candidates := i.extractor.Extract(transcript)
	quoted := strings.ContainsAny(transcript, "\"'\u2018\u2019\u201c\u201d")
	for _, c := range candidates {
		e := Evidence{Candidate: c}
		a, b := bounds(transcript, c.Start, c.End)
		reason := ""
		switch {
		case quoted:
			reason = "quoted_or_apostrophe_text"
		case strings.ContainsAny(c.Evidence, ".;!?\r\n"):
			reason = "cross_clause_evidence"
		default:
			prefix := strings.TrimSpace(transcript[a:c.Start])
			suffix := strings.TrimSpace(transcript[c.End:b])
			switch c.Kind {
			case calltypedata.CallType:
				// Only same-clause trailing qualifiers may follow an alarm phrase.
				if alarm(c.CanonicalValue) {
					for _, q := range candidates {
						if q.Kind != calltypedata.AlarmQualifier || q.Start < c.End || q.End > b {
							continue
						}
						lead := strings.TrimSpace(transcript[c.End:q.Start])
						if lead == "," && strings.TrimSpace(transcript[q.End:b]) == "" {
							suffix = ""
						}
					}
				}
				embedded := strings.HasPrefix(c.Exact, "respond ")
				if !((embedded && prefix == "") || (!embedded && cue(prefix))) {
					reason = "unsupported_dispatch_prefix"
				} else if suffix != "" {
					reason = "unsupported_dispatch_suffix"
				}
			case calltypedata.AlarmLevel:
				if prefix != "" || suffix != "" {
					reason = "unsupported_level_context"
				}
			case calltypedata.AlarmQualifier:
				reason = "no_unambiguous_same_clause_alarm"
			}
		}
		e.RejectionReason = reason
		e.Accepted = reason == ""
		switch c.Kind {
		case calltypedata.CallType:
			r.CallTypes = append(r.CallTypes, e)
			if e.Accepted {
				found := false
				for _, v := range r.Alternatives {
					if v == c.CanonicalValue {
						found = true
					}
				}
				if !found {
					r.Alternatives = append(r.Alternatives, c.CanonicalValue)
				}
			}
		case calltypedata.AlarmLevel:
			r.AlarmLevels = append(r.AlarmLevels, e)
		case calltypedata.AlarmQualifier:
			r.Qualifiers = append(r.Qualifiers, e)
		}
	}
	if len(r.Alternatives) == 1 {
		r.State = Resolved
		r.CallType = r.Alternatives[0]
	}
	if len(r.Alternatives) > 1 {
		r.State = Ambiguous
	}
	// Association cannot resolve a missing or conflicting call type.
	if r.State == Resolved && alarm(r.CallType) && !quoted {
		for n := range r.Qualifiers {
			q := &r.Qualifiers[n]
			if strings.ContainsAny(q.Evidence, ".;!?\r\n") {
				continue
			}
			qa, qb := bounds(transcript, q.Start, q.End)
			for _, c := range r.CallTypes {
				ca, cb := bounds(transcript, c.Start, c.End)
				if c.Accepted && c.CanonicalValue == r.CallType && qa == ca && qb == cb &&
					c.End <= q.Start && strings.TrimSpace(transcript[c.End:q.Start]) == "," &&
					strings.TrimSpace(transcript[q.End:qb]) == "" {
					q.Accepted = true
					q.RejectionReason = ""
					q.AssociatedCallType = r.CallType
				}
			}
		}
	}
	return r
}
