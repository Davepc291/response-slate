package transcripteval

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(n string) record {
	sum := sha256.Sum256([]byte("SYNTHETIC " + n))
	fp := "pcm-s16le-16000-mono-v1:sha256:" + hex.EncodeToString(sum[:])
	id := md5.Sum([]byte(fp))
	return record{DatasetID: "gfr-audio-v1:" + hex.EncodeToString(id[:]), Fingerprint: fp, AttemptID: 1, Channel: "CH1A", TGID: 57201, DurationMS: 1000, Raw: "SYNTHETIC Engine 2 on scene", Reference: "SYNTHETIC Engine 2 on scene", Model: "small.en", Split: "train", ReviewedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func line(r record) []byte { b, _ := json.Marshal(r); return append(b, '\n') }
func TestWordMetrics(t *testing.T) {
	cases := []struct {
		ref, hyp string
		want     edits
	}{
		{"a b", "a c", edits{s: 1}}, {"a b", "a", edits{d: 1}}, {"a", "a b", edits{i: 1}},
		{"a b", "", edits{d: 2}}, {"", "a b", edits{i: 2}}, {"a b", "b a", edits{s: 2}},
		{"ENGINE 2, on-scene!", "engine 2 ON scene", edits{}},
	}
	for _, c := range cases {
		if got := distance(tokens(c.ref), tokens(c.hyp)); got != c.want {
			t.Errorf("%q / %q: %+v", c.ref, c.hyp, got)
		}
	}
	c := newCounts()
	c.add(tokens("Engine 2 on scene on scene"), tokens("engine 2 on scene"), edits{d: 2})
	c.finish()
	if c.Exact != 0 || c.ReferenceWords != 6 || *c.WER != 2.0/6 {
		t.Fatal(c)
	}
	for _, term := range c.Critical {
		if term.Phrase == "on scene" && (term.Occurrences != 2 || term.Correct != 1 || term.Misses != 1 || *term.Recall != 0.5) {
			t.Fatal(term)
		}
	}
	if occurrences(tokens("unclear cleared Engine 20"), tokens("clear")) != 0 || occurrences(tokens("Engine 20"), tokens("Engine 2")) != 0 {
		t.Fatal("substring match")
	}
	if occurrences(tokens("10-4, GOOD copy"), tokens("10-4")) != 1 {
		t.Fatal("punctuation normalization")
	}
	// A matching phrase in a different record cannot cover a reference miss.
	c = newCounts()
	c.add(tokens("clear"), tokens("wrong"), edits{s: 1})
	c.add(tokens("wrong"), tokens("clear clear"), edits{s: 1, i: 1})
	c.finish()
	if c.CriticalTotals.Occurrences != 1 || c.CriticalTotals.Correct != 0 || c.CriticalTotals.Misses != 1 {
		t.Fatal("cross-record credit")
	}
	// Nested phrases count independently, and boundaries avoid inflection matches.
	c = newCounts()
	c.add(tokens("last unit on scene"), tokens("last unit on scene"), edits{})
	c.finish()
	if c.CriticalTotals.Occurrences != 2 || c.CriticalTotals.Correct != 2 {
		t.Fatal("nested terms")
	}
	c = newCounts()
	c.finish()
	if c.WER != nil || c.ExactRate != nil || c.Critical[0].Recall != nil {
		t.Fatal("undefined rate")
	}
}

func TestStrictUnicodeAndFieldNames(t *testing.T) {
	valid := line(fixture("unicode"))
	for _, replacement := range []string{`"raw_model_transcript":"\ud800"`, `"raw_model_transcript":"\udc00"`, `"raw_model_transcript":"\ud800\u0041"`, `"RAW_MODEL_TRANSCRIPT":"hello"`} {
		data := bytes.Replace(valid, []byte(`"raw_model_transcript":"SYNTHETIC Engine 2 on scene"`), []byte(replacement), 1)
		if _, e := evaluate(bytes.NewReader(data)); e == nil {
			t.Fatal("invalid Unicode/key accepted")
		}
	}
	for _, replacement := range []string{`"raw_model_transcript":"\ud83d\ude00"`, `"raw_model_transcript":"\\ud800"`, `"raw_model_transcript":""`} {
		data := bytes.Replace(valid, []byte(`"raw_model_transcript":"SYNTHETIC Engine 2 on scene"`), []byte(replacement), 1)
		if _, e := evaluate(bytes.NewReader(data)); e != nil {
			t.Fatal("valid Unicode/empty hypothesis rejected", e)
		}
	}
}

func TestVocabularyVersionOne(t *testing.T) {
	want := []string{"Engine 2", "Engine 5", "GEMS", "Chief", "medic", "dispatch", "10-4", "good copy", "on scene", "clear", "canceled", "transported one", "last unit on scene", "disregard that last"}
	c := newCounts()
	if len(c.Critical) != len(want) {
		t.Fatal("vocabulary changed without version")
	}
	for i, p := range want {
		if c.Critical[i].Phrase != p {
			t.Fatal("vocabulary changed without version")
		}
	}
}
func TestReportDeterminismAndSplits(t *testing.T) {
	a, b := fixture("a"), fixture("b")
	b.Split = "test"
	b.Raw = "SYNTHETIC Engine 5 on scene"
	data := append(line(a), line(b)...)
	r, e := evaluate(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	if r.Overall.Records != 2 || r.Overall.Exact != 1 || r.Overall.Substitutions != 1 || r.Overall.ReferenceWords != 10 || *r.Overall.WER != 0.1 || *r.Overall.ExactRate != 0.5 || r.Splits["validation"].Records != 0 || r.Splits["validation"].WER != nil || !strings.HasPrefix(r.Validation, "unavailable") {
		t.Fatalf("%+v", r)
	}
	r2, e := evaluate(bytes.NewReader(data))
	if e != nil || !reflect.DeepEqual(r, r2) {
		t.Fatal("nondeterministic")
	}
	r2, e = evaluate(bytes.NewReader(append(line(b), line(a)...)))
	if e != nil {
		t.Fatal(e)
	}
	r2.DatasetSHA256 = r.DatasetSHA256
	if !reflect.DeepEqual(r, r2) {
		t.Fatal("order changed metrics")
	}
	b.Split = "validation"
	r, e = evaluate(bytes.NewReader(line(b)))
	if e != nil || !strings.HasPrefix(r.Validation, "available") {
		t.Fatal(e, r)
	}
	if *ratio(3, 1) != 3 {
		t.Fatal("WER may exceed 1")
	}
}
func TestRejectMalformedRecords(t *testing.T) {
	cases := map[string]func(*record){
		"missing reference": func(r *record) { r.Reference = "" }, "punctuation reference": func(r *record) { r.Reference = "..." },
		"unknown split": func(r *record) { r.Split = "dev" }, "bad fingerprint": func(r *record) { r.Fingerprint = "bad" },
		"bad ID": func(r *record) { r.DatasetID = "bad" }, "bad channel": func(r *record) { r.Channel = "wrong" },
		"bad tgid": func(r *record) { r.TGID = 1 }, "bad attempt": func(r *record) { r.AttemptID = 0 },
		"bad duration": func(r *record) { r.DurationMS = -1 }, "missing date": func(r *record) { r.ReviewedAt = time.Time{} },
		"model URL": func(r *record) { r.Model = "https://secret" }, "controls": func(r *record) { r.Raw = "\x00" },
		"format controls": func(r *record) { r.Reference = "a\u200db" }, "text bound": func(r *record) { r.Raw = strings.Repeat("a", MaxTextBytes+1) },
		"token bound": func(r *record) { r.Raw = strings.Repeat("a ", MaxTokens+1) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := fixture(name)
			change(&r)
			if _, e := evaluate(bytes.NewReader(line(r))); e == nil {
				t.Fatal("accepted invalid")
			}
		})
	}
	valid := line(fixture("valid"))
	bad := [][]byte{nil, []byte("\n"), []byte("{}\n"), []byte("[]\n"), []byte("null\n"), []byte("{\n"), append([]byte{0xef, 0xbb, 0xbf}, valid...), append([]byte{0xff}, valid...), append(valid, valid...), bytes.Replace(valid, []byte(`"split":"train"`), []byte(`"split":"train","split":"test"`), 1), bytes.Replace(valid, []byte(`"split":"train"`), []byte(`"unexpected":"train"`), 1), bytes.Replace(valid, []byte(`"split":"train"`), []byte(`"split":null`), 1), append(bytes.TrimSpace(valid), []byte(" {}")...), []byte(strings.Repeat("a", MaxLineBytes+1))}
	for i, data := range bad {
		if _, e := evaluate(bytes.NewReader(data)); e == nil {
			t.Errorf("accepted invalid case %d", i)
		}
	}
	for _, data := range [][]byte{bytes.TrimSuffix(valid, []byte("\n")), bytes.ReplaceAll(valid, []byte("\n"), []byte("\r\n"))} {
		if _, e := evaluate(bytes.NewReader(data)); e != nil {
			t.Fatal("valid line ending", e)
		}
	}
}
func TestResourceBudgets(t *testing.T) {
	var b bytes.Buffer
	for i := 0; i <= MaxRecords; i++ {
		b.Write(line(fixture(string(rune(i)))))
	}
	if _, e := evaluate(&b); e == nil {
		t.Fatal("record limit")
	}
	b.Reset()
	for i := 0; i < 5; i++ {
		r := fixture(string(rune(i)))
		r.Reference = strings.Repeat("a ", MaxTokens)
		r.Raw = r.Reference
		b.Write(line(r))
	}
	if _, e := evaluate(&b); e == nil {
		t.Fatal("cell limit")
	}
}
func TestSafeFileAndUnchangedContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "synthetic.jsonl")
	data := line(fixture("file"))
	if e := os.WriteFile(p, data, 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.Stat(p)
	r, e := EvaluateFile(p)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.Stat(p)
	actual, _ := os.ReadFile(p)
	sum := sha256.Sum256(data)
	if !bytes.Equal(actual, data) || !before.ModTime().Equal(after.ModTime()) || r.DatasetSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("source changed/hash mismatch")
	}
	for _, bad := range []string{"", "relative.jsonl", dir, filepath.Join(dir, "missing"), dir + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(dir) + string(os.PathSeparator) + "synthetic.jsonl"} {
		if _, e := EvaluateFile(bad); e == nil {
			t.Fatal("unsafe path accepted")
		}
	}
	f, e := os.OpenFile(p, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(MaxFileBytes + 1); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e := EvaluateFile(p); e == nil {
		t.Fatal("oversized file")
	}
}
func TestLinkedFilesAndAncestors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if e := os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(target, "synthetic.jsonl")
	if e := os.WriteFile(p, line(fixture("link")), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "link.jsonl")
	if e := os.Symlink(p, link); e != nil {
		t.Skip("OS denies symlink creation; Windows reparse attributes tested independently")
	}
	if _, e := EvaluateFile(link); e == nil {
		t.Fatal("file link accepted")
	}
	ancestor := filepath.Join(dir, "ancestor")
	if e := os.Symlink(target, ancestor); e != nil {
		t.Fatal(e)
	}
	if _, e := EvaluateFile(filepath.Join(ancestor, "synthetic.jsonl")); e == nil {
		t.Fatal("linked ancestor accepted")
	}
}
