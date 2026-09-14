// Package dispatchinterpretation composes independent offline shadow results.
package dispatchinterpretation

import (
	"greenwich-fire-responder/backend/internal/addresspipeline"
	"greenwich-fire-responder/backend/internal/addressrole"
	"greenwich-fire-responder/backend/internal/calltypeinterpret"
	"greenwich-fire-responder/backend/internal/calltypepipeline"
)

const Version = "dispatch-interpretation-v1"

type ShadowState string

const (
	BothResolved        ShadowState = "both_resolved"
	AddressOnlyResolved ShadowState = "address_only_resolved"
	TypeOnlyResolved    ShadowState = "type_only_resolved"
	ContainsAmbiguity   ShadowState = "contains_ambiguity"
	NeitherResolved     ShadowState = "neither_resolved"
)

// Result owns both complete native results. Mirrored fields agree at return.
// ShadowState has no event-identity, readiness, or operational meaning.
type Result struct {
	Version       string
	ShadowOnly    bool
	Transcript    string
	Address       addresspipeline.Result
	CallType      calltypepipeline.Result
	AddressState  addressrole.State
	CallTypeState calltypeinterpret.State
	ShadowState   ShadowState
}

// Pipeline holds only immutable components; construct with New before use.
// Concurrent calls retain no input history or result cache.
type Pipeline struct {
	address  *addresspipeline.Pipeline
	callType *calltypepipeline.Pipeline
}

func New() (*Pipeline, error) {
	a, err := addresspipeline.New()
	if err != nil {
		return nil, err
	}
	c, err := calltypepipeline.New()
	if err != nil {
		return nil, err
	}
	return &Pipeline{address: a, callType: c}, nil
}

// Process invokes address first, then call type, on the same unchanged string.
// No result informs the other component. Invalid input keeps native outcomes.
func (p *Pipeline) Process(transcript string) Result {
	a := p.address.Process(transcript)
	c := p.callType.Process(transcript)
	return Result{Version: Version, ShadowOnly: true, Transcript: transcript, Address: a, CallType: c,
		AddressState: a.State, CallTypeState: c.State, ShadowState: summarize(a.State, c.State)}
}

func summarize(a addressrole.State, c calltypeinterpret.State) ShadowState {
	switch a {
	case addressrole.Resolved, addressrole.Unsupported, addressrole.NoEvidence, addressrole.Ambiguous:
	default:
		panic("dispatchinterpretation: unknown address state requires contract review")
	}
	switch c {
	case calltypeinterpret.Resolved, calltypeinterpret.Unresolved, calltypeinterpret.Ambiguous:
	default:
		panic("dispatchinterpretation: unknown call-type state requires contract review")
	}
	if a == addressrole.Ambiguous || c == calltypeinterpret.Ambiguous {
		return ContainsAmbiguity
	}
	if a == addressrole.Resolved {
		if c == calltypeinterpret.Resolved {
			return BothResolved
		}
		return AddressOnlyResolved
	}
	if c == calltypeinterpret.Resolved {
		return TypeOnlyResolved
	}
	return NeitherResolved
}
