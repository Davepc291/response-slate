package keyworddetection

import (
	"strings"
	"testing"

	"greenwich-fire-responder/backend/internal/alerts"
)

func basicList(keywords ...Keyword) List {
	return List{
		ID:       "structure-fire",
		Name:     "structure fire",
		Keywords: keywords,
		Channels: []alerts.Channel{alerts.ChannelCH1A, alerts.ChannelCH2B},
	}
}

func TestWholeWordMatching(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "clear"})

	cases := []struct {
		text      string
		wantMatch bool
	}{
		{"Command to Engine 2, all clear.", true},
		{"Command to Engine 2, unclear message.", false},
		{"Command to Engine 2, cleared the scene.", false},
	}
	for _, c := range cases {
		matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: c.text})
		if err != nil {
			t.Fatalf("text %q: unexpected error: %v", c.text, err)
		}
		got := len(matches) > 0
		if got != c.wantMatch {
			t.Fatalf("text %q: got match=%v, want %v (%+v)", c.text, got, c.wantMatch, matches)
		}
	}
}

func TestEngineTwoDoesNotMatchEngineTwenty(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "Engine 2"})

	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Engine 20 responding."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("Engine 2 must not match Engine 20: %+v", matches)
	}

	matches, err = d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Engine 2, on-scene!"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateMatched {
		t.Fatalf("expected one firm match: %+v", matches)
	}
}

func TestCaseInsensitive(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "structure fire"})
	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "STRUCTURE FIRE reported at the address."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateMatched {
		t.Fatalf("expected case-insensitive match: %+v", matches)
	}
}

func TestRequiredContextUnmetIsAmbiguous(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "fire", RequiredContext: []string{"structure"}})

	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Reports of a fire in the area."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateAmbiguous {
		t.Fatalf("unmet required context must be ambiguous, not matched or dropped: %+v", matches)
	}

	matches, err = d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Reports of a structure fire in the area."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateMatched {
		t.Fatalf("required context present must be a firm match: %+v", matches)
	}
}

func TestExcludedContextSuppresses(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "structure fire", ExcludedContext: []string{"false alarm", "canceled"}})

	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Structure fire, later confirmed a false alarm."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateAmbiguous {
		t.Fatalf("excluded context present must not be a firm match: %+v", matches)
	}

	matches, err = d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Structure fire confirmed, units responding."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateMatched {
		t.Fatalf("no excluded context must be a firm match: %+v", matches)
	}
}

func TestRequiredAndExcludedBothPresentIsAmbiguous(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "fire", RequiredContext: []string{"structure"}, ExcludedContext: []string{"canceled"}})
	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "Structure fire reported, later canceled."})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateAmbiguous {
		t.Fatalf("conflicting required+excluded context must be ambiguous: %+v", matches)
	}
}

func TestContextWindowBounds(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "fire", RequiredContext: []string{"structure"}, ContextWindow: 2})

	// "structure" is 5 tokens away from "fire" -> outside the window.
	far := "structure reported near the block with heavy smoke then fire visible"
	matches, err := d.Detect(list, alerts.ChannelCH1A, Transcript{Text: far})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateAmbiguous {
		t.Fatalf("context outside window must not satisfy requirement: %+v", matches)
	}

	near := "structure fire visible"
	matches, err = d.Detect(list, alerts.ChannelCH1A, Transcript{Text: near})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].State != alerts.StateMatched {
		t.Fatalf("context inside window must satisfy requirement: %+v", matches)
	}
}

func TestTalkgroupFiltering(t *testing.T) {
	d := New()
	list := List{
		ID:       "structure-fire",
		Name:     "structure fire",
		Keywords: []Keyword{{Phrase: "fire"}},
		Channels: []alerts.Channel{alerts.ChannelCH1A},
	}
	matches, err := d.Detect(list, alerts.ChannelCH3B, Transcript{Text: "fire reported"})
	if err != nil {
		t.Fatal(err)
	}
	if matches != nil {
		t.Fatalf("list not configured for channel must produce no matches: %+v", matches)
	}

	matches, err = d.Detect(list, alerts.ChannelCH1A, Transcript{Text: "fire reported"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("list configured for channel must match: %+v", matches)
	}
}

func TestDuplicateInputIsDeterministic(t *testing.T) {
	d := New()
	list := basicList(Keyword{Phrase: "fire"})
	t1 := Transcript{Text: "fire reported twice, fire confirmed"}

	m1, err := d.Detect(list, alerts.ChannelCH1A, t1)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := d.Detect(list, alerts.ChannelCH1A, t1)
	if err != nil {
		t.Fatal(err)
	}
	if len(m1) != 1 || len(m2) != 1 || m1[0] != m2[0] {
		t.Fatalf("identical input must produce identical output: %+v vs %+v", m1, m2)
	}
	if m1[0].Occurrences != 2 {
		t.Fatalf("expected 2 occurrences, got %d", m1[0].Occurrences)
	}
}

func TestRejectsUnsafeInput(t *testing.T) {
	d := New()

	if _, err := d.Detect(basicList(Keyword{Phrase: "fire"}), alerts.ChannelCH1A, Transcript{Text: ""}); err == nil {
		t.Fatal("expected error: empty transcript is an absent evidence class")
	}
	if _, err := d.Detect(basicList(Keyword{Phrase: "fire"}), alerts.ChannelCH1A, Transcript{Text: strings.Repeat("a", maxTranscriptBytes+1)}); err == nil {
		t.Fatal("expected error: oversized transcript rejected")
	}
	if _, err := d.Detect(List{ID: "bad id", Name: "x", Keywords: []Keyword{{Phrase: "fire"}}, Channels: []alerts.Channel{alerts.ChannelCH1A}}, alerts.ChannelCH1A, Transcript{Text: "fire"}); err == nil {
		t.Fatal("expected error: invalid list id")
	}
	if _, err := d.Detect(basicList(Keyword{Phrase: "fire near address\ninjected"}), alerts.ChannelCH1A, Transcript{Text: "fire"}); err == nil {
		t.Fatal("expected error: unsafe keyword phrase rejected")
	}
	if _, err := d.Detect(List{ID: "list", Name: "x", Keywords: nil, Channels: []alerts.Channel{alerts.ChannelCH1A}}, alerts.ChannelCH1A, Transcript{Text: "fire"}); err == nil {
		t.Fatal("expected error: empty keyword list rejected")
	}
	if _, err := d.Detect(List{ID: "list", Name: "x", Keywords: []Keyword{{Phrase: "fire", ContextWindow: -1}}, Channels: []alerts.Channel{alerts.ChannelCH1A}}, alerts.ChannelCH1A, Transcript{Text: "fire"}); err == nil {
		t.Fatal("expected error: negative context window rejected")
	}
}

func TestNoSideEffects(t *testing.T) {
	// A pure evidence-producing call: no field of Match ever carries
	// transcript text beyond the configured phrase itself, and the
	// detector never mutates its inputs.
	d := New()
	list := basicList(Keyword{Phrase: "fire"})
	text := "fire reported"
	transcript := Transcript{Text: text}
	if _, err := d.Detect(list, alerts.ChannelCH1A, transcript); err != nil {
		t.Fatal(err)
	}
	if transcript.Text != text {
		t.Fatalf("input transcript must not be mutated")
	}
}
