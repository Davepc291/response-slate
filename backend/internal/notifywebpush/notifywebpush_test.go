package notifywebpush

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/notifyrelay"
)

// testVAPIDPublicKey returns a well-formed, base64url-encoded 65-byte
// uncompressed-point-shaped value: the exact shape a real RFC 8292 VAPID
// public key has, but not a real key. Test material only -- never
// production VAPID material.
func testVAPIDPublicKey() string {
	b := make([]byte, vapidPublicKeyLen)
	b[0] = 0x04
	for i := 1; i < len(b); i++ {
		b[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// testVAPIDPrivateKey returns a well-formed, base64url-encoded 32-byte
// scalar-shaped value. Test material only -- never production VAPID
// material.
func testVAPIDPrivateKey() string {
	b := make([]byte, vapidPrivateKeyMaxLen)
	for i := range b {
		b[i] = byte(i + 50)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// testShortVAPIDPrivateKey simulates the documented math/big.Int.Bytes()
// leading-zero-byte truncation quirk: a legitimately generated scalar that
// happens to be shorter than 32 bytes. New must still accept this.
func testShortVAPIDPrivateKey() string {
	b := make([]byte, 30)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validOptions() Options {
	return Options{
		Enabled:         true,
		Env:             EnvDev,
		VAPIDPublicKey:  testVAPIDPublicKey(),
		VAPIDPrivateKey: testVAPIDPrivateKey(),
		VAPIDSubject:    "mailto:ops@example.com",
		TTL:             DefaultTTL,
		Urgency:         UrgencyNormal,
		SendTimeout:     DefaultSendTimeout,
	}
}

// testSubscriberECDHPublicKey returns a genuinely valid, uncompressed
// P-256 point: unlike a VAPID key (only ever base64url-decoded and never
// treated as a curve point by this package's own validation), a
// subscription's p256dh value IS unmarshalled onto the curve by
// webpush-go's real RFC 8291 ECDH key agreement, so a test fixture built
// from arbitrary bytes (as notifyrelay's own tests safely use, since that
// package never performs curve arithmetic) would fail deep inside the
// library with an unrelated "not a valid point on the curve" error. This
// is still test-only key material -- never a real subscriber's key.
func testSubscriberECDHPublicKey() []byte {
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic("generating test EC key: " + err.Error())
	}
	return elliptic.Marshal(elliptic.P256(), x, y)
}

func validTestModeRequest() notifyrelay.Request {
	pub := testSubscriberECDHPublicKey()
	auth := make([]byte, 16)
	for i := range auth {
		auth[i] = byte(i + 200)
	}
	return notifyrelay.Request{
		OutboxID: 1,
		Endpoint: "https://push.example.com/subscription/abc123",
		P256dh:   base64.RawURLEncoding.EncodeToString(pub),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
		Payload:  notifyrelay.Payload{Title: "Dispatch alert", Body: "Engine 3 dispatched"},
		TestMode: true,
	}
}

// --- Options / New validation ---

func TestNewAcceptsWellFormedOptions(t *testing.T) {
	s, err := New(validOptions(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected a non-nil Sender")
	}
}

func TestNewAcceptsShortPrivateKeyQuirk(t *testing.T) {
	o := validOptions()
	o.VAPIDPrivateKey = testShortVAPIDPrivateKey()
	if _, err := New(o, nil); err != nil {
		t.Fatalf("expected a legitimately short (leading-zero-truncated) private key to be accepted, got %v", err)
	}
}

func TestNewRefusesDisabled(t *testing.T) {
	o := validOptions()
	o.Enabled = false
	if _, err := New(o, nil); !errors.Is(err, ErrRelayDisabled) {
		t.Fatalf("expected %v, got %v", ErrRelayDisabled, err)
	}
}

func TestNewRefusesInvalidEnv(t *testing.T) {
	o := validOptions()
	o.Env = "staging"
	if _, err := New(o, nil); !errors.Is(err, ErrInvalidEnv) {
		t.Fatalf("expected %v, got %v", ErrInvalidEnv, err)
	}
}

func TestNewRefusesMalformedPublicKey(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"not base64":       "!!!not-valid-base64!!!",
		"wrong length":     base64.RawURLEncoding.EncodeToString([]byte("too-short")),
		"wrong first byte": func() string { b := make([]byte, 65); b[0] = 0x02; return base64.RawURLEncoding.EncodeToString(b) }(),
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			o := validOptions()
			o.VAPIDPublicKey = key
			if _, err := New(o, nil); !errors.Is(err, ErrInvalidVAPIDPublicKey) {
				t.Fatalf("expected %v, got %v", ErrInvalidVAPIDPublicKey, err)
			}
		})
	}
}

func TestNewRefusesMalformedPrivateKey(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"not base64":   "!!!not-valid-base64!!!",
		"too long":     base64.RawURLEncoding.EncodeToString(make([]byte, 33)),
		"way too long": base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			o := validOptions()
			o.VAPIDPrivateKey = key
			if _, err := New(o, nil); !errors.Is(err, ErrInvalidVAPIDPrivateKey) {
				t.Fatalf("expected %v, got %v", ErrInvalidVAPIDPrivateKey, err)
			}
		})
	}
}

func TestNewAcceptsHTTPSSubject(t *testing.T) {
	o := validOptions()
	o.VAPIDSubject = "https://example.com/contact"
	if _, err := New(o, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRefusesInvalidSubject(t *testing.T) {
	cases := []string{"", "not-an-address", "http://example.com/contact", "mailto:missing-domain", "mailto:@example.com"}
	for _, subject := range cases {
		t.Run(subject, func(t *testing.T) {
			o := validOptions()
			o.VAPIDSubject = subject
			if _, err := New(o, nil); !errors.Is(err, ErrInvalidVAPIDSubject) {
				t.Fatalf("subject %q: expected %v, got %v", subject, ErrInvalidVAPIDSubject, err)
			}
		})
	}
}

func TestNewRefusesInvalidTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, TTLMin - time.Second, TTLMax + time.Second} {
		o := validOptions()
		o.TTL = ttl
		if _, err := New(o, nil); !errors.Is(err, ErrInvalidTTL) {
			t.Fatalf("ttl %v: expected %v, got %v", ttl, ErrInvalidTTL, err)
		}
	}
}

func TestNewRefusesInvalidUrgency(t *testing.T) {
	o := validOptions()
	o.Urgency = "critical"
	if _, err := New(o, nil); !errors.Is(err, ErrInvalidUrgency) {
		t.Fatalf("expected %v, got %v", ErrInvalidUrgency, err)
	}
}

func TestNewRefusesInvalidSendTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, SendTimeoutMin - time.Millisecond, SendTimeoutMax + time.Second} {
		o := validOptions()
		o.SendTimeout = timeout
		if _, err := New(o, nil); !errors.Is(err, ErrInvalidSendTimeout) {
			t.Fatalf("timeout %v: expected %v, got %v", timeout, ErrInvalidSendTimeout, err)
		}
	}
}

// --- Send: defensive/fail-closed behavior (package-internal to reach the
// unexported Enabled=false state directly, since New never returns one) ---

func TestSendDefensivelyRefusesWhenDisabled(t *testing.T) {
	s := &Sender{options: validOptions()}
	s.options.Enabled = false

	_, err := s.Send(context.Background(), validTestModeRequest())
	if !errors.Is(err, ErrRelayDisabled) {
		t.Fatalf("expected %v, got %v", ErrRelayDisabled, err)
	}
}

func TestSendOnNilSenderRefusesRatherThanPanics(t *testing.T) {
	var s *Sender
	_, err := s.Send(context.Background(), validTestModeRequest())
	if !errors.Is(err, ErrRelayDisabled) {
		t.Fatalf("expected %v, got %v", ErrRelayDisabled, err)
	}
}

// noDialTransport fails any RoundTrip attempt, so a test using it proves no
// HTTP activity occurred if Send still reports success.
type noDialTransport struct{ t *testing.T }

func (n noDialTransport) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Fatal("unexpected HTTP activity: this call path must fail closed before any network request")
	return nil, nil
}

func TestSendRefusesInvalidRequestBeforeAnyHTTPActivity(t *testing.T) {
	s, err := New(validOptions(), noDialTransport{t})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := validTestModeRequest()
	req.Endpoint = "not-a-url"

	_, err = s.Send(context.Background(), req)
	if !errors.Is(err, notifyrelay.ErrInvalidEndpoint) {
		t.Fatalf("expected %v, got %v", notifyrelay.ErrInvalidEndpoint, err)
	}
}

func TestSendRefusesEnvironmentMismatchBeforeAnyHTTPActivity(t *testing.T) {
	s, err := New(validOptions(), noDialTransport{t}) // Env: dev
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := validTestModeRequest()
	req.TestMode = false // dev requires TestMode == true

	_, err = s.Send(context.Background(), req)
	if !errors.Is(err, ErrEnvironmentMismatch) {
		t.Fatalf("expected %v, got %v", ErrEnvironmentMismatch, err)
	}
}

func TestCheckEnvironmentPairing(t *testing.T) {
	cases := []struct {
		env      Env
		testMode bool
		wantErr  error
	}{
		{EnvDev, true, nil},
		{EnvDev, false, ErrEnvironmentMismatch},
		{EnvPreview, true, nil},
		{EnvPreview, false, ErrEnvironmentMismatch},
		{EnvProduction, false, nil},
		{EnvProduction, true, ErrEnvironmentMismatch},
	}
	for _, tc := range cases {
		err := checkEnvironmentPairing(tc.env, tc.testMode)
		if tc.wantErr == nil {
			if err != nil {
				t.Fatalf("env=%s testMode=%v: expected nil, got %v", tc.env, tc.testMode, err)
			}
			continue
		}
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("env=%s testMode=%v: expected %v, got %v", tc.env, tc.testMode, tc.wantErr, err)
		}
	}
}

// --- vapidSubscriberForLibrary ---

func TestVapidSubscriberForLibraryStripsMailtoPrefix(t *testing.T) {
	if got := vapidSubscriberForLibrary("mailto:ops@example.com"); got != "ops@example.com" {
		t.Fatalf("expected mailto: prefix stripped, got %q", got)
	}
}

func TestVapidSubscriberForLibraryPassesThroughHTTPS(t *testing.T) {
	subject := "https://example.com/contact"
	if got := vapidSubscriberForLibrary(subject); got != subject {
		t.Fatalf("expected an https subject to pass through unchanged, got %q", got)
	}
}

// --- Retry-After parsing ---

func TestParseRetryAfterDeltaSeconds(t *testing.T) {
	got := parseRetryAfter("120")
	if got == nil || *got != 120*time.Second {
		t.Fatalf("expected 120s, got %v", got)
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	target := time.Now().Add(30 * time.Second).UTC()
	header := target.Format(http.TimeFormat)

	got := parseRetryAfter(header)
	if got == nil {
		t.Fatal("expected a non-nil duration")
	}
	diff := *got - 30*time.Second
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Fatalf("expected approximately 30s, got %v", *got)
	}
}

func TestParseRetryAfterPastHTTPDateClampsToZero(t *testing.T) {
	header := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(header)
	if got == nil || *got != 0 {
		t.Fatalf("expected a clamped zero duration, got %v", got)
	}
}

func TestParseRetryAfterMalformed(t *testing.T) {
	for _, header := range []string{"", "not-a-number-or-date", "-5", "  ", "Thursday"} {
		if got := parseRetryAfter(header); got != nil {
			t.Fatalf("header %q: expected nil, got %v", header, *got)
		}
	}
}

// --- classifyResponse mapping ---

func resp(status int, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h}
}

func TestClassifyResponseMapping(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		wantOutcome notifyrelay.Outcome
		wantCode    string // "" means nil Code
	}{
		{"201 created", http.StatusCreated, notifyrelay.OutcomeAccepted, ""},
		{"202 accepted", http.StatusAccepted, notifyrelay.OutcomeAccepted, ""},
		{"200 unexpected success", http.StatusOK, notifyrelay.OutcomeAccepted, "unexpected_success_status"},
		{"301 unexpected redirect", http.StatusMovedPermanently, notifyrelay.OutcomePermanentFailure, "unexpected_redirect_status"},
		{"400 bad request", http.StatusBadRequest, notifyrelay.OutcomePermanentFailure, "malformed_request"},
		{"401 unauthorized (our credential)", http.StatusUnauthorized, notifyrelay.OutcomePermanentFailure, "vapid_auth_absent"},
		{"403 forbidden (our credential)", http.StatusForbidden, notifyrelay.OutcomePermanentFailure, "vapid_auth_rejected"},
		{"404 not found (subscription)", http.StatusNotFound, notifyrelay.OutcomeUnauthorized, "subscription_gone"},
		{"410 gone (subscription)", http.StatusGone, notifyrelay.OutcomeUnauthorized, "subscription_gone"},
		{"413 too large", http.StatusRequestEntityTooLarge, notifyrelay.OutcomePermanentFailure, "payload_too_large"},
		{"429 rate limited", http.StatusTooManyRequests, notifyrelay.OutcomeTemporaryFailure, "rate_limited"},
		{"402 unrecognized 4xx", http.StatusPaymentRequired, notifyrelay.OutcomePermanentFailure, "unrecognized_client_error"},
		{"500 recognized-range server error", http.StatusInternalServerError, notifyrelay.OutcomeTemporaryFailure, "unrecognized_server_error"},
		{"503 recognized-range server error", http.StatusServiceUnavailable, notifyrelay.OutcomeTemporaryFailure, "unrecognized_server_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyResponse(resp(tc.status, nil))
			if result.Outcome != tc.wantOutcome {
				t.Fatalf("expected outcome %q, got %q", tc.wantOutcome, result.Outcome)
			}
			if tc.wantCode == "" {
				if result.Code != nil {
					t.Fatalf("expected nil Code, got %q", *result.Code)
				}
			} else if result.Code == nil || *result.Code != tc.wantCode {
				t.Fatalf("expected Code %q, got %v", tc.wantCode, result.Code)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("expected a valid Result, got %v", err)
			}
		})
	}
}

func TestClassifyResponseUnrecognizedStatusFallsBackToTemporaryFailure(t *testing.T) {
	result := classifyResponse(resp(999, nil))
	if result.Outcome != notifyrelay.OutcomeTemporaryFailure {
		t.Fatalf("expected temporary_failure, got %q", result.Outcome)
	}
	if result.Code == nil || *result.Code != "unrecognized_status" {
		t.Fatalf("expected Code unrecognized_status, got %v", result.Code)
	}
}

func TestClassifyResponseParsesRetryAfterOnRateLimit(t *testing.T) {
	result := classifyResponse(resp(http.StatusTooManyRequests, map[string]string{"Retry-After": "45"}))
	if result.RetryAfter == nil || *result.RetryAfter != 45*time.Second {
		t.Fatalf("expected RetryAfter 45s, got %v", result.RetryAfter)
	}
}

func TestClassifyResponseParsesRetryAfterOnServerError(t *testing.T) {
	result := classifyResponse(resp(http.StatusServiceUnavailable, map[string]string{"Retry-After": "10"}))
	if result.RetryAfter == nil || *result.RetryAfter != 10*time.Second {
		t.Fatalf("expected RetryAfter 10s, got %v", result.RetryAfter)
	}
}

func TestClassifyResponseNoRetryAfterHeaderLeavesItNil(t *testing.T) {
	result := classifyResponse(resp(http.StatusTooManyRequests, nil))
	if result.RetryAfter != nil {
		t.Fatalf("expected nil RetryAfter, got %v", *result.RetryAfter)
	}
}

// --- non-leakage ---

// TestSendNeverExposesSecretsInErrorText proves that rejecting a malformed
// subscription through this concrete Sender never echoes the offending
// value back in the returned error.
func TestSendNeverExposesSecretsInErrorText(t *testing.T) {
	s, err := New(validOptions(), noDialTransport{t})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	secretAuth := "leaked-auth-secret-value-that-must-never-appear-in-an-error"
	req := validTestModeRequest()
	req.Auth = secretAuth

	_, sendErr := s.Send(context.Background(), req)
	if sendErr == nil {
		t.Fatal("expected an error for a malformed auth value")
	}
	if strings.Contains(sendErr.Error(), secretAuth) {
		t.Fatalf("Send error %q must never contain the caller-supplied secret value", sendErr.Error())
	}
}

// fakeBody tracks whether Close was called and how many bytes were read,
// without performing any real I/O.
type fakeBody struct {
	io.Reader
	closed bool
}

func (f *fakeBody) Close() error {
	f.closed = true
	return nil
}

func TestDrainAndCloseAlwaysClosesTheBody(t *testing.T) {
	body := &fakeBody{Reader: strings.NewReader("some response content that must never be logged")}
	drainAndClose(body)
	if !body.closed {
		t.Fatal("expected drainAndClose to close the body")
	}
}

func TestResultWithCodeNeverAcceptsMalformedCode(t *testing.T) {
	// Defense in depth: even if a future edit passed a malformed literal,
	// resultWithCode must never surface it as a Code (see its own comment).
	result := resultWithCode(notifyrelay.OutcomeAccepted, "Not Valid! 123")
	if result.Code != nil {
		t.Fatalf("expected a malformed code to be dropped, not surfaced, got %q", *result.Code)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("expected the resulting Result to still validate, got %v", err)
	}
}
