package calltypepipeline_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/calltypecandidate"
	"greenwich-fire-responder/backend/internal/calltypedata"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
	"greenwich-fire-responder/backend/internal/calltypepipeline"
)

func newPipeline(t *testing.T) *calltypepipeline.Pipeline {
	t.Helper()
	p, err := calltypepipeline.New()
	if err != nil || p == nil {
		t.Fatalf("construction: %v", err)
	}
	return p
}

func TestCatalogThroughPipeline(t *testing.T) {
	p := newPipeline(t)
	c, err := calltypedata.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[calltypedata.Kind]map[string]bool{}
	for _, phrase := range c.Phrases() {
		t.Run(phrase.Exact, func(t *testing.T) {
			text := phrase.Exact
			if phrase.Kind == calltypedata.CallType && !strings.HasPrefix(text, "respond ") {
				text = "respond to " + text
			}
			r := p.Process(text)
			if len(r.Candidates) == 0 || r.Candidates[0].Phrase != phrase {
				t.Fatalf("missing catalog metadata: %#v", r)
			}
			if r.Transcript != text || r.Interpretation.Transcript != text || r.State != r.Interpretation.State {
				t.Fatal("result contract")
			}
			if phrase.Kind == calltypedata.CallType {
				if r.State != calltypeinterpret.Resolved || r.Interpretation.CallType != phrase.CanonicalValue {
					t.Fatalf("%#v", r)
				}
			} else {
				if r.State != calltypeinterpret.Unresolved || r.Interpretation.CallType != "" {
					t.Fatal("level/qualifier invented type")
				}
				if phrase.Kind == calltypedata.AlarmLevel && len(r.Interpretation.AlarmLevels) != 1 {
					t.Fatal("level lost")
				}
				if phrase.Kind == calltypedata.AlarmQualifier && len(r.Interpretation.Qualifiers) != 1 {
					t.Fatal("qualifier lost")
				}
			}
		})
		if seen[phrase.Kind] == nil {
			seen[phrase.Kind] = map[string]bool{}
		}
		seen[phrase.Kind][phrase.CanonicalValue] = true
	}
	if len(c.Phrases()) != 42 || len(seen[calltypedata.CallType]) != 12 || len(seen[calltypedata.AlarmLevel]) != 4 || len(seen[calltypedata.AlarmQualifier]) != 2 {
		t.Fatal("coverage counts")
	}
}

func TestStatesAndRepetition(t *testing.T) {
	p := newPipeline(t)
	for _, tc := range []struct {
		text     string
		state    calltypeinterpret.State
		mentions int
	}{
		{"respond gas leak; respond gas leak", calltypeinterpret.Resolved, 2},
		{"respond gas leak; respond wires down; respond gas leak", calltypeinterpret.Ambiguous, 3},
		{"respond wires down; respond gas leak; respond gas leak", calltypeinterpret.Ambiguous, 3},
		{"gas leak", calltypeinterpret.Unresolved, 1},
	} {
		r := p.Process(tc.text)
		if r.State != tc.state || len(r.Interpretation.CallTypes) != tc.mentions || len(r.Candidates) != tc.mentions {
			t.Fatalf("%#v", r)
		}
		if tc.state != calltypeinterpret.Resolved && r.Interpretation.CallType != "" {
			t.Fatal("invented resolution")
		}
	}
	for _, q := range []string{"smoke activation", "general fire activation"} {
		r := p.Process("respond residential alarm, " + q)
		if r.State != calltypeinterpret.Resolved || !r.Interpretation.Qualifiers[0].Accepted || r.Interpretation.Qualifiers[0].AssociatedCallType != "ALARM RESD" {
			t.Fatal("association changed")
		}
		r = p.Process("respond residential alarm, " + q + "; respond commercial alarm")
		if r.State != calltypeinterpret.Ambiguous || r.Interpretation.Qualifiers[0].Accepted {
			t.Fatal("ambiguous qualifier changed")
		}
	}
}

func TestNativeParityAndRejections(t *testing.T) {
	p := newPipeline(t)
	e, err := calltypecandidate.New()
	if err != nil {
		t.Fatal(err)
	}
	i, err := calltypeinterpret.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"", "alarm", "respond CO alarm", "I-95", "Merritt Parkway", "Engine 5", "93 Doubling Road",
		"gas leak", "do not respond gas leak", "'respond gas leak'", "yesterday respond gas leak",
		"if needed respond gas leak", "we are investigating gas leak",
		"respond gas leak at 12 Example Road", "respond gas. leak", "working fire here",
		"smoke activation", "respond natural gas leak", "respond gas leak\xff", "respond gas leak\u200b",
		"respond residential alarm, smoke activation; respond commercial alarm",
	} {
		t.Run(text, func(t *testing.T) {
			r := p.Process(text)
			if !reflect.DeepEqual(r.Candidates, e.Extract(text)) || !reflect.DeepEqual(r.Interpretation, i.Interpret(text)) || r.State != r.Interpretation.State {
				t.Fatal("native behavior changed")
			}
			for _, collection := range [][]calltypeinterpret.Evidence{r.Interpretation.CallTypes, r.Interpretation.AlarmLevels, r.Interpretation.Qualifiers} {
				for _, ev := range collection {
					if !ev.Accepted && ev.RejectionReason == "" {
						t.Fatal("rejection reason lost")
					}
				}
			}
			if text != "respond natural gas leak" && !strings.Contains(text, "respond commercial alarm") && r.State != calltypeinterpret.Unresolved {
				t.Fatal("unsupported text resolved")
			}
		})
	}
}

func TestOffsetsAndReturnedCollectionIsolation(t *testing.T) {
	p := newPipeline(t)
	text := "é 🚒; respond residential alarm, smoke activation; minor alarm; gas leak"
	want := p.Process(text)
	before := p.Process(text)
	if want.Transcript != text || want.Interpretation.Transcript != text {
		t.Fatal("original changed")
	}
	for n, c := range want.Candidates {
		if text[c.Start:c.End] != c.Evidence || (n > 0 && c.Start <= want.Candidates[n-1].Start) {
			t.Fatal("candidate offsets/order")
		}
	}
	for _, collection := range [][]calltypeinterpret.Evidence{want.Interpretation.CallTypes, want.Interpretation.AlarmLevels, want.Interpretation.Qualifiers} {
		for n, c := range collection {
			if text[c.Start:c.End] != c.Evidence || (n > 0 && c.Start <= collection[n-1].Start) {
				t.Fatal("interpreted offsets/order")
			}
		}
	}
	changed := p.Process(text)
	changed.Candidates[0].Exact = "mutated"
	changed.Interpretation.CallTypes[0].Evidence = "mutated"
	changed.Interpretation.AlarmLevels[0].CanonicalValue = "mutated"
	changed.Interpretation.Qualifiers[0].AssociatedCallType = "mutated"
	changed.Interpretation.Alternatives[0] = "mutated"
	changed.Transcript = "mutated"
	changed.State = calltypeinterpret.Ambiguous
	if !reflect.DeepEqual(want, p.Process(text)) || !reflect.DeepEqual(want, before) {
		t.Fatal("mutable internal/shared state")
	}
	for n := 0; n < 20; n++ {
		if !reflect.DeepEqual(want, p.Process(text)) {
			t.Fatal("nondeterministic")
		}
	}
	for _, s := range []string{"respond", "gas", "leak", "smoke activation", ""} {
		if p.Process(s).State != calltypeinterpret.Unresolved {
			t.Fatal("cross-transmission inference")
		}
	}
}

func TestOneThousandConcurrentCalls(t *testing.T) {
	p := newPipeline(t)
	texts := []string{"respond gas leak", "respond gas leak; respond wires down", "gas leak", "minor alarm", "respond residential alarm, smoke activation", "respond", "leak", ""}
	wants := make([]calltypepipeline.Result, len(texts))
	for n, s := range texts {
		wants[n] = p.Process(s)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 1000; n++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			k := index % len(texts)
			if got := p.Process(texts[k]); !reflect.DeepEqual(got, wants[k]) {
				t.Errorf("call %d mismatch", index)
			}
		}(n)
	}
	close(start)
	wg.Wait()
}
