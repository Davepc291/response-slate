// Package unitpipeline composes complete native offline unit and status results.
// It adds no recognition, reconciliation, or operational state.
package unitpipeline

import (
	"greenwich-fire-responder/backend/internal/unitrecognition"
	"greenwich-fire-responder/backend/internal/unitstatus"
)

// Result preserves both independent native results, including their differences.
// It is an envelope, not a deep-clone API or an aggregate resolution decision.
type Result struct {
	Transcript string
	Units      unitrecognition.Result
	UnitStatus unitstatus.Result
}

// Pipeline owns immutable matchers only. Construct with New before use;
// a constructed Pipeline supports concurrent Process calls without input history.
type Pipeline struct {
	units    *unitrecognition.Matcher
	statuses *unitstatus.Matcher
}

func New() (*Pipeline, error) {
	return newPipeline(unitrecognition.New, unitstatus.New)
}

// Constructor functions are a private test seam, never global mutable hooks.
func newPipeline(newUnits func() (*unitrecognition.Matcher, error), newStatuses func() (*unitstatus.Matcher, error)) (*Pipeline, error) {
	units, err := newUnits()
	if err != nil {
		return nil, err
	}
	statuses, err := newStatuses()
	if err != nil {
		return nil, err
	}
	return &Pipeline{units: units, statuses: statuses}, nil
}

// Process passes the identical original string to each recognizer independently.
// Native collections belong to this call; neither result informs the other.
func (p *Pipeline) Process(transcript string) Result {
	return process(transcript, p.units.Recognize, p.statuses.Recognize)
}

// This private seam tests invocation order and preservation of native results
// that validated production catalogs cannot currently produce (e.g. alternatives).
func process(transcript string, recognizeUnits func(string) unitrecognition.Result, recognizeStatus func(string) unitstatus.Result) Result {
	units := recognizeUnits(transcript)
	statuses := recognizeStatus(transcript)
	return Result{Transcript: transcript, Units: units, UnitStatus: statuses}
}
