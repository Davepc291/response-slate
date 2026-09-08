package transcriptexperiment

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"greenwich-fire-responder/backend/internal/transcripteval"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeDB struct {
	evidence map[int64]Evidence
	closed   bool
	calls    []int64
}

func (d *fakeDB) Resolve(_ context.Context, id int64) (Evidence, error) {
	d.calls = append(d.calls, id)
	e, ok := d.evidence[id]
	if !ok {
		return e, errors.New("SYNTHETIC SECRET")
	}
	return e, nil
}
func (d *fakeDB) Close() { d.closed = true }

type fakeSender struct {
	calls   int
	hook    func()
	fail    bool
	db      *fakeDB
	prompts []string
}

func (s *fakeSender) Close() {}
func (s *fakeSender) Send(_ context.Context, _ []byte, _ string, prompt string) (string, Outcome, error) {
	if !s.db.closed {
		panic("transaction held during HTTP")
	}
	s.calls++
	s.prompts = append(s.prompts, prompt)
	if s.hook != nil {
		s.hook()
	}
	if s.fail {
		return "", Outcome{HTTPStatus: 500, Result: "failed"}, errors.New("SYNTHETIC SECRET")
	}
	return "SYNTHETIC result", Outcome{HTTPStatus: 200, DurationMS: 2, Result: "ok"}, nil
}
func setup(t *testing.T) (Options, *fakeDB, []transcripteval.Record) {
	t.Helper()
	dir := t.TempDir()
	recordings := filepath.Join(dir, "recordings")
	if e := os.Mkdir(recordings, 0700); e != nil {
		t.Fatal(e)
	}
	o := Options{Dataset: filepath.Join(dir, "dataset.jsonl"), Recordings: recordings, Output: filepath.Join(dir, "result.jsonl"), Name: "synthetic-v1", Split: "train", RequestTimeout: time.Second, CommandTimeout: time.Minute}
	db := &fakeDB{evidence: map[int64]Evidence{}}
	var records []transcripteval.Record
	var data bytes.Buffer
	for i, split := range []string{"train", "train", "test"} {
		id := int64(i + 1)
		audio := []byte(fmt.Sprintf("SYNTHETIC audio %d", i))
		path := filepath.Join(recordings, fmt.Sprintf("synthetic%d.mp3", i))
		if e := os.WriteFile(path, audio, 0600); e != nil {
			t.Fatal(e)
		}
		fp := "pcm-s16le-16000-mono-v1:sha256:" + sum(audio)
		md := md5.Sum([]byte(fp))
		r := transcripteval.Record{DatasetID: "gfr-audio-v1:" + hex.EncodeToString(md[:]), Fingerprint: fp, AttemptID: id, Channel: "CH1A", TGID: 57201, DurationMS: 1000, Raw: "SYNTHETIC baseline", Reference: "SYNTHETIC reference", Model: Model, Split: split, ReviewedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		records = append(records, r)
		json.NewEncoder(&data).Encode(r)
		db.evidence[id] = Evidence{id, path, fp, r.Channel, r.Model, r.Raw, r.TGID, r.DurationMS}
	}
	if e := os.WriteFile(o.Dataset, data.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return o, db, records
}
func execute(o Options, db *fakeDB, s *fakeSender) (Summary, error) {
	return Run(context.Background(), o, func(context.Context) (Resolver, error) { return db, nil }, s)
}
func TestExperimentSuccess(t *testing.T) {
	o, db, original := setup(t)
	s := &fakeSender{db: db}
	before, _ := os.ReadFile(o.Dataset)
	summary, e := execute(o, db, s)
	if e != nil {
		t.Fatal(e)
	}
	if summary.RequestCount != 2 || s.calls != 2 || !summary.Published || !summary.SourcesUnchanged || summary.PromptSHA256 != sum(nil) || summary.Timing.Mean != 2 || summary.OutputSHA256 == "" {
		t.Fatal(summary)
	}
	for _, id := range db.calls {
		if id == 3 {
			t.Fatal("held-out access")
		}
	}
	output, _ := os.ReadFile(o.Output)
	records, _, e := transcripteval.DecodeDataset(output)
	if e != nil || len(records) != 2 {
		t.Fatal(e)
	}
	for _, r := range records {
		if r.Raw != "SYNTHETIC result" || r.Reference != original[0].Reference || r.Model != Model {
			t.Fatal("output contract")
		}
	}
	after, _ := os.ReadFile(o.Dataset)
	if !bytes.Equal(before, after) || sum(output) != summary.OutputSHA256 {
		t.Fatal("integrity")
	}
	meta, _ := os.ReadFile(o.Output + ".experiment-summary.json")
	if bytes.Contains(meta, []byte(o.Recordings)) || bytes.Contains(meta, []byte("SYNTHETIC reference")) {
		t.Fatal("metadata leak")
	}
	if _, e = execute(o, db, s); e == nil {
		t.Fatal("overwrite silently allowed")
	}
	o.Overwrite = true
	db.closed = false
	if _, e = execute(o, db, s); e != nil {
		t.Fatal(e)
	}
}
func TestPromptAndTestOptIn(t *testing.T) {
	o, db, _ := setup(t)
	o.Split = "test"
	s := &fakeSender{db: db}
	if _, e := execute(o, db, s); e == nil || s.calls != 0 {
		t.Fatal("test not protected")
	}
	o.AllowTest = true
	o.Prompt = filepath.Join(filepath.Dir(o.Dataset), "prompt.txt")
	prompt := []byte("SYNTHETIC prompt\n")
	os.WriteFile(o.Prompt, prompt, 0600)
	summary, e := execute(o, db, s)
	if e != nil || summary.RequestCount != 1 || summary.PromptSHA256 != sum(prompt) || s.prompts[0] != string(prompt) {
		t.Fatal(e, summary)
	}
}
func TestEvidenceMismatchBeforeRequests(t *testing.T) {
	changes := []func(*Evidence){func(e *Evidence) { e.AttemptID++ }, func(e *Evidence) { e.Fingerprint = "wrong" }, func(e *Evidence) { e.Channel = "CH2B" }, func(e *Evidence) { e.TGID++ }, func(e *Evidence) { e.DurationMS++ }, func(e *Evidence) { e.Model = "other" }, func(e *Evidence) { e.Raw = "changed" }, func(e *Evidence) { e.Path = "relative" }}
	for i, change := range changes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			o, db, _ := setup(t)
			e := db.evidence[2]
			change(&e)
			db.evidence[2] = e
			s := &fakeSender{db: db}
			if _, err := execute(o, db, s); err == nil || s.calls != 0 || !db.closed {
				t.Fatal("bad evidence accepted")
			}
		})
	}
	o, db, _ := setup(t)
	e := db.evidence[2]
	e.Path = db.evidence[1].Path
	db.evidence[2] = e
	s := &fakeSender{db: db}
	if _, err := execute(o, db, s); err == nil || s.calls != 0 {
		t.Fatal("duplicate source")
	}
}
func TestFailureAndIntegrity(t *testing.T) {
	o, db, _ := setup(t)
	s := &fakeSender{db: db, fail: true}
	summary, e := execute(o, db, s)
	if e != ErrRequest || s.calls != 1 || !summary.SourcesUnchanged || strings.Contains(e.Error(), "SECRET") {
		t.Fatal(e, summary)
	}
	if _, e = os.Stat(o.Output); !os.IsNotExist(e) {
		t.Fatal("partial result published")
	}
	o, db, _ = setup(t)
	s = &fakeSender{db: db}
	s.hook = func() { os.WriteFile(db.evidence[1].Path, []byte("SYNTHETIC altered fixture"), 0600) }
	summary, e = execute(o, db, s)
	if e != ErrIntegrity || summary.SourcesUnchanged {
		t.Fatal("source mutation missed", e)
	}
	if _, e = os.Stat(o.Output); !os.IsNotExist(e) {
		t.Fatal("bad result published")
	}
}
func TestInputRejection(t *testing.T) {
	mutations := []func(*Options){func(o *Options) { o.Split = "" }, func(o *Options) { o.Split = "all" }, func(o *Options) { o.AllowTest = true }, func(o *Options) { o.Name = "../bad" }, func(o *Options) { o.Dataset = "relative" }, func(o *Options) { o.Output = o.Dataset }, func(o *Options) { o.Output = filepath.Join(o.Recordings, "out.jsonl") }, func(o *Options) { o.CommandTimeout = 2 * time.Hour }, func(o *Options) { o.RequestTimeout = 0 }}
	for _, mutate := range mutations {
		o, db, _ := setup(t)
		mutate(&o)
		s := &fakeSender{db: db}
		if _, e := execute(o, db, s); e == nil || s.calls != 0 {
			t.Fatal("unsafe input")
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 4097), "\xff", "bad\x00", "\ufeffbad"} {
		o, db, _ := setup(t)
		o.Prompt = filepath.Join(filepath.Dir(o.Dataset), "prompt.txt")
		os.WriteFile(o.Prompt, []byte(bad), 0600)
		s := &fakeSender{db: db}
		if _, e := execute(o, db, s); e == nil || s.calls != 0 {
			t.Fatal("unsafe prompt")
		}
	}
	o, db, records := setup(t)
	data, _ := json.Marshal(records[0])
	os.WriteFile(o.Dataset, append(append(data, '\n'), data...), 0600)
	s := &fakeSender{db: db}
	if _, e := execute(o, db, s); e == nil || s.calls != 0 {
		t.Fatal("duplicate dataset")
	}
}
func TestCancellationAndUnavailableDatabase(t *testing.T) {
	o, db, _ := setup(t)
	s := &fakeSender{db: db}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Run(ctx, o, func(context.Context) (Resolver, error) { t.Fatal("opened on canceled run"); return db, nil }, s); e == nil || s.calls != 0 {
		t.Fatal("cancellation")
	}
	summary, e := Run(context.Background(), o, func(context.Context) (Resolver, error) { return nil, errors.New("SYNTHETIC SECRET") }, s)
	if e != ErrDatabase || summary.RequestCount != 0 {
		t.Fatal("unsafe database error")
	}
	o.Split = "validation"
	if _, e := execute(o, db, s); e == nil || len(db.calls) != 0 {
		t.Fatal("empty split")
	}
}

func TestFailedOverwritePreservesArtifacts(t *testing.T) {
	o, db, _ := setup(t)
	o.Overwrite = true
	for _, p := range []string{o.Output, o.Output + ".experiment-summary.json"} {
		if e := os.WriteFile(p, []byte("SYNTHETIC prior artifact"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := execute(o, db, &fakeSender{db: db, fail: true}); e == nil {
		t.Fatal("expected failed request")
	}
	for _, p := range []string{o.Output, o.Output + ".experiment-summary.json"} {
		data, e := os.ReadFile(p)
		if e != nil || string(data) != "SYNTHETIC prior artifact" {
			t.Fatal("old artifact changed")
		}
	}
}
func TestTimingDefinitions(t *testing.T) {
	s := Summary{Outcomes: []Outcome{{DurationMS: 4}, {DurationMS: 1}, {DurationMS: 3}, {DurationMS: 2}}}
	s.finish(time.Now())
	if s.Timing.Min != 1 || s.Timing.Max != 4 || s.Timing.Median != 2.5 || s.Timing.Mean != 2.5 || s.Timing.Total != 10 || s.Timing.P95 != 4 {
		t.Fatal(s.Timing)
	}
}
