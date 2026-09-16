// Package shadowprocessor returns caller-owned, in-memory native shadow results.
// It has no operational authority, persistence, or input history.
package shadowprocessor

import (
	"errors"
	"strings"

	"greenwich-fire-responder/backend/internal/dispatchinterpretation"
	"greenwich-fire-responder/backend/internal/unitpipeline"
)

type SourceKind string

const (
	SourceSynthetic SourceKind = "synthetic"
	SourceReplay    SourceKind = "replay"
)

type TextKind string

const (
	TextSynthetic      TextKind = "synthetic"
	TextRawModel       TextKind = "raw_model"
	TextNormalized     TextKind = "normalized"
	TextHumanReference TextKind = "human_reference"
)

type Source struct {
	Kind      SourceKind
	Reference string
	TextKind  TextKind
	// Optional opaque caller-supplied provenance; never interpreted or opened.
	RecordingRef string
	AttemptRef   string
	Model        string
}

type Input struct {
	Source     Source
	Transcript string
}

type Result struct {
	Dispatch *dispatchinterpretation.Result
	Units    *unitpipeline.Result
}

type StageRecord struct {
	Stage       string
	Outcome     string
	FailureCode string
}

type AuditRecord struct {
	Version    string
	ShadowOnly bool
	Input      Input
	Result     Result
	Stages     []StageRecord
}

// Processor holds immutable native pipelines only. Construct with New before use.
// A constructed processor supports concurrent calls; it retains no call data.
type Processor struct {
	dispatch *dispatchinterpretation.Pipeline
	units    *unitpipeline.Pipeline
}

func New() (*Processor, error) {
	return newProcessor(dispatchinterpretation.New, unitpipeline.New)
}

// Private constructor seam, with no mutable global hooks or stored callbacks.
func newProcessor(newDispatch func() (*dispatchinterpretation.Pipeline, error), newUnits func() (*unitpipeline.Pipeline, error)) (*Processor, error) {
	d, err := newDispatch()
	if err != nil {
		return nil, errors.New("shadowprocessor: dispatch construction failed")
	}
	u, err := newUnits()
	if err != nil {
		return nil, errors.New("shadowprocessor: units construction failed")
	}
	return &Processor{dispatch: d, units: u}, nil
}

// Process passes the original transcript to dispatch first and units second.
// Completed describes execution only, including native rejection or ambiguity.
func (p *Processor) Process(input Input) (AuditRecord, error) {
	return process(input, p.dispatch.Process, p.units.Process)
}

// Private invocation seam permits testing panics and future native result shapes.
func process(input Input, dispatch func(string) dispatchinterpretation.Result, units func(string) unitpipeline.Result) (AuditRecord, error) {
	r := AuditRecord{Version: "shadow-processor-v1", ShadowOnly: true, Input: input,
		Stages: []StageRecord{{Stage: "input", Outcome: "completed"}, {Stage: "dispatch", Outcome: "skipped"}, {Stage: "units", Outcome: "skipped"}}}
	if code := validate(input); code != "" {
		r.Stages[0].Outcome, r.Stages[0].FailureCode = "failed", code
		return r, errors.New("shadowprocessor: " + code)
	}
	r.Result.Dispatch, r.Stages[1] = invoke("dispatch", input.Transcript, dispatch)
	r.Result.Units, r.Stages[2] = invoke("units", input.Transcript, units)
	if r.Result.Dispatch == nil || r.Result.Units == nil {
		return r, errors.New("shadowprocessor: native_stage_panicked")
	}
	return r, nil
}

func validate(input Input) string {
	if strings.TrimSpace(input.Source.Reference) == "" {
		return "missing_source_reference"
	}
	if input.Source.Kind != SourceSynthetic && input.Source.Kind != SourceReplay {
		return "invalid_source_kind"
	}
	switch input.Source.TextKind {
	case TextSynthetic, TextRawModel, TextNormalized, TextHumanReference:
	default:
		return "invalid_text_kind"
	}
	if len(input.Transcript) > 65536 {
		return "transcript_too_large"
	}
	return ""
}

func invoke[T any](stage, transcript string, run func(string) T) (result *T, record StageRecord) {
	record = StageRecord{Stage: stage, Outcome: "failed", FailureCode: "native_stage_panicked"}
	defer func() {
		// Do not inspect, format, or retain the panic payload, including panic(nil).
		_ = recover()
	}()
	native := run(transcript)
	result = &native
	record.Outcome, record.FailureCode = "completed", ""
	return
}
