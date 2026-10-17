package main

import (
	"bytes"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const testToken = "a-sufficiently-long-test-token-value"

func validSubscriptionBody() string {
	return `{"endpoint":"https://push.example.com/subscription/super-secret-endpoint-id","keys":{"p256dh":"secret-p256dh-value","auth":"secret-auth-value"}}`
}

// fakeWriteFile is an in-memory WriteFile fake. existing marks paths that
// should behave as already-existing (returning an os.ErrExist-satisfying
// error), mirroring the real exclusive-create semantics without touching
// the filesystem.
type fakeWriteFile struct {
	calls    []writeCall
	existing map[string]bool
	err      error // if set (and the path is not "existing"), returned instead of a nil success
}

type writeCall struct {
	path string
	data []byte
}

func (f *fakeWriteFile) write(path string, data []byte) error {
	f.calls = append(f.calls, writeCall{path: path, data: data})
	if f.existing[path] {
		return &os.PathError{Op: "open", Path: path, Err: os.ErrExist}
	}
	if f.err != nil {
		return f.err
	}
	return nil
}

func newTestReceiver(wf *fakeWriteFile, logBuf *bytes.Buffer) *Receiver {
	cfg := Config{Listen: "127.0.0.1:8099", Out: "subscription.json", Token: testToken, Confirmed: true}
	logger := log.New(logBuf, "", 0)
	return NewReceiver(cfg, Deps{WriteFile: wf.write, Logger: logger})
}

func doSubmit(r *Receiver, body, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/submit", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)
	return rec
}

// --- authentication ---

func TestHandleSubmitRejectsMissingAuthHeader(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	rec := doSubmit(r, validSubscriptionBody(), "")
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(wf.calls) != 0 {
		t.Fatal("expected no write attempt without an Authorization header")
	}
}

func TestHandleSubmitRejectsWrongToken(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	rec := doSubmit(r, validSubscriptionBody(), "Bearer wrong-token-value")
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(wf.calls) != 0 {
		t.Fatal("expected no write attempt with the wrong token")
	}
}

func TestHandleSubmitAcceptsCorrectToken(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	rec := doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(wf.calls) != 1 {
		t.Fatalf("expected exactly one write, got %d", len(wf.calls))
	}
}

// --- single-use behavior ---

func TestHandleSubmitRefusesSecondRequestAfterSuccess(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	first := doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	if first.Code != 200 {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}

	second := doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	if second.Code != 410 {
		t.Fatalf("expected second request to be refused with 410, got %d", second.Code)
	}
	if len(wf.calls) != 1 {
		t.Fatalf("expected exactly one write across both requests, got %d", len(wf.calls))
	}
}

// TestHandleSubmitChecksAuthBeforeUsedState proves authentication is
// checked before the single-use state: an unauthenticated request against
// an already-used receiver must get the identical 401 an unauthenticated
// request against a fresh receiver would get, never a 410 -- so a caller
// who has not proven they hold the token can never learn whether this
// receiver has already been used.
func TestHandleSubmitChecksAuthBeforeUsedState(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	first := doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	if first.Code != 200 {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}

	unauth := doSubmit(r, validSubscriptionBody(), "Bearer wrong-"+testToken)
	if unauth.Code != 401 {
		t.Fatalf("expected an unauthenticated request against an already-used receiver to get 401, got %d", unauth.Code)
	}

	noHeader := doSubmit(r, validSubscriptionBody(), "")
	if noHeader.Code != 401 {
		t.Fatalf("expected a missing-auth request against an already-used receiver to get 401, got %d", noHeader.Code)
	}
}

// TestHandleSubmitRefusesWhenOutputAlreadyExists proves the exclusive-
// create semantics hold even on a fresh Receiver (for example, after a
// process restart): a pre-existing output file is treated identically to
// "already used," not as a generic write failure.
func TestHandleSubmitRefusesWhenOutputAlreadyExists(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{"subscription.json": true}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	rec := doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	if rec.Code != 410 {
		t.Fatalf("expected 410 for a pre-existing output path, got %d", rec.Code)
	}
}

func TestHandleSubmitClosesDoneExactlyOnceAfterSuccess(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	cfg := Config{Listen: "127.0.0.1:8099", Out: "subscription.json", Token: testToken, Confirmed: true}
	done := make(chan struct{})
	r := NewReceiver(cfg, Deps{WriteFile: wf.write, Done: done})

	doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
	select {
	case <-done:
	default:
		t.Fatal("expected done to be closed after the first successful receive")
	}

	// A second call must never attempt to close an already-closed channel
	// (which would panic) -- the second request is refused before reaching
	// that point at all, but this proves it defensively too.
	doSubmit(r, validSubscriptionBody(), "Bearer "+testToken)
}

// --- input validation ---

func TestHandleSubmitRejectsMalformedJSON(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	rec := doSubmit(r, "not json at all", "Bearer "+testToken)
	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if len(wf.calls) != 0 {
		t.Fatal("expected no write attempt for malformed JSON")
	}
}

func TestHandleSubmitRejectsIncompleteShape(t *testing.T) {
	cases := []string{
		`{}`,
		`{"endpoint":"https://push.example.com/x"}`,
		`{"endpoint":"https://push.example.com/x","keys":{"p256dh":"a"}}`,
		`{"endpoint":"http://not-https.example.com/x","keys":{"p256dh":"a","auth":"b"}}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			wf := &fakeWriteFile{existing: map[string]bool{}}
			var logBuf bytes.Buffer
			r := newTestReceiver(wf, &logBuf)

			rec := doSubmit(r, body, "Bearer "+testToken)
			if rec.Code != 400 {
				t.Fatalf("expected 400, got %d", rec.Code)
			}
			if len(wf.calls) != 0 {
				t.Fatal("expected no write attempt for an incomplete/invalid shape")
			}
		})
	}
}

func TestHandleSubmitRejectsOversizedBody(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	huge := `{"endpoint":"https://push.example.com/x","keys":{"p256dh":"` + strings.Repeat("a", maxBodyBytes+1) + `","auth":"b"}}`
	rec := doSubmit(r, huge, "Bearer "+testToken)
	if rec.Code != 413 {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if len(wf.calls) != 0 {
		t.Fatal("expected no write attempt for an oversized body")
	}
}

// --- method/path restrictions ---

func TestHandlerRejectsWrongMethodAndPath(t *testing.T) {
	wf := &fakeWriteFile{existing: map[string]bool{}}
	var logBuf bytes.Buffer
	r := newTestReceiver(wf, &logBuf)

	get := httptest.NewRequest("GET", "/submit", nil)
	getRec := httptest.NewRecorder()
	r.Handler().ServeHTTP(getRec, get)
	if getRec.Code != 405 {
		t.Fatalf("expected 405 for GET /submit, got %d", getRec.Code)
	}

	other := httptest.NewRequest("POST", "/other", strings.NewReader(validSubscriptionBody()))
	otherRec := httptest.NewRecorder()
	r.Handler().ServeHTTP(otherRec, other)
	if otherRec.Code != 404 {
		t.Fatalf("expected 404 for POST /other, got %d", otherRec.Code)
	}
}

// --- secret redaction ---

// TestHandleSubmitNeverLogsOrEchoesSecrets proves that across every path
// this handler can take -- unauthorized, malformed, invalid shape, success,
// already-used -- neither the HTTP response body nor the log output ever
// contains the submitted endpoint, p256dh, or auth value, or the bearer
// token itself.
func TestHandleSubmitNeverLogsOrEchoesSecrets(t *testing.T) {
	endpoint := "https://push.example.com/subscription/distinct-secret-endpoint-marker"
	p256dh := "distinct-secret-p256dh-marker"
	auth := "distinct-secret-auth-marker"
	body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"` + p256dh + `","auth":"` + auth + `"}}`
	secrets := []string{endpoint, p256dh, auth, testToken}

	assertClean := func(t *testing.T, logBuf *bytes.Buffer, rec *httptest.ResponseRecorder) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(logBuf.String(), s) {
				t.Fatalf("log output must never contain %q:\n%s", s, logBuf.String())
			}
			if strings.Contains(rec.Body.String(), s) {
				t.Fatalf("response body must never contain %q:\n%s", s, rec.Body.String())
			}
		}
	}

	t.Run("unauthorized", func(t *testing.T) {
		wf := &fakeWriteFile{existing: map[string]bool{}}
		var logBuf bytes.Buffer
		r := newTestReceiver(wf, &logBuf)
		rec := doSubmit(r, body, "Bearer wrong-"+testToken)
		assertClean(t, &logBuf, rec)
	})

	t.Run("malformed", func(t *testing.T) {
		wf := &fakeWriteFile{existing: map[string]bool{}}
		var logBuf bytes.Buffer
		r := newTestReceiver(wf, &logBuf)
		rec := doSubmit(r, "not json, but mentions "+endpoint, "Bearer "+testToken)
		assertClean(t, &logBuf, rec)
	})

	t.Run("success", func(t *testing.T) {
		wf := &fakeWriteFile{existing: map[string]bool{}}
		var logBuf bytes.Buffer
		r := newTestReceiver(wf, &logBuf)
		rec := doSubmit(r, body, "Bearer "+testToken)
		assertClean(t, &logBuf, rec)
		// The write call itself legitimately receives the raw bytes (that
		// is its entire job) -- only the HTTP response and log output are
		// asserted secret-free here.
		if len(wf.calls) != 1 {
			t.Fatalf("expected exactly one write, got %d", len(wf.calls))
		}
	})

	t.Run("already_used", func(t *testing.T) {
		wf := &fakeWriteFile{existing: map[string]bool{}}
		var logBuf bytes.Buffer
		r := newTestReceiver(wf, &logBuf)
		doSubmit(r, body, "Bearer "+testToken)
		logBuf.Reset()
		rec := doSubmit(r, body, "Bearer "+testToken)
		assertClean(t, &logBuf, rec)
	})
}

// --- Config.Validate ---

func validConfig() Config {
	return Config{Listen: "127.0.0.1:8099", Out: "subscription.json", Token: testToken, Confirmed: true}
}

func TestConfigValidateRequiresConfirmation(t *testing.T) {
	cfg := validConfig()
	cfg.Confirmed = false
	if err := cfg.Validate(); !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected %v, got %v", ErrNotConfirmed, err)
	}
}

func TestConfigValidateRequiresOutputPath(t *testing.T) {
	cfg := validConfig()
	cfg.Out = ""
	if err := cfg.Validate(); !errors.Is(err, ErrNoOutputPath) {
		t.Fatalf("expected %v, got %v", ErrNoOutputPath, err)
	}
}

func TestConfigValidateRequiresToken(t *testing.T) {
	cfg := validConfig()
	cfg.Token = ""
	if err := cfg.Validate(); !errors.Is(err, ErrTokenEmpty) {
		t.Fatalf("expected %v, got %v", ErrTokenEmpty, err)
	}
}

func TestConfigValidateRejectsShortToken(t *testing.T) {
	cfg := validConfig()
	cfg.Token = "too-short"
	if err := cfg.Validate(); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("expected %v, got %v", ErrTokenTooShort, err)
	}
}

func TestConfigValidateRejectsNonLoopbackListen(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8099", "192.168.1.50:8099", "evil.example.com:8099"} {
		t.Run(addr, func(t *testing.T) {
			cfg := validConfig()
			cfg.Listen = addr
			if err := cfg.Validate(); !errors.Is(err, ErrListenNotLoopback) {
				t.Fatalf("addr %q: expected %v, got %v", addr, ErrListenNotLoopback, err)
			}
		})
	}
}

func TestConfigValidateAcceptsLoopbackListenVariants(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8099", "localhost:8099", "[::1]:8099"} {
		t.Run(addr, func(t *testing.T) {
			cfg := validConfig()
			cfg.Listen = addr
			if err := cfg.Validate(); err != nil {
				t.Fatalf("addr %q: unexpected error: %v", addr, err)
			}
		})
	}
}
