package unitstatus

import "greenwich-fire-responder/backend/internal/unitrecognition"

type Disposition string

const (
	Accepted   Disposition = "accepted"
	Rejected   Disposition = "rejected"
	Ambiguous  Disposition = "ambiguous"
	Unresolved Disposition = "unresolved"
)

// Span indexes the unchanged transcript in UTF-8 bytes, [Start, End).
type Span struct {
	Text       string
	Start, End int
}

type Evidence struct {
	Span
	Canonical   Status
	Disposition Disposition
	Reason      string
}

type UnitEvidence struct {
	unitrecognition.Unit
	Span
}

// Association keeps candidates for audit; Unit is non-nil only when accepted.
// StatusIndex references the separate, source-ordered StatusEvidence slice.
type Association struct {
	StatusIndex int
	Status      Evidence
	Unit        *UnitEvidence
	Candidates  []UnitEvidence
	Clause, Cue Span
	Disposition Disposition
	Reason      string
}

// All slices and pointers are deep caller-owned. State describes associations,
// not status evidence or verified operational state.
type Result struct {
	Transcript     string
	StatusEvidence []Evidence
	Associations   []Association
	State          string
	Reason         string
}

func span(s string, start, end int) Span { return Span{s[start:end], start, end} }

func (r *Result) resolve() {
	statuses := make(map[string]Status)
	conflicts := make(map[string]bool)
	for _, a := range r.Associations {
		if a.Unit == nil {
			continue
		}
		id := a.Unit.CanonicalID
		if prior, ok := statuses[id]; ok && prior != a.Status.Canonical {
			conflicts[id] = true
		}
		statuses[id] = a.Status.Canonical
	}
	accepted := false
	for i := range r.Associations {
		a := &r.Associations[i]
		if a.Unit == nil {
			continue
		}
		if conflicts[a.Unit.CanonicalID] {
			a.Unit = nil
			a.Disposition, a.Reason = Ambiguous, "conflicting_statuses"
		} else {
			accepted = true
		}
	}
	switch {
	case len(conflicts) > 0:
		r.State, r.Reason = "ambiguous", "conflicting_statuses"
	case accepted:
		r.State, r.Reason = "resolved", ""
	default:
		r.State, r.Reason = "unresolved", "no_accepted_association"
	}
}
