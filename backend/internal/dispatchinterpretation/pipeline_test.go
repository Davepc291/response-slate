package dispatchinterpretation

import (
	"greenwich-fire-responder/backend/internal/addresspipeline"
	"greenwich-fire-responder/backend/internal/addressrole"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
	"greenwich-fire-responder/backend/internal/calltypepipeline"
	"reflect"
	"sync"
	"testing"
)

func setup(t *testing.T) *Pipeline {
	t.Helper()
	p, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestMatrix(t *testing.T) {
	addresses := []addressrole.State{addressrole.Resolved, addressrole.Unsupported, addressrole.NoEvidence, addressrole.Ambiguous}
	types := []calltypeinterpret.State{calltypeinterpret.Resolved, calltypeinterpret.Unresolved, calltypeinterpret.Ambiguous}
	want := [][]ShadowState{
		{BothResolved, AddressOnlyResolved, ContainsAmbiguity},
		{TypeOnlyResolved, NeitherResolved, ContainsAmbiguity},
		{TypeOnlyResolved, NeitherResolved, ContainsAmbiguity},
		{ContainsAmbiguity, ContainsAmbiguity, ContainsAmbiguity},
	}
	for i, a := range addresses {
		for j, c := range types {
			t.Run(string(a)+"/"+string(c), func(t *testing.T) {
				if summarize(a, c) != want[i][j] {
					t.Fatal("matrix mismatch")
				}
			})
		}
	}
	// Some combinations cannot currently arise from one native transcript.
	// Exercise the complete summary contract directly rather than fake evidence.
	for _, pair := range []struct {
		a addressrole.State
		c calltypeinterpret.State
	}{{"future", calltypeinterpret.Resolved}, {addressrole.Resolved, "future"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("unknown state silently mapped")
				}
			}()
			summarize(pair.a, pair.c)
		}()
	}
}

var fixtures = []struct {
	text string
	a    addressrole.State
	c    calltypeinterpret.State
	s    ShadowState
}{
	{"respond to 93 Doubling Road; respond gas leak", addressrole.Resolved, calltypeinterpret.Resolved, BothResolved},
	{"respond to a residential alarm at 93 Doubling Road", addressrole.Resolved, calltypeinterpret.Unresolved, AddressOnlyResolved},
	{"respond gas leak", addressrole.Unsupported, calltypeinterpret.Resolved, TypeOnlyResolved},
	{"respond to 93 Doubling Road; respond to 30 Edgewood Drive", addressrole.Ambiguous, calltypeinterpret.Unresolved, ContainsAmbiguity},
	{"respond gas leak; respond wires down", addressrole.Unsupported, calltypeinterpret.Ambiguous, ContainsAmbiguity},
	{"working fire", addressrole.NoEvidence, calltypeinterpret.Unresolved, NeitherResolved},
	{"smoke activation", addressrole.NoEvidence, calltypeinterpret.Unresolved, NeitherResolved},
	{"93 Doubling Road; 30 Edgewood Drive; gas leak; wires down", addressrole.Unsupported, calltypeinterpret.Unresolved, NeitherResolved},
	{"respond to 93 Doubling Road", addressrole.Resolved, calltypeinterpret.Unresolved, AddressOnlyResolved},
	{"respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond gas leak", addressrole.Ambiguous, calltypeinterpret.Resolved, ContainsAmbiguity},
	{"respond to 93 Doubling Road; respond gas leak; respond wires down", addressrole.Resolved, calltypeinterpret.Ambiguous, ContainsAmbiguity},
	{"respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond gas leak; respond wires down", addressrole.Ambiguous, calltypeinterpret.Ambiguous, ContainsAmbiguity},
}

func TestFixturesAndNativeParity(t *testing.T) {
	p := setup(t)
	a, e := addresspipeline.New()
	if e != nil {
		t.Fatal(e)
	}
	c, e := calltypepipeline.New()
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range fixtures {
		t.Run(f.text, func(t *testing.T) {
			r := p.Process(f.text)
			if r.Version != "dispatch-interpretation-v1" || !r.ShadowOnly || r.Transcript != f.text ||
				r.AddressState != f.a || r.CallTypeState != f.c || r.ShadowState != f.s {
				t.Fatalf("%+v", r)
			}
			if !reflect.DeepEqual(r.Address, a.Process(f.text)) || !reflect.DeepEqual(r.CallType, c.Process(f.text)) {
				t.Fatal("native output changed")
			}
			check(t, r)
		})
	}
	r := p.Process(fixtures[0].text)
	if r.Address.Candidates[0].Start != 11 || r.Address.Candidates[0].End != 27 ||
		r.CallType.Candidates[0].Start != 37 || r.CallType.Candidates[0].End != 45 {
		t.Fatal("approved spans")
	}
	if p.Process(fixtures[1].text).CallType.Interpretation.CallTypes[0].RejectionReason != "unsupported_dispatch_suffix" {
		t.Fatal("diagnostic lost")
	}
}
func check(t *testing.T, r Result) {
	t.Helper()
	if r.Address.Transcript != r.Transcript || r.CallType.Transcript != r.Transcript || r.CallType.Interpretation.Transcript != r.Transcript ||
		r.AddressState != r.Address.State || r.Address.State != r.Address.Interpretation.State ||
		r.CallTypeState != r.CallType.State || r.CallType.State != r.CallType.Interpretation.State {
		t.Fatal("mirrors")
	}
	span := func(s string, a, b int) {
		t.Helper()
		if a < 0 || b > len(r.Transcript) || a >= b || r.Transcript[a:b] != s {
			t.Fatal("byte evidence")
		}
	}
	for _, v := range r.Address.Candidates {
		span(v.Evidence, v.Start, v.End)
	}
	ps := append([]addressrole.Primary{}, r.Address.Interpretation.Alternatives...)
	if r.Address.Interpretation.Primary != nil {
		ps = append(ps, *r.Address.Interpretation.Primary)
	}
	for _, p := range ps {
		for _, m := range p.Mentions {
			span(m.Address.Evidence, m.Address.Start, m.Address.End)
			span(m.Cue.Text, m.Cue.Start, m.Cue.End)
		}
	}
	for _, v := range r.Address.Interpretation.Unresolved {
		span(v.Evidence, v.Start, v.End)
	}
	for _, v := range r.Address.Interpretation.CrossStreets {
		for _, m := range v.Mentions {
			span(m.Text, m.Start, m.End)
		}
		for _, m := range v.Cues {
			span(m.Text, m.Start, m.End)
		}
	}
	for _, v := range r.CallType.Candidates {
		span(v.Evidence, v.Start, v.End)
	}
	for _, list := range [][]calltypeinterpret.Evidence{r.CallType.Interpretation.CallTypes, r.CallType.Interpretation.AlarmLevels, r.CallType.Interpretation.Qualifiers} {
		for _, v := range list {
			span(v.Evidence, v.Start, v.End)
		}
	}
}

func TestInvalidIsolationAndDeterminism(t *testing.T) {
	p := setup(t)
	for _, s := range []string{"\xff", "respond gas leak\x00", "respond to 93 Doubling Road\u200b"} {
		r := p.Process(s)
		if r.Transcript != s || r.AddressState != addressrole.Unsupported || r.CallTypeState != calltypeinterpret.Unresolved || r.Address.Interpretation.Reason == "" {
			t.Fatal("invalid input changed")
		}
		check(t, r)
	}
	for _, s := range []string{"93", "Doubling Road", "gas leak", "alarm", "CO alarm", "I-95", "Merritt Parkway", "Engine 5", ""} {
		r := p.Process(s)
		if r.ShadowState != NeitherResolved {
			t.Fatal("inferred data")
		}
	}
	for _, s := range []string{fixtures[8].text, fixtures[2].text} {
		r := p.Process(s)
		if r.ShadowState == BothResolved {
			t.Fatal("combined transmissions")
		}
	}
	text := "é 🚒; respond to 93 Doubling Road; cross street is Valley Drive; respond gas leak"
	want := p.Process(text)
	check(t, want)
	for n := 0; n < 20; n++ {
		if !reflect.DeepEqual(want, p.Process(text)) {
			t.Fatal("nondeterministic")
		}
	}
}

// Mutate every reachable exported string and slice element, including nested
// pointer-backed primary mentions, without changing the expected snapshots.
func mutate(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			mutate(v.Elem())
		}
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			mutate(v.Field(n))
		}
	case reflect.Slice:
		for n := 0; n < v.Len(); n++ {
			mutate(v.Index(n))
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	case reflect.Bool:
		if v.CanSet() {
			v.SetBool(false)
		}
	case reflect.Int:
		if v.CanSet() {
			v.SetInt(-1)
		}
	}
}
func TestMutationAndThousandConcurrentCalls(t *testing.T) {
	p := setup(t)
	texts := []string{
		"respond to 93 Doubling Road; cross street is Valley Drive; hydrant at 328 Pemberwick Road; respond residential alarm, smoke activation; minor alarm; gas leak",
		"respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond gas leak; respond wires down",
		"", "respond gas leak", "93", "Doubling Road", "\xff",
	}
	wants := make([]Result, len(texts))
	for n, s := range texts {
		wants[n] = p.Process(s)
		changed := p.Process(s)
		mutate(reflect.ValueOf(&changed).Elem())
		if !reflect.DeepEqual(wants[n], p.Process(s)) {
			t.Fatal("mutation leak")
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 1000; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			k := n % len(texts)
			got := p.Process(texts[k])
			if !reflect.DeepEqual(got, wants[k]) {
				t.Error("concurrent mismatch")
			}
			mutate(reflect.ValueOf(&got).Elem())
		}(n)
	}
	close(start)
	wg.Wait()
	for n, s := range texts {
		if !reflect.DeepEqual(wants[n], p.Process(s)) {
			t.Fatal("concurrent mutation leak")
		}
	}
}
