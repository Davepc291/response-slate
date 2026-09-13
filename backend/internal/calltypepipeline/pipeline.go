// Package calltypepipeline composes the existing offline call-type stages.
package calltypepipeline

import (
	"greenwich-fire-responder/backend/internal/calltypecandidate"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
)

// Result preserves native stage outputs. State mirrors Interpretation.State
// at return time; it does not introduce a resolution policy.
type Result struct {
	Transcript     string
	Candidates     []calltypecandidate.Candidate
	Interpretation calltypeinterpret.Result
	State          calltypeinterpret.State
}

// Pipeline holds immutable matchers, never input history or result caches.
// Construct with New before use. Concurrent Process calls are supported.
type Pipeline struct {
	candidates     *calltypecandidate.Extractor
	interpretation *calltypeinterpret.Interpreter
}

func New() (*Pipeline, error) {
	candidates, err := calltypecandidate.New()
	if err != nil {
		return nil, err
	}
	interpretation, err := calltypeinterpret.New()
	if err != nil {
		return nil, err
	}
	return &Pipeline{candidates: candidates, interpretation: interpretation}, nil
}

// Process passes one unchanged transcript to extraction, then interpretation.
// The existing interpreter extracts its own evidence internally. All returned
// collections belong to this call and cannot mutate the pipeline.
func (p *Pipeline) Process(transcript string) Result {
	candidates := p.candidates.Extract(transcript)
	interpretation := p.interpretation.Interpret(transcript)
	return Result{Transcript: transcript, Candidates: candidates, Interpretation: interpretation, State: interpretation.State}
}
