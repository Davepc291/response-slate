package transcriptreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	submission Submission
	records    []ExportRecord
	fail       error
	deadline   bool
	count      int
	emitHook   func()
}

func (s *fakeStore) seen(c context.Context) { _, s.deadline = c.Deadline(); s.count++ }
func (s *fakeStore) Queue(c context.Context, n int) ([]Candidate, error) {
	s.seen(c)
	return []Candidate{}, s.fail
}
func (s *fakeStore) Show(c context.Context, id int64) (Detail, error) {
	s.seen(c)
	return Detail{}, s.fail
}
func (s *fakeStore) Submit(c context.Context, in Submission) (Review, error) {
	s.seen(c)
	s.submission = in
	return Review{ID: 5, Verdict: in.Verdict}, s.fail
}
func (s *fakeStore) History(c context.Context, id int64, n int) ([]Review, error) {
	s.seen(c)
	return []Review{}, s.fail
}
func (s *fakeStore) Stats(c context.Context) (Stats, error) { s.seen(c); return Stats{}, s.fail }
func (s *fakeStore) Export(c context.Context, n int, emit func(ExportRecord) error) error {
	s.seen(c)
	for _, r := range s.records {
		if e := emit(r); e != nil {
			return e
		}
		if s.emitHook != nil {
			s.emitHook()
		}
	}
	return s.fail
}
func fixtureRecord() ExportRecord {
	return ExportRecord{DatasetID: "gfr-audio-v1:fixture", Fingerprint: "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("a", 64), AttemptID: 1, Channel: "CH1A", TGID: 57201, DurationMS: 1000, Raw: "  SYNTHETIC raw\n", Reference: "SYNTHETIC human input\n", Model: "small.en", Split: "train", ReviewedAt: time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)}
}
func TestCorrectionFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reference.txt")
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"utf8", []byte("SYNTHETIC é\r\n\tline"), true}, {"bom", append([]byte{239, 187, 191}, []byte("text")...), true},
		{"size", bytes.Repeat([]byte{'x'}, MaxTextBytes+1), false}, {"invalid-utf8", []byte{255}, false},
		{"control", []byte("text\x00"), false}, {"format-control", []byte("text\u202e"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if os.WriteFile(path, tc.data, 0600) != nil {
				t.Fatal("fixture")
			}
			_, e := ReadCorrection(path)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
		})
	}
	for _, bad := range []string{"relative.txt", filepath.Dir(path), filepath.Join(filepath.Dir(path), "missing"), filepath.Dir(path) + string(filepath.Separator) + ".." + string(filepath.Separator) + "reference.txt"} {
		if _, e := ReadCorrection(bad); e == nil {
			t.Fatal("unsafe accepted")
		}
	}
}
func TestSubmissionValidation(t *testing.T) {
	good := Submission{TransmissionID: 1, AttemptID: 2, Reviewer: "fixture-reviewer", Verdict: "accepted", Reference: "SYNTHETIC reference"}
	if good.Validate() != nil {
		t.Fatal("valid fixture rejected")
	}
	for _, mutate := range []func(*Submission){
		func(s *Submission) { s.Reference = " \n\t" }, func(s *Submission) { s.Reference = strings.Repeat("x", MaxTextBytes+1) },
		func(s *Submission) { s.Reviewer = "" }, func(s *Submission) { s.Reviewer = "secret\x01" }, func(s *Submission) { s.Verdict = "auto-approved" },
		func(s *Submission) { s.TransmissionID = 0 }, func(s *Submission) { s.AttemptID = 0 }, func(s *Submission) { s.Verdict = "excluded"; s.Reason = " " },
	} {
		s := good
		mutate(&s)
		if s.Validate() == nil {
			t.Fatal("invalid submission accepted")
		}
	}
	follow := good
	follow.Verdict = "needs_followup"
	follow.Reference = ""
	if follow.Validate() != nil {
		t.Fatal("follow-up requires invented text")
	}
}
func TestCLIExplicitSubmissionAndDeadlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reference.txt")
	os.WriteFile(path, []byte("SYNTHETIC input\n"), 0600)
	for _, cmd := range [][]string{{"queue"}, {"show", "--id", "1"}, {"history", "--id", "1"}, {"stats"},
		{"submit", "--id", "1", "--attempt", "2", "--reviewer", "fixture", "--verdict", "accepted", "--text-file", path, "--confirm-human"}} {
		s := &fakeStore{}
		var out bytes.Buffer
		if e := Run(context.Background(), cmd, &out, s, time.Second); e != nil || !s.deadline {
			t.Fatal(e, s.deadline)
		}
		if cmd[0] == "submit" && s.submission.Reference != "SYNTHETIC input\n" {
			t.Fatal("reference modified")
		}
	}
	for _, cmd := range [][]string{{"queue", "--limit", "201"}, {"queue", "--limit", "0"}, {"history", "--id", "1", "--limit", "501"}, {"show", "--id", "0"}, {"unknown"}, {"submit", "--id", "1", "--attempt", "2", "--reviewer", "fixture", "--verdict", "accepted", "--text-file", path}, {"submit", "--id", "1", "--reference", "fragile"}} {
		s := &fakeStore{}
		if Run(context.Background(), cmd, &bytes.Buffer{}, s, time.Second) == nil || s.count != 0 {
			t.Fatal("invalid CLI reached database", cmd[0])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &fakeStore{}
	if e := Run(ctx, []string{"stats"}, &bytes.Buffer{}, s, time.Second); e != ErrUnavailable || s.count != 0 {
		t.Fatal(e)
	}
	s = &fakeStore{fail: ErrUnavailable}
	var out bytes.Buffer
	if e := Run(context.Background(), []string{"stats"}, &out, s, time.Second); e != ErrUnavailable || out.Len() != 0 {
		t.Fatal("unsafe database failure")
	}
	for _, v := range []string{"secret-value", "0s", "31s"} {
		if _, e := Timeout(v); e == nil || strings.Contains(e.Error(), v) {
			t.Fatal("unsafe timeout validation")
		}
	}
}
func TestAtomicExportAndPrivacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.jsonl")
	old := []byte("old bytes")
	os.WriteFile(path, old, 0600)
	s := &fakeStore{records: []ExportRecord{fixtureRecord()}}
	if _, e := ExportAtomic(context.Background(), path, false, 1, s); e != ErrExists {
		t.Fatal(e)
	}
	s.fail = errors.New("synthetic stream failure")
	if _, e := ExportAtomic(context.Background(), path, true, 1, s); e == nil {
		t.Fatal("failure published")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, old) {
		t.Fatal("old export changed on failure")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("temporary artifact leaked")
	}
	s.fail = nil
	if n, e := ExportAtomic(context.Background(), path, true, 1, s); e != nil || n != 1 {
		t.Fatal(e, n)
	}
	actual, _ = os.ReadFile(path)
	var record map[string]any
	if json.Unmarshal(actual, &record) != nil || len(record) != 11 {
		t.Fatal("export shape", string(actual))
	}
	for _, forbidden := range []string{"source_path", "filename", "rid", "reviewer", "notes", "reason", "password", "token", "provider_url", "audio_bytes"} {
		if _, ok := record[forbidden]; ok {
			t.Fatal("export leak", forbidden)
		}
	}
	var decoded ExportRecord
	if json.Unmarshal(actual, &decoded) != nil || decoded != fixtureRecord() {
		t.Fatal("exact evidence changed")
	}
	s.records = append(s.records, s.records[0])
	if _, e := ExportAtomic(context.Background(), path, true, 2, s); e == nil {
		t.Fatal("duplicate or unsorted records accepted")
	}
	actual2, _ := os.ReadFile(path)
	if !bytes.Equal(actual2, actual) {
		t.Fatal("failed export replaced valid output")
	}
	if _, e := ExportAtomic(context.Background(), filepath.Join(dir, "recording.mp3"), true, 1, s); e != ErrInput {
		t.Fatal("audio output allowed")
	}
	if _, e := ExportAtomic(context.Background(), path, true, MaxExportRecords+1, s); e != ErrInput {
		t.Fatal("unbounded export")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.records = s.records[:1]
	s.emitHook = cancel
	if _, e := ExportAtomic(ctx, path, true, 1, s); e != ErrUnavailable {
		t.Fatal("canceled export published", e)
	}
}
func TestSymlinkFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	os.WriteFile(target, []byte("SYNTHETIC"), 0600)
	link := filepath.Join(dir, "link.txt")
	if e := os.Symlink(target, link); e != nil {
		t.Skip("OS does not permit unprivileged symlink creation; reparse classification tested separately")
	}
	if _, e := ReadCorrection(link); e != ErrInput {
		t.Fatal("linked correction accepted")
	}
	export := filepath.Join(dir, "link.jsonl")
	if os.Symlink(target, export) != nil {
		t.Fatal("link fixture")
	}
	if _, e := ExportAtomic(context.Background(), export, true, 1, &fakeStore{}); e != ErrInput {
		t.Fatal("linked export accepted")
	}
	parent := filepath.Join(dir, "linked-dir")
	if os.Symlink(dir, parent) != nil {
		t.Fatal("parent link fixture")
	}
	if _, e := ReadCorrection(filepath.Join(parent, "real.txt")); e != ErrInput {
		t.Fatal("linked parent accepted")
	}
}
