package shadowreplay

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"greenwich-fire-responder/backend/internal/addressrole"
	"greenwich-fire-responder/backend/internal/calltypedata"
	"greenwich-fire-responder/backend/internal/dispatchinterpretation"
	"greenwich-fire-responder/backend/internal/shadowprocessor"
	"greenwich-fire-responder/backend/internal/unitpipeline"
	"greenwich-fire-responder/backend/internal/unitrecognition"
)

func fixture(text string) shadowprocessor.Input {
	return shadowprocessor.Input{Source: shadowprocessor.Source{Kind: shadowprocessor.SourceSynthetic, Reference: " fixture ", TextKind: shadowprocessor.TextSynthetic}, Transcript: text}
}

func setup(t *testing.T) *Runner {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func setupProcessor(t *testing.T) *shadowprocessor.Processor {
	t.Helper()
	p, err := shadowprocessor.New()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func counted(kind, ref, textKind, recording, attempt, model, transcript string) shadowprocessor.Input {
	return shadowprocessor.Input{
		Source: shadowprocessor.Source{
			Kind:         shadowprocessor.SourceKind(kind),
			Reference:    ref,
			TextKind:     shadowprocessor.TextKind(textKind),
			RecordingRef: recording,
			AttemptRef:   attempt,
			Model:        model,
		},
		Transcript: transcript,
	}
}

func fieldBytes(in shadowprocessor.Input) int {
	return len(in.Source.Kind) + len(in.Source.Reference) + len(in.Source.TextKind) + len(in.Source.RecordingRef) + len(in.Source.AttemptRef) + len(in.Source.Model) + len(in.Transcript)
}

func sized(total int) shadowprocessor.Input {
	in := counted("synthetic", "ref", "synthetic", "rec", "att", "mod", "")
	used := fieldBytes(in)
	if total < used {
		panic("sized fixture smaller than metadata")
	}
	in.Transcript = strings.Repeat("a", total-used)
	return in
}

func nInputs(n int, text string) []shadowprocessor.Input {
	out := make([]shadowprocessor.Input, n)
	for i := range out {
		out[i] = fixture(text)
		out[i].Source.Reference = "ref"
	}
	return out
}

func TestConstruction(t *testing.T) {
	r, err := New()
	if err != nil || r == nil || r.processor == nil {
		t.Fatal("public construction failed")
	}
	for _, fail := range []bool{true, false} {
		var calls int
		got, err := newRunner(func() (*shadowprocessor.Processor, error) {
			calls++
			if fail {
				return nil, errors.New("sensitive constructor payload transcript panic stack")
			}
			return shadowprocessor.New()
		})
		if calls != 1 {
			t.Fatal(calls)
		}
		if fail {
			if got != nil || err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "transcript") || strings.Contains(err.Error(), "panic") {
				t.Fatal("partial runner or unsafe constructor error")
			}
		} else if err != nil || got == nil || got.processor == nil {
			t.Fatal("construction failed")
		}
	}
	got, err := newRunner(func() (*shadowprocessor.Processor, error) { return nil, nil })
	if got != nil || err == nil {
		t.Fatal("nil processor accepted")
	}
}

func TestNilEmptyAndSingleton(t *testing.T) {
	r := setup(t)
	for _, inputs := range [][]shadowprocessor.Input{nil, {}, nInputs(1, "engine 2 on scene")} {
		var calls int
		records, err := run(inputs, func(in shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
			calls++
			return r.processor.Process(in)
		})
		if records == nil || err != nil {
			t.Fatalf("empty contract: %v %v", records, err)
		}
		if len(inputs) == 0 {
			if len(records) != 0 || calls != 0 {
				t.Fatal("nil/empty processed")
			}
			continue
		}
		if calls != 1 || len(records) != 1 {
			t.Fatal("singleton")
		}
		want, werr := r.processor.Process(inputs[0])
		if werr != nil || !reflect.DeepEqual(records[0], want) {
			t.Fatal("singleton parity")
		}
	}
	public, err := r.Run(nil)
	if err != nil || public == nil || len(public) != 0 {
		t.Fatal("public nil")
	}
	public, err = r.Run([]shadowprocessor.Input{})
	if err != nil || public == nil || len(public) != 0 {
		t.Fatal("public empty")
	}
}

func TestRecordCountAdmission(t *testing.T) {
	r := setup(t)
	for _, n := range []int{1, 999, 1000} {
		inputs := nInputs(n, "x")
		var calls int
		records, err := run(inputs, func(in shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
			calls++
			return r.processor.Process(in)
		})
		if err != nil || calls != n || len(records) != n {
			t.Fatalf("count %d: calls=%d err=%v", n, calls, err)
		}
		want, _ := r.processor.Process(inputs[0])
		if !reflect.DeepEqual(records[0], want) || !reflect.DeepEqual(records[n-1], want) {
			t.Fatal("count parity")
		}
	}
	var calls int
	records, err := run(nInputs(1001, "x"), func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
		calls++
		t.Fatal("prefix processed")
		return shadowprocessor.AuditRecord{}, nil
	})
	if records != nil || err == nil || err.Error() != "shadowreplay: too many input records" || calls != 0 {
		t.Fatal("1001 not rejected before processing")
	}
}

func TestStringByteAdmission(t *testing.T) {
	r := setup(t)
	for _, total := range []int{4194303, 4194304} {
		inputs := []shadowprocessor.Input{sized(total)}
		if fieldBytes(inputs[0]) != total {
			t.Fatal("metadata omitted from total")
		}
		var calls int
		records, err := run(inputs, func(in shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
			calls++
			return r.processor.Process(in)
		})
		if calls != 1 || records == nil || len(records) != 1 {
			t.Fatalf("byte boundary %d not admitted", total)
		}
		want, werr := r.processor.Process(inputs[0])
		if !reflect.DeepEqual(records[0], want) || (err == nil) != (werr == nil) {
			t.Fatal("admitted byte boundary parity")
		}
		if total == 4194304 && (err == nil || err.Error() != "shadowreplay: one or more records failed") {
			t.Fatal("exact 4MiB still subject to processor transcript limit")
		}
	}
	var calls int
	records, err := run([]shadowprocessor.Input{sized(4194305)}, func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
		calls++
		t.Fatal("over-budget processed")
		return shadowprocessor.AuditRecord{}, nil
	})
	if records != nil || err == nil || err.Error() != "shadowreplay: input string bytes exceed limit" || calls != 0 {
		t.Fatal("4,194,305 not rejected")
	}
}

func TestSevenFieldsContribute(t *testing.T) {
	blank, ok := chargeInput(10, shadowprocessor.Input{})
	if !ok || blank != 10 {
		t.Fatal("blank fields")
	}
	in := counted("ab", "cd", "ef", "gh", "ij", "kl", "mn")
	got, ok := chargeInput(100, in)
	if !ok || got != 86 || fieldBytes(in) != 14 {
		t.Fatal("seven fields")
	}
	named := counted(string(shadowprocessor.SourceReplay), "ref", string(shadowprocessor.TextHumanReference), "", "", "", "")
	if fieldBytes(named) != len(shadowprocessor.SourceReplay)+len("ref")+len(shadowprocessor.TextHumanReference) {
		t.Fatal("named string kinds")
	}
	unknown := counted("Replay", "x", "RAW_MODEL", "rec", "att", "mod", "txt")
	if fieldBytes(unknown) != 6+1+9+3+3+3+3 {
		t.Fatal("unknown values")
	}
	shared := strings.Repeat("z", 50)
	a, b := fixture(shared), fixture(shared)
	remaining, ok := chargeInput(200, a)
	remaining, ok2 := chargeInput(remaining, b)
	if !ok || !ok2 || remaining != 200-2*fieldBytes(a) {
		t.Fatal("shared storage counted twice")
	}
	dup := []shadowprocessor.Input{fixture("same"), fixture("same")}
	if admit(dup) != nil {
		t.Fatal("duplicates")
	}
	multi := counted("\u00e9", "\U0001f692", "\u00e9", "\xff", "\x01", "\xed\xa0\x80", "\xc0\xaf")
	if fieldBytes(multi) != 2+4+2+1+1+3+2 {
		t.Fatal("multibyte/invalid/control bytes")
	}
	if _, ok := chargeInput(fieldBytes(multi)-1, multi); ok {
		t.Fatal("multibyte undercount")
	}
}

func TestOverflowSafeAdmission(t *testing.T) {
	if _, ok := charge(10, math.MaxInt); ok {
		t.Fatal("maxint wrapped")
	}
	if _, ok := charge(0, 1); ok {
		t.Fatal("zero remaining")
	}
	if _, ok := charge(maxInputStringBytes, maxInputStringBytes+1); ok {
		t.Fatal("limit plus one")
	}
	got, ok := charge(maxInputStringBytes, maxInputStringBytes)
	if !ok || got != 0 {
		t.Fatal("exact limit")
	}
	got, ok = charge(5, 5)
	if !ok || got != 0 {
		t.Fatal("exact remaining")
	}
	got, ok = charge(1, 0)
	if !ok || got != 1 {
		t.Fatal("zero length")
	}
	got, ok = charge(0, 0)
	if !ok || got != 0 {
		t.Fatal("zero of zero")
	}
	got, ok = charge(math.MaxInt, math.MaxInt)
	if !ok || got != 0 {
		t.Fatal("max remaining")
	}
	if _, ok := charge(math.MaxInt-1, math.MaxInt); ok {
		t.Fatal("maxint minus one")
	}
}

func TestCountRejectionTakesPrecedence(t *testing.T) {
	inputs := make([]shadowprocessor.Input, 1001)
	for i := range inputs {
		inputs[i] = sized(4194305)
	}
	var calls int
	records, err := run(inputs, func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
		calls++
		t.Fatal("processed")
		return shadowprocessor.AuditRecord{}, nil
	})
	if records != nil || calls != 0 || err == nil || err.Error() != "shadowreplay: too many input records" {
		t.Fatal("count precedence")
	}
}

func TestLateOverBudgetRejectsPrefix(t *testing.T) {
	prefix := nInputs(3, "engine 2 on scene")
	over := sized(4194305)
	inputs := append(prefix, over)
	var calls int
	records, err := run(inputs, func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
		calls++
		t.Fatal("valid prefix processed")
		return shadowprocessor.AuditRecord{}, nil
	})
	if records != nil || calls != 0 || err == nil || err.Error() != "shadowreplay: input string bytes exceed limit" {
		t.Fatal("late over-budget")
	}
}

func TestTranscriptSizeBoundaries(t *testing.T) {
	r := setup(t)
	p := r.processor
	for _, size := range []int{0, 65535, 65536, 65537} {
		text := ""
		if size > 0 {
			tail := "; engine 2 on scene"
			n := size - len(tail)
			text = strings.Repeat("\u00e9", n/2) + strings.Repeat("x", n%2) + tail
		}
		in := fixture(text)
		in.Source.RecordingRef = "recording-id"
		in.Source.AttemptRef = "attempt-id"
		in.Source.Model = "opaque-model"
		if len(in.Transcript) != size {
			t.Fatal("size fixture")
		}
		records, err := r.Run([]shadowprocessor.Input{in})
		want, werr := p.Process(in)
		if len(records) != 1 || !reflect.DeepEqual(records[0], want) {
			t.Fatal("transcript boundary parity")
		}
		if size <= 65536 {
			if err != nil || werr != nil {
				t.Fatal("admitted transcript rejected")
			}
		} else if err == nil || err.Error() != "shadowreplay: one or more records failed" || werr == nil {
			t.Fatal("processor oversize lost")
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
	if len(statuses) != 20 {
		t.Fatal("status contract changed")
	}
	for _, status := range statuses {
		texts = append(texts, status, "engine 2 "+status, "engine 2 and engine 5 "+status, "ENGINE\t2, "+strings.ToUpper(strings.ReplaceAll(status, " ", "\t")))
	}
	return texts
}

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

func TestParityOrderDuplicatesAndSpans(t *testing.T) {
	r := setup(t)
	p := r.processor
	d, err := dispatchinterpretation.New()
	if err != nil {
		t.Fatal(err)
	}
	u, err := unitpipeline.New()
	if err != nil {
		t.Fatal(err)
	}
	texts := parityFixtures(t)
	inputs := make([]shadowprocessor.Input, len(texts))
	direct := make([]shadowprocessor.AuditRecord, len(texts))
	states := map[dispatchinterpretation.ShadowState]bool{}
	for i, text := range texts {
		inputs[i] = fixture(text)
		inputs[i].Source.Kind = shadowprocessor.SourceReplay
		inputs[i].Source.TextKind = shadowprocessor.TextRawModel
		direct[i], err = p.Process(inputs[i])
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*direct[i].Result.Dispatch, d.Process(text)) || !reflect.DeepEqual(*direct[i].Result.Units, u.Process(text)) {
			t.Fatalf("native parity for %q", text)
		}
		states[direct[i].Result.Dispatch.ShadowState] = true
		checkSpans(t, text, reflect.ValueOf(direct[i].Result))
		for _, a := range direct[i].Result.Units.UnitStatus.Associations {
			if a.StatusIndex < 0 || a.StatusIndex >= len(direct[i].Result.Units.UnitStatus.StatusEvidence) || a.Status != direct[i].Result.Units.UnitStatus.StatusEvidence[a.StatusIndex] {
				t.Fatal("StatusIndex changed")
			}
		}
	}
	if len(states) != 5 {
		t.Fatalf("summary state coverage: %v", states)
	}
	var live int
	var events []string
	var calls []string
	records, err := run(inputs, func(in shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
		if live != 0 {
			t.Fatal("overlapped process")
		}
		live++
		events = append(events, "start:"+in.Transcript)
		calls = append(calls, in.Transcript)
		rec, err := p.Process(in)
		events = append(events, "return:"+in.Transcript)
		live--
		return rec, err
	})
	if err != nil || len(records) != len(inputs) || len(calls) != len(inputs) {
		t.Fatal("invocation count")
	}
	for i, text := range texts {
		if calls[i] != text || events[2*i] != "start:"+text || events[2*i+1] != "return:"+text {
			t.Fatal("sequential order")
		}
		if records[i].Input != inputs[i] || !reflect.DeepEqual(records[i], direct[i]) {
			t.Fatal("positional parity")
		}
	}
	dup := []shadowprocessor.Input{inputs[0], inputs[0], fixture("engine 5 training"), inputs[0]}
	dupOut, err := r.Run(dup)
	if err != nil || len(dupOut) != 4 || !reflect.DeepEqual(dupOut[0], dupOut[1]) || !reflect.DeepEqual(dupOut[0], dupOut[3]) || dupOut[2].Input.Transcript != "engine 5 training" {
		t.Fatal("duplicates")
	}
	for _, prefix := range []string{"", "\u00e9; ", "\U0001f692; "} {
		got, err := r.Run([]shadowprocessor.Input{fixture(prefix + "engine 2 on scene")})
		if err != nil {
			t.Fatal(err)
		}
		units, status := got[0].Result.Units.Units, got[0].Result.Units.UnitStatus
		if units.State != unitrecognition.Unresolved || units.Mentions[0].Accepted || units.Mentions[0].Reason != "unsupported_context" || status.Associations[0].Unit == nil {
			t.Fatal("native disagreement lost")
		}
		n := len(prefix)
		m, a := units.Mentions[0], status.Associations[0]
		if m.Start != n || m.End != n+8 || a.Unit.Start != n || a.Unit.End != n+8 || a.Status.Start != n+9 || a.Status.End != n+17 {
			t.Fatal("unit offsets")
		}
	}
}

func TestFailureContinuation(t *testing.T) {
	r := setup(t)
	p := r.processor
	valid := fixture("engine 2 on scene")
	valid2 := fixture("respond gas leak")
	missing := fixture("sensitive synthetic transcript")
	missing.Source.Reference = ""
	invalidKind := fixture("engine 5 training")
	invalidKind.Source.Kind = "Replay"
	invalidText := fixture("engine 5 training")
	invalidText.Source.TextKind = "RAW_MODEL"
	oversize := fixture(strings.Repeat("x", 65537))
	rejected := fixture("unknown")
	clear := fixture("engine 2 CLEAR")
	cases := []struct {
		name   string
		inputs []shadowprocessor.Input
		fail   bool
	}{
		{"success-failure-success", []shadowprocessor.Input{valid, missing, valid2}, true},
		{"failure-first", []shadowprocessor.Input{missing, valid}, true},
		{"failure-last", []shadowprocessor.Input{valid, oversize}, true},
		{"consecutive-failures", []shadowprocessor.Input{missing, invalidKind, invalidText}, true},
		{"all-failures", []shadowprocessor.Input{missing, oversize}, true},
		{"native-rejections", []shadowprocessor.Input{rejected, clear, fixture("")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, err := r.Run(tc.inputs)
			if len(records) != len(tc.inputs) {
				t.Fatal("dropped records")
			}
			if tc.fail {
				if err == nil || err.Error() != "shadowreplay: one or more records failed" {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal("native rejection became runner error")
			}
			for i, in := range tc.inputs {
				want, _ := p.Process(in)
				if !reflect.DeepEqual(records[i], want) {
					t.Fatal("independent result lost")
				}
			}
		})
	}
}

func panicRecord(in shadowprocessor.Input, mask int, units *unitpipeline.Result, dispatch *dispatchinterpretation.Result) shadowprocessor.AuditRecord {
	r := shadowprocessor.AuditRecord{
		Version:    "shadow-processor-v1",
		ShadowOnly: true,
		Input:      in,
		Stages: []shadowprocessor.StageRecord{
			{Stage: "input", Outcome: "completed"},
			{Stage: "dispatch", Outcome: "completed"},
			{Stage: "units", Outcome: "completed"},
		},
		Result: shadowprocessor.Result{Dispatch: dispatch, Units: units},
	}
	if mask&1 != 0 {
		r.Stages[1] = shadowprocessor.StageRecord{Stage: "dispatch", Outcome: "failed", FailureCode: "native_stage_panicked"}
		r.Result.Dispatch = nil
	}
	if mask&2 != 0 {
		r.Stages[2] = shadowprocessor.StageRecord{Stage: "units", Outcome: "failed", FailureCode: "native_stage_panicked"}
		r.Result.Units = nil
	}
	return r
}

func TestRecoveredStageFailureContinuation(t *testing.T) {
	p := setupProcessor(t)
	valid := fixture("engine 2 on scene")
	mid := fixture("sensitive synthetic transcript; engine 2 on scene")
	last := fixture("respond gas leak")
	ok1, _ := p.Process(valid)
	ok3, _ := p.Process(last)
	wantMid, _ := p.Process(mid)
	payloads := []error{
		errors.New("sensitive panic payload transcript stack"),
		errors.New("shadowprocessor: native_stage_panicked"),
		errors.New("shadowprocessor: native_stage_panicked"), // panic(nil) remains a non-nil Process error upstream
	}
	for _, payload := range payloads {
		for _, mask := range []int{1, 2, 3} {
			synthetic := panicRecord(mid, mask, wantMid.Result.Units, wantMid.Result.Dispatch)
			if mask&1 == 0 {
				synthetic.Result.Dispatch = wantMid.Result.Dispatch
			}
			if mask&2 == 0 {
				synthetic.Result.Units = wantMid.Result.Units
			}
			var calls []string
			var live int
			records, err := run([]shadowprocessor.Input{valid, mid, last}, func(in shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
				if live != 0 {
					t.Fatal("overlapped")
				}
				live++
				defer func() { live-- }()
				calls = append(calls, in.Transcript)
				if in.Transcript == mid.Transcript {
					return synthetic, payload
				}
				return p.Process(in)
			})
			if len(records) != 3 || len(calls) != 3 || calls[0] != valid.Transcript || calls[2] != last.Transcript {
				t.Fatal("continuation")
			}
			if err == nil || err.Error() != "shadowreplay: one or more records failed" {
				t.Fatal(err)
			}
			if payload != nil && strings.Contains(err.Error(), "sensitive") {
				t.Fatal("payload disclosed")
			}
			if !reflect.DeepEqual(records[0], ok1) || !reflect.DeepEqual(records[2], ok3) || !reflect.DeepEqual(records[1], synthetic) {
				t.Fatal("stage-failure records")
			}
		}
	}
}

func TestCompleteRecordForwarding(t *testing.T) {
	in := fixture("\tSensitive synthetic \xff\r\n")
	for _, mode := range []string{"zero", "empty", "populated"} {
		rec := shadowprocessor.AuditRecord{Version: "shadow-processor-v1", ShadowOnly: true, Input: in}
		if mode != "zero" {
			populate(reflect.ValueOf(&rec).Elem(), mode == "empty")
			rec.Input = in
			rec.Version = "shadow-processor-v1"
			rec.ShadowOnly = true
		}
		records, err := run([]shadowprocessor.Input{in}, func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error) {
			return rec, nil
		})
		if err != nil || !reflect.DeepEqual(records[0], rec) {
			t.Fatal("forwarding")
		}
		if mode == "populated" && rec.Result.Units != nil && len(rec.Result.Units.Units.Mentions) > 0 && &records[0].Result.Units.Units.Mentions[0] != &rec.Result.Units.Units.Mentions[0] {
			t.Fatal("reconstructed")
		}
	}
}

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

func TestOwnershipHistoryAndIsolation(t *testing.T) {
	r := setup(t)
	in := fixture("engine 2 on scene")
	clone := in
	records, err := r.Run([]shadowprocessor.Input{in, in})
	if err != nil || in != clone || records[0].Input != clone {
		t.Fatal("input mutated")
	}
	mutate(reflect.ValueOf(&records[0]).Elem())
	if records[1].Input.Transcript != "engine 2 on scene" || records[1].Stages[0].Stage != "input" {
		t.Fatal("duplicate ownership")
	}
	later, err := r.Run([]shadowprocessor.Input{in})
	if err != nil || later[0].Input.Transcript != "engine 2 on scene" {
		t.Fatal("history after mutation")
	}
	rejected, err := r.Run(nInputs(1001, "x"))
	if rejected != nil || err == nil {
		t.Fatal("admission")
	}
	afterReject, err := r.Run([]shadowprocessor.Input{in})
	if err != nil || afterReject[0].Input.Transcript != "engine 2 on scene" {
		t.Fatal("history after admission rejection")
	}
	failedIn := in
	failedIn.Source.Reference = ""
	failed, err := r.Run([]shadowprocessor.Input{failedIn, in})
	if err == nil || failed[1].Input.Transcript != "engine 2 on scene" {
		t.Fatal("history after processing failure")
	}
	mutate(reflect.ValueOf(&failed[0]).Elem())
	again, err := r.Run([]shadowprocessor.Input{in})
	if err != nil || again[0].Input.Transcript != "engine 2 on scene" {
		t.Fatal("failure mutation leaked")
	}
	want, _ := r.processor.Process(in)
	changed, _ := r.Run([]shadowprocessor.Input{in})
	mutate(reflect.ValueOf(&changed[0].Result.Dispatch.Address).Elem())
	if !reflect.DeepEqual(changed[0].Result.Dispatch.CallType, want.Result.Dispatch.CallType) {
		t.Fatal("address contaminated call type")
	}
	changed, _ = r.Run([]shadowprocessor.Input{in})
	mutate(reflect.ValueOf(&changed[0].Result.Dispatch.CallType).Elem())
	if !reflect.DeepEqual(changed[0].Result.Dispatch.Address, want.Result.Dispatch.Address) {
		t.Fatal("call type contaminated address")
	}
	changed, _ = r.Run([]shadowprocessor.Input{in})
	mutate(reflect.ValueOf(changed[0].Result.Dispatch).Elem())
	if !reflect.DeepEqual(changed[0].Result.Units, want.Result.Units) {
		t.Fatal("outer branch contamination")
	}
	changed, _ = r.Run([]shadowprocessor.Input{in})
	mutate(reflect.ValueOf(&changed[0].Result.Units.Units).Elem())
	if !reflect.DeepEqual(changed[0].Result.Units.UnitStatus, want.Result.Units.UnitStatus) {
		t.Fatal("unit branch contamination")
	}
	changed, _ = r.Run([]shadowprocessor.Input{in})
	mutate(reflect.ValueOf(&changed[0].Result.Units.UnitStatus).Elem())
	if !reflect.DeepEqual(changed[0].Result.Units.Units, want.Result.Units.Units) || !reflect.DeepEqual(changed[0].Result.Dispatch, want.Result.Dispatch) {
		t.Fatal("status branch contamination")
	}
	mutate(reflect.ValueOf(&changed[0]).Elem())
	for repeat := 0; repeat < 3; repeat++ {
		got, err := r.Run([]shadowprocessor.Input{in})
		if err != nil || !reflect.DeepEqual(got[0], want) {
			t.Fatal("repeatability")
		}
	}
	parts, err := r.Run([]shadowprocessor.Input{fixture("93"), fixture("Doubling Road"), fixture("engine"), fixture("2 on scene")})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range parts {
		if rec.Result.Dispatch.AddressState == addressrole.Resolved || rec.Result.Units.UnitStatus.State == "resolved" {
			t.Fatal("joined inputs")
		}
	}
	first, _ := r.Run([]shadowprocessor.Input{fixture("93")})
	second, _ := r.Run([]shadowprocessor.Input{fixture("Doubling Road")})
	if first[0].Result.Dispatch.AddressState == addressrole.Resolved || second[0].Result.Dispatch.AddressState == addressrole.Resolved {
		t.Fatal("joined runs")
	}
}

func TestSafeErrors(t *testing.T) {
	r := setup(t)
	sensitive := fixture("sensitive synthetic transcript panic payload credentials")
	sensitive.Source.Reference = ""
	sensitive.Source.RecordingRef = "secret-recording"
	records, err := r.Run([]shadowprocessor.Input{sensitive, fixture("engine 2 on scene")})
	if len(records) != 2 || err == nil || err.Error() != "shadowreplay: one or more records failed" {
		t.Fatal(err)
	}
	msg := err.Error()
	for _, leak := range []string{"sensitive", "transcript", "panic", "credentials", "secret-recording", "fixture", "engine"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("disclosed %q", leak)
		}
	}
	_, err = r.Run(nInputs(1001, "sensitive"))
	if err.Error() != "shadowreplay: too many input records" {
		t.Fatal(err)
	}
	_, err = r.Run([]shadowprocessor.Input{sized(4194305)})
	if err.Error() != "shadowreplay: input string bytes exceed limit" {
		t.Fatal(err)
	}
}

func TestThousandConcurrentRuns(t *testing.T) {
	r := setup(t)
	texts := []string{"engine 2 on scene", "engine 5 training", "respond gas leak", "unknown", "engine 2 CLEAR"}
	wants := make([][]shadowprocessor.AuditRecord, len(texts))
	for i, text := range texts {
		var err error
		wants[i], err = r.Run([]shadowprocessor.Input{fixture(text), fixture(text)})
		if err != nil || len(wants[i]) != 2 {
			t.Fatal(err)
		}
	}
	results := make([][]shadowprocessor.AuditRecord, 1000)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			k := i % len(texts)
			got, err := r.Run([]shadowprocessor.Input{fixture(texts[k]), fixture(texts[k])})
			if err != nil || !reflect.DeepEqual(got, wants[k]) {
				t.Error("concurrent parity")
			}
			results[i] = got
			other, _ := r.Run([]shadowprocessor.Input{fixture(texts[k]), fixture(texts[k])})
			mutate(reflect.ValueOf(&other[0]).Elem())
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
		got, err := r.Run([]shadowprocessor.Input{fixture(text), fixture(text)})
		if err != nil || !reflect.DeepEqual(got, wants[i]) {
			t.Fatal("concurrent mutation leaked")
		}
	}
}
