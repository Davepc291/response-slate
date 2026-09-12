package addressrole

import (
	"reflect"
	"strings"
	"testing"
)

func interpreter(t *testing.T) *Interpreter {
	t.Helper()
	in, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return in
}
func TestDispatchAndCrossStreets(t *testing.T) {
	in := interpreter(t)
	for _, tc := range []struct {
		text, number, street string
		cross                []string
	}{
		{"Respond to 30 Edgewood Drive, cross street is Valley Drive", "30", "edgewood drive", []string{"valley drive"}},
		{"Respond to a residential alarm at 93 Doubling Road. Cross streets are Dingletown Road and Hill Road.", "93", "doubling road", []string{"dingletown road", "hill road"}},
		{"Engine 4, respond 534 Riversville Road, runs between Quaker Lane and Fairchild Lane", "534", "riversville road", []string{"quaker lane", "fairchild lane"}},
		{"Respond to 65 Hunting Ridge Road", "65", "hunting ridge road", nil},
		{"Engine 4 respond 534 Riversville Road", "534", "riversville road", nil},
		{"respond to 30 Edgewood Drive cross street is Valley Drive", "30", "edgewood drive", []string{"valley drive"}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r := in.Interpret(tc.text)
			if r.State != Resolved || r.Primary == nil || r.Primary.HouseNumber != tc.number || r.Primary.CanonicalStreet != tc.street || len(r.Alternatives) != 0 || len(r.CrossStreets) != len(tc.cross) {
				t.Fatalf("unexpected result %+v", r)
			}
			for i, name := range tc.cross {
				if r.CrossStreets[i].CanonicalStreet != name {
					t.Fatal("cross street order/name")
				}
			}
			verifyEvidence(t, tc.text, r)
		})
	}
}
func TestRepeatedAndConflictingAddresses(t *testing.T) {
	in := interpreter(t)
	r := in.Interpret("Respond to 30 Edgewood Drive; respond to 30 EDGEWOOD DRIVE.")
	if r.State != Resolved || len(r.Primary.Mentions) != 2 {
		t.Fatal("lost duplicate evidence", r)
	}
	text := "respond to 30 Edgewood Drive; respond to 93 Doubling Road; respond to 30 Edgewood Drive"
	r = in.Interpret(text)
	if r.State != Ambiguous || r.Primary != nil || len(r.Alternatives) != 2 || len(r.Alternatives[0].Mentions) != 2 || r.Reason == "" {
		t.Fatalf("selected conflicting address %+v", r)
	}
	verifyEvidence(t, text, r)
	r = in.Interpret("respond to 30 Edgewood Drive. We flowed water from the hydrant at 328 Pemberwick Road")
	if r.State != Resolved || r.Primary.HouseNumber != "30" || len(r.Unresolved) != 1 {
		t.Fatal("operational mention changed primary")
	}
}
func TestUnsupportedAndNoEvidence(t *testing.T) {
	in := interpreter(t)
	for _, text := range []string{
		"We flowed water from the hydrant at 328 Pemberwick Road",
		"30 Edgewood Drive", "Do not respond to 30 Edgewood Drive", "We did respond to 30 Edgewood Drive",
		"Respond to an alarm. The hydrant is at 328 Pemberwick Road",
		"respond to\n30 Edgewood Drive", "respond to; 30 Edgewood Drive", "respond to another location at 30 Edgewood Drive",
	} {
		r := in.Interpret(text)
		if r.State != Unsupported || r.Primary != nil || len(r.Alternatives) != 0 {
			t.Fatalf("invented primary for %q: %+v", text, r)
		}
	}
	for _, text := range []string{"", "All units clear", "Valley Drive", "responding to Unknown Road"} {
		if r := in.Interpret(text); r.State != NoEvidence {
			t.Fatalf("unexpected evidence %+v", r)
		}
	}
	for _, text := range []string{"respond to 99 Unknown Example Road", "respond to Edgewood Drive", "cross street is Unknown Example Road"} {
		r := in.Interpret(text)
		if r.Primary != nil || len(r.CrossStreets) != 0 {
			t.Fatal("invented unknown evidence")
		}
	}
}
func TestCrossOnlyAndExactDeduplication(t *testing.T) {
	in := interpreter(t)
	text := "Cross streets are Valley Drive and Bedford Road; cross street is VALLEY DRIVE; cross street is Valley Road"
	r := in.Interpret(text)
	if r.State != Unsupported || r.Primary != nil || len(r.CrossStreets) != 3 || len(r.CrossStreets[0].Mentions) != 2 || r.CrossStreets[1].CanonicalStreet != "bedford road" || r.CrossStreets[2].CanonicalStreet != "valley road" {
		t.Fatalf("bad cross result %+v", r)
	}
	verifyEvidence(t, text, r)
	for _, text := range []string{"cross streets are Valley Drive and Unknown Example Road", "runs between Quaker Lane", "cross street is Valley Dr", "cross street is Valley Driveway", "not cross street is Valley Drive", "cross street is 30 Valley Drive"} {
		if r := in.Interpret(text); len(r.CrossStreets) != 0 {
			t.Fatalf("unsupported cross pattern %q: %+v", text, r)
		}
	}
	r = in.Interpret("cross street is Edgewood Avenue Connector")
	if len(r.CrossStreets) != 1 || r.CrossStreets[0].CanonicalStreet != "edgewood avenue connector" {
		t.Fatal("not longest street")
	}
}
func verifyEvidence(t *testing.T, text string, r Result) {
	t.Helper()
	check := func(e Evidence) {
		if e.Start < 0 || e.End > len(text) || e.Start >= e.End || text[e.Start:e.End] != e.Text {
			t.Fatalf("invalid byte evidence %+v", e)
		}
	}
	ps := r.Alternatives
	if r.Primary != nil {
		ps = append(append([]Primary{}, ps...), *r.Primary)
	}
	for _, p := range ps {
		for _, m := range p.Mentions {
			check(Evidence{m.Address.Evidence, m.Address.Start, m.Address.End})
			check(m.Cue)
		}
	}
	for _, c := range r.CrossStreets {
		for _, e := range c.Mentions {
			check(e)
		}
		for _, e := range c.Cues {
			check(e)
		}
	}
	for _, c := range r.Unresolved {
		check(Evidence{c.Evidence, c.Start, c.End})
	}
}
func TestNormalizationAndOriginalOffsets(t *testing.T) {
	in := interpreter(t)
	text := "Équipe: RESPOND TO (30 EDGEWOOD-DRIVE); CROSS STREET IS \"Valley Drive\"."
	original := text
	r := in.Interpret(text)
	if r.State != Resolved || len(r.CrossStreets) != 1 || text != original {
		t.Fatal("normalization", r)
	}
	m := r.Primary.Mentions[0].Address
	if m.Start != strings.Index(text, "30 EDGEWOOD") || m.Evidence != "30 EDGEWOOD-DRIVE" || r.CrossStreets[0].Mentions[0].Start != strings.Index(text, "Valley Drive") {
		t.Fatal("incorrect original offsets")
	}
	verifyEvidence(t, text, r)
	if !reflect.DeepEqual(r, in.Interpret(text)) {
		t.Fatal("unstable results")
	}
	for _, text := range []string{"prerespond to 30 Edgewood Drive", "respond to 30 Edgewood Driveway", "crossstreet is Valley Drive", "cross street is NotValley Drive"} {
		r := in.Interpret(text)
		if r.Primary != nil || len(r.CrossStreets) != 0 {
			t.Fatal("substring match", text)
		}
	}
}
func TestNoCrossCallStateAndInvalidInput(t *testing.T) {
	in := interpreter(t)
	_ = in.Interpret("respond to 30 Edgewood Drive; cross street is Valley Drive")
	for _, text := range []string{"respond to", "30 Edgewood Drive", "cross street is", "Valley Drive", ""} {
		r := in.Interpret(text)
		if r.Primary != nil || len(r.CrossStreets) != 0 {
			t.Fatal("state retained")
		}
	}
	for _, text := range []string{"respond to 30 Edgewood Drive\xff", "respond to 30 Edgewood Drive\x00", "cross street is Valley\u200b Drive", "\ufeffrespond to 30 Edgewood Drive"} {
		r := in.Interpret(text)
		if r.State != Unsupported || r.Primary != nil || len(r.CrossStreets) != 0 || len(r.Unresolved) != 0 {
			t.Fatal("invalid text accepted")
		}
	}
}
func TestAccessRoadPrimary(t *testing.T) {
	in := interpreter(t)
	r := in.Interpret("respond to 55 North St Driveway")
	if r.State != Resolved || r.Primary.HouseNumber != "55" || r.Primary.CanonicalStreet != "55 north st driveway" || string(r.Primary.DictionaryKind) != "access road" {
		t.Fatal("lost access-road identity")
	}
}
