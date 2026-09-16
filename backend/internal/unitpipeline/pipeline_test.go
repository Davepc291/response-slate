package unitpipeline

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/unitrecognition"
	"greenwich-fire-responder/backend/internal/unitstatus"
)

func setup(t *testing.T) *Pipeline {
	t.Helper()
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConstructorFailures(t *testing.T) {
	want := errors.New("synthetic construction failure")
	for _, failUnits := range []bool{true, false} {
		var calls []string
		p, err := newPipeline(func() (*unitrecognition.Matcher, error) {
			calls = append(calls, "units")
			if failUnits {
				return nil, want
			}
			return unitrecognition.New()
		}, func() (*unitstatus.Matcher, error) {
			calls = append(calls, "status")
			return nil, want
		})
		if p != nil || err != want {
			t.Fatalf("partial pipeline or changed error: %v %v", p, err)
		}
		expected := []string{"units"}
		if !failUnits {
			expected = append(expected, "status")
		}
		if !reflect.DeepEqual(calls, expected) {
			t.Fatalf("constructor order: %v", calls)
		}
	}
}

func TestNativeCatalogParity(t *testing.T) {
	p := setup(t)
	u, err := unitrecognition.New()
	if err != nil {
		t.Fatal(err)
	}
	s, err := unitstatus.New()
	if err != nil {
		t.Fatal(err)
	}
	catalog := u.Catalog()
	if len(catalog) != 26 {
		t.Fatal("unit contract changed")
	}
	// The unit catalog is the source of truth, not another maintained unit list.
	for _, unit := range catalog {
		for _, text := range []string{unit.Phrase, strings.ToUpper(unit.Phrase), unit.Phrase + " on scene"} {
			checkParity(t, p, u, s, text)
		}
	}
	// Inputs only: canonical mapping and recognition remain native responsibilities.
	phrases := []string{"dispatched", "responding", "en route", "in route", "enroute", "on the way",
		"on scene", "on location", "arrived", "10-23", "returning", "returning to quarters", "RTQ",
		"back to quarters", "available", "in service", "back in service", "out of service", "on air", "training"}
	for _, phrase := range phrases {
		for _, text := range []string{phrase, "engine 2 " + phrase, "engine 2 and engine 5 " + phrase, "ENGINE\t2, " + strings.ToUpper(strings.ReplaceAll(phrase, " ", "\t"))} {
			checkParity(t, p, u, s, text)
		}
	}
}

func fixtures() []struct{ name, text string } {
	return []struct{ name, text string }{
		{"empty", ""}, {"partial unit", "engine"}, {"unit only", "engine 2"}, {"status only", "on scene"},
		{"missing unit", "engine 20 on scene"}, {"clear", "engine 2 CLEAR"},
		{"disagreement", "engine 2 on scene"}, {"distinct statuses", "engine 2 on air; engine 5 training"},
		{"coordinated", "engine 2 and engine 5 are on scene"}, {"three units", "engine 2 and engine 5 and squad 8, are training"},
		{"repeated unit", "engine 2 and engine 2 on scene"}, {"comma list", "engine 2, engine 5 on scene"},
		{"alternatives", "engine 2 or engine 5 on scene"}, {"unknown list member", "engine 2 and engine 20 on scene"},
		{"wrong linker", "engine 2 and engine 5 is on scene"},
		{"conflict", "engine 2 responding; engine 2 on scene"},
		{"conflict and independent unit", "engine 2 and engine 5 on scene; engine 2 responding"},
		{"repeat", "engine 2 on scene; engine 2 on scene"},
		{"question", "engine 2 on scene?"}, {"ellipsis question", "engine 2 on scene...?"},
		{"exclamation question", "engine 2 and engine 5 on scene!?"}, {"question exclamation", "engine 2 on scene?!"},
		{"mixed roles", "engine 2 on scene!? engine 5 responding."},
		{"negation", "engine 2 not on scene"}, {"command", "engine 2 return to quarters"},
		{"command with status evidence", "engine 2 report on scene"}, {"acknowledgement", "engine 2 good copy on scene"},
		{"history", "yesterday engine 2 on scene"}, {"hypothetical", "if engine 2 is available"},
		{"quotation", "\"engine 2 on scene\""}, {"curly quotation", "\u201cengine 2 on scene\u201d"},
		{"unmatched quotation", "\"engine 2 on scene; engine 5 responding"},
		{"discussion", "we discussed engine 2 on scene"}, {"unsupported exclusion", "engine 2 last unit on scene"},
		{"longest", "engine 2 returning to quarters"}, {"nested", "engine 2 and engine 5 back in service"},
		{"numeric collisions", "engine 21 and engine 51 on scene; tanker 17 and rescue 51 responding"},
		{"suffix", "engine 2V on scene"}, {"code boundary", "engine 2 10-230"},
		{"machine aliases", "E2 EN_ROUTE ON_SCENE ACTIVE"}, {"unit punctuation", "engine-2"},
		{"unit punctuation in status clause", "engine-2 on scene"}, {"status punctuation", "engine 2 on,scene"},
		{"symbol", "engine + 2 on scene"}, {"number word", "engine two on scene"},
		{"metadata is not identity", "RID 123 TGID 57201 CH1A on scene"},
		{"document multibyte", "\u00e9; engine 2 on scene"}, {"emoji", "\U0001f692; ENGINE\t2, ON\tSCENE"},
		{"different control policy", "\u200b; engine 2 on scene"},
		{"different line context", "yesterday engine 2\nengine 5 on scene"},
	}
}

func TestFailureCaseParity(t *testing.T) {
	p := setup(t)
	u, _ := unitrecognition.New()
	s, _ := unitstatus.New()
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) { checkParity(t, p, u, s, f.text) })
	}
	for _, boundary := range []string{".", ";", ":", "!", "?", "\r", "\n", "\r\n"} {
		checkParity(t, p, u, s, "engine 2"+boundary+"on scene")
		checkParity(t, p, u, s, "engine 2 and engine 5"+boundary+"on scene")
		checkParity(t, p, u, s, "engine 2 on scene"+boundary+"engine 5 responding")
	}
	bad := []string{"\xff", "\xc0\xaf", "\xed\xa0\x80", "\x7f", "\u0085", "\ufeff", "\u200b", "\u200d", "\u202e", "\u2066", "\u2028", "\u2029"}
	for c := byte(0); c < 32; c++ {
		if c != '\t' && c != '\r' && c != '\n' {
			bad = append(bad, string([]byte{c}))
		}
	}
	for _, control := range bad {
		checkParity(t, p, u, s, control+"; engine 2 on scene")
	}
}

func TestDisagreementAndDocumentOffsets(t *testing.T) {
	p := setup(t)
	r := p.Process("engine 2 on scene")
	if r.Units.State != unitrecognition.Unresolved || r.Units.Mentions[0].Accepted || r.Units.Mentions[0].Reason != "unsupported_context" || r.UnitStatus.Associations[0].Unit == nil {
		t.Fatalf("native disagreement lost: %+v", r)
	}
	for _, tc := range []struct {
		text           string
		us, ue, ss, se int
	}{
		{"engine 2 on scene", 0, 8, 9, 17}, {"\u00e9; engine 2 on scene", 4, 12, 13, 21},
	} {
		r := p.Process(tc.text)
		u, a := r.Units.Mentions[0], r.UnitStatus.Associations[0]
		if u.Start != tc.us || u.End != tc.ue || a.Unit.Start != tc.us || a.Unit.End != tc.ue || a.Status.Start != tc.ss || a.Status.End != tc.se {
			t.Fatalf("offsets changed: %+v", r)
		}
	}
}

func checkParity(t *testing.T, p *Pipeline, u *unitrecognition.Matcher, s *unitstatus.Matcher, text string) {
	t.Helper()
	r := p.Process(text)
	if r.Transcript != text || r.Units.Transcript != text || r.UnitStatus.Transcript != text ||
		!reflect.DeepEqual(r.Units, u.Recognize(text)) || !reflect.DeepEqual(r.UnitStatus, s.Recognize(text)) {
		t.Fatalf("native parity failed for %q: %+v", text, r)
	}
	checkSpans(t, r)
}

func checkSpans(t *testing.T, r Result) {
	t.Helper()
	check := func(text string, start, end int) {
		if start < 0 || end < start || end > len(r.Transcript) || r.Transcript[start:end] != text {
			t.Fatalf("invalid span %q [%d,%d)", text, start, end)
		}
	}
	for _, m := range r.Units.Mentions {
		check(m.Evidence, m.Start, m.End)
	}
	for _, e := range r.UnitStatus.StatusEvidence {
		check(e.Text, e.Start, e.End)
	}
	for _, a := range r.UnitStatus.Associations {
		if a.StatusIndex < 0 || a.StatusIndex >= len(r.UnitStatus.StatusEvidence) || a.Status != r.UnitStatus.StatusEvidence[a.StatusIndex] {
			t.Fatal("StatusIndex relationship changed")
		}
		for _, span := range []unitstatus.Span{a.Status.Span, a.Clause, a.Cue} {
			check(span.Text, span.Start, span.End)
		}
		if a.Unit != nil {
			check(a.Unit.Text, a.Unit.Start, a.Unit.End)
		}
		for _, c := range a.Candidates {
			check(c.Text, c.Start, c.End)
		}
	}
}

func TestExecutionOrderAndSyntheticNativePreservation(t *testing.T) {
	// Validated catalogs cannot produce these alternatives. This tests forwarding,
	// not a new recognition alias or resolution rule.
	text := "\tfixture\xff\r\n"
	for _, empty := range []bool{false, true} {
		u := unitrecognition.Result{Transcript: text, State: unitrecognition.Ambiguous, Reason: "conflicting_identities",
			Mentions: []unitrecognition.Mention{{Reason: "conflicting_identities", Alternatives: []unitrecognition.Unit{{CanonicalID: "A"}, {CanonicalID: "B"}}}}}
		s := unitstatus.Result{Transcript: text, State: "ambiguous", Reason: "synthetic native reason",
			Associations: []unitstatus.Association{{Disposition: unitstatus.Ambiguous, Reason: "conflicting_unit_identities", Candidates: []unitstatus.UnitEvidence{{Unit: unitrecognition.Unit{CanonicalID: "A"}}, {Unit: unitrecognition.Unit{CanonicalID: "B"}}}}}}
		if empty {
			u.Mentions = []unitrecognition.Mention{}
			s.StatusEvidence = []unitstatus.Evidence{}
			s.Associations = nil
			s.State = "future_native_state"
		}
		var calls []string
		r := process(text, func(got string) unitrecognition.Result {
			if got != text {
				t.Fatal("unit input rewritten")
			}
			calls = append(calls, "units")
			return u
		}, func(got string) unitstatus.Result {
			if got != text {
				t.Fatal("status input rewritten")
			}
			calls = append(calls, "status")
			return s
		})
		if !reflect.DeepEqual(calls, []string{"units", "status"}) || !reflect.DeepEqual(r, Result{text, u, s}) {
			t.Fatalf("native forwarding: %+v", r)
		}
	}
}

// Mutation traverses all exported native fields, including nested alternatives,
// candidates, selected unit pointers, spans, and rejection reasons.
func mutate(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			mutate(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			mutate(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			mutate(v.Index(i))
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	case reflect.Bool:
		if v.CanSet() {
			v.SetBool(!v.Bool())
		}
	case reflect.Int:
		if v.CanSet() {
			v.SetInt(-1)
		}
	}
}

func TestOwnershipIsolationAndThousandConcurrentCalls(t *testing.T) {
	p := setup(t)
	texts := []string{"engine 2", "on scene", "engine", "2 on scene", "", "\xff", "engine 2 and engine 5 on scene", "engine 2 responding; engine 2 on scene", "engine 2 back in service", "engine 2 on scene!?", "\u00e9; engine 5 training"}
	wants := make([]Result, len(texts))
	for i, text := range texts {
		wants[i] = p.Process(text)
		changed := p.Process(text)
		mutate(reflect.ValueOf(&changed.Units).Elem())
		if !reflect.DeepEqual(changed.UnitStatus, wants[i].UnitStatus) {
			t.Fatal("cross-branch contamination")
		}
		changed = p.Process(text)
		mutate(reflect.ValueOf(&changed.UnitStatus).Elem())
		if !reflect.DeepEqual(changed.Units, wants[i].Units) {
			t.Fatal("cross-branch contamination")
		}
	}
	for repeat := 0; repeat < 10; repeat++ {
		for i, text := range texts {
			if !reflect.DeepEqual(p.Process(text), wants[i]) {
				t.Fatal("repeated-call contamination")
			}
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			k := i % len(texts)
			r := p.Process(texts[k])
			if !reflect.DeepEqual(r, wants[k]) {
				t.Error("concurrent mismatch")
			}
			mutate(reflect.ValueOf(&r).Elem())
		}(i)
	}
	close(start)
	wg.Wait()
	for i, text := range texts {
		if !reflect.DeepEqual(p.Process(text), wants[i]) {
			t.Fatal("concurrent mutation leak")
		}
	}
	// Copies returned by Catalog also cannot alter either privately owned matcher.
	copy := p.units.Catalog()
	copy[0].Phrase = "mutated"
	if p.Process("car 4 on scene").UnitStatus.Associations[0].Unit.CanonicalID != "DC" {
		t.Fatal("catalog contamination")
	}
}
