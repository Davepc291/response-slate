package shadowreplayeval

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/addressrole"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
	"greenwich-fire-responder/backend/internal/dispatchinterpretation"
	"greenwich-fire-responder/backend/internal/shadowimport"
	"greenwich-fire-responder/backend/internal/shadowprocessor"
	"greenwich-fire-responder/backend/internal/shadowreplay"
	"greenwich-fire-responder/backend/internal/unitrecognition"
)

type jsonLine struct {
	DatasetID   string    `json:"dataset_id"`
	Fingerprint string    `json:"audio_fingerprint"`
	AttemptID   int64     `json:"transcription_attempt_id"`
	Channel     string    `json:"channel"`
	TGID        int64     `json:"tgid"`
	DurationMS  int64     `json:"duration_ms"`
	Raw         string    `json:"raw_model_transcript"`
	Reference   string    `json:"human_reference_transcript"`
	Model       string    `json:"model"`
	Split       string    `json:"split"`
	ReviewedAt  time.Time `json:"reviewed_at"`
}

func ident(seed string) (id, fp string) {
	sum := sha256.Sum256([]byte("SYNTHETIC " + seed))
	fp = "pcm-s16le-16000-mono-v1:sha256:" + hex.EncodeToString(sum[:])
	md := md5.Sum([]byte(fp))
	return "gfr-audio-v1:" + hex.EncodeToString(md[:]), fp
}

func object(seed, raw, channel string, tgid int64) jsonLine {
	id, fp := ident(seed)
	return jsonLine{
		DatasetID: id, Fingerprint: fp, AttemptID: 1, Channel: channel, TGID: tgid,
		DurationMS: 1, Raw: raw, Reference: "human-" + seed, Model: "small.en",
		Split: "train", ReviewedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func marshalLine(o jsonLine) []byte {
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func src(o jsonLine, kind shadowprocessor.TextKind) shadowimport.Source {
	return shadowimport.Source{Bytes: marshalLine(o), TextKind: kind}
}

func setup(t *testing.T) *Evaluator {
	t.Helper()
	e, err := New()
	if err != nil || e == nil || e.importer == nil || e.runner == nil {
		t.Fatal(err)
	}
	return e
}

func mustEval(t *testing.T, set SetKind, sources []shadowimport.Source) Result {
	t.Helper()
	got, err := setup(t).Evaluate(set, sources)
	if err != nil || got.Pairs == nil {
		t.Fatal(err)
	}
	return got
}

func zero(r Result) bool {
	return r.Pairs == nil && reflect.DeepEqual(r.Summary, Summary{})
}

func TestConstruction(t *testing.T) {
	e, err := New()
	if err != nil || e == nil || e.importer == nil || e.runner == nil {
		t.Fatal("public construction")
	}
	var imports, runners int
	got, err := newEvaluator(func() (*shadowimport.Importer, error) {
		imports++
		return nil, errors.New("sensitive importer payload transcript panic")
	}, func() (*shadowreplay.Runner, error) {
		runners++
		t.Fatal("runner after importer failure")
		return shadowreplay.New()
	})
	if got != nil || err == nil || err.Error() != "shadowreplayeval: construction failed" || imports != 1 || runners != 0 {
		t.Fatal("importer failure")
	}
	if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "transcript") || strings.Contains(err.Error(), "panic") {
		t.Fatal("unsafe constructor error")
	}
	imports, runners = 0, 0
	got, err = newEvaluator(func() (*shadowimport.Importer, error) {
		imports++
		return shadowimport.New()
	}, func() (*shadowreplay.Runner, error) {
		runners++
		return nil, errors.New("sensitive runner payload")
	})
	if got != nil || err == nil || err.Error() != "shadowreplayeval: construction failed" || imports != 1 || runners != 1 {
		t.Fatal("runner failure")
	}
	got, err = newEvaluator(func() (*shadowimport.Importer, error) { return nil, nil }, func() (*shadowreplay.Runner, error) {
		t.Fatal("runner after nil importer")
		return shadowreplay.New()
	})
	if got != nil || err == nil || err.Error() != "shadowreplayeval: construction failed" {
		t.Fatal("nil importer")
	}
	got, err = newEvaluator(shadowimport.New, func() (*shadowreplay.Runner, error) { return nil, nil })
	if got != nil || err == nil || err.Error() != "shadowreplayeval: construction failed" {
		t.Fatal("nil runner")
	}
	got, err = newEvaluator(shadowimport.New, shadowreplay.New)
	if err != nil || got == nil {
		t.Fatal("seam success")
	}
}

func TestNilEmptySources(t *testing.T) {
	e := setup(t)
	im, r := e.importer, e.runner
	for _, sources := range [][]shadowimport.Source{nil, {}} {
		var imports, runs int
		got, err := evaluate(SetDevelopment, sources, func(s []shadowimport.Source) ([]shadowimport.Record, error) {
			imports++
			return im.Import(s)
		}, func(in []shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
			runs++
			return r.Run(in)
		})
		if err != nil || got.Pairs == nil || len(got.Pairs) != 0 || imports != 1 || runs != 1 {
			t.Fatal("nil/empty")
		}
		s := got.Summary
		if !s.ImportAccepted || s.ReplayExecutionFailed || !s.PairingIntact || s.Records != 0 || s.ProcessorFailed != 0 || s.ProcessorCompleted != 0 {
			t.Fatal("empty summary flags")
		}
		if s.Version != "shadow-replay-evaluation-v1" || s.Set != SetDevelopment || s.TextKind != "" {
			t.Fatal("empty provenance")
		}
		if s.DispatchShadowState == nil || s.AddressState == nil || s.CallTypeState == nil || s.UnitState == nil || s.UnitStatusState == nil {
			t.Fatal("nil maps")
		}
		if len(s.DispatchShadowState) != 0 || s.NativeDisagreement != 0 || s.EmptyChannel != 0 || s.ZeroTGID != 0 {
			t.Fatal("nonzero empty counts")
		}
	}
}

func TestInvalidSetKind(t *testing.T) {
	line := src(object("set", "engine 2 on scene", "CH1A", 57201), shadowprocessor.TextRawModel)
	for _, set := range []SetKind{"", "train", "test", "Development", "held-out"} {
		var imports, runs int
		got, err := evaluate(set, []shadowimport.Source{line}, func([]shadowimport.Source) ([]shadowimport.Record, error) {
			imports++
			t.Fatal("imported")
			return nil, nil
		}, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
			runs++
			t.Fatal("ran")
			return nil, nil
		})
		if !zero(got) || err == nil || err.Error() != "shadowreplayeval: invalid evaluation set" || imports != 0 || runs != 0 {
			t.Fatalf("set %q", set)
		}
	}
}

func TestMixedTextKindRejectedBeforeImport(t *testing.T) {
	a := src(object("mix-a", "engine 2 on scene", "CH1A", 57201), shadowprocessor.TextRawModel)
	b := src(object("mix-b", "engine 5 training", "CH1A", 57201), shadowprocessor.TextHumanReference)
	var imports, runs int
	got, err := evaluate(SetHeldOut, []shadowimport.Source{a, b}, func([]shadowimport.Source) ([]shadowimport.Record, error) {
		imports++
		t.Fatal("imported")
		return nil, nil
	}, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
		runs++
		t.Fatal("ran")
		return nil, nil
	})
	if !zero(got) || err == nil || err.Error() != "shadowreplayeval: mixed text kind" || imports != 0 || runs != 0 {
		t.Fatal(err)
	}
	same := []shadowimport.Source{
		src(object("one", "respond gas leak", "CH1A", 57201), shadowprocessor.TextRawModel),
		src(object("two", "unknown", "CH1A", 57201), shadowprocessor.TextRawModel),
	}
	got, err = setup(t).Evaluate(SetDevelopment, same)
	if err != nil || len(got.Pairs) != 2 || got.Summary.TextKind != shadowprocessor.TextRawModel {
		t.Fatal("uniform kinds")
	}
}

func TestImportRejectionZeroReplay(t *testing.T) {
	e := setup(t)
	cases := [][]shadowimport.Source{
		{{Bytes: []byte("{"), TextKind: shadowprocessor.TextRawModel}},
		{src(object("syn", "x", "CH1A", 1), shadowprocessor.TextSynthetic)},
		{src(object("norm", "x", "CH1A", 1), shadowprocessor.TextNormalized)},
		{{Bytes: marshalLine(object("ok", "x", "CH1A", 1)), TextKind: ""}},
	}
	wants := []string{"shadowimport: invalid source", "shadowimport: invalid text kind", "shadowimport: invalid text kind", "shadowimport: invalid text kind"}
	for i, sources := range cases {
		var imports, runs int
		got, err := evaluate(SetDevelopment, sources, func(s []shadowimport.Source) ([]shadowimport.Record, error) {
			imports++
			return e.importer.Import(s)
		}, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
			runs++
			t.Fatal("replay after import rejection")
			return nil, nil
		})
		if !zero(got) || err == nil || err.Error() != wants[i] || imports != 1 || runs != 0 {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestSingletonMultiplePairingOrderAndProvenance(t *testing.T) {
	first := object("first", "alpha", " CH1A ", 1)
	second := object("second", "beta", "", 0)
	second.Split = "test"
	dup := first
	dup.Channel, dup.TGID = "ZZZ", 999
	unlike := object("unlike", "gamma", "NOPE", 12345)
	srcA := bytes.Join([][]byte{marshalLine(first), marshalLine(second)}, nil)
	srcB := bytes.Join([][]byte{marshalLine(dup), marshalLine(unlike)}, nil)
	got := mustEval(t, SetHeldOut, []shadowimport.Source{{Bytes: bytes.Join([][]byte{srcA, srcB}, nil), TextKind: shadowprocessor.TextRawModel}})
	if len(got.Pairs) != 4 || !got.Summary.PairingIntact || !got.Summary.ImportAccepted || got.Summary.Set != SetHeldOut {
		t.Fatal("admitted")
	}
	wantCh := []string{" CH1A ", "", "ZZZ", "NOPE"}
	wantTG := []int64{1, 0, 999, 12345}
	wantText := []string{"alpha", "beta", "alpha", "gamma"}
	for i, p := range got.Pairs {
		if p.Channel != wantCh[i] || p.TGID != wantTG[i] || p.Audit.Input.Transcript != wantText[i] {
			t.Fatal("positional pairing")
		}
		if !p.Audit.ShadowOnly || p.Audit.Input.Source.Kind != shadowprocessor.SourceReplay || p.Audit.Input.Source.TextKind != shadowprocessor.TextRawModel {
			t.Fatal("integrity")
		}
	}
	if got.Summary.EmptyChannel != 1 || got.Summary.ZeroTGID != 1 || got.Summary.DuplicateReferences != 1 || got.Summary.Records != 4 {
		t.Fatalf("counts %+v", got.Summary)
	}
	got.Pairs[0].Channel, got.Pairs[0].TGID = "mutated", -1
	if got.Pairs[2].Channel != "ZZZ" || got.Pairs[2].TGID != 999 {
		t.Fatal("duplicate provenance shared")
	}
}

func TestRawModelAndHumanReferenceSeparate(t *testing.T) {
	o := object("sel", "RAW ONLY", "CH1A", 57201)
	o.Reference = "HUMAN ONLY"
	raw := mustEval(t, SetDevelopment, []shadowimport.Source{src(o, shadowprocessor.TextRawModel)})
	human := mustEval(t, SetDevelopment, []shadowimport.Source{src(o, shadowprocessor.TextHumanReference)})
	if raw.Pairs[0].Audit.Input.Transcript != "RAW ONLY" || raw.Summary.TextKind != shadowprocessor.TextRawModel {
		t.Fatal("raw_model")
	}
	if human.Pairs[0].Audit.Input.Transcript != "HUMAN ONLY" || human.Summary.TextKind != shadowprocessor.TextHumanReference {
		t.Fatal("human_reference")
	}
	emptyRaw := o
	emptyRaw.Raw = ""
	got := mustEval(t, SetDevelopment, []shadowimport.Source{src(emptyRaw, shadowprocessor.TextRawModel)})
	if got.Pairs[0].Audit.Input.Transcript != "" {
		t.Fatal("fallback to human")
	}
}

func TestValidInvalidValidContinuation(t *testing.T) {
	ok1 := object("ok1", "engine 2 on scene", "CH1A", 57201)
	bad := object("bad", strings.Repeat("x", 65537), "CH1A", 57201)
	ok2 := object("ok2", "respond gas leak", "CH1A", 57201)
	data := bytes.Join([][]byte{marshalLine(ok1), marshalLine(bad), marshalLine(ok2)}, nil)
	got, err := setup(t).Evaluate(SetDevelopment, []shadowimport.Source{{Bytes: data, TextKind: shadowprocessor.TextRawModel}})
	if err == nil || err.Error() != "shadowreplay: one or more records failed" || len(got.Pairs) != 3 {
		t.Fatal(err)
	}
	if !got.Summary.ImportAccepted || !got.Summary.ReplayExecutionFailed || !got.Summary.PairingIntact {
		t.Fatal("flags")
	}
	if got.Summary.ProcessorFailed != 1 || got.Summary.ProcessorCompleted != 2 {
		t.Fatalf("execution %d %d", got.Summary.ProcessorFailed, got.Summary.ProcessorCompleted)
	}
	if got.Pairs[0].Audit.Input.Transcript != "engine 2 on scene" || got.Pairs[2].Audit.Input.Transcript != "respond gas leak" {
		t.Fatal("later success dropped")
	}
	if got.Pairs[1].Audit.Stages[0].FailureCode != "transcript_too_large" || got.Pairs[1].Audit.Result.Dispatch != nil {
		t.Fatal("validation failure")
	}
	if got.Pairs[2].Audit.Result.Dispatch == nil || got.Pairs[2].Audit.Result.Dispatch.ShadowState != dispatchinterpretation.TypeOnlyResolved {
		t.Fatal("native result lost")
	}
}

func TestRecoveredNativeStageFailure(t *testing.T) {
	e := setup(t)
	p, err := shadowprocessor.New()
	if err != nil {
		t.Fatal(err)
	}
	sources := []shadowimport.Source{
		src(object("a", "engine 2 on scene", "CH1A", 57201), shadowprocessor.TextRawModel),
		src(object("b", "respond gas leak", "CH1A", 57201), shadowprocessor.TextRawModel),
		src(object("c", "unknown", "CH1A", 57201), shadowprocessor.TextRawModel),
	}
	got, err := evaluate(SetDevelopment, sources, e.importer.Import, func(inputs []shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
		out := make([]shadowprocessor.AuditRecord, len(inputs))
		for i, in := range inputs {
			if i == 1 {
				out[i] = shadowprocessor.AuditRecord{
					Version: "shadow-processor-v1", ShadowOnly: true, Input: in,
					Stages: []shadowprocessor.StageRecord{
						{Stage: "input", Outcome: "completed"},
						{Stage: "dispatch", Outcome: "failed", FailureCode: "native_stage_panicked"},
						{Stage: "units", Outcome: "completed"},
					},
				}
				u, _ := p.Process(in)
				out[i].Result.Units = u.Result.Units
				continue
			}
			out[i], _ = p.Process(in)
		}
		return out, errors.New("shadowreplay: one or more records failed")
	})
	if err == nil || err.Error() != "shadowreplay: one or more records failed" || len(got.Pairs) != 3 {
		t.Fatal(err)
	}
	if got.Summary.ProcessorFailed != 1 || got.Summary.ProcessorCompleted != 2 || !got.Summary.PairingIntact {
		t.Fatal("recovered continuation")
	}
	if got.Pairs[0].Audit.Result.Units == nil || got.Pairs[2].Audit.Result.Dispatch == nil || got.Pairs[1].Audit.Result.Dispatch != nil {
		t.Fatal("stage pointers")
	}
	if got.Summary.DispatchShadowState[string(dispatchinterpretation.NeitherResolved)] != 2 {
		t.Fatalf("nil dispatch skipped in histogram %+v", got.Summary.DispatchShadowState)
	}
}

func TestImpossiblePairingMismatch(t *testing.T) {
	e := setup(t)
	sources := []shadowimport.Source{src(object("m", "engine 2 on scene", "CH1A", 57201), shadowprocessor.TextRawModel)}
	got, err := evaluate(SetDevelopment, sources, e.importer.Import, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
		return nil, errors.New("shadowreplay: too many input records")
	})
	if got.Pairs != nil || err == nil || err.Error() != "shadowreplay: too many input records" {
		t.Fatal("nil audits")
	}
	if !got.Summary.ImportAccepted || !got.Summary.ReplayExecutionFailed || got.Summary.PairingIntact || got.Summary.DispatchShadowState != nil {
		t.Fatal("nil-audit summary")
	}
	got, err = evaluate(SetDevelopment, sources, e.importer.Import, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
		return []shadowprocessor.AuditRecord{{ShadowOnly: true}, {ShadowOnly: true}}, nil
	})
	if got.Pairs != nil || err == nil || err.Error() != "shadowreplayeval: pairing mismatch" || got.Summary.PairingIntact || !got.Summary.ImportAccepted {
		t.Fatal("length mismatch")
	}
	if got.Summary.ReplayExecutionFailed || got.Summary.DispatchShadowState != nil {
		t.Fatal("mismatch histograms")
	}
	got, err = evaluate(SetDevelopment, sources, e.importer.Import, func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
		return nil, nil
	})
	if got.Pairs != nil || err == nil || err.Error() != "shadowreplayeval: pairing mismatch" || got.Summary.ReplayExecutionFailed {
		t.Fatal("nil audits without replay error")
	}
}

func TestNativeHistogramsDisagreementAndBothResolved(t *testing.T) {
	lines := []jsonLine{
		object("both", "respond to 93 Doubling Road; respond gas leak", "CH1A", 57201),
		object("addr", "respond to 93 Doubling Road", "CH1A", 57201),
		object("typ", "respond gas leak", "CH1A", 57201),
		object("amb", "respond to 93 Doubling Road; respond to 30 Edgewood Drive", "CH1A", 57201),
		object("neither", "unknown", "CH1A", 57201),
		object("scene", "engine 2 on scene", "CH1A", 57201),
		object("clear", "engine 2 CLEAR", "CH1A", 57201),
		object("ambunit", "engine 2 and engine 20 on scene", "CH1A", 57201),
	}
	var buf bytes.Buffer
	for _, o := range lines {
		buf.Write(marshalLine(o))
	}
	got := mustEval(t, SetDevelopment, []shadowimport.Source{{Bytes: buf.Bytes(), TextKind: shadowprocessor.TextRawModel}})
	s := got.Summary
	if s.Records != 8 || s.ProcessorCompleted != 8 || s.ProcessorFailed != 0 || s.ReplayExecutionFailed {
		t.Fatal("execution")
	}
	if s.DispatchShadowState["both_resolved"] != 1 || s.DispatchShadowState["address_only_resolved"] != 1 || s.DispatchShadowState["type_only_resolved"] != 1 {
		t.Fatal("dispatch resolved")
	}
	if s.DispatchShadowState["contains_ambiguity"] != 1 || s.DispatchShadowState["neither_resolved"] != 4 {
		t.Fatalf("dispatch remainder %+v", s.DispatchShadowState)
	}
	if s.AddressState[string(addressrole.Resolved)] != 2 || s.AddressState[string(addressrole.Ambiguous)] != 1 {
		t.Fatal("address")
	}
	if s.CallTypeState[string(calltypeinterpret.Resolved)] != 2 || s.CallTypeState[string(calltypeinterpret.Unresolved)] != 6 {
		t.Fatal("call type")
	}
	if s.NativeDisagreement != 1 || s.AssociationsProposed < 1 {
		t.Fatalf("disagreement %d proposed %d", s.NativeDisagreement, s.AssociationsProposed)
	}
	scene := got.Pairs[5]
	u, st := scene.Audit.Result.Units.Units, scene.Audit.Result.Units.UnitStatus
	if u.State != unitrecognition.Unresolved || u.Mentions[0].Accepted || u.Mentions[0].Reason != "unsupported_context" || st.Associations[0].Unit == nil {
		t.Fatal("native disagreement lost")
	}
	both := got.Pairs[0].Audit.Result.Dispatch
	if both.ShadowState != dispatchinterpretation.BothResolved || both.AddressState != addressrole.Resolved || both.CallTypeState != calltypeinterpret.Resolved {
		t.Fatal("both_resolved evidence")
	}
	if !both.ShadowOnly {
		t.Fatal("dispatch operational")
	}
}

func TestSanitizedSummaryAndSafeErrors(t *testing.T) {
	o := object("secret-path", "sensitive transcript panic credentials", "CH1A", 57201)
	o.Model = "secret-model"
	got := mustEval(t, SetDevelopment, []shadowimport.Source{src(o, shadowprocessor.TextRawModel)})
	walkStrings(t, reflect.ValueOf(got.Summary), func(s string) {
		for _, leak := range []string{"sensitive", "transcript", "panic", "credentials", "secret-path", "CH1A", "gfr-audio", "small.en", "secret-model", "pcm-s16le", `:\`, "/"} {
			if strings.Contains(s, leak) {
				t.Fatalf("summary leaked %q in %q", leak, s)
			}
		}
	})
	bad := bytes.Replace(marshalLine(o), []byte(`"split":"train"`), []byte(`"split":"nope"`), 1)
	res, err := setup(t).Evaluate(SetDevelopment, []shadowimport.Source{{Bytes: bad, TextKind: shadowprocessor.TextRawModel}})
	if !zero(res) || err == nil || err.Error() != "shadowimport: invalid source" {
		t.Fatal(err)
	}
	msg := err.Error()
	for _, leak := range []string{"sensitive", "transcript", "panic", "credentials", "secret-path", "CH1A", "gfr-audio"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error leaked %q", leak)
		}
	}
}

func walkStrings(t *testing.T, v reflect.Value, fn func(string)) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkStrings(t, v.Elem(), fn)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			walkStrings(t, v.Field(i), fn)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkStrings(t, k, fn)
			walkStrings(t, v.MapIndex(k), fn)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStrings(t, v.Index(i), fn)
		}
	case reflect.String:
		fn(v.String())
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
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			cur := iter.Value()
			if cur.Kind() == reflect.Int {
				nv := reflect.New(v.Type().Elem()).Elem()
				nv.SetInt(cur.Int() + 1)
				v.SetMapIndex(iter.Key(), nv)
			}
		}
	case reflect.String:
		v.SetString("mutated")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int64:
		v.SetInt(-1)
	}
}

func TestOwnershipRepeatedConcurrent(t *testing.T) {
	e := setup(t)
	o := object("own", "engine 2 on scene", "CH1A", 57201)
	line := marshalLine(o)
	sources := []shadowimport.Source{{Bytes: append([]byte(nil), line...), TextKind: shadowprocessor.TextRawModel}}
	orig := append([]byte(nil), sources[0].Bytes...)
	first, err := e.Evaluate(SetDevelopment, sources)
	if err != nil {
		t.Fatal(err)
	}
	first.Pairs[0].Channel = "mutated"
	first.Pairs[0].TGID = -2
	first.Pairs[0].Audit.Input.Transcript = "mutated"
	first.Summary.DispatchShadowState["both_resolved"] = 99
	sources[0].Bytes[0] ^= 0
	if !bytes.Equal(sources[0].Bytes, orig) {
		t.Fatal("caller bytes mutated")
	}
	second, err := e.Evaluate(SetDevelopment, sources)
	if err != nil || second.Pairs[0].Channel != "CH1A" || second.Pairs[0].Audit.Input.Transcript == "mutated" || second.Summary.DispatchShadowState["both_resolved"] == 99 {
		t.Fatal("history")
	}
	want := second
	for i := 0; i < 3; i++ {
		again, err := e.Evaluate(SetDevelopment, sources)
		if err != nil || !reflect.DeepEqual(again.Summary, want.Summary) || again.Pairs[0].Audit.Input != want.Pairs[0].Audit.Input {
			t.Fatal("repeat")
		}
	}
	mutate(reflect.ValueOf(&second.Pairs[0]).Elem())
	later, err := e.Evaluate(SetDevelopment, sources)
	if err != nil || later.Pairs[0].Channel != "CH1A" || later.Pairs[0].Audit.Input.Transcript != "engine 2 on scene" {
		t.Fatal("mutation leaked")
	}
	results := make([]Result, 1000)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			set := SetDevelopment
			if i%2 == 1 {
				set = SetHeldOut
			}
			got, err := e.Evaluate(set, []shadowimport.Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextRawModel}})
			if err != nil || got.Pairs[0].Channel != "CH1A" || got.Summary.Set != set || !got.Summary.PairingIntact {
				t.Error("concurrent")
			}
			results[i] = got
			other, _ := e.Evaluate(set, []shadowimport.Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextRawModel}})
			mutate(reflect.ValueOf(&other).Elem())
		}(i)
	}
	close(start)
	wg.Wait()
	for i, got := range results {
		set := SetDevelopment
		if i%2 == 1 {
			set = SetHeldOut
		}
		if got.Pairs[0].Channel != "CH1A" || got.Summary.Set != set {
			t.Fatal("concurrent ownership")
		}
	}
	final, err := e.Evaluate(SetDevelopment, sources)
	if err != nil || final.Pairs[0].Channel != "CH1A" || !reflect.DeepEqual(final.Summary, want.Summary) {
		t.Fatal("concurrent leak")
	}
}

func TestProductionCodeBoundary(t *testing.T) {
	body, err := os.ReadFile("evaluate.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, banned := range []string{
		"transcripteval", "transcriptreview", "database", "net/http", "net.", "os.", "sql",
		"whisper", "ffmpeg", "encoding/json", "path/filepath", "os/exec", "ioutil",
		"cad", "websocket", "Open(", "ReadFile", "WriteFile", "ListenAndServe",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("forbidden %q", banned)
		}
	}
	if strings.Contains(text, "accuracy") || strings.Contains(text, "WER") || strings.Contains(text, "pass rate") {
		t.Fatal("accuracy metric")
	}
}
