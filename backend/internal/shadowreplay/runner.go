// Package shadowreplay sequentially replays caller-supplied transcripts through
// the unchanged shadow processor. It has no operational authority or history.
package shadowreplay

import (
	"errors"

	"greenwich-fire-responder/backend/internal/shadowprocessor"
)

const (
	maxInputRecords     = 1000
	maxInputStringBytes = 4 * 1024 * 1024
)

// Runner holds one reusable shadow processor. Construct with New before use.
// A constructed runner supports concurrent Run calls and retains no run data.
type Runner struct {
	processor *shadowprocessor.Processor
}

func New() (*Runner, error) {
	return newRunner(shadowprocessor.New)
}

// Private constructor seam, with no mutable global hooks or stored callbacks.
func newRunner(newProcessor func() (*shadowprocessor.Processor, error)) (*Runner, error) {
	p, err := newProcessor()
	if err != nil || p == nil {
		return nil, errors.New("shadowreplay: processor construction failed")
	}
	return &Runner{processor: p}, nil
}

// Run admits the whole sequence, then processes each input once in caller order.
func (r *Runner) Run(inputs []shadowprocessor.Input) ([]shadowprocessor.AuditRecord, error) {
	return run(inputs, r.processor.Process)
}

// Private invocation seam forwards complete processor records without remodeling them.
func run(inputs []shadowprocessor.Input, process func(shadowprocessor.Input) (shadowprocessor.AuditRecord, error)) ([]shadowprocessor.AuditRecord, error) {
	if err := admit(inputs); err != nil {
		return nil, err
	}
	records := make([]shadowprocessor.AuditRecord, len(inputs))
	failed := false
	for i := range inputs {
		record, err := process(inputs[i])
		records[i] = record
		if err != nil {
			failed = true
		}
	}
	if failed {
		return records, errors.New("shadowreplay: one or more records failed")
	}
	return records, nil
}

func admit(inputs []shadowprocessor.Input) error {
	if len(inputs) > maxInputRecords {
		return errors.New("shadowreplay: too many input records")
	}
	remaining := maxInputStringBytes
	for i := range inputs {
		var ok bool
		remaining, ok = chargeInput(remaining, inputs[i])
		if !ok {
			return errors.New("shadowreplay: input string bytes exceed limit")
		}
	}
	return nil
}

func chargeInput(remaining int, in shadowprocessor.Input) (int, bool) {
	for _, n := range [...]int{
		len(in.Source.Kind),
		len(in.Source.Reference),
		len(in.Source.TextKind),
		len(in.Source.RecordingRef),
		len(in.Source.AttemptRef),
		len(in.Source.Model),
		len(in.Transcript),
	} {
		var ok bool
		remaining, ok = charge(remaining, n)
		if !ok {
			return remaining, false
		}
	}
	return remaining, true
}

// Reject before subtracting so a length above the remaining budget cannot wrap.
func charge(remaining, n int) (int, bool) {
	if n > remaining {
		return remaining, false
	}
	return remaining - n, true
}
