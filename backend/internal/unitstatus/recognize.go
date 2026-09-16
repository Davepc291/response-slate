package unitstatus

import "strings"

// Recognize accepts one unchanged transcript. Evidence acceptance means a
// supported phrase was found; only the separate association grammar proposes
// a unit/status pair. Neither result establishes operational truth.
func (m *Matcher) Recognize(s string) Result {
	r := Result{Transcript: s, State: "unresolved", Reason: "invalid_utf8_or_unsafe_controls"}
	if !valid(s) {
		return r
	}
	r.StatusEvidence = statusEvidence(s)
	quoted := strings.ContainsAny(s, "\"'\u2018\u2019\u201c\u201d")
	for _, c := range clauses(s) {
		var units []UnitEvidence
		matchedUnits := false
		for i := range r.StatusEvidence {
			e := &r.StatusEvidence[i]
			if e.Start < c.Start || e.End > contentEnd(c) {
				continue
			}
			if !matchedUnits {
				units = m.unitEvidence(s, c)
				matchedUnits = true
			}
			role := ""
			if quoted {
				role = "quoted_or_apostrophe_text"
			} else if strings.ContainsRune(c.Text, '?') {
				role = "question"
			}
			if role != "" && e.Canonical != "" {
				e.Disposition, e.Reason = Rejected, role
			}
			for _, a := range m.associate(s, c, *e, units) {
				a.StatusIndex = i
				r.Associations = append(r.Associations, a)
			}
		}
	}
	r.resolve()
	return r
}

func (m *Matcher) associate(s string, c Span, e Evidence, units []UnitEvidence) []Association {
	a := Association{Status: e, Clause: c, Candidates: append([]UnitEvidence(nil), units...), Disposition: Unresolved}
	switch {
	case e.Disposition != Accepted:
		a.Disposition, a.Reason = e.Disposition, e.Reason
	case len(units) == 0:
		a.Reason = "missing_unit"
	default:
		// Overlapping candidates cannot select one unique catalog identity.
		for i := 1; i < len(units); i++ {
			if units[i].Start < units[i-1].End {
				a.Disposition, a.Reason = Ambiguous, "conflicting_unit_identities"
				return []Association{a}
			}
		}
		last := units[len(units)-1]
		if last.End > e.Start || strings.Trim(s[c.Start:units[0].Start], " \t") != "" || strings.Trim(s[e.End:contentEnd(c)], " \t") != "" {
			a.Disposition, a.Reason = Rejected, "unsupported_context"
			return []Association{a}
		}
		// Only explicit same-clause coordination applies one status to a list.
		for i := 1; i < len(units); i++ {
			if asciiLower(strings.Trim(s[units[i-1].End:units[i].Start], " \t")) != "and" {
				a.Disposition, a.Reason = Rejected, "unsupported_coordination"
				return []Association{a}
			}
		}
		a.Cue = span(s, last.End, e.Start)
		if !link(a.Cue.Text, len(units) > 1) {
			a.Disposition, a.Reason = Rejected, "unsupported_context"
			return []Association{a}
		}
		a.Disposition, a.Reason = Accepted, "same_clause_single_unit"
		if len(units) > 1 {
			a.Reason = "same_clause_coordinated_units"
		}
		var out []Association
		for _, u := range units {
			item := a
			item.Unit = &u
			item.Candidates = append([]UnitEvidence(nil), units...)
			out = append(out, item)
		}
		return out
	}
	return []Association{a}
}

// One optional comma may precede the status/linker. Singular is and plural are
// must agree with the unit or coordinated list; extra text is rejected.
func link(s string, coordinated bool) bool {
	if s == "" {
		return false
	}
	s = asciiLower(strings.Trim(s, " \t"))
	if strings.HasPrefix(s, ",") {
		s = strings.Trim(s[1:], " \t")
	}
	linker := "is"
	if coordinated {
		linker = "are"
	}
	return s == "" || s == linker
}
