// Command pushtest-receiver is a standalone, local-development-only tool
// that accepts exactly one browser PushSubscription submission over HTTP
// and writes it, unmodified, to one local file -- the local, safe
// subscription-transfer step of the iPhone Push Test preparation (Part 2A).
//
// It performs no database access of any kind: it imports no notify-family
// package, holds no GFR_DATABASE_URL, and cannot reach, read, or modify any
// production or development database. Its only job is validating one JSON
// shape and writing bytes to one file.
//
// This tool is never imported by, registered with, or reachable from
// backend/cmd/api, and binds only to a loopback address itself (see
// requireLoopbackListen) -- any wider reachability (for example, from a
// phone on another device) must come from a separate reverse proxy (Caddy)
// pointed at this loopback address, never from this tool binding wide
// itself.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// maxBodyBytes bounds how much this tool will ever read from one request
// body: a resource-safety ceiling against a pathological input, not a claim
// about any real PushSubscription JSON's actual size (always a few hundred
// bytes), mirroring sandbox-device's own identical maxSubscriptionJSONBytes
// convention.
const maxBodyBytes = 16 << 10

// minTokenLen is a basic sanity floor on GFR_PUSHTEST_RECEIVER_TOKEN: not a
// claim of cryptographic strength (this tool cannot verify entropy), only a
// refusal of an obviously-too-short value typed by mistake.
const minTokenLen = 16

// Sentinel errors. None of these, nor any response this tool ever writes,
// echoes the submitted endpoint, p256dh, or auth value.
var (
	// ErrNotConfirmed marks a run that omitted the required -confirm flag.
	ErrNotConfirmed = errors.New("pushtest-receiver: refusing to run without -confirm")
	// ErrNoOutputPath marks a run with no -out path.
	ErrNoOutputPath = errors.New("pushtest-receiver: -out is required")
	// ErrTokenEmpty marks a run with no GFR_PUSHTEST_RECEIVER_TOKEN set.
	ErrTokenEmpty = errors.New("pushtest-receiver: GFR_PUSHTEST_RECEIVER_TOKEN is not set")
	// ErrTokenTooShort marks a token shorter than minTokenLen.
	ErrTokenTooShort = errors.New("pushtest-receiver: GFR_PUSHTEST_RECEIVER_TOKEN must be at least 16 characters")
	// ErrListenNotLoopback marks a -listen host other than 127.0.0.1,
	// localhost, or ::1. Structural, defense-in-depth: this tool must never
	// be directly reachable from any wider network itself.
	ErrListenNotLoopback = errors.New("pushtest-receiver: -listen must be a loopback address (127.0.0.1, localhost, or ::1)")
)

// Config is the parsed, validated input for one pushtest-receiver run.
type Config struct {
	Listen    string
	Out       string
	Token     string
	Confirmed bool
}

// Validate checks every field of cfg independently, entirely before any
// listener is bound. Order mirrors the other sandbox tools' own locked
// convention: confirmation first, then the remaining structural checks.
func (c Config) Validate() error {
	if !c.Confirmed {
		return ErrNotConfirmed
	}
	if c.Out == "" {
		return ErrNoOutputPath
	}
	if c.Token == "" {
		return ErrTokenEmpty
	}
	if len(c.Token) < minTokenLen {
		return ErrTokenTooShort
	}
	return requireLoopbackListen(c.Listen)
}

// requireLoopbackListen rejects every -listen host except 127.0.0.1,
// localhost, and ::1, mirroring the database tools' own identical
// requireLoopbackDatabase host allowlist exactly.
func requireLoopbackListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("pushtest-receiver: -listen is not a valid host:port: %w", err)
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return ErrListenNotLoopback
	}
}

// subscription mirrors the standard browser PushSubscription.toJSON()
// shape, duplicated from sandbox-device's own identical subscriptionJSON
// type rather than imported: this tool must depend on no notify-family
// package at all (it never imports notifydevices/notifydevicestore/
// notifyrelay/notifywebpush), since it never touches a database or the push
// transport -- only shape validation and a file write.
type subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// validate checks structural shape only (non-empty fields, an absolute
// https:// endpoint) -- deliberately not full key-size validation
// (notifydevices.Subscription.Validate's job, applied later by
// sandbox-device): this tool's only responsibility is "is this plausibly a
// PushSubscription," not full domain validation, since it has no
// notifydevices dependency to perform that with.
func (s subscription) validate() error {
	if strings.TrimSpace(s.Endpoint) == "" || strings.TrimSpace(s.Keys.P256dh) == "" || strings.TrimSpace(s.Keys.Auth) == "" {
		return errors.New("invalid subscription shape")
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("invalid subscription shape")
	}
	return nil
}

// WriteFile persists the validated raw subscription bytes to one path.
// Real implementations must behave like an exclusive create -- fail with
// an error satisfying errors.Is(err, os.ErrExist) if the path already
// exists -- so a second successful submission is structurally impossible
// even across a process restart. Injected so tests never touch the real
// filesystem.
type WriteFile func(path string, data []byte) error

// Deps carries every side-effecting dependency a Receiver needs.
type Deps struct {
	WriteFile WriteFile
	// Logger receives exactly one line per request, each a fixed,
	// hardcoded outcome label -- never a value derived from the request
	// body, header, or any subscription field. This is a structural
	// guarantee from how handleSubmit is written, not a redaction step
	// applied after the fact: no log call in this file is ever
	// constructed from secret-bearing input.
	Logger *log.Logger
	// Done, if non-nil, is closed exactly once, immediately after the
	// first successful receive's response has been written -- so main.go
	// can shut the server down right after, keeping this tool single-use
	// at the process level too, not only at the output-file level.
	Done chan struct{}
}

// Receiver is the one-time subscription receiver. Construct with
// NewReceiver.
type Receiver struct {
	token string
	out   string
	write WriteFile
	log   *log.Logger
	done  chan struct{}

	mu   sync.Mutex
	used bool
}

// NewReceiver constructs a ready-to-use Receiver. cfg must already have
// passed Config.Validate(); NewReceiver does not re-check it.
func NewReceiver(cfg Config, deps Deps) *Receiver {
	logger := deps.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Receiver{token: cfg.Token, out: cfg.Out, write: deps.WriteFile, log: logger, done: deps.Done}
}

// Handler returns the http.Handler this tool serves. The only route is
// POST /submit; every other method or path falls through to
// http.ServeMux's own automatic 405/404 handling.
func (r *Receiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /submit", r.handleSubmit)
	return mux
}

func (r *Receiver) alreadyUsed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

// handleSubmit enforces, in order: bearer-token authentication (constant-
// time comparison, checked FIRST so an unauthenticated caller can never
// distinguish "already used" from "wrong token" -- both read identically
// as 401, leaking nothing about this receiver's state to a caller who has
// not proven they hold the token), single-use (checked twice after that --
// once as a fast path, once again immediately before the write, inside the
// same lock the write itself holds, closing the narrow window between the
// two checks), a bounded body size, JSON shape, and subscription-shape
// validation. Only on passing every check does it write the file and mark
// itself used. Every rejection path logs one fixed outcome label and
// returns a generic body -- never the Authorization header value, the
// request body, or any parsed field.
func (r *Receiver) handleSubmit(w http.ResponseWriter, req *http.Request) {
	const prefix = "Bearer "
	authHeader := req.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, prefix)
	if !strings.HasPrefix(authHeader, prefix) || subtle.ConstantTimeCompare([]byte(token), []byte(r.token)) != 1 {
		r.log.Printf("unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.alreadyUsed() {
		r.log.Printf("rejected: already used")
		http.Error(w, "this receiver has already accepted one submission", http.StatusGone)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, maxBodyBytes)
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		r.log.Printf("rejected: body too large or unreadable")
		http.Error(w, "invalid request", http.StatusRequestEntityTooLarge)
		return
	}

	var sub subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		r.log.Printf("rejected: malformed JSON")
		http.Error(w, "invalid subscription shape", http.StatusBadRequest)
		return
	}
	if err := sub.validate(); err != nil {
		r.log.Printf("rejected: invalid subscription shape")
		http.Error(w, "invalid subscription shape", http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		r.log.Printf("rejected: already used")
		http.Error(w, "this receiver has already accepted one submission", http.StatusGone)
		return
	}
	if err := r.write(r.out, raw); err != nil {
		r.mu.Unlock()
		if errors.Is(err, os.ErrExist) {
			r.log.Printf("rejected: output path already exists (already used)")
			http.Error(w, "this receiver has already accepted one submission", http.StatusGone)
			return
		}
		r.log.Printf("rejected: could not write output file")
		http.Error(w, "could not store submission", http.StatusInternalServerError)
		return
	}
	r.used = true
	r.mu.Unlock()

	r.log.Printf("received: one subscription written")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"received"}`))

	if r.done != nil {
		select {
		case <-r.done:
		default:
			close(r.done)
		}
	}
}

// realWriteFile is the production WriteFile: an exclusive create, so a
// second call against the same path always fails with an error satisfying
// errors.Is(err, os.ErrExist), regardless of process restarts.
func realWriteFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
