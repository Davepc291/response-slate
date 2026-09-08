// transcript-eval is offline: no environment, database or service configuration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"greenwich-fire-responder/backend/internal/transcripteval"
	"io"
	"os"
)

func run(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("transcript-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("dataset", "", "absolute private JSONL path")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *path == "" {
		fmt.Fprintln(errOut, "usage: transcript-eval --dataset <absolute private JSONL path>")
		return 2
	}
	report, e := transcripteval.EvaluateFile(*path)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if encoder.Encode(report) != nil {
		fmt.Fprintln(errOut, "could not write evaluation report")
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
