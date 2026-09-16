package shadowprocessor

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/addressrole"
	"greenwich-fire-responder/backend/internal/calltypedata"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
	"greenwich-fire-responder/backend/internal/dispatchinterpretation"
	"greenwich-fire-responder/backend/internal/unitpipeline"
	"greenwich-fire-responder/backend/internal/unitrecognition"
)

func fixture(text string) Input {
	return Input{Source: Source{Kind: SourceSynthetic, Reference: " fixture ", TextKind: TextSynthetic}, Transcript: text}
}

func setup(t *testing.T) *Processor {
	t.Helper()
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func completed(t *testing.T, r AuditRecord, input Input, err error) {
	t.Helper()
	want := []StageRecord{{"input", "completed", ""}, {"dispatch", "completed", ""}, {"units", "completed", ""}}
	if err != nil || r.Version != "shadow-processor-v1" || !r.ShadowOnly || r.Input != input || r.Result.Dispatch == nil || r.Result.Units == nil || !reflect.DeepEqual(r.Stages, want) {
		t.Fatalf("audit envelope: %+v, %v", r, err)
	}
}

func TestConstruction(t *testing.T) {
	for _, failure := range []string{"", "dispatch", "units"} {
		var calls []string
		p, err := newProcessor(func() (*dispatchinterpretation.Pipeline, error) {
			calls = append(calls, "dispatch")
			if failure == "dispatch" {
				return nil, errors.New("sensitive constructor payload")
			}
			return dispatchinterpretation.New()
		}, func() (*unitpipeline.Pipeline, error) {
			calls = append(calls, "units")
			if failure == "units" {
				return nil, errors.New("sensitive constructor payload")
			}
			return unitpipeline.New()
		})
		want := []string{"dispatch", "units"}
		if failure == "dispatch" {
			want = want[:1]
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatal(calls)
		}
		if failure == "" {
			if err != nil || p == nil {
				t.Fatal("construction failed")
			}
		} else if p != nil || err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatal("partial processor or unsafe error")
		}
	}
}

func TestValidationBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*Input)
	}{
		{"empty reference", "missing_source_reference", func(i *Input) { i.Source.Reference = "" }},
		{"blank reference", "missing_source_reference", func(i *Input) { i.Source.Reference = " \t\r\n\u2003" }},
		{"missing kind", "invalid_source_kind", func(i *Input) { i.Source.Kind = "" }},
		{"unknown kind", "invalid_source_kind", func(i *Input) { i.Source.Kind = "Replay" }},
		{"missing text kind", "invalid_text_kind", func(i *Input) { i.Source.TextKind = "" }},
		{"unknown text kind", "invalid_text_kind", func(i *Input) { i.Source.TextKind = "RAW_MODEL" }},
		{"oversize", "transcript_too_large", func(i *Input) { i.Transcript = strings.Repeat("x", 65537) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := fixture("sensitive synthetic transcript")
			tc.change(&i)
			r, err := process(i, func(string) dispatchinterpretation.Result {
				t.Fatal("dispatch executed")
				return dispatchinterpretation.Result{}
			}, func(string) unitpipeline.Result { t.Fatal("units executed"); return unitpipeline.Result{} })
			want := []StageRecord{{"input", "failed", tc.code}, {"dispatch", "skipped", ""}, {"units", "skipped", ""}}
			if err == nil || err.Error() != "shadowprocessor: "+tc.code || r.Input != i || !r.ShadowOnly || r.Version != "shadow-processor-v1" || r.Result != (Result{}) || !reflect.DeepEqual(r.Stages, want) {
				t.Fatal("validation contract")
			}
		})
	}
}

func TestSizeBoundariesAndProvenance(t *testing.T) {
	p := setup(t)
	for _, size := range []int{0, 65535, 65536, 65537} {
		// Multibyte text proves the limit counts bytes; put evidence at the end.
		text := ""
		if size > 0 {
			tail := "; engine 2 on scene"
			n := size - len(tail)
			text = strings.Repeat("\u00e9", n/2) + strings.Repeat("x", n%2) + tail
		}
		i := fixture(text)
		r, err := p.Process(i)
		if len(text) != size || r.Input != i {
			t.Fatal("input truncated")
		}
		if size <= 65536 {
			completed(t, r, i, err)
			if r.Result.Dispatch.Transcript != text || r.Result.Units.Transcript != text {
				t.Fatal("native truncation")
			}
		} else if err == nil || r.Stages[0].FailureCode != "transcript_too_large" || r.Result != (Result{}) {
			t.Fatal("oversize accepted")
		}
	}
	i := fixture("engine 2 on scene")
	want, _ := p.Process(i)
	for _, kind := range []SourceKind{SourceSynthetic, SourceReplay} {
		for _, textKind := range []TextKind{TextSynthetic, TextRawModel, TextNormalized, TextHumanReference} {
			i.Source = Source{kind, " duplicate ", textKind, "recording-id", "attempt-id", "opaque-model"}
			r, err := p.Process(i)
			completed(t, r, i, err)
			if !reflect.DeepEqual(r.Result, want.Result) {
				t.Fatal("provenance affected interpretation")
			}
		}
	}
}

func parityFixtures(t *testing.T) []string {
	t.Helper()
	texts := []string{
		"", "engine", "2 on scene", "unknown", "93", "Doubling Road", "gas leak", "working fire", "smoke activation",
		"respond to 93 Doubling Road; respond gas leak", "respond to a residential alarm at 93 Doubling Road",
		"respond gas leak", "respond to 93 Doubling Road", "93 Doubling Road; gas leak",
		"respond to 93 Doubling Road; respond to 93 Doubling Road; cross street is Valley Drive; hydrant at 328 Pemberwick Road; 999999",
		"respond to 93 Doubling Road; respond to 30 Edgewood Drive",
		"respond gas leak; respond wires down",
		"respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond gas leak",
		"respond to 93 Doubling Road; respond gas leak; respond wires down",
		"respond to 93 Doubling Road; respond to 30 Edgewood Drive; respond gas leak; respond wires down",
		"respond residential alarm, smoke activation; minor alarm; gas leak",
		"respond residential alarm, general fire activation; respond commercial alarm",
		"engine 2 on scene", "engine 2 CLEAR", "engine 2 on air; engine 5 training",
		"engine 2 and engine 5 are on scene", "engine 2 and engine 5 and squad 8, are training",
		"engine 2 and engine 2 on scene", "engine 2, engine 5 on scene", "engine 2 or engine 5 on scene",
		"engine 2 and engine 20 on scene", "engine 2 and engine 5 is on scene",
		"engine 2 and engine 5 on scene; engine 2 responding", "engine 2 on scene; engine 2 on scene",
		"engine 2 on scene!? engine 5 responding.", "engine 2 on scene?!", "engine 2 on scene...?",
		"engine 2 not on scene", "engine 2 return to quarters", "engine 2 report on scene", "engine 2 good copy on scene",
		"yesterday engine 2 on scene", "if engine 2 is available", "\"engine 2 on scene\"",
		"\u201cengine 2 on scene\u201d", "\"engine 2 on scene; engine 5 responding",
		"we discussed engine 2 on scene", "engine 2 last unit on scene",
		"engine 21 and engine 51 on scene; tanker 17 and rescue 51 responding", "engine 2V on scene", "engine 2 10-230",
		"engine-2 on scene", "engine 2 on,scene", "engine + 2 on scene", "engine two on scene", "E2 EN_ROUTE ON_SCENE ACTIVE",
		"\u00e9; engine 2 on scene", "\U0001f692; ENGINE\t2, ON\tSCENE", "yesterday engine 2\nengine 5 on scene",
	}
	for _, prefix := range []string{"not ", "yesterday ", "if ", "report ", "good copy ", "\""} {
		texts = append(texts, prefix+"respond gas leak", prefix+"respond to 93 Doubling Road")
	}
	for _, sep := range []string{".", ";", ":", "!", "?", "\r", "\n", "\r\n"} {
		texts = append(texts, "engine 2"+sep+"on scene", "engine 2 and engine 5"+sep+"on scene", "engine 2 on scene"+sep+"engine 5 responding", "respond to 93 Doubling Road"+sep+"respond gas leak")
	}
	for _, bad := range []string{"\xff", "\xc0\xaf", "\xed\xa0\x80", "\x7f", "\u0085", "\ufeff", "\u200b", "\u200d", "\u202e", "\u2066", "\u2028", "\u2029"} {
		texts = append(texts, bad+"; engine 2 on scene; respond gas leak")
	}
	for c := byte(0); c < 32; c++ {
		if c != '\t' && c != '\r' && c != '\n' {
			texts = append(texts, string([]byte{c})+"; engine 2 on scene")
		}
	}
	catalog, err := calltypedata.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Phrases()) != 42 {
		t.Fatal("call-type contract changed")
	}
	for _, phrase := range catalog.Phrases() {
		texts = append(texts, phrase.Exact, "respond to "+phrase.Exact, "not "+phrase.Exact, strings.ToUpper(phrase.Exact), phrase.Exact+"; "+phrase.Exact)
	}
	u, err := unitrecognition.New()
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Catalog()) != 26 {
		t.Fatal("unit contract changed")
	}
	for _, unit := range u.Catalog() {
		texts = append(texts, unit.Phrase, strings.ToUpper(unit.Phrase), unit.Phrase+" on scene")
	}
	statuses := []string{"dispatched", "responding", "en route", "in route", "enroute", "on the way", "on scene", "on location", "arrived", "10-23", "returning", "returning to quarters", "RTQ", "back to quarters", "available", "in service", "back in service", "out of service", "on air", "training"}
	for _, status := range statuses {
		texts = append(texts, status, "engine 2 "+status, "engine 2 and engine 5 "+status, "ENGINE\t2, "+strings.ToUpper(strings.ReplaceAll(status, " ", "\t")))
	}
	return texts
}

func TestCompleteNativeParity(t *testing.T) {
	p := setup(t)
	d, err := dispatchinterpretation.New()
	if err != nil {
		t.Fatal(err)
	}
	u, err := unitpipeline.New()
	if err != nil {
		t.Fatal(err)
	}
	states := map[dispatchinterpretation.ShadowState]bool{}
	for _, text := range parityFixtures(t) {
		i := fixture(text)
		r, err := p.Process(i)
		completed(t, r, i, err)
		if !reflect.DeepEqual(*r.Result.Dispatch, d.Process(text)) || !reflect.DeepEqual(*r.Result.Units, u.Process(text)) {
			t.Fatalf("native parity for %q", text)
		}
		states[r.Result.Dispatch.ShadowState] = true
		checkSpans(t, text, reflect.ValueOf(r.Result))
		for _, a := range r.Result.Units.UnitStatus.Associations {
			if a.StatusIndex < 0 || a.StatusIndex >= len(r.Result.Units.UnitStatus.StatusEvidence) || a.Status != r.Result.Units.UnitStatus.StatusEvidence[a.StatusIndex] {
				t.Fatal("StatusIndex changed")
			}
		}
	}
	if len(states) != 5 {
		t.Fatalf("summary state coverage: %v", states)
	}
}

// Recursively verify all native evidence/span structs against unchanged bytes.
func checkSpans(t *testing.T, text string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			checkSpans(t, text, v.Elem())
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			checkSpans(t, text, v.Index(i))
		}
	case reflect.Struct:
		start, end := v.FieldByName("Start"), v.FieldByName("End")
		if start.IsValid() && end.IsValid() {
			evidence := v.FieldByName("Evidence")
			if !evidence.IsValid() {
				evidence = v.FieldByName("Text")
			}
			if evidence.IsValid() && evidence.Kind() == reflect.String {
				a, b := int(start.Int()), int(end.Int())
				if a < 0 || b < a || b > len(text) || text[a:b] != evidence.String() {
					t.Fatalf("changed byte span %d:%d", a, b)
				}
			}
		}
		for i := 0; i < v.NumField(); i++ {
			checkSpans(t, text, v.Field(i))
		}
	}
}

func TestDocumentOffsetsAndDisagreement(t *testing.T) {
	p := setup(t)
	for _, prefix := range []string{"", "\u00e9; ", "\U0001f692; "} {
		r, err := p.Process(fixture(prefix + "engine 2 on scene"))
		if err != nil {
			t.Fatal(err)
		}
		u, s := r.Result.Units.Units, r.Result.Units.UnitStatus
		if u.State != unitrecognition.Unresolved || u.Mentions[0].Accepted || u.Mentions[0].Reason != "unsupported_context" || s.Associations[0].Unit == nil {
			t.Fatal("native disagreement lost")
		}
		m, a := u.Mentions[0], s.Associations[0]
		n := len(prefix)
		if m.Start != n || m.End != n+8 || a.Unit.Start != n || a.Unit.End != n+8 || a.Status.Start != n+9 || a.Status.End != n+17 {
			t.Fatal("unit offsets")
		}
		r, err = p.Process(fixture(prefix + "respond to 93 Doubling Road; respond gas leak"))
		if err != nil {
			t.Fatal(err)
		}
		d := r.Result.Dispatch
		if d.Address.Candidates[0].Start != n+11 || d.Address.Candidates[0].End != n+27 || d.CallType.Candidates[0].Start != n+37 || d.CallType.Candidates[0].End != n+45 {
			t.Fatal("dispatch offsets")
		}
	}
}

// Populate every native field, including alternatives, candidates, pointers and
// future strings. This guards complete forwarding without reproducing schemas.
func populate(v reflect.Value, empty bool) {
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		populate(v.Elem(), empty)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			populate(v.Field(i), empty)
		}
	case reflect.Slice:
		n := 2
		if empty {
			n = 0
		}
		v.Set(reflect.MakeSlice(v.Type(), n, n))
		for i := 0; i < n; i++ {
			populate(v.Index(i), empty)
		}
	case reflect.String:
		v.SetString("future_native_value")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int:
		v.SetInt(17)
	}
}

func TestExecutionOrderAndCompleteForwarding(t *testing.T) {
	for _, mode := range []string{"nil", "empty", "populated"} {
		d, u := dispatchinterpretation.Result{}, unitpipeline.Result{}
		if mode != "nil" {
			populate(reflect.ValueOf(&d).Elem(), mode == "empty")
			populate(reflect.ValueOf(&u).Elem(), mode == "empty")
		}
		i := fixture("\tSensitive synthetic \xff\r\n")
		var calls []string
		r, err := process(i, func(s string) dispatchinterpretation.Result {
			if s != i.Transcript {
				t.Fatal("dispatch input changed")
			}
			calls = append(calls, "dispatch")
			return d
		}, func(s string) unitpipeline.Result {
			if s != i.Transcript {
				t.Fatal("units input changed")
			}
			calls = append(calls, "units")
			return u
		})
		completed(t, r, i, err)
		if !reflect.DeepEqual(calls, []string{"dispatch", "units"}) || !reflect.DeepEqual(*r.Result.Dispatch, d) || !reflect.DeepEqual(*r.Result.Units, u) {
			t.Fatal("forwarding changed native data")
		}
		if mode == "populated" && &r.Result.Units.Units.Mentions[0] != &u.Units.Mentions[0] {
			t.Fatal("native result was reconstructed or cloned")
		}
	}
	// Some native summary combinations cannot arise from text today. Forward all
	// 12 existing state pairs without interpreting or recomputing their summary.
	for _, a := range []addressrole.State{addressrole.Resolved, addressrole.Unsupported, addressrole.NoEvidence, addressrole.Ambiguous} {
		for _, c := range []calltypeinterpret.State{calltypeinterpret.Resolved, calltypeinterpret.Unresolved, calltypeinterpret.Ambiguous} {
			d := dispatchinterpretation.Result{AddressState: a, CallTypeState: c, ShadowState: "opaque_native_summary"}
			r, err := process(fixture(""), func(string) dispatchinterpretation.Result { return d }, func(string) unitpipeline.Result { return unitpipeline.Result{} })
			if err != nil || !reflect.DeepEqual(*r.Result.Dispatch, d) {
				t.Fatal("native state pair changed")
			}
		}
	}
}

func TestPanicIsolation(t *testing.T) {
	p := setup(t)
	i := fixture("sensitive synthetic transcript; engine 2 on scene")
	want, _ := p.Process(i)
	for _, payload := range []any{"sensitive panic payload", errors.New("sensitive error"), nil} {
		for _, mask := range []int{1, 2, 3} {
			var calls []string
			r, err := process(i, func(s string) dispatchinterpretation.Result {
				calls = append(calls, "dispatch")
				if s != i.Transcript {
					t.Fatal("changed input")
				}
				if mask&1 != 0 {
					panic(payload)
				}
				return p.dispatch.Process(s)
			}, func(s string) unitpipeline.Result {
				calls = append(calls, "units")
				if s != i.Transcript {
					t.Fatal("changed input")
				}
				if mask&2 != 0 {
					panic(payload)
				}
				return p.units.Process(s)
			})
			if err == nil || err.Error() != "shadowprocessor: native_stage_panicked" || r.Input != i || !r.ShadowOnly || r.Version != "shadow-processor-v1" || !reflect.DeepEqual(calls, []string{"dispatch", "units"}) {
				t.Fatal("panic boundary")
			}
			stages := []StageRecord{{"input", "completed", ""}, {"dispatch", "completed", ""}, {"units", "completed", ""}}
			if mask&1 != 0 {
				stages[1] = StageRecord{"dispatch", "failed", "native_stage_panicked"}
				if r.Result.Dispatch != nil {
					t.Fatal("unreturned dispatch exposed")
				}
			} else if !reflect.DeepEqual(r.Result.Dispatch, want.Result.Dispatch) {
				t.Fatal("dispatch lost")
			}
			if mask&2 != 0 {
				stages[2] = StageRecord{"units", "failed", "native_stage_panicked"}
				if r.Result.Units != nil {
					t.Fatal("unreturned units exposed")
				}
			} else if !reflect.DeepEqual(r.Result.Units, want.Result.Units) {
				t.Fatal("units lost")
			}
			if !reflect.DeepEqual(r.Stages, stages) {
				t.Fatal("panic stages")
			}
		}
	}
}

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
		v.SetString("mutated")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int:
		v.SetInt(-1)
	}
}

func TestOwnershipReplayAndThousandConcurrentCalls(t *testing.T) {
	p := setup(t)
	// The caller retains duplicate replay records, including duplicate references.
	var replay []AuditRecord
	for _, text := range []string{"engine 2 on scene", "engine 5 training", "engine 2 on scene"} {
		input := fixture(text)
		input.Source.Kind = SourceReplay
		input.Source.TextKind = TextRawModel
		r, err := p.Process(input)
		completed(t, r, input, err)
		replay = append(replay, r)
	}
	if len(replay) != 3 || !reflect.DeepEqual(replay[0], replay[2]) || replay[1].Input.Transcript != "engine 5 training" {
		t.Fatal("duplicate replay records or caller order lost")
	}
	mutate(reflect.ValueOf(&replay[0]).Elem())
	if replay[2].Input.Transcript != "engine 2 on scene" || replay[2].Stages[0].Stage != "input" {
		t.Fatal("duplicate replay ownership")
	}
	texts := parityFixtures(t)
	wants := make([]AuditRecord, len(texts))
	for i, text := range texts {
		input := fixture(text)
		wants[i], _ = p.Process(input)
		changed, _ := p.Process(input)
		mutate(reflect.ValueOf(&changed.Result.Dispatch.Address).Elem())
		if !reflect.DeepEqual(changed.Result.Dispatch.CallType, wants[i].Result.Dispatch.CallType) {
			t.Fatal("address contaminated call type")
		}
		changed, _ = p.Process(input)
		mutate(reflect.ValueOf(&changed.Result.Dispatch.CallType).Elem())
		if !reflect.DeepEqual(changed.Result.Dispatch.Address, wants[i].Result.Dispatch.Address) {
			t.Fatal("call type contaminated address")
		}
		changed, _ = p.Process(input)
		mutate(reflect.ValueOf(changed.Result.Dispatch).Elem())
		if !reflect.DeepEqual(changed.Result.Units, wants[i].Result.Units) {
			t.Fatal("outer branch contamination")
		}
		changed, _ = p.Process(input)
		mutate(reflect.ValueOf(&changed.Result.Units.Units).Elem())
		if !reflect.DeepEqual(changed.Result.Units.UnitStatus, wants[i].Result.Units.UnitStatus) {
			t.Fatal("unit branch contamination")
		}
		changed, _ = p.Process(input)
		mutate(reflect.ValueOf(&changed.Result.Units.UnitStatus).Elem())
		if !reflect.DeepEqual(changed.Result.Units.Units, wants[i].Result.Units.Units) || !reflect.DeepEqual(changed.Result.Dispatch, wants[i].Result.Dispatch) {
			t.Fatal("status branch contamination")
		}
		mutate(reflect.ValueOf(&changed).Elem())
		for repeat := 0; repeat < 3; repeat++ {
			got, err := p.Process(input)
			if err != nil || !reflect.DeepEqual(got, wants[i]) {
				t.Fatal("call or matcher contamination")
			}
		}
	}
	// Caller-owned positions retain duplicate references and input order.
	results := make([]AuditRecord, 1000)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			k := i % len(texts)
			r, err := p.Process(fixture(texts[k]))
			if err != nil || !reflect.DeepEqual(r, wants[k]) {
				t.Error("concurrent parity")
			}
			results[i] = r
			other, _ := p.Process(fixture(texts[k]))
			mutate(reflect.ValueOf(&other).Elem())
		}(i)
	}
	close(start)
	wg.Wait()
	for i, got := range results {
		if !reflect.DeepEqual(got, wants[i%len(texts)]) {
			t.Fatal("caller ordering or concurrent ownership")
		}
	}
	for i, text := range texts {
		got, _ := p.Process(fixture(text))
		if !reflect.DeepEqual(got, wants[i]) {
			t.Fatal("concurrent mutation leaked")
		}
	}
	// Separate transmissions must never complete one another.
	for _, text := range []string{"93", "Doubling Road", "engine", "2 on scene"} {
		r, _ := p.Process(fixture(text))
		if r.Result.Dispatch.AddressState == addressrole.Resolved || r.Result.Units.UnitStatus.State == "resolved" {
			t.Fatal("joined calls")
		}
	}
}
