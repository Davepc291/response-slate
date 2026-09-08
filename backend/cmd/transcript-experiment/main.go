package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"greenwich-fire-responder/backend/internal/transcriptexperiment"
	"io"
	"os"
	"os/signal"
	"time"
)

func run(ctx context.Context, args []string, out, errs io.Writer) int {
	fs := flag.NewFlagSet("transcript-experiment", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o transcriptexperiment.Options
	fs.StringVar(&o.Dataset, "dataset", "", "absolute private dataset JSONL")
	fs.StringVar(&o.Recordings, "recordings-dir", "", "absolute source directory")
	fs.StringVar(&o.Output, "output", "", "absolute private result JSONL")
	fs.StringVar(&o.Name, "name", "", "experiment name")
	fs.StringVar(&o.Prompt, "prompt-file", "", "optional absolute UTF-8 prompt path")
	fs.StringVar(&o.Split, "split", "", "train or validation; test additionally requires --allow-test")
	fs.BoolVar(&o.AllowTest, "allow-test", false, "explicitly permit test split")
	fs.BoolVar(&o.Overwrite, "overwrite", false, "replace existing private output and summary")
	fs.DurationVar(&o.RequestTimeout, "request-timeout", 60*time.Second, "1s to 2m")
	fs.DurationVar(&o.CommandTimeout, "command-timeout", 30*time.Minute, "at most 1h")
	if fs.Parse(args) != nil || fs.NArg() != 0 || o.Validate() != nil {
		fmt.Fprintln(errs, "invalid arguments; see docs/transcription-experiments.md")
		return 2
	}
	sender, e := transcriptexperiment.NewProvider(o.RequestTimeout)
	if e != nil {
		fmt.Fprintln(errs, e)
		return 2
	}
	defer sender.Close()
	summary, e := transcriptexperiment.Run(ctx, o, func(c context.Context) (transcriptexperiment.Resolver, error) {
		return transcriptexperiment.OpenDatabase(c, os.Getenv("GFR_DATABASE_URL"))
	}, sender)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if enc.Encode(summary) != nil {
		fmt.Fprintln(errs, "could not write experiment summary")
		return 1
	}
	if e != nil {
		fmt.Fprintln(errs, e)
		return 1
	}
	return 0
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
