package transcriptreview

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"
)

func Timeout(value string) (time.Duration, error) {
	if value == "" {
		return 10 * time.Second, nil
	}
	d, e := time.ParseDuration(value)
	if e != nil || d < time.Second || d > 30*time.Second {
		return 0, ErrInput
	}
	return d, nil
}
func Run(parent context.Context, args []string, out io.Writer, store Store, timeout time.Duration) error {
	if len(args) == 0 || timeout < time.Second || timeout > 30*time.Second {
		return ErrUsage
	}
	cmd := args[0]
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var id, attempt int64
	var limit int
	var reviewer, verdict, textPath, reason, notes, output string
	var confirm, overwrite bool
	switch cmd {
	case "queue":
		f.IntVar(&limit, "limit", 25, "")
	case "show":
		f.Int64Var(&id, "id", 0, "")
	case "history":
		f.Int64Var(&id, "id", 0, "")
		f.IntVar(&limit, "limit", 50, "")
	case "submit":
		f.Int64Var(&id, "id", 0, "")
		f.Int64Var(&attempt, "attempt", 0, "")
		f.StringVar(&reviewer, "reviewer", "", "")
		f.StringVar(&verdict, "verdict", "", "")
		f.StringVar(&textPath, "text-file", "", "")
		f.StringVar(&reason, "reason", "", "")
		f.StringVar(&notes, "notes", "", "")
		f.BoolVar(&confirm, "confirm-human", false, "")
	case "export":
		f.IntVar(&limit, "limit", 100, "")
		f.StringVar(&output, "output", "", "")
		f.BoolVar(&overwrite, "overwrite", false, "")
	case "stats":
	default:
		return ErrUsage
	}
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return ErrUsage
	}
	if (cmd == "show" || cmd == "history" || cmd == "submit") && id < 1 {
		return ErrInput
	}
	if cmd == "queue" && (limit < 1 || limit > 200) || cmd == "history" && (limit < 1 || limit > 500) {
		return ErrInput
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return ErrUnavailable
	}
	encode := func(v any) error {
		if json.NewEncoder(out).Encode(v) != nil {
			return ErrInput
		}
		return nil
	}
	switch cmd {
	case "queue":
		r, e := store.Queue(ctx, limit)
		if e != nil {
			return e
		}
		return encode(r)
	case "show":
		r, e := store.Show(ctx, id)
		if e != nil {
			return e
		}
		return encode(r)
	case "history":
		r, e := store.History(ctx, id, limit)
		if e != nil {
			return e
		}
		return encode(r)
	case "stats":
		r, e := store.Stats(ctx)
		if e != nil {
			return e
		}
		return encode(r)
	case "submit":
		if !confirm {
			return ErrInput
		}
		reference := ""
		var e error
		if textPath != "" {
			reference, e = ReadCorrection(textPath)
			if e != nil {
				return e
			}
		}
		in := Submission{id, attempt, reviewer, verdict, reference, reason, notes}
		if e = in.Validate(); e != nil {
			return e
		}
		r, e := store.Submit(ctx, in)
		if e != nil {
			return e
		}
		return encode(struct {
			ID      int64  `json:"review_id"`
			Verdict string `json:"verdict"`
		}{r.ID, r.Verdict})
	case "export":
		n, e := ExportAtomic(ctx, output, overwrite, limit, store)
		if e != nil {
			return e
		}
		return encode(struct {
			Records int `json:"exported_records"`
			Limit   int `json:"limit"`
		}{n, limit})
	}
	return ErrUsage
}
