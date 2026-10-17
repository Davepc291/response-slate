package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

const shutdownGrace = 500 * time.Millisecond
const shutdownTimeout = 5 * time.Second

const usageText = `pushtest-receiver is a standalone, local-development-only tool that
accepts exactly one browser PushSubscription submission over HTTP and
writes it, unmodified, to one local file -- the iPhone Push Test
preparation's local, safe subscription-transfer step (Part 2A).

LOCAL DEVELOPMENT ONLY. This tool:
  - binds only to the loopback address given by -listen (default
    127.0.0.1:8099); it refuses any other host and never binds a wildcard
    or public address itself -- any wider reachability (for example, from
    a phone on another device) must come from a separate reverse proxy
    (Caddy) pointed at this loopback address, never from this tool binding
    wider itself;
  - requires a bearer token (GFR_PUSHTEST_RECEIVER_TOKEN, read from the
    environment only, never a flag) on every request, checked BEFORE the
    single-use state (so an unauthenticated caller can never learn whether
    this receiver has already been used), compared in constant time;
  - accepts exactly one successful submission: a second attempt, even with
    the correct token, is refused (410 Gone), and the output file is
    created with exclusive-create semantics (fails if it already exists),
    so this holds even across a process restart;
  - performs no database access of any kind and imports no notify-family
    package -- it cannot reach, read, or modify any production or
    development database;
  - never logs, prints, or echoes the submitted endpoint, p256dh, or auth
    values, on any path, success or failure;
  - exits on its own shortly after the first successful receive.

Prerequisites:
  - GFR_PUSHTEST_RECEIVER_TOKEN set in the process environment: a long,
    random, locally-generated value. This is typed by hand into the test
    page's own password-type input at test time -- never stored in any
    file the page serves, never committed, never logged.
  - -out pointing at a file that does not yet exist, outside this
    repository (or under an already-gitignored path).

Windows file permissions (-out path), corrected in Part 2B: this tool
creates the output file with POSIX mode 0600, which Go's os package only
approximates on Windows -- it does NOT translate to an owner-only ACL the
way it would on Linux/macOS. Real protection on Windows comes from where
you put the file, not from the mode bits:
  - Prefer a path under your own user profile that is not already shared
    (for example %LOCALAPPDATA%\GreenwichFireResponder\pushtest\, created
    fresh for this), never a shared/public directory such as a mapped
    drive or a folder other accounts on this machine can browse.
  - To explicitly verify or tighten the resulting ACL after the file is
    written, use icacls with your own Windows account name in place of
    <account>, for example:
      icacls "<path>" /inheritance:r /grant:r <account>:(R,W)
    which removes inherited permissions and grants only that account
    read/write -- run this once the file exists, as a manual verification
    step, not something this tool does for you.
  - Delete the file once sandbox-device has imported it; it is not needed
    again and is not deleted automatically (mirroring sandbox-device's own
    identical reminder for the subscription file it reads).

Usage:
  pushtest-receiver -out subscription.json -confirm

Flags:
`

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "pushtest-receiver:", err)
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	logger := log.New(os.Stdout, "pushtest-receiver: ", log.LstdFlags)
	done := make(chan struct{})
	receiver := NewReceiver(cfg, Deps{WriteFile: realWriteFile, Logger: logger, Done: done})

	server := &http.Server{Addr: cfg.Listen, Handler: receiver.Handler()}
	go func() {
		<-done
		// Give the in-flight response time to flush to the client before
		// shutting the listener down.
		time.Sleep(shutdownGrace)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	logger.Printf("listening on %s (single-use; exits automatically after one successful receive)", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "pushtest-receiver:", err)
		os.Exit(1)
	}
}

// parseFlags parses args into a Config. GFR_PUSHTEST_RECEIVER_TOKEN is read
// from the process environment (via getenv, injected for tests), matching
// every other sandbox tool's own identical convention for a sensitive
// value: never a command-line flag, so it never lands in shell history.
func parseFlags(args []string, getenv func(string) string, usageOut io.Writer) (Config, error) {
	fs := flag.NewFlagSet("pushtest-receiver", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.Usage = func() {
		fmt.Fprint(usageOut, usageText)
		fs.PrintDefaults()
	}

	listen := fs.String("listen", "127.0.0.1:8099", "Loopback address to listen on.")
	out := fs.String("out", "", "Path to write the received subscription JSON to; must not already exist (required).")
	confirm := fs.Bool("confirm", false, "Required. Confirms you intend to start this one-time local receiver.")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	return Config{
		Listen:    *listen,
		Out:       *out,
		Token:     getenv("GFR_PUSHTEST_RECEIVER_TOKEN"),
		Confirmed: *confirm,
	}, nil
}
