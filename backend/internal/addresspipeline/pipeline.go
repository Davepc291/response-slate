// Package addresspipeline composes the existing offline address stages without
// adding matching, role interpretation, or production integration.
package addresspipeline

import (
	"greenwich-fire-responder/backend/internal/addresscandidate"
	"greenwich-fire-responder/backend/internal/addressrole"
)

// Result preserves both stage outputs, including separate candidate mentions.
// State is the unchanged Interpretation.State, not a new resolution policy.
type Result struct {
	Transcript     string
	Candidates     []addresscandidate.Candidate
	Interpretation addressrole.Result
	State          addressrole.State
}

// Pipeline contains only immutable matchers, never transcripts or prior results.
// Construct with New. A constructed Pipeline supports concurrent Process calls.
type Pipeline struct {
	candidates *addresscandidate.Extractor
	roles      *addressrole.Interpreter
}

func New() (*Pipeline, error) {
	candidates, err := addresscandidate.New()
	if err != nil {
		return nil, err
	}
	roles, err := addressrole.New()
	if err != nil {
		return nil, err
	}
	return &Pipeline{candidates: candidates, roles: roles}, nil
}

// Process accepts exactly one transcript, unchanged, and invokes extraction
// before interpretation. The existing role API takes the original string and
// internally extracts its own evidence; no interpretation rules are reproduced
// here. All slices and role results are owned by this call, not the pipeline.
func (p *Pipeline) Process(transcript string) Result {
	candidates := p.candidates.Extract(transcript)
	interpretation := p.roles.Interpret(transcript)
	return Result{
		Transcript:     transcript,
		Candidates:     candidates,
		Interpretation: interpretation,
		State:          interpretation.State,
	}
}
