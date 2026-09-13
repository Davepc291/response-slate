package calltypecandidate

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/calltypedata"
)

func mustNew(t *testing.T) *Extractor {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAllApprovedPhrases(t *testing.T) {
	e := mustNew(t)
	catalog, err := calltypedata.Load()
	if err != nil {
		t.Fatal(err)
	}
	phrases := catalog.Phrases()
	if len(phrases) != 42 {
		t.Fatal("expected 42 approved phrases")
	}
	seen := map[calltypedata.Kind]map[string]bool{}
	for _, p := range phrases {
		t.Run(p.Exact, func(t *testing.T) {
			for _, text := range []string{p.Exact, strings.ToUpper(p.Exact)} {
				got := e.Extract(text)
				if len(got) == 0 || got[0].Phrase != p || got[0].Evidence != text || got[0].Start != 0 || got[0].End != len(text) {
					t.Fatalf("missing exact catalog evidence: %#v", got)
				}
				for _, m := range got {
					if text[m.Start:m.End] != m.Evidence {
						t.Fatal("invalid offsets")
					}
				}
			}
		})
		if seen[p.Kind] == nil {
			seen[p.Kind] = map[string]bool{}
		}
		seen[p.Kind][p.CanonicalValue] = true
	}
	for kind, count := range map[calltypedata.Kind]int{calltypedata.CallType: 12, calltypedata.AlarmLevel: 4, calltypedata.AlarmQualifier: 2} {
		if len(seen[kind]) != count {
			t.Fatalf("incorrect %s coverage", kind)
		}
	}
}

func TestNormalizationAndOffsets(t *testing.T) {
	e := mustNew(t)
	for _, gap := range []string{" ", "  ", "\t", "\r\n", ", ", "-", ". ", "; ", ": ", "! ", "? ", " (", "\u201c", "\u2019"} {
		text := "é 🚒: GAS" + gap + "LEAK!"
		got := e.Extract(text)
		evidence := "GAS" + gap + "LEAK"
		start := len("é 🚒: ")
		if len(got) != 1 || got[0].Exact != "gas leak" || got[0].Evidence != evidence ||
			got[0].Start != start || got[0].End != start+len(evidence) || text[got[0].Start:got[0].End] != evidence {
			t.Fatalf("gap %q: %#v", gap, got)
		}
	}
	text := "“Motor VEHICLE accident,   injuries.”"
	got := e.Extract(text)
	if len(got) != 1 || got[0].Exact != "motor vehicle accident, injuries" ||
		got[0].Evidence != "Motor VEHICLE accident,   injuries" {
		t.Fatalf("%#v", got)
	}
}

func TestExclusionsAndBoundaries(t *testing.T) {
	e := mustNew(t)
	for _, text := range []string{
		"", "alarm", "wires", "CO alarm", "investigate", "investigation", "I-95", "Merritt Parkway",
		"unknown", "gas", "leak", "natural gas", "residential alarm", "MVA", "MVA with",
		"motor vehicle accident", "investigation elsewhere", "gas leakage", "biogas leak",
		"gas leak2", "2gas leak", "gas leaké", "égas leak", "gas leak\u0301",
		"gas_leak", "gas / leak", "gas + leak", "gas 💧 leak", "gas\u00a0leak", "gas—leak",
		"gas unknown leak", "gas le ak", "wires downfall",
	} {
		if got := e.Extract(text); len(got) != 0 {
			t.Errorf("%q unexpectedly matched %#v", text, got)
		}
	}
}

func TestLongestAtSameStart(t *testing.T) {
	// Synthetic matcher fixture exercises same-start prefix selection. These
	// phrases are not runtime vocabulary and cannot enter New's catalog.
	e := &Extractor{}
	for _, p := range []calltypedata.Phrase{
		{Exact: "fixture", Kind: calltypedata.CallType, CanonicalValue: "short"},
		{Exact: "fixture longer", Kind: calltypedata.CallType, CanonicalValue: "long"},
	} {
		if err := insert(&e.root, p); err != nil {
			t.Fatal(err)
		}
	}
	got := e.Extract("fixture longer; fixture")
	if len(got) != 2 || got[0].CanonicalValue != "long" || got[1].CanonicalValue != "short" {
		t.Fatalf("%#v", got)
	}
	if err := insert(&e.root, calltypedata.Phrase{Exact: "FIXTURE, longer"}); err == nil {
		t.Fatal("collision accepted")
	}
	if err := insert(&e.root, calltypedata.Phrase{Exact: ""}); err == nil {
		t.Fatal("empty phrase accepted")
	}
}

func TestDifferentStartOverlap(t *testing.T) {
	got := mustNew(t).Extract("natural gas leak")
	if len(got) != 2 || got[0].Exact != "natural gas leak" || got[1].Exact != "gas leak" ||
		got[0].Start != 0 || got[1].Start != 8 {
		t.Fatalf("different start evidence lost: %#v", got)
	}
}

func TestRepeatedMixedEvidenceAndNoInterpretation(t *testing.T) {
	e := mustNew(t)
	text := "Box alarm; gas leak; smoke activation; wires down; gas leak."
	original := strings.Clone(text)
	got := e.Extract(text)
	want := []string{"box alarm", "gas leak", "smoke activation", "wires down", "gas leak"}
	if len(got) != len(want) {
		t.Fatalf("%#v", got)
	}
	for i, m := range got {
		if m.Exact != want[i] || text[m.Start:m.End] != m.Evidence ||
			(i > 0 && m.Start <= got[i-1].Start) {
			t.Fatalf("order/evidence: %#v", got)
		}
	}
	if text != original {
		t.Fatal("original changed")
	}
	for _, s := range []string{"no gas leak", "we discussed a gas leak", "'gas leak'"} {
		got := e.Extract(s)
		if len(got) != 1 || !got[0].RequiresDispatchContext {
			t.Fatal("evidence extraction interpreted context")
		}
	}
}

func TestRejectUnsafeWholeTranscript(t *testing.T) {
	e := mustNew(t)
	for _, bad := range []string{"\xff", "\x00", "\x01", "\x0b", "\x0c", "\x7f", "\u0085", "\ufeff", "\u200b", "\u200d", "\u202e", "\u2066", "\u2028", "\u2029"} {
		for _, text := range []string{bad + "gas leak", "gas leak" + bad, "gas" + bad + " leak"} {
			if got := e.Extract(text); got != nil {
				t.Errorf("unsafe input returned %#v", got)
			}
		}
	}
}

func TestDeterminismIsolationAndConcurrency(t *testing.T) {
	e := mustNew(t)
	text := "é: minor alarm, gas leak; gas leak"
	want := e.Extract(text)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(e.Extract(text), want) {
			t.Fatal("nondeterministic")
		}
	}
	for _, text := range []string{"gas", "leak", ""} {
		if len(e.Extract(text)) != 0 {
			t.Fatal("cross-call evidence")
		}
	}
	altered := e.Extract(text)
	altered[0].Exact = "mutated"
	if !reflect.DeepEqual(e.Extract(text), want) {
		t.Fatal("mutable matcher exposed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if !reflect.DeepEqual(e.Extract(text), want) {
					t.Error("concurrent mismatch")
				}
				if len(e.Extract("leak")) != 0 {
					t.Error("shared transcript state")
				}
			}
		}()
	}
	wg.Wait()
}
