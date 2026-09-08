// Package transcriptexperiment implements a separate, opt-in offline-dataset
// experiment. It never imports or alters the production transcription worker.
package transcriptexperiment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"greenwich-fire-responder/backend/internal/transcripteval"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInput     = errors.New("invalid experiment input or unsafe file target")
	ErrIntegrity = errors.New("source integrity verification failed")
	ErrEvidence  = errors.New("dataset does not match eligible canonical database evidence")
	ErrDatabase  = errors.New("read-only local database unavailable or not configured")
	ErrRequest   = errors.New("local experiment request failed; no retry performed")
)

type Options struct {
	Dataset, Recordings, Output, Name, Prompt, Split string
	AllowTest, Overwrite                             bool
	RequestTimeout, CommandTimeout                   time.Duration
}

func (o Options) Validate() error {
	if !safePath(o.Dataset) || !safePath(o.Recordings) || !safePath(o.Output) || !strings.HasSuffix(strings.ToLower(o.Output), ".jsonl") || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(o.Name) || (o.Prompt != "" && !safePath(o.Prompt)) || o.RequestTimeout < time.Second || o.RequestTimeout > 2*time.Minute || o.CommandTimeout < o.RequestTimeout || o.CommandTimeout > time.Hour {
		return ErrInput
	}
	if o.Split != "train" && o.Split != "validation" && !(o.Split == "test" && o.AllowTest) {
		return ErrInput
	}
	if o.AllowTest && o.Split != "test" {
		return ErrInput
	}
	if inside(o.Recordings, o.Output) || strings.EqualFold(o.Output, o.Dataset) || strings.EqualFold(o.Output, o.Prompt) {
		return ErrInput
	}
	return nil
}
func safeText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && strings.IndexFunc(text, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\r' && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf)
	}) < 0
}

type Timing struct {
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	Mean   float64 `json:"mean"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
	Total  float64 `json:"total"`
}
type Summary struct {
	Version          string    `json:"version"`
	Experiment       string    `json:"experiment"`
	PromptSHA256     string    `json:"prompt_sha256"`
	PromptPresent    bool      `json:"prompt_present"`
	DatasetSHA256    string    `json:"dataset_sha256"`
	OutputSHA256     string    `json:"output_sha256"`
	Model            string    `json:"model"`
	Split            string    `json:"split"`
	RequestCount     int       `json:"request_count"`
	Outcomes         []Outcome `json:"outcomes"`
	Timing           Timing    `json:"request_timing_ms"`
	TotalMS          float64   `json:"total_ms"`
	SourcesUnchanged bool      `json:"sources_unchanged"`
	Published        bool      `json:"published"`
	Status           string    `json:"status"`
	StartedAt        time.Time `json:"started_at"`
}

func (s *Summary) finish(start time.Time) {
	s.TotalMS = float64(time.Since(start)) / float64(time.Millisecond)
	var v []float64
	for _, o := range s.Outcomes {
		v = append(v, o.DurationMS)
		s.Timing.Total += o.DurationMS
	}
	if len(v) == 0 {
		return
	}
	sort.Float64s(v)
	s.Timing.Min = v[0]
	s.Timing.Max = v[len(v)-1]
	s.Timing.Mean = s.Timing.Total / float64(len(v))
	s.Timing.Median = v[len(v)/2]
	if len(v)%2 == 0 {
		s.Timing.Median = (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	s.Timing.P95 = v[(95*len(v)+99)/100-1]
}

type OpenResolver func(context.Context) (Resolver, error)
type selected struct {
	record transcripteval.Record
	source *snapshot
}

func Run(parent context.Context, o Options, open OpenResolver, sender Sender) (summary Summary, err error) {
	start := time.Now()
	summary = Summary{Version: "gfr-whisper-experiment-v1", Experiment: o.Name, Model: Model, Split: o.Split, StartedAt: start.UTC(), Status: "failed"}
	defer func() { summary.Timing = Timing{}; summary.finish(start) }()
	if o.Validate() != nil {
		return summary, ErrInput
	}
	ctx, cancel := context.WithTimeout(parent, o.CommandTimeout)
	defer cancel()
	dataset, data, e := readSnapshot(o.Dataset, transcripteval.MaxFileBytes)
	if e != nil {
		return summary, e
	}
	records, e := transcripteval.DecodeRecords(data)
	if e != nil {
		return summary, ErrInput
	}
	summary.DatasetSHA256 = sum(data)
	var inputs = []*snapshot{dataset}
	prompt := ""
	if o.Prompt != "" {
		p, b, e := readSnapshot(o.Prompt, 4096)
		if e != nil || !safeText(string(b), 4096) || strings.TrimSpace(string(b)) == "" {
			return summary, ErrInput
		}
		inputs = append(inputs, p)
		prompt = string(b)
		summary.PromptPresent = true
	}
	summary.PromptSHA256 = sum([]byte(prompt))
	dir, e := os.Lstat(o.Recordings)
	if e != nil || isLink(dir) || !dir.IsDir() {
		return summary, ErrInput
	}
	if _, e = ancestors(filepath.Join(o.Recordings, "unused")); e != nil {
		return summary, e
	}
	destination, e := outputTarget(o.Output, o.Overwrite)
	if e != nil {
		return summary, e
	}
	sidecar, e := outputTarget(o.Output+".experiment-summary.json", o.Overwrite)
	if e != nil {
		return summary, e
	}
	for _, t := range []*target{destination, sidecar} {
		for _, in := range inputs {
			if strings.EqualFold(t.path, in.path) || (t.info != nil && os.SameFile(t.info, in.info)) {
				return summary, ErrInput
			}
		}
	}
	var chosen []transcripteval.Record
	for _, r := range records {
		if r.Split == o.Split {
			if r.Model != Model {
				return summary, ErrEvidence
			}
			chosen = append(chosen, r)
		}
	}
	if len(chosen) == 0 {
		return summary, ErrInput
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].DatasetID < chosen[j].DatasetID })
	if ctx.Err() != nil {
		return summary, ErrRequest
	}
	db, e := open(ctx)
	if e != nil {
		return summary, ErrDatabase
	}
	var selectedRecords []selected
	// Always recheck every selected source after a run, including failed requests.
	verify := func() error {
		for _, s := range inputs {
			if _, e := s.read(); e != nil {
				return e
			}
		}
		for _, r := range selectedRecords {
			if _, e := r.source.read(); e != nil {
				return e
			}
		}
		return nil
	}
	defer func() {
		if e := verify(); e != nil {
			summary.SourcesUnchanged = false
			summary.Status = "integrity_failed"
			err = e
		} else {
			summary.SourcesUnchanged = true
		}
	}()
	e = func() error {
		defer db.Close()
		paths := map[string]bool{}
		for _, r := range chosen {
			if ctx.Err() != nil {
				return ErrRequest
			}
			ev, e := db.Resolve(ctx, r.AttemptID)
			if e != nil {
				return ErrEvidence
			}
			if ev.AttemptID != r.AttemptID || ev.Fingerprint != r.Fingerprint || ev.Channel != r.Channel || ev.TGID != r.TGID || ev.DurationMS != r.DurationMS || ev.Model != r.Model || ev.Raw != r.Raw || !inside(o.Recordings, ev.Path) {
				return ErrEvidence
			}
			key := strings.ToLower(ev.Path)
			if paths[key] {
				return ErrEvidence
			}
			paths[key] = true
			ext := strings.ToLower(filepath.Ext(ev.Path))
			if ext != ".mp3" && ext != ".wav" {
				return ErrInput
			}
			source, b, e := readSnapshot(ev.Path, MaxAudioBytes)
			if e != nil || len(b) == 0 {
				return ErrInput
			}
			for _, old := range selectedRecords {
				if os.SameFile(old.source.info, source.info) {
					return ErrInput
				}
			}
			for _, t := range []*target{destination, sidecar} {
				if t.info != nil && os.SameFile(t.info, source.info) {
					return ErrInput
				}
			}
			selectedRecords = append(selectedRecords, selected{r, source})
		}
		return nil
	}()
	if e != nil {
		return summary, e
	}
	var output bytes.Buffer
	for _, r := range selectedRecords {
		if ctx.Err() != nil {
			return summary, ErrRequest
		}
		audio, e := r.source.read()
		if e != nil {
			return summary, e
		}
		text, out, e := sender.Send(ctx, audio, strings.ToLower(filepath.Ext(r.source.path)), prompt)
		out.DatasetID = r.record.DatasetID
		out.SourceSHA256 = r.source.hash
		summary.Outcomes = append(summary.Outcomes, out)
		summary.RequestCount++
		if e != nil {
			return summary, ErrRequest
		}
		record := r.record
		record.Raw = text
		if json.NewEncoder(&output).Encode(record) != nil || output.Len() > transcripteval.MaxFileBytes {
			return summary, ErrInput
		}
	}
	if _, e = transcripteval.DecodeRecords(output.Bytes()); e != nil {
		return summary, ErrInput
	}
	if verify() != nil {
		return summary, ErrIntegrity
	}
	summary.SourcesUnchanged = true
	if ctx.Err() != nil {
		return summary, ErrRequest
	}
	summary.OutputSHA256 = sum(output.Bytes())
	summary.Status = "prepared"
	summary.finish(start)
	meta, e := json.MarshalIndent(summary, "", "  ")
	if e != nil || len(meta) > 1<<20 {
		return summary, ErrInput
	}
	// Each file is atomic, but two-file publication is not a filesystem transaction.
	// Publish metadata first; its output hash must match the eventual dataset.
	if sidecar.publish(meta) != nil {
		summary.Published = false
		summary.Status = "publish_failed"
		return summary, ErrInput
	}
	if destination.publish(output.Bytes()) != nil {
		summary.Published = false
		summary.Status = "publish_failed"
		return summary, ErrInput
	}
	summary.Published = true
	summary.Status = "completed"
	return summary, nil
}
