package unitstatus

import (
	"greenwich-fire-responder/backend/internal/unitrecognition"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func matcher(t *testing.T) *Matcher {
	t.Helper()
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEveryApprovedPhrase(t *testing.T) {
	m := matcher(t)
	// Independent expectations, not generated from the implementation catalog.
	wants := []struct {
		text   string
		status Status
	}{
		{"dispatched", Dispatched}, {"responding", Enroute}, {"en route", Enroute},
		{"in route", Enroute}, {"enroute", Enroute}, {"on the way", Enroute},
		{"on scene", Onscene}, {"on location", Onscene}, {"arrived", Onscene}, {"10-23", Onscene},
		{"returning", EnrouteToQuarters}, {"returning to quarters", EnrouteToQuarters},
		{"RTQ", EnrouteToQuarters}, {"back to quarters", Quarters}, {"available", Quarters},
		{"in service", InService}, {"back in service", InService}, {"out of service", OutOfService},
		{"on air", OnAir}, {"training", Training},
	}
	for _, tc := range wants {
		t.Run(tc.text, func(t *testing.T) {
			for _, phrase := range []string{tc.text, strings.ToUpper(tc.text), strings.ReplaceAll(tc.text, " ", "\t  ")} {
				standalone := m.Recognize(phrase)
				if standalone.State != "unresolved" || standalone.StatusEvidence[0].Canonical != tc.status || standalone.StatusEvidence[0].Disposition != Accepted {
					t.Fatalf("standalone: %+v", standalone)
				}
				r := m.Recognize("engine 2 " + phrase)
				if r.State != "resolved" || r.StatusEvidence[0].Canonical != tc.status || r.Associations[0].Unit == nil || r.Associations[0].Unit.CanonicalID != "E2" {
					t.Fatalf("%+v", r)
				}
				checkSpans(t, r)
			}
		})
	}
}

func TestExistingCatalogAndStep5D2Unchanged(t *testing.T) {
	m := matcher(t)
	units, err := unitrecognition.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units.Catalog() {
		s := u.Phrase + " on scene"
		r := m.Recognize(s)
		if r.State != "resolved" || r.Associations[0].Unit.Unit != u {
			t.Fatalf("%s: %+v", s, r)
		}
		old := units.Recognize(s)
		if old.State != unitrecognition.Unresolved || old.Mentions[0].Accepted || old.Mentions[0].Reason != "unsupported_context" {
			t.Fatalf("Step 5D2 changed: %+v", old)
		}
	}
}

func TestSingleUnitGrammarAndPunctuation(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"engine 2 on scene", "engine 2,on scene", "ENGINE\t2, ON SCENE", "engine 2 is on scene",
		"engine 2, is on scene", " \tengine 2\t  is\t on\t scene\t ",
		"engine 2 on scene.", "engine 2 on scene;", "engine 2 on scene:", "engine 2 on scene!",
		"engine 2 on scene\r\n", "engine 2 on scene; engine 5 responding",
	} {
		r := m.Recognize(s)
		if r.State != "resolved" {
			t.Errorf("%q: %+v", s, r)
		}
		checkSpans(t, r)
	}
	for _, s := range []string{
		"engine 2,, on scene", "engine 2 are on scene", "engine 2 is,is on scene", "engine 2 ison scene",
		"(engine 2) on scene", "engine 2 - on scene", "engine 2 / on scene", "engine 2on scene",
		"on scene engine 2", "engine 2 on scene now", "engine 2 on scene, responding",
		"engine 2 on scene and responding", "engine 2 on scene on scene", "engine 2 is",
	} {
		assertUnassociated(t, m.Recognize(s))
	}
}

func TestMultipleUnitsAndAmbiguousIdentity(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"engine 2, engine 5 on scene", "engine 2 engine 5 on scene", "engine 2 or engine 5 on scene",
		"engine 2 and engine 20 on scene", "engine 20 and engine 2 on scene",
		"engine 2 and engine 5 is on scene", "engine 2 and engine 5,, on scene",
		"engine 2 and engine 5 not on scene", "engine 2 and engine 5 were on scene",
		"if engine 2 and engine 5 are on scene", "engine 2 and engine 5 report on scene",
		"engine 2 and engine 5 on scene yesterday", "\"engine 2 and engine 5 on scene\"",
		"engine 2 on scene and engine 5", "engine 2 responding and engine 5 on scene",
	} {
		assertUnassociated(t, m.Recognize(s))
	}
	// Validated production catalog has no collisions. Inject a conflicting identity
	// only into this private matcher to exercise defensive ambiguity behavior.
	u := m.units[1]
	u.CanonicalID = "synthetic-conflict"
	m.units = append(m.units, u)
	r := m.Recognize("engine 2 on scene")
	assertUnassociated(t, r)
	if r.Associations[0].Disposition != Ambiguous || len(r.Associations[0].Candidates) != 2 {
		t.Fatalf("%+v", r)
	}
	// Do not select an identity or distribute status through an ambiguous list.
	assertUnassociated(t, m.Recognize("engine 2 and engine 5 are on scene"))
}

func TestCoordinatedUnits(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"engine 2 and engine 5 on scene", "engine 2 and engine 5 are on scene",
		"engine 2 and engine 5, on scene", "engine 2 and engine 5, are on scene",
		"ENGINE\t2\tAND\tENGINE 5\tARE\tON\tSCENE!",
	} {
		r := m.Recognize(s)
		if r.State != "resolved" || len(r.StatusEvidence) != 1 || len(r.Associations) != 2 {
			t.Fatalf("%q: %+v", s, r)
		}
		for i, id := range []string{"E2", "E5"} {
			a := r.Associations[i]
			if a.Unit == nil || a.Unit.CanonicalID != id || a.Disposition != Accepted || a.Status.Canonical != Onscene || a.StatusIndex != 0 {
				t.Fatalf("%q: %+v", s, a)
			}
		}
		checkSpans(t, r)
	}
	r := m.Recognize("engine 2 and engine 5 and squad 8 are training")
	if r.State != "resolved" || len(r.Associations) != 3 || r.Associations[2].Unit.CanonicalID != "SQ8" {
		t.Fatalf("three units: %+v", r)
	}
	r = m.Recognize("engine 2 and engine 2 on scene")
	if r.State != "resolved" || len(r.Associations) != 2 || r.Associations[0].Unit.Start == r.Associations[1].Unit.Start {
		t.Fatalf("repeated evidence: %+v", r)
	}
	// Full phrase and nested evidence remain separate for coordinated lists too.
	r = m.Recognize("engine 2 and engine 5 back in service")
	if r.State != "resolved" || len(r.StatusEvidence) != 2 || len(r.Associations) != 3 || r.Associations[2].Unit != nil {
		t.Fatalf("nested evidence: %+v", r)
	}
	checkSpans(t, r)
}

func TestMixedTerminalPunctuation(t *testing.T) {
	m := matcher(t)
	for _, prefix := range []string{"engine 2 on scene", "engine 2 and engine 5 are on scene"} {
		for _, ending := range []string{"...?", "!?", "?!", "?", "...?!", "!?\r\n"} {
			s := prefix + ending
			r := m.Recognize(s)
			assertUnassociated(t, r)
			if r.Transcript != s || len(r.StatusEvidence) != 1 {
				t.Fatalf("lost evidence: %+v", r)
			}
			e := r.StatusEvidence[0]
			if e.Disposition != Rejected || e.Reason != "question" || e.Text != "on scene" || e.Start != len(prefix)-len("on scene") || e.End != len(prefix) {
				t.Fatalf("question evidence: %+v", e)
			}
		}
		for _, ending := range []string{".", "!", "...", "!!", "!.", ";", ":", "\r\n"} {
			r := m.Recognize(prefix + ending)
			if r.State != "resolved" {
				t.Fatalf("affirmative %q: %+v", ending, r)
			}
			checkSpans(t, r)
		}
	}
	r := m.Recognize("\u00e9; engine 2 on scene...?")
	assertUnassociated(t, r)
	if r.StatusEvidence[0].Start != 13 || r.StatusEvidence[0].End != 21 {
		t.Fatalf("UTF-8 question offsets: %+v", r)
	}
	// A question does not consume or qualify the next independent clause/line.
	for _, separator := range []string{" ", "\r\n"} {
		r := m.Recognize("engine 2 on scene!?" + separator + "engine 5 responding.")
		if r.State != "resolved" || len(r.Associations) != 2 || r.Associations[0].Unit != nil || r.Associations[1].Unit.CanonicalID != "E5" {
			t.Fatalf("question scope: %+v", r)
		}
		checkSpans(t, r)
	}
}

func TestCoordinatedScopeAndConflicts(t *testing.T) {
	m := matcher(t)
	for _, separator := range []string{".", ";", ":", "!", "?", "\r", "\n", "\r\n"} {
		assertUnassociated(t, m.Recognize("engine 2 and engine 5"+separator+"on scene"))
		assertUnassociated(t, m.Recognize("engine 2 and"+separator+"engine 5 are on scene"))
		r := m.Recognize("engine 2" + separator + "engine 5 and squad 8 are training")
		if r.State != "resolved" || len(r.Associations) != 2 || r.Associations[0].Unit.CanonicalID != "E5" || r.Associations[1].Unit.CanonicalID != "SQ8" {
			t.Fatalf("cross-clause distribution: %+v", r)
		}
	}
	r := m.Recognize("engine 2 and engine 5 on scene; squad 8 responding")
	if r.State != "resolved" || len(r.Associations) != 3 || r.Associations[2].Unit.CanonicalID != "SQ8" || r.Associations[2].Status.Canonical != Enroute {
		t.Fatalf("independent clauses: %+v", r)
	}
	r = m.Recognize("engine 2 and engine 5 on scene; engine 2 responding")
	if r.State != "ambiguous" || r.Associations[0].Unit != nil || r.Associations[2].Unit != nil || r.Associations[1].Unit.CanonicalID != "E5" {
		t.Fatalf("per-unit conflict: %+v", r)
	}
	checkSpans(t, r)
}

func TestSpeechRoles(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"engine 2 on scene?", "is engine 2 on scene?", "engine 2 not on scene", "engine 2 is not on scene",
		"not engine 2 on scene", "no engine 2 on scene", "yesterday engine 2 on scene", "engine 2 on scene yesterday",
		"engine 2 was on scene", "if engine 2 is available", "engine 2 on scene if needed",
		"engine 2 report on scene", "engine 2 return to quarters", "engine 2 respond", "dispatch engine 2",
		"engine 2 be available", "engine 2, 10-4, on scene", "engine 2 good copy on scene",
		"dispatcher says engine 2 on scene", "this is engine 2 on scene", "we discussed engine 2 on scene",
		"\"engine 2 on scene\"", "\u201cengine 2 on scene\u201d", "'engine 2 on scene'",
		"\"quoted; engine 2 on scene", "engine 2 on scene; quoted\"", "engine 2 isn't on scene",
		"engine 2 last unit on scene", "engine 2 disregard that last on scene",
	} {
		assertUnassociated(t, m.Recognize(s))
	}
	for _, s := range []string{"engine 2 on scene?", "\"engine 2 on scene\""} {
		r := m.Recognize(s)
		if len(r.StatusEvidence) != 1 || r.StatusEvidence[0].Disposition != Rejected {
			t.Fatalf("role evidence: %+v", r)
		}
	}
}

func TestClearExclusionsAndCollisions(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{"clear", "CLEAR", "engine 2 clear"} {
		r := m.Recognize(s)
		assertUnassociated(t, r)
		if len(r.StatusEvidence) != 1 || r.StatusEvidence[0].Canonical != "" || r.StatusEvidence[0].Disposition != Unresolved || r.StatusEvidence[0].Reason != "clear_unresolved" {
			t.Fatalf("%+v", r)
		}
	}
	for _, s := range []string{
		"EN_ROUTE", "ON_SCENE", "ACTIVE", "10-4", "good copy", "canceled", "transported one", "disregard that last",
		"scene", "unclear", "cleared", "arrival", "arrives", "return", "RTQs", "ONAIR", "10-230", "110-23",
		"10 23", "10--23", "xresponding", "respondingx", "responding\u0301", "\u00e9responding", "re\u017fponding",
		"on-scene", "on_scene", "on,scene", "on.scene", "on\nscene", "on\rscene", "on\u00a0scene", "on / scene",
	} {
		r := m.Recognize(s)
		if len(r.StatusEvidence) != 0 {
			t.Errorf("unapproved %q: %+v", s, r)
		}
	}
	assertUnassociated(t, m.Recognize("last unit on scene"))
	for _, unit := range []string{"engine 20", "engine 2V", "engine 2x", "engine two", "eng 2", "engine2", "E2", "chief", "car 5", "engine 2\u0301", "engine 2\u00e9", "\u00e9engine 2"} {
		r := m.Recognize(unit + " on scene")
		assertUnassociated(t, r)
		if len(r.StatusEvidence) != 1 {
			t.Fatalf("lost status: %+v", r)
		}
	}
	for _, tc := range []struct{ s, id string }{{"engine 21", "E21V"}, {"engine 51", "E51V"}, {"tanker 17", "TK17V"}, {"rescue 51", "RESCUE51V"}} {
		r := m.Recognize(tc.s + " on scene")
		if r.Associations[0].Unit.CanonicalID != tc.id {
			t.Fatalf("numeric collision: %+v", r)
		}
	}
	r := m.Recognize("engine 2 returning to quarters")
	if len(r.StatusEvidence) != 1 || r.StatusEvidence[0].Text != "returning to quarters" {
		t.Fatal("longest same start")
	}
	r = m.Recognize("engine 2 back in service")
	if len(r.StatusEvidence) != 2 || r.StatusEvidence[1].Text != "in service" || r.Associations[1].Unit != nil || r.Associations[0].Unit == nil {
		t.Fatalf("nested evidence: %+v", r)
	}
}

func TestIsolationConflictsAndRepetition(t *testing.T) {
	m := matcher(t)
	for _, delimiter := range []string{".", ";", ":", "!", "?", "\r", "\n", "\r\n"} {
		assertUnassociated(t, m.Recognize("engine 2"+delimiter+"on scene"))
		assertUnassociated(t, m.Recognize("engine"+delimiter+"2 on scene"))
	}
	for _, s := range []string{"engine 2", "on scene", "engine", "2 on scene", "57201 on scene", "RID 123 on scene", "CH1A on scene", ""} {
		assertUnassociated(t, m.Recognize(s))
	}
	r := m.Recognize("engine 2 responding; engine 2 on scene; engine 5 training")
	if r.State != "ambiguous" || r.Associations[0].Unit != nil || r.Associations[1].Unit != nil || r.Associations[2].Unit == nil || r.Associations[0].Reason != "conflicting_statuses" {
		t.Fatalf("conflicts: %+v", r)
	}
	r = m.Recognize("engine 2 on scene; engine 2 on scene")
	if r.State != "resolved" || len(r.Associations) != 2 || r.Associations[0].Unit == nil || r.Associations[1].Unit == nil {
		t.Fatalf("repeat: %+v", r)
	}
	checkSpans(t, r)
	r = m.Recognize("engine 2 on scene; engine 5 responding")
	if r.State != "resolved" || r.Associations[0].Unit.CanonicalID != "E2" || r.Associations[1].Unit.CanonicalID != "E5" {
		t.Fatalf("independent units: %+v", r)
	}
}

func TestDocumentByteOffsets(t *testing.T) {
	m := matcher(t)
	for _, tc := range []struct {
		text           string
		us, ue, ss, se int
	}{
		{"engine 2 on scene", 0, 8, 9, 17},
		{"\u00e9; engine 2 on scene", 4, 12, 13, 21},
		{"\U0001f692; ENGINE\t2, ON\tSCENE", 6, 14, 16, 24},
	} {
		r := m.Recognize(tc.text)
		if r.State != "resolved" {
			t.Fatalf("%+v", r)
		}
		a := r.Associations[0]
		if a.Unit.Start != tc.us || a.Unit.End != tc.ue || a.Status.Start != tc.ss || a.Status.End != tc.se {
			t.Fatalf("offsets: %+v", a)
		}
		checkSpans(t, r)
	}
}

func TestInvalidInput(t *testing.T) {
	m := matcher(t)
	bad := []string{"\xff", "\xc0\xaf", "\xed\xa0\x80"}
	for c := byte(0); c < 32; c++ {
		if c != '\t' && c != '\r' && c != '\n' {
			bad = append(bad, string([]byte{c}))
		}
	}
	for _, control := range bad {
		s := "engine 2 on scene" + control
		r := m.Recognize(s)
		if r.Transcript != s || r.State != "unresolved" || r.Reason != "invalid_utf8_or_unsafe_controls" || len(r.StatusEvidence) != 0 || len(r.Associations) != 0 {
			t.Fatalf("%q: %+v", s, r)
		}
	}
}

func TestOwnershipAndThousandConcurrentCalls(t *testing.T) {
	m := matcher(t)
	texts := []string{"engine 2 on scene", "engine 2 and engine 5 on scene", "engine 2 responding; engine 2 on scene", "on scene", "clear", "engine 2", "\xff", "\u00e9; engine 5 training", "engine 2 back in service", "engine 2 and engine 5 are on scene!?"}
	wants := make([]Result, len(texts))
	for i, text := range texts {
		wants[i] = m.Recognize(text)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 1000; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			i := n % len(texts)
			r := m.Recognize(texts[i])
			if !reflect.DeepEqual(r, wants[i]) {
				t.Error("concurrent mismatch")
			}
			if len(r.StatusEvidence) > 0 {
				r.StatusEvidence[0].Canonical = "mutated"
			}
			for j := range r.Associations {
				a := &r.Associations[j]
				a.Status.Text, a.Clause.Text, a.Cue.Text = "mutated", "mutated", "mutated"
				if a.Unit != nil {
					a.Unit.CanonicalID = "mutated"
				}
				if len(a.Candidates) > 0 {
					a.Candidates[0].Phrase = "mutated"
				}
			}
		}(n)
	}
	close(start)
	wg.Wait()
	for repeat := 0; repeat < 10; repeat++ {
		for i, text := range texts {
			if !reflect.DeepEqual(m.Recognize(text), wants[i]) {
				t.Fatal("mutation/state leak")
			}
		}
	}
	r := m.Recognize("engine 2 on scene; engine 2 on scene")
	r.Associations[0].Unit.CanonicalID = "mutated"
	r.Associations[0].Candidates[0].CanonicalID = "mutated"
	if r.Associations[1].Unit.CanonicalID != "E2" || r.Associations[1].Candidates[0].CanonicalID != "E2" {
		t.Fatal("within-result alias")
	}
	r = m.Recognize("engine 2 and engine 5 on scene")
	r.Associations[0].Unit.CanonicalID = "mutated"
	r.Associations[0].Candidates[1].CanonicalID = "mutated"
	if r.Associations[1].Unit.CanonicalID != "E5" || r.Associations[1].Candidates[1].CanonicalID != "E5" {
		t.Fatal("coordinated association alias")
	}
}

func assertUnassociated(t *testing.T, r Result) {
	t.Helper()
	if r.State == "resolved" {
		t.Fatalf("unexpected resolution: %+v", r)
	}
	for _, a := range r.Associations {
		if a.Unit != nil || a.Disposition == Accepted {
			t.Fatalf("unexpected association: %+v", r)
		}
	}
	checkSpans(t, r)
}

func checkSpans(t *testing.T, r Result) {
	t.Helper()
	check := func(s Span) {
		if s.Start < 0 || s.End < s.Start || s.End > len(r.Transcript) || r.Transcript[s.Start:s.End] != s.Text {
			t.Fatalf("invalid span: %+v", s)
		}
	}
	for i, e := range r.StatusEvidence {
		check(e.Span)
		if i > 0 && r.StatusEvidence[i-1].Start >= e.Start {
			t.Fatal("evidence order")
		}
	}
	for _, a := range r.Associations {
		check(a.Status.Span)
		check(a.Clause)
		check(a.Cue)
		if a.Status != r.StatusEvidence[a.StatusIndex] {
			t.Fatal("evidence reference mismatch")
		}
		if a.Unit != nil {
			check(a.Unit.Span)
		}
		for _, u := range a.Candidates {
			check(u.Span)
		}
	}
}
