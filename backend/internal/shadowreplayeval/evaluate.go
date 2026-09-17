// Package shadowreplayeval composes caller-supplied review-export JSONL through
// unchanged import and replay. It has no operational authority or history.
package shadowreplayeval

import (
	"errors"

	"greenwich-fire-responder/backend/internal/shadowimport"
	"greenwich-fire-responder/backend/internal/shadowprocessor"
	"greenwich-fire-responder/backend/internal/shadowreplay"
)

type SetKind string

const (
	SetDevelopment SetKind = "development"
	SetHeldOut     SetKind = "held_out"
)

type Pair struct {
	Channel string
	TGID    int64
	Audit   shadowprocessor.AuditRecord
}

type Summary struct {
	Version               string
	Set                   SetKind
	TextKind              shadowprocessor.TextKind
	Records               int
	ImportAccepted        bool
	ReplayExecutionFailed bool
	ProcessorFailed       int
	ProcessorCompleted    int
	DispatchShadowState   map[string]int
	AddressState          map[string]int
	CallTypeState         map[string]int
	UnitState             map[string]int
	UnitStatusState       map[string]int
	AssociationsProposed  int
	AssociationsUnbound   int
	NativeDisagreement    int
	DuplicateReferences   int
	EmptyChannel          int
	ZeroTGID              int
	PairingIntact         bool
}

type Result struct {
	Pairs   []Pair
	Summary Summary
}

// Evaluator holds one importer and one runner. Construct with New before use.
// A constructed evaluator supports concurrent Evaluate calls and retains no run data.
type Evaluator struct {
	importer *shadowimport.Importer
	runner   *shadowreplay.Runner
}

func New() (*Evaluator, error) {
	return newEvaluator(shadowimport.New, shadowreplay.New)
}

// Private constructor seam, with no mutable global hooks or stored callbacks.
func newEvaluator(newImporter func() (*shadowimport.Importer, error), newRunner func() (*shadowreplay.Runner, error)) (*Evaluator, error) {
	im, err := newImporter()
	if err != nil || im == nil {
		return nil, errors.New("shadowreplayeval: construction failed")
	}
	r, err := newRunner()
	if err != nil || r == nil {
		return nil, errors.New("shadowreplayeval: construction failed")
	}
	return &Evaluator{importer: im, runner: r}, nil
}

func (e *Evaluator) Evaluate(set SetKind, sources []shadowimport.Source) (Result, error) {
	return evaluate(set, sources, e.importer.Import, e.runner.Run)
}

// Private invocation seam permits counting calls and injecting impossible pairing.
func evaluate(
	set SetKind,
	sources []shadowimport.Source,
	importFn func([]shadowimport.Source) ([]shadowimport.Record, error),
	runFn func([]shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error),
) (Result, error) {
	if set != SetDevelopment && set != SetHeldOut {
		return Result{}, errors.New("shadowreplayeval: invalid evaluation set")
	}
	var kind shadowprocessor.TextKind
	if len(sources) > 0 {
		kind = sources[0].TextKind
		for i := 1; i < len(sources); i++ {
			if sources[i].TextKind != kind {
				return Result{}, errors.New("shadowreplayeval: mixed text kind")
			}
		}
	}
	records, err := importFn(sources)
	if err != nil {
		return Result{}, err
	}
	inputs := make([]shadowprocessor.Input, len(records))
	for i := range records {
		inputs[i] = records[i].Input
	}
	audits, err := runFn(inputs)
	if audits == nil {
		sum := Summary{Version: "shadow-replay-evaluation-v1", Set: set, TextKind: kind, ImportAccepted: true, ReplayExecutionFailed: err != nil}
		if err != nil {
			return Result{Summary: sum}, err
		}
		return Result{Summary: sum}, errors.New("shadowreplayeval: pairing mismatch")
	}
	if len(audits) != len(records) {
		sum := Summary{Version: "shadow-replay-evaluation-v1", Set: set, TextKind: kind, ImportAccepted: true, ReplayExecutionFailed: err != nil}
		return Result{Summary: sum}, errors.New("shadowreplayeval: pairing mismatch")
	}
	pairs := make([]Pair, len(records))
	intact := true
	for i := range records {
		pairs[i] = Pair{Channel: records[i].Channel, TGID: records[i].TGID, Audit: audits[i]}
		if pairs[i].Channel != records[i].Channel || pairs[i].TGID != records[i].TGID || pairs[i].Audit.Input != records[i].Input || !pairs[i].Audit.ShadowOnly {
			intact = false
		}
	}
	return Result{Pairs: pairs, Summary: summarize(set, kind, records, pairs, intact, err != nil)}, err
}

func summarize(set SetKind, kind shadowprocessor.TextKind, records []shadowimport.Record, pairs []Pair, intact, replayFailed bool) Summary {
	s := Summary{
		Version:               "shadow-replay-evaluation-v1",
		Set:                   set,
		TextKind:              kind,
		Records:               len(pairs),
		ImportAccepted:        records != nil,
		ReplayExecutionFailed: replayFailed,
		PairingIntact:         intact,
		DispatchShadowState:   map[string]int{},
		AddressState:          map[string]int{},
		CallTypeState:         map[string]int{},
		UnitState:             map[string]int{},
		UnitStatusState:       map[string]int{},
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		if hasFailedStage(p.Audit) {
			s.ProcessorFailed++
		}
		if stagesCompleted(p.Audit) {
			s.ProcessorCompleted++
		}
		if p.Channel == "" {
			s.EmptyChannel++
		}
		if p.TGID == 0 {
			s.ZeroTGID++
		}
		ref := p.Audit.Input.Source.Reference
		if seen[ref] {
			s.DuplicateReferences++
		}
		seen[ref] = true
		if d := p.Audit.Result.Dispatch; d != nil {
			s.DispatchShadowState[string(d.ShadowState)]++
			s.AddressState[string(d.AddressState)]++
			s.CallTypeState[string(d.CallTypeState)]++
		}
		if u := p.Audit.Result.Units; u != nil {
			s.UnitState[string(u.Units.State)]++
			s.UnitStatusState[u.UnitStatus.State]++
			unsupported, associated := false, false
			for _, m := range u.Units.Mentions {
				if !m.Accepted && m.Reason == "unsupported_context" {
					unsupported = true
				}
			}
			for i := range u.UnitStatus.Associations {
				if u.UnitStatus.Associations[i].Unit != nil {
					s.AssociationsProposed++
					associated = true
				} else {
					s.AssociationsUnbound++
				}
			}
			if unsupported && associated {
				s.NativeDisagreement++
			}
		}
	}
	return s
}

func hasFailedStage(a shadowprocessor.AuditRecord) bool {
	for _, s := range a.Stages {
		if s.Outcome == "failed" {
			return true
		}
	}
	return false
}

func stagesCompleted(a shadowprocessor.AuditRecord) bool {
	var input, dispatch, units bool
	for _, s := range a.Stages {
		if s.Outcome != "completed" {
			continue
		}
		switch s.Stage {
		case "input":
			input = true
		case "dispatch":
			dispatch = true
		case "units":
			units = true
		}
	}
	return input && dispatch && units
}
