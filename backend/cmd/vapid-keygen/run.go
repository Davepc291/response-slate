// Command vapid-keygen is a standalone, local-development-only tool that
// generates exactly one dev/sandbox VAPID key pair (RFC 8292) for Step
// 8D-B Part 13's controlled sandbox push path. It has no database access,
// no network access, and writes no file: it prints both keys to stdout
// exactly once per invocation, as ready-to-paste GFR_NOTIFY_VAPID_* lines,
// and nothing else. The developer is responsible for pasting the output
// into their own local, gitignored .env (never committed, never logged by
// this tool).
//
// This tool is never imported by, registered with, or reachable from
// backend/cmd/api. It generates no production key: every key it produces
// is explicitly labeled DEV/SANDBOX ONLY, and nothing in this tool
// distinguishes or supports a "production" mode at all.
package main

import (
	"fmt"
	"io"
)

// Generator produces one fresh VAPID key pair, base64url-encoded exactly as
// github.com/SherClockHolmes/webpush-go's own GenerateVAPIDKeys produces
// them. Declared as an injectable function (rather than calling
// webpush.GenerateVAPIDKeys directly in Run) so tests can supply a fixed,
// deterministic pair instead of depending on real crypto randomness for
// anything beyond the production entrypoint's own direct call in main.go.
type Generator func() (privateKey, publicKey string, err error)

// Deps carries every dependency Run needs, so tests never touch a real
// io.Writer bound to a terminal and never need to parse real generated key
// material to assert this tool's own output framing.
type Deps struct {
	Stdout   io.Writer
	Generate Generator
}

// Run generates exactly one VAPID key pair and prints it to deps.Stdout in
// a clearly dev/sandbox-labeled, ready-to-paste form. It performs no file
// I/O, no network I/O, no database access, and no structured logging --
// only plain fmt.Fprint* calls directly to the given writer, exactly once.
func Run(deps Deps) error {
	privateKey, publicKey, err := deps.Generate()
	if err != nil {
		return fmt.Errorf("vapid-keygen: generate key pair: %w", err)
	}

	fmt.Fprintln(deps.Stdout, "=== DEV/SANDBOX ONLY VAPID key pair (Step 8D-B Part 13) ===")
	fmt.Fprintln(deps.Stdout, "Never commit, log, or share the private key. Paste both lines into")
	fmt.Fprintln(deps.Stdout, "your own local, gitignored .env -- this tool writes no file itself.")
	fmt.Fprintln(deps.Stdout)
	fmt.Fprintf(deps.Stdout, "GFR_NOTIFY_VAPID_PUBLIC_KEY=%s\n", publicKey)
	fmt.Fprintf(deps.Stdout, "GFR_NOTIFY_VAPID_PRIVATE_KEY=%s\n", privateKey)
	fmt.Fprintln(deps.Stdout)
	fmt.Fprintln(deps.Stdout, "This key pair is not valid for, and must never be used in, production.")
	return nil
}
