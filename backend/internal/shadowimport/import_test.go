package shadowimport

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/shadowprocessor"
	"greenwich-fire-responder/backend/internal/shadowreplay"
)

func ident(seed string) (id, fp string) {
	sum := sha256.Sum256([]byte("SYNTHETIC " + seed))
	fp = "pcm-s16le-16000-mono-v1:sha256:" + hex.EncodeToString(sum[:])
	md := md5.Sum([]byte(fp))
	return "gfr-audio-v1:" + hex.EncodeToString(md[:]), fp
}

func object(seed string) jsonObject {
	id, fp := ident(seed)
	return jsonObject{
		DatasetID: id, Fingerprint: fp, AttemptID: 1, Channel: "CH1A", TGID: 57201,
		DurationMS: 1, Raw: "raw-" + seed, Reference: "ref-" + seed, Model: "small.en",
		Split: "train", ReviewedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func marshalLine(o jsonObject) []byte {
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func setup(t *testing.T) *Importer {
	t.Helper()
	im, err := New()
	if err != nil || im == nil {
		t.Fatal(err)
	}
	return im
}

func mustImport(t *testing.T, sources []Source) []Record {
	t.Helper()
	recs, err := setup(t).Import(sources)
	if err != nil || recs == nil {
		t.Fatal(err)
	}
	return recs
}

func reject(t *testing.T, want string, sources []Source) {
	t.Helper()
	recs, err := setup(t).Import(sources)
	if recs != nil || err == nil || err.Error() != want {
		t.Fatalf("got recs=%v err=%v want %s", recs, err, want)
	}
}

func fieldBytes(in shadowprocessor.Input) int {
	return len(in.Source.Kind) + len(in.Source.Reference) + len(in.Source.TextKind) + len(in.Source.RecordingRef) + len(in.Source.AttemptRef) + len(in.Source.Model) + len(in.Transcript)
}

func TestNilEmptyAndConstruction(t *testing.T) {
	im, err := New()
	if err != nil || im == nil {
		t.Fatal("construction")
	}
	for _, sources := range [][]Source{nil, {}} {
		recs, err := im.Import(sources)
		if err != nil || recs == nil || len(recs) != 0 {
			t.Fatal("nil/empty")
		}
	}
}

func TestEmptySourceBytes(t *testing.T) {
	reject(t, "shadowimport: empty source", []Source{{TextKind: shadowprocessor.TextRawModel}})
	reject(t, "shadowimport: empty source", []Source{{Bytes: []byte{}, TextKind: shadowprocessor.TextRawModel}})
	reject(t, "shadowimport: empty source", []Source{
		{Bytes: marshalLine(object("ok")), TextKind: shadowprocessor.TextRawModel},
		{Bytes: nil, TextKind: shadowprocessor.TextHumanReference},
	})
}

func TestTextKindSelectionAndNoFallback(t *testing.T) {
	o := object("sel")
	o.Raw = "RAW ONLY"
	o.Reference = "HUMAN ONLY"
	raw := mustImport(t, []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextRawModel}})
	if len(raw) != 1 || raw[0].Input.Transcript != "RAW ONLY" || raw[0].Input.Source.TextKind != shadowprocessor.TextRawModel {
		t.Fatal("raw_model")
	}
	human := mustImport(t, []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextHumanReference}})
	if human[0].Input.Transcript != "HUMAN ONLY" || human[0].Input.Source.TextKind != shadowprocessor.TextHumanReference {
		t.Fatal("human_reference")
	}
	emptyRaw := o
	emptyRaw.Raw = ""
	got := mustImport(t, []Source{{Bytes: marshalLine(emptyRaw), TextKind: shadowprocessor.TextRawModel}})
	if got[0].Input.Transcript != "" {
		t.Fatal("fallback to human")
	}
	emptyHuman := o
	emptyHuman.Reference = ""
	got = mustImport(t, []Source{{Bytes: marshalLine(emptyHuman), TextKind: shadowprocessor.TextHumanReference}})
	if got[0].Input.Transcript != "" {
		t.Fatal("fallback to raw")
	}
	reject(t, "shadowimport: invalid text kind", []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextSynthetic}})
	reject(t, "shadowimport: invalid text kind", []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextNormalized}})
	reject(t, "shadowimport: invalid text kind", []Source{{Bytes: marshalLine(o)}})
	reject(t, "shadowimport: invalid text kind", []Source{{Bytes: []byte("not-json"), TextKind: shadowprocessor.TextSynthetic}})
}

func TestExactMappingOrderDuplicatesAndProvenance(t *testing.T) {
	first := object("first")
	first.Channel, first.TGID, first.AttemptID, first.Model = " CH1A ", 1, 0, ""
	first.Raw, first.Reference = "alpha", "ALPHA"
	first.ReviewedAt = time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	second := object("second")
	second.Channel, second.TGID, second.Split = "", 0, "test"
	second.Raw, second.Reference = "beta", "BETA"
	second.ReviewedAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	dup := first
	dup.Channel, dup.TGID = "ZZZ", 999
	unlike := object("unlike")
	unlike.Channel, unlike.TGID = "NOPE", 12345
	unlike.Reference = "gamma"
	srcA := bytes.Join([][]byte{marshalLine(first), marshalLine(second)}, nil)
	srcB := bytes.Join([][]byte{marshalLine(dup), marshalLine(unlike)}, nil)
	recs := mustImport(t, []Source{
		{Bytes: srcA, TextKind: shadowprocessor.TextRawModel},
		{Bytes: srcB, TextKind: shadowprocessor.TextHumanReference},
	})
	if len(recs) != 4 {
		t.Fatal(len(recs))
	}
	wantIDs := []string{first.DatasetID, second.DatasetID, dup.DatasetID, unlike.DatasetID}
	wantText := []string{"alpha", "beta", "ALPHA", "gamma"}
	wantKind := []shadowprocessor.TextKind{shadowprocessor.TextRawModel, shadowprocessor.TextRawModel, shadowprocessor.TextHumanReference, shadowprocessor.TextHumanReference}
	wantCh := []string{" CH1A ", "", "ZZZ", "NOPE"}
	wantTG := []int64{1, 0, 999, 12345}
	for i, rec := range recs {
		if rec.Input.Source.Kind != shadowprocessor.SourceReplay || rec.Input.Source.Reference != wantIDs[i] || rec.Input.Transcript != wantText[i] || rec.Input.Source.TextKind != wantKind[i] {
			t.Fatal("mapping/order")
		}
		if rec.Channel != wantCh[i] || rec.TGID != wantTG[i] {
			t.Fatal("channel/tgid")
		}
	}
	if recs[0].Input.Source.AttemptRef != "0" || recs[0].Input.Source.RecordingRef != first.Fingerprint || recs[0].Input.Source.Model != "" {
		t.Fatal("attempt/fingerprint/model")
	}
	if recs[0].Input.Source.Reference != recs[2].Input.Source.Reference {
		t.Fatal("duplicate id dropped")
	}
	recs[0].Channel, recs[0].TGID, recs[0].Input.Transcript = "mutated", -1, "mutated"
	if recs[2].Channel != "ZZZ" || recs[2].TGID != 999 || recs[2].Input.Transcript != "ALPHA" {
		t.Fatal("duplicate provenance shared")
	}
}

func TestCRLFEmbeddedControlsAndMultibyte(t *testing.T) {
	o := object("bytes")
	o.Raw = "a\r\nb\tc\u00e9"
	o.Reference = "nope"
	line := marshalLine(o)
	crlf := bytes.ReplaceAll(line, []byte{'\n'}, []byte{'\r', '\n'})
	recs := mustImport(t, []Source{{Bytes: crlf, TextKind: shadowprocessor.TextRawModel}})
	if recs[0].Input.Transcript != "a\r\nb\tc\u00e9" {
		t.Fatal("framing rewrote transcript")
	}
}

func TestMalformedAndSchemaRejection(t *testing.T) {
	valid := marshalLine(object("schema"))
	reject(t, "shadowimport: invalid source", []Source{{Bytes: append([]byte{0xEF, 0xBB, 0xBF}, valid...), TextKind: shadowprocessor.TextRawModel}})
	reject(t, "shadowimport: invalid source", []Source{{Bytes: []byte{0xff}, TextKind: shadowprocessor.TextRawModel}})
	reject(t, "shadowimport: invalid source", []Source{{Bytes: []byte("\n"), TextKind: shadowprocessor.TextRawModel}})
	reject(t, "shadowimport: invalid source", []Source{{Bytes: append(append([]byte{}, valid...), '\n'), TextKind: shadowprocessor.TextRawModel}})
	cases := [][]byte{
		bytes.Replace(valid, []byte(`"raw_model_transcript":"raw-schema"`), []byte(`"raw_model_transcript":"\ud800"`), 1),
		bytes.Replace(valid, []byte(`"raw_model_transcript":"raw-schema"`), []byte(`"raw_model_transcript":"\udc00"`), 1),
		bytes.Replace(valid, []byte(`"raw_model_transcript":"raw-schema"`), []byte(`"raw_model_transcript":"\ud800\u0041"`), 1),
		bytes.Replace(valid, []byte(`"raw_model_transcript":"raw-schema"`), []byte(`"RAW_MODEL_TRANSCRIPT":"hello"`), 1),
		bytes.Replace(valid, []byte(`"channel":"CH1A"`), []byte(`"channel":null`), 1),
		bytes.Replace(valid, []byte(`"tgid":57201`), []byte(`"tgid":"57201"`), 1),
		bytes.Replace(valid, []byte(`"duration_ms":1`), []byte(`"duration_ms":1.5`), 1),
		bytes.Replace(valid, []byte(`"duration_ms":1`), []byte(`"duration_ms":-1`), 1),
		bytes.Replace(valid, []byte(`"split":"train"`), []byte(`"split":"Train"`), 1),
	}
	for _, data := range cases {
		reject(t, "shadowimport: invalid source", []Source{{Bytes: data, TextKind: shadowprocessor.TextRawModel}})
	}
	base := append([]byte(nil), bytes.TrimSpace(valid)...)
	dupKey := append(append([]byte(nil), base[:len(base)-1]...), []byte(`,"channel":"X"}`+"\n")...)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: dupKey, TextKind: shadowprocessor.TextRawModel}})
	extra := append(append([]byte(nil), base[:len(base)-1]...), []byte(`,"extra":1}`+"\n")...)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: extra, TextKind: shadowprocessor.TextRawModel}})
	trailing := append(append([]byte(nil), base...), []byte(" 1\n")...)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: trailing, TextKind: shadowprocessor.TextRawModel}})
	missing := bytes.Replace(append([]byte(nil), valid...), []byte(`,"model":"small.en"`), nil, 1)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: missing, TextKind: shadowprocessor.TextRawModel}})
	badID := object("schema")
	badID.DatasetID = "gfr-audio-v1:deadbeef"
	reject(t, "shadowimport: invalid source", []Source{{Bytes: marshalLine(badID), TextKind: shadowprocessor.TextRawModel}})
	zeroTime := object("schema")
	zeroTime.ReviewedAt = time.Time{}
	reject(t, "shadowimport: invalid source", []Source{{Bytes: marshalLine(zeroTime), TextKind: shadowprocessor.TextRawModel}})
	paired := bytes.Replace(valid, []byte(`"raw_model_transcript":"raw-schema"`), []byte(`"raw_model_transcript":"\ud83d\ude00"`), 1)
	if _, err := setup(t).Import([]Source{{Bytes: paired, TextKind: shadowprocessor.TextRawModel}}); err != nil {
		t.Fatal("paired surrogate")
	}
}

func TestSourceCountAndByteLimits(t *testing.T) {
	one := []Source{{Bytes: marshalLine(object("one")), TextKind: shadowprocessor.TextRawModel}}
	if len(mustImport(t, one)) != 1 {
		t.Fatal("one")
	}
	for _, n := range []int{999, 1000} {
		sources := make([]Source, n)
		for i := range sources {
			sources[i] = Source{Bytes: marshalLine(object("c" + strconv.Itoa(i))), TextKind: shadowprocessor.TextRawModel}
		}
		if len(mustImport(t, sources)) != n {
			t.Fatal(n)
		}
	}
	over := make([]Source, 1001)
	for i := range over {
		over[i] = Source{Bytes: []byte("not-json"), TextKind: shadowprocessor.TextRawModel}
	}
	reject(t, "shadowimport: too many sources", over)
	reject(t, "shadowimport: source bytes exceed limit", []Source{{Bytes: make([]byte, maxSourceBytes+1), TextKind: shadowprocessor.TextRawModel}})
	big := make([]byte, maxSourceBytes)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: big, TextKind: shadowprocessor.TextRawModel}})
	almost := make([]byte, maxSourceBytes-1)
	reject(t, "shadowimport: invalid source", []Source{{Bytes: almost, TextKind: shadowprocessor.TextRawModel}})
	left, right := make([]byte, maxSourceBytes/2+1), make([]byte, maxSourceBytes/2+1)
	reject(t, "shadowimport: source bytes exceed limit", []Source{
		{Bytes: left, TextKind: shadowprocessor.TextRawModel},
		{Bytes: right, TextKind: shadowprocessor.TextHumanReference},
	})
}

func TestJSONLineAndRecordLimits(t *testing.T) {
	o := object("line")
	o.Raw = ""
	o.Reference = ""
	base, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	o.Raw = strings.Repeat("a", maxJSONLine-len(base))
	exact, err := json.Marshal(o)
	if err != nil || len(exact) != maxJSONLine {
		t.Fatalf("line size %d", len(exact))
	}
	if len(mustImport(t, []Source{{Bytes: exact, TextKind: shadowprocessor.TextRawModel}})) != 1 {
		t.Fatal("exact line")
	}
	reject(t, "shadowimport: invalid source", []Source{{Bytes: append(exact, 'x'), TextKind: shadowprocessor.TextRawModel}})
	var buf bytes.Buffer
	for i := 0; i < 1000; i++ {
		buf.Write(marshalLine(object("r" + strconv.Itoa(i))))
	}
	if len(mustImport(t, []Source{{Bytes: buf.Bytes(), TextKind: shadowprocessor.TextRawModel}})) != 1000 {
		t.Fatal("1000 records")
	}
	buf.Write(marshalLine(object("r1000")))
	reject(t, "shadowimport: too many input records", []Source{{Bytes: buf.Bytes(), TextKind: shadowprocessor.TextRawModel}})
}

func TestInputStringByteBudget(t *testing.T) {
	recordsForTotal := func(total int, channel string) []byte {
		const n = 5
		objs := make([]jsonObject, n)
		mapped := make([]Record, n)
		used := 0
		for i := 0; i < n; i++ {
			objs[i] = object("budget" + strconv.Itoa(i))
			objs[i].Channel = channel
			objs[i].Model = "x"
			objs[i].Raw = ""
			objs[i].Reference = ""
			mapped[i] = mapRecord(shadowprocessor.TextRawModel, objs[i])
			used += fieldBytes(mapped[i].Input)
		}
		if total < used {
			t.Fatalf("used %d", used)
		}
		pad := total - used
		for i := 0; i < n; i++ {
			share := pad / (n - i)
			objs[i].Raw = strings.Repeat("a", share)
			pad -= share
		}
		var buf bytes.Buffer
		for i := range objs {
			b := marshalLine(objs[i])
			if len(bytes.TrimSpace(b)) > maxJSONLine {
				t.Fatalf("line %d", len(b))
			}
			buf.Write(b)
		}
		return buf.Bytes()
	}
	for _, total := range []int{4194303, 4194304} {
		data := recordsForTotal(total, strings.Repeat("Z", 1024))
		recs := mustImport(t, []Source{{Bytes: data, TextKind: shadowprocessor.TextRawModel}})
		sum := 0
		for _, rec := range recs {
			sum += fieldBytes(rec.Input)
			if rec.Channel != strings.Repeat("Z", 1024) {
				t.Fatal("channel counted or rewritten")
			}
		}
		if sum != total {
			t.Fatalf("sum %d want %d", sum, total)
		}
	}
	reject(t, "shadowimport: input string bytes exceed limit", []Source{{Bytes: recordsForTotal(4194305, ""), TextKind: shadowprocessor.TextRawModel}})
	late := marshalLine(object("late-ok"))
	over := recordsForTotal(4194305, "")
	reject(t, "shadowimport: input string bytes exceed limit", []Source{{Bytes: append(late, over...), TextKind: shadowprocessor.TextRawModel}})
}

func TestOverflowSafeAccounting(t *testing.T) {
	if _, ok := charge(10, math.MaxInt); ok {
		t.Fatal("maxint wrapped")
	}
	if _, ok := charge(0, 1); ok {
		t.Fatal("zero remaining")
	}
	got, ok := charge(maxInputStringBytes, maxInputStringBytes)
	if !ok || got != 0 {
		t.Fatal("exact input budget")
	}
	if _, ok := charge(maxSourceBytes, maxSourceBytes+1); ok {
		t.Fatal("source plus one")
	}
	got, ok = charge(5, 0)
	if !ok || got != 5 {
		t.Fatal("zero length")
	}
}

func TestTranscriptSizeAndReplayCompatibility(t *testing.T) {
	p, err := shadowprocessor.New()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := shadowreplay.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 65535, 65536, 65537} {
		o := object("size")
		o.Raw = strings.Repeat("é", size/2) + strings.Repeat("x", size%2)
		if len(o.Raw) != size {
			o.Raw = strings.Repeat("x", size)
		}
		recs := mustImport(t, []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextRawModel}})
		if recs[0].Input.Transcript != o.Raw {
			t.Fatal("rewritten")
		}
		audit, perr := p.Process(recs[0].Input)
		inputs := []shadowprocessor.Input{recs[0].Input}
		audits, rerr := runner.Run(inputs)
		if size <= 65536 {
			if perr != nil || rerr != nil || audits[0].Input.Transcript != o.Raw {
				t.Fatal("valid size")
			}
		} else if perr == nil || rerr == nil || rerr.Error() != "shadowreplay: one or more records failed" || audit.Stages[0].FailureCode != "transcript_too_large" {
			t.Fatal("processor limit waived")
		}
	}
	o := object("parity")
	o.Raw = "\u00e9; engine 2 on scene"
	recs := mustImport(t, []Source{{Bytes: marshalLine(o), TextKind: shadowprocessor.TextRawModel}})
	direct, err := p.Process(recs[0].Input)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := runner.Run([]shadowprocessor.Input{recs[0].Input})
	if err != nil || !reflect.DeepEqual(audits[0], direct) || direct.Input.Transcript != recs[0].Input.Transcript {
		t.Fatal("parity")
	}
	u := direct.Result.Units.Units.Mentions[0]
	if u.Start != 4 || recs[0].Input.Transcript[u.Start:u.End] != u.Evidence {
		t.Fatal("offsets")
	}
}

func TestOwnershipRepeatedConcurrentAndSafeErrors(t *testing.T) {
	line := marshalLine(object("own"))
	src := []Source{{Bytes: append([]byte(nil), line...), TextKind: shadowprocessor.TextRawModel}}
	orig := append([]byte(nil), src[0].Bytes...)
	im := setup(t)
	first := mustImport(t, src)
	first[0].Channel = "mutated"
	first[0].TGID = -2
	first[0].Input.Transcript = "mutated"
	src[0].Bytes[0] ^= 0
	src[0].Bytes[0] ^= 0
	if !bytes.Equal(src[0].Bytes, orig) {
		t.Fatal("caller bytes mutated")
	}
	second := mustImport(t, src)
	if second[0].Channel != "CH1A" || second[0].TGID != 57201 || second[0].Input.Transcript == "mutated" {
		t.Fatal("history")
	}
	for i := 0; i < 3; i++ {
		again, err := im.Import(src)
		if err != nil || !reflect.DeepEqual(again, second) {
			t.Fatal("repeat")
		}
	}
	sensitive := object("secret-path")
	sensitive.Raw = "sensitive transcript panic credentials"
	bad := bytes.Replace(marshalLine(sensitive), []byte(`"split":"train"`), []byte(`"split":"nope"`), 1)
	recs, err := im.Import([]Source{{Bytes: bad, TextKind: shadowprocessor.TextRawModel}})
	if recs != nil || err == nil || err.Error() != "shadowimport: invalid source" {
		t.Fatal(err)
	}
	msg := err.Error()
	for _, leak := range []string{"sensitive", "transcript", "panic", "credentials", "secret-path", "CH1A", "gfr-audio", "small.en", "\\", ":\\"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("disclosed %q in %q", leak, msg)
		}
	}
	results := make([][]Record, 1000)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, err := im.Import([]Source{{Bytes: marshalLine(object("own")), TextKind: shadowprocessor.TextRawModel}})
			if err != nil || !reflect.DeepEqual(got, second) {
				t.Error("concurrent")
			}
			results[i] = got
			other, _ := im.Import([]Source{{Bytes: marshalLine(object("own")), TextKind: shadowprocessor.TextRawModel}})
			other[0].Channel = "mutated"
			other[0].TGID = -2
		}(i)
	}
	close(start)
	wg.Wait()
	for _, got := range results {
		if got[0].Channel != "CH1A" || got[0].TGID != 57201 {
			t.Fatal("concurrent ownership")
		}
	}
	final, err := im.Import(src)
	if err != nil || !reflect.DeepEqual(final, second) {
		t.Fatal("concurrent leak")
	}
}

func TestProductionImportDoesNotInvokeReplayOrProcessing(t *testing.T) {
	body, err := os.ReadFile("import.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, banned := range []string{"shadowreplay", "transcripteval", "transcriptreview", "database", "net/http", ".Process(", ".Run("} {
		if strings.Contains(text, banned) {
			t.Fatalf("forbidden %q", banned)
		}
	}
}
