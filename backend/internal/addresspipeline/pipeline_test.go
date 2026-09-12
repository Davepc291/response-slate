package addresspipeline_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/addresscandidate"
	"greenwich-fire-responder/backend/internal/addresspipeline"
	"greenwich-fire-responder/backend/internal/addressrole"
)

func pipeline(t *testing.T) *addresspipeline.Pipeline {
	t.Helper()
	p, err := addresspipeline.New()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSupportedTranscripts(t *testing.T) {
	p := pipeline(t)
	for _, tc := range []struct {
		text, number, street string
		kind                 addresscandidate.Kind
		cross                []string
	}{
		{"Respond to 93 Doubling Road", "93", "doubling road", addresscandidate.Street, nil},
		{"Respond to 30 Edgewood Drive, cross street is Valley Drive", "30", "edgewood drive", addresscandidate.Street, []string{"valley drive"}},
		{"Respond to 65 Hunting Ridge Road", "65", "hunting ridge road", addresscandidate.Street, nil},
		{"Respond to 328 Pemberwick Road", "328", "pemberwick road", addresscandidate.Street, nil},
		{"Engine 4, respond 534 Riversville Road, runs between Quaker Lane and Fairchild Lane", "534", "riversville road", addresscandidate.Street, []string{"quaker lane", "fairchild lane"}},
		{"Respond to 55 North St Driveway", "55", "55 north st driveway", addresscandidate.AccessRoad, nil},
		{"Respond to 55 North Turning Loop Extension", "55", "55 north turning loop extension", addresscandidate.AccessRoad, nil},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r := p.Process(tc.text)
			if r.Transcript != tc.text || r.State != addressrole.Resolved || r.Interpretation.State != r.State || len(r.Candidates) != 1 {
				t.Fatalf("unexpected result %+v", r)
			}
			c := r.Candidates[0]
			primary := r.Interpretation.Primary
			if c.HouseNumber != tc.number || c.CanonicalStreet != tc.street || c.DictionaryKind != tc.kind || primary == nil || primary.HouseNumber != tc.number || primary.CanonicalStreet != tc.street || primary.DictionaryKind != tc.kind {
				t.Fatal("lost address identity", r)
			}
			if len(r.Interpretation.CrossStreets) != len(tc.cross) {
				t.Fatal("cross role count")
			}
			for i, name := range tc.cross {
				if r.Interpretation.CrossStreets[i].CanonicalStreet != name {
					t.Fatal("lost cross role/order")
				}
			}
			verifyEvidence(t, r)
		})
	}
}

func TestRepeatedMentionsAndAmbiguity(t *testing.T) {
	p := pipeline(t)
	r := p.Process("respond to 93 Doubling Road; respond to 93 DOUBLING ROAD")
	if r.State != addressrole.Resolved || len(r.Candidates) != 2 || len(r.Interpretation.Primary.Mentions) != 2 {
		t.Fatal("repeated candidate lost", r)
	}
	if r.Candidates[0].Start >= r.Candidates[1].Start {
		t.Fatal("reordered candidates")
	}
	verifyEvidence(t, r)
	r = p.Process("respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond to 93 Doubling Road")
	if r.State != addressrole.Ambiguous || len(r.Candidates) != 3 || r.Interpretation.Primary != nil || len(r.Interpretation.Alternatives) != 2 || len(r.Interpretation.Alternatives[0].Mentions) != 2 || r.Interpretation.Reason == "" {
		t.Fatal("lost conflict evidence", r)
	}
	for i, want := range []string{"93", "30", "93"} {
		if r.Candidates[i].HouseNumber != want {
			t.Fatal("candidate grouping/order changed")
		}
	}
	verifyEvidence(t, r)
}

func TestUnresolvedAndUnsupportedInput(t *testing.T) {
	p := pipeline(t)
	for _, tc := range []struct {
		text                     string
		state                    addressrole.State
		count, unresolved, cross int
	}{
		{"We flowed water from the hydrant at 328 Pemberwick Road", addressrole.Unsupported, 1, 1, 0},
		{"93 Doubling Road; 30 Edgewood Drive", addressrole.Unsupported, 2, 2, 0},
		{"Doubling Road", addressrole.NoEvidence, 0, 0, 0},
		{"93 Unknown Example Road", addressrole.NoEvidence, 0, 0, 0},
		{"respond to 93 Unknown Example Road", addressrole.Unsupported, 0, 0, 0},
		{"cross street is Valley Drive", addressrole.Unsupported, 0, 0, 1},
		{"", addressrole.NoEvidence, 0, 0, 0},
		{"respond to 93 Doubling Road\xff", addressrole.Unsupported, 0, 0, 0},
		{"respond to 93 Doubling Road\x00", addressrole.Unsupported, 0, 0, 0},
		{"respond to 93 Doubling\u200b Road", addressrole.Unsupported, 0, 0, 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r := p.Process(tc.text)
			if r.Transcript != tc.text || r.State != tc.state || r.Interpretation.Primary != nil || len(r.Candidates) != tc.count || len(r.Interpretation.Unresolved) != tc.unresolved || len(r.Interpretation.CrossStreets) != tc.cross {
				t.Fatal("changed unsupported semantics", r)
			}
			verifyEvidence(t, r)
		})
	}
}

// Compare the full native outputs, not just selected fields, so the pipeline
// cannot silently filter evidence, invent states, or apply a second policy.
func TestMatchesExistingAPIs(t *testing.T) {
	p := pipeline(t)
	extractor, err := addresscandidate.New()
	if err != nil {
		t.Fatal(err)
	}
	roles, err := addressrole.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"respond to 30 Edgewood Drive; cross street is Valley Drive; cross street is VALLEY DRIVE",
		"respond to 30 Edgewood Drive; respond to 93 Doubling Road",
		"respond to 30 Edgewood Drive; hydrant at 328 Pemberwick Road",
		"Équipe: RESPOND TO (93 DOUBLING-ROAD); cross streets are Valley Drive and Bedford Road",
		"respond to 55 North Turning Loop Extension", "93.5 Doubling Road", "respond to\n93 Doubling Road", "\xff", "",
	} {
		r := p.Process(text)
		if !reflect.DeepEqual(r.Candidates, extractor.Extract(text)) || !reflect.DeepEqual(r.Interpretation, roles.Interpret(text)) || r.State != r.Interpretation.State || r.Transcript != text {
			t.Fatal("composition changed dependency result", text)
		}
		verifyEvidence(t, r)
	}
}

func TestOriginalBytesAndDeterminism(t *testing.T) {
	p := pipeline(t)
	text := "Équipe: RESPOND TO (93 DOUBLING-ROAD); CROSS STREET IS \"Valley Drive\"."
	original := text
	want := p.Process(text)
	if len(want.Candidates) != 1 || want.Candidates[0].Start != strings.Index(text, "93") || want.Candidates[0].Evidence != "93 DOUBLING-ROAD" || want.Interpretation.CrossStreets[0].Mentions[0].Start != strings.Index(text, "Valley Drive") {
		t.Fatal("original offsets not preserved")
	}
	for i := 0; i < 20; i++ {
		got := p.Process(text)
		if text != original || !reflect.DeepEqual(got, want) {
			t.Fatal("changed transcript or nondeterministic result")
		}
		verifyEvidence(t, got)
	}
}

func TestIndependentCallsAndResults(t *testing.T) {
	p := pipeline(t)
	for _, text := range []string{"93", "Doubling Road", "respond to", "30 Edgewood Drive", "cross street is", "Valley Drive"} {
		r := p.Process(text)
		if r.Interpretation.Primary != nil || len(r.Interpretation.CrossStreets) != 0 {
			t.Fatal("combined calls", r)
		}
	}
	text := "respond to 93 Doubling Road; cross street is Valley Drive"
	want := p.Process(text)
	changed := p.Process(text)
	changed.Candidates[0].CanonicalStreet = "changed"
	changed.Interpretation.Primary.Mentions[0].Address.Evidence = "changed"
	changed.Interpretation.CrossStreets[0].Mentions[0].Text = "changed"
	if !reflect.DeepEqual(p.Process(text), want) {
		t.Fatal("caller mutation affected pipeline state")
	}
	if got := p.Process(""); got.Transcript != "" || got.State != addressrole.NoEvidence || len(got.Candidates) != 0 || got.Interpretation.Primary != nil {
		t.Fatal("prior transcript retained")
	}
}

func TestConcurrentCalls(t *testing.T) {
	p := pipeline(t)
	texts := []string{"respond to 93 Doubling Road", "respond to 30 Edgewood Drive; cross street is Valley Drive", "respond to 93 Doubling Road; respond to 65 Hunting Ridge Road", "hydrant at 328 Pemberwick Road", "93", "Doubling Road", "\xff", ""}
	want := make([]addresspipeline.Result, len(texts))
	for i, text := range texts {
		want[i] = p.Process(text)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repeat := 0; repeat < 10; repeat++ {
				for i, text := range texts {
					if got := p.Process(text); !reflect.DeepEqual(got, want[i]) {
						t.Errorf("concurrent result changed for %q", text)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

func verifyEvidence(t *testing.T, r addresspipeline.Result) {
	t.Helper()
	check := func(text string, start, end int) {
		t.Helper()
		if start < 0 || end > len(r.Transcript) || start >= end || r.Transcript[start:end] != text {
			t.Fatalf("invalid original evidence %q [%d:%d]", text, start, end)
		}
	}
	for i, c := range r.Candidates {
		check(c.Evidence, c.Start, c.End)
		if i > 0 && c.Start < r.Candidates[i-1].End {
			t.Fatal("candidate order/overlap changed")
		}
	}
	primaries := append([]addressrole.Primary{}, r.Interpretation.Alternatives...)
	if r.Interpretation.Primary != nil {
		primaries = append(primaries, *r.Interpretation.Primary)
	}
	for _, p := range primaries {
		for _, m := range p.Mentions {
			check(m.Address.Evidence, m.Address.Start, m.Address.End)
			check(m.Cue.Text, m.Cue.Start, m.Cue.End)
		}
	}
	for _, c := range r.Interpretation.CrossStreets {
		for _, m := range c.Mentions {
			check(m.Text, m.Start, m.End)
		}
		for _, cue := range c.Cues {
			check(cue.Text, cue.Start, cue.End)
		}
	}
	for _, c := range r.Interpretation.Unresolved {
		check(c.Evidence, c.Start, c.End)
	}
}
