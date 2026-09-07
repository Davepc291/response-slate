// transcript-review is an explicit local human-review tool, never a server.
package main

import (
	"context"
	"fmt"
	"greenwich-fire-responder/backend/internal/transcriptreview"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, transcriptreview.ErrUsage)
		os.Exit(1)
	}
	timeout, e := transcriptreview.Timeout(os.Getenv("GFR_REVIEW_DB_TIMEOUT"))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	store, closeDB, e := transcriptreview.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if e == nil {
		e = transcriptreview.Run(ctx, os.Args[1:], os.Stdout, store, timeout)
		closeDB()
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
