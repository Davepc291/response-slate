package calltypeinterpret

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/calltypedata"
)

func setup(t *testing.T) *Interpreter {
	t.Helper()
	i, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return i
}

func TestApprovedTypes(t *testing.T) {
	i := setup(t)
	catalog, err := calltypedata.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range catalog.Phrases() {
		if p.Kind != calltypedata.CallType {
			continue
		}
		t.Run(p.Exact, func(t *testing.T) {
			text := p.Exact
			if !strings.HasPrefix(text, "respond ") {
				text = "respond to " + text
			}
			r := i.Interpret(text)
			if r.State != Resolved || r.CallType != p.CanonicalValue {
				t.Fatalf("%#v", r)
			}
			seen[p.CanonicalValue] = true
		})
	}
	if len(seen) != 12 {
		t.Fatal("missing type coverage")
	}
}

func TestSeparationAndQualifiers(t *testing.T) {
	i := setup(t)
	for _, p := range []string{"still alarm", "minor alarm", "box alarm", "working fire"} {
		r := i.Interpret(p)
		if r.State != Unresolved || len(r.AlarmLevels) != 1 || !r.AlarmLevels[0].Accepted {
			t.Fatalf("%#v", r)
		}
	}
	for _, q := range []string{"general fire activation", "smoke activation"} {
		r := i.Interpret(q)
		if r.State != Unresolved || len(r.Qualifiers) != 1 || r.Qualifiers[0].Accepted {
			t.Fatalf("%#v", r)
		}
		for _, p := range []string{"respond to a residential alarm", "respond commercial alarm"} {
			r = i.Interpret(p + ", " + q)
			if r.State != Resolved || len(r.Qualifiers) != 1 || !r.Qualifiers[0].Accepted ||
				r.Qualifiers[0].AssociatedCallType != r.CallType {
				t.Fatalf("%#v", r)
			}
		}
		r = i.Interpret("respond residential alarm; " + q)
		if r.Qualifiers[0].Accepted {
			t.Fatal("cross-clause association")
		}
		r = i.Interpret("respond residential alarm, " + q + "; respond commercial alarm")
		if r.State != Ambiguous || r.Qualifiers[0].Accepted {
			t.Fatal("ambiguous association")
		}
	}
}

func TestRepeatedConflictingAndOrdering(t *testing.T) {
	i := setup(t)
	r := i.Interpret("respond gas leak; respond gas leak")
	if r.State != Resolved || len(r.CallTypes) != 2 || len(r.Alternatives) != 1 {
		t.Fatalf("%#v", r)
	}
	for _, text := range []string{
		"respond gas leak; respond wires down; respond gas leak",
		"respond wires down; respond gas leak; respond gas leak",
		"working fire; respond wires down; respond gas leak",
	} {
		r = i.Interpret(text)
		if r.State != Ambiguous || r.CallType != "" || len(r.Alternatives) != 2 {
			t.Fatalf("%#v", r)
		}
		for n, e := range r.CallTypes {
			if !e.Accepted || (n > 0 && e.Start <= r.CallTypes[n-1].Start) {
				t.Fatal("evidence lost/order")
			}
		}
	}
}

func TestRejectedContext(t *testing.T) {
	i := setup(t)
	for _, text := range []string{
		"gas leak", "do not respond gas leak", "respond to no gas leak",
		"respond gas leak not confirmed", "yesterday respond gas leak",
		"if needed respond gas leak", "respond gas leak if needed",
		"we are investigating gas leak", "we responded to gas leak",
		"we flowed water for a water problem", "we discussed respond residential alarm",
		"'respond gas leak'", "he said \"respond gas leak\"",
		"historical: respond gas leak", "respond gas leak yesterday",
		"respond gas. leak", "respond\ngas leak", "respond gas leak at 12 Example Road",
	} {
		t.Run(text, func(t *testing.T) {
			r := i.Interpret(text)
			if r.State != Unresolved || r.CallType != "" {
				t.Fatalf("%#v", r)
			}
			for _, e := range r.CallTypes {
				if e.Accepted || e.RejectionReason == "" {
					t.Fatal("missing audit rejection")
				}
			}
		})
	}
}

func TestIncompleteAndInvalid(t *testing.T) {
	i := setup(t)
	for _, text := range []string{"", "alarm", "respond alarm", "respond CO alarm", "respond wires", "respond investigate",
		"I-95", "Merritt Parkway", "Engine 5", "respond", "respond gas",
		"respond gas leak\xff", "respond gas leak\x00", "respond gas leak\u200b"} {
		r := i.Interpret(text)
		if r.State != Unresolved || len(r.CallTypes) != 0 {
			t.Fatalf("%q: %#v", text, r)
		}
	}
}

func TestEvidenceDeterminismConcurrencyAndIsolation(t *testing.T) {
	i := setup(t)
	text := "é discussion; RESPOND TO GAS LEAK; minor alarm; respond residential alarm, smoke activation"
	want := i.Interpret(text)
	if want.Transcript != text || want.State != Ambiguous {
		t.Fatalf("%#v", want)
	}
	for _, collection := range [][]Evidence{want.CallTypes, want.AlarmLevels, want.Qualifiers} {
		for _, e := range collection {
			if text[e.Start:e.End] != e.Evidence {
				t.Fatal("byte evidence changed")
			}
		}
	}
	for n := 0; n < 20; n++ {
		if !reflect.DeepEqual(want, i.Interpret(text)) {
			t.Fatal("nondeterministic")
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if !reflect.DeepEqual(want, i.Interpret(text)) {
					t.Error("concurrent mismatch")
				}
			}
		}()
	}
	wg.Wait()
	for _, s := range []string{"gas leak", "smoke activation", ""} {
		if i.Interpret(s).State != Unresolved {
			t.Fatal("cross-call state")
		}
	}
	changed := i.Interpret(text)
	changed.CallTypes[0].Evidence = "changed"
	if !reflect.DeepEqual(want, i.Interpret(text)) {
		t.Fatal("shared output")
	}
}
