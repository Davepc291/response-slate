package notifywebpush

// This file's tests are local-only: every server they talk to is an
// httptest.Server/httptest.NewTLSServer bound to 127.0.0.1 on an
// OS-assigned ephemeral port. None of them ever addresses Google, Mozilla,
// Apple, or any real browser push endpoint, and none of them uses or
// requires production VAPID material -- every Options value comes from
// this package's own test-only validOptions()/testVAPIDPublicKey()/
// testVAPIDPrivateKey() helpers (notifywebpush_test.go).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/notifyrelay"
)

func requestForServer(server *httptest.Server) notifyrelay.Request {
	req := validTestModeRequest()
	req.Endpoint = server.URL + "/subscription/abc123"
	return req
}

func TestIntegrationAcceptedOnCreatedStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeAccepted {
		t.Fatalf("expected accepted, got %q", result.Outcome)
	}
}

func TestIntegrationUnauthorizedOnGoneStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeUnauthorized {
		t.Fatalf("expected unauthorized, got %q", result.Outcome)
	}
}

func TestIntegrationTemporaryFailureWithDeltaSecondsRetryAfter(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeTemporaryFailure {
		t.Fatalf("expected temporary_failure, got %q", result.Outcome)
	}
	if result.RetryAfter == nil || *result.RetryAfter != 7*time.Second {
		t.Fatalf("expected RetryAfter 7s, got %v", result.RetryAfter)
	}
}

func TestIntegrationRateLimitedWithHTTPDateRetryAfter(t *testing.T) {
	target := time.Now().Add(15 * time.Second).UTC()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", target.Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeTemporaryFailure {
		t.Fatalf("expected temporary_failure, got %q", result.Outcome)
	}
	if result.RetryAfter == nil {
		t.Fatal("expected a non-nil RetryAfter")
	}
	diff := *result.RetryAfter - 15*time.Second
	if diff < -3*time.Second || diff > 3*time.Second {
		t.Fatalf("expected approximately 15s, got %v", *result.RetryAfter)
	}
}

// TestIntegrationRedirectIsNeverFollowed proves this Sender's transport
// refuses to auto-follow an HTTP redirect: the redirect target handler must
// never be invoked, and the classified Result must reflect the unfollowed
// redirect response itself, never whatever the target would have returned.
func TestIntegrationRedirectIsNeverFollowed(t *testing.T) {
	var targetHits int32

	mux := http.NewServeMux()
	mux.HandleFunc("/subscription/abc123", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		w.WriteHeader(http.StatusCreated)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomePermanentFailure {
		t.Fatalf("expected permanent_failure, got %q", result.Outcome)
	}
	if result.Code == nil || *result.Code != "unexpected_redirect_status" {
		t.Fatalf("expected Code unexpected_redirect_status, got %v", result.Code)
	}
	if atomic.LoadInt32(&targetHits) != 0 {
		t.Fatalf("expected the redirect target to never be reached, got %d hits", targetHits)
	}
}

// TestIntegrationTimeoutProducesTemporaryFailure proves the bounded
// per-send context timeout actually cancels a slow request, rather than
// waiting for the handler's full delay.
func TestIntegrationTimeoutProducesTemporaryFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
			w.WriteHeader(http.StatusCreated)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	o := validOptions()
	o.SendTimeout = SendTimeoutMin // 1s

	s, err := New(o, server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	start := time.Now()
	result, err := s.Send(context.Background(), requestForServer(server))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeTemporaryFailure {
		t.Fatalf("expected temporary_failure, got %q", result.Outcome)
	}
	if result.Code == nil || *result.Code != "network_error" {
		t.Fatalf("expected Code network_error, got %v", result.Code)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("expected the send to be cancelled near the configured 1s timeout, took %v", elapsed)
	}
}

// TestIntegrationResponseBodyNeverLeaksAndConnectionIsReusable proves a
// push service's response body content never reaches Result, and that two
// sequential sends against the same Sender/server both succeed (basic
// evidence the body is actually drained, not left hanging).
func TestIntegrationResponseBodyNeverLeaksAndConnectionIsReusable(t *testing.T) {
	secret := "super-secret-response-body-content-that-must-never-leak"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 0; i < 2; i++ {
		result, err := s.Send(context.Background(), requestForServer(server))
		if err != nil {
			t.Fatalf("send %d: unexpected error: %v", i, err)
		}
		if result.Outcome != notifyrelay.OutcomePermanentFailure {
			t.Fatalf("send %d: expected permanent_failure, got %q", i, result.Outcome)
		}
		if result.Code != nil && strings.Contains(*result.Code, secret) {
			t.Fatalf("send %d: Code must never contain the response body secret, got %q", i, *result.Code)
		}
	}
}

// TestIntegrationNoRequestReachesServerOnInvalidSubscription proves an
// invalid Request is rejected before any HTTP activity, and that the
// resulting error never contains the offending secret-shaped value.
func TestIntegrationNoRequestReachesServerOnInvalidSubscription(t *testing.T) {
	var hits int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secretAuth := "leaked-auth-value-that-must-never-reach-the-server-or-an-error"
	req := requestForServer(server)
	req.Auth = secretAuth

	_, sendErr := s.Send(context.Background(), req)
	if sendErr == nil {
		t.Fatal("expected an error for a malformed auth value")
	}
	if strings.Contains(sendErr.Error(), secretAuth) {
		t.Fatalf("error %q must never contain the caller-supplied secret value", sendErr.Error())
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected zero HTTP activity for an invalid request, got %d hits", hits)
	}
}

// TestIntegrationNoRequestReachesServerOnEnvironmentMismatch proves the
// Step 8D-B Part 12B environment/test-mode pairing check runs before any
// HTTP activity.
func TestIntegrationNoRequestReachesServerOnEnvironmentMismatch(t *testing.T) {
	var hits int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport) // Env: dev
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := requestForServer(server)
	req.TestMode = false // dev requires TestMode == true

	_, sendErr := s.Send(context.Background(), req)
	if sendErr == nil {
		t.Fatal("expected an environment mismatch error")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("expected zero HTTP activity for an environment mismatch, got %d hits", hits)
	}
}

// countingRoundTripper proves an injected transport is actually the one
// used, rather than silently ignored in favor of some other default.
type countingRoundTripper struct {
	inner http.RoundTripper
	count int32
}

func (c *countingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	atomic.AddInt32(&c.count, 1)
	return c.inner.RoundTrip(r)
}

func TestIntegrationInjectedTransportIsActuallyUsed(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	counting := &countingRoundTripper{inner: server.Client().Transport}
	s, err := New(validOptions(), counting)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := s.Send(context.Background(), requestForServer(server)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&counting.count) != 1 {
		t.Fatalf("expected the injected transport to be used exactly once, got %d", counting.count)
	}
}

// TestIntegrationTLSVerificationRejectsUntrustedCertificate proves this
// Sender's default (nil-transport) path performs normal Go TLS
// certificate verification -- it does not blanket-trust an
// httptest.NewTLSServer's self-signed certificate, which a real
// InsecureSkipVerify misconfiguration would.
func TestIntegrationTLSVerificationRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s, err := New(validOptions(), nil) // no injected transport: default cloned transport
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, sendErr := s.Send(context.Background(), requestForServer(server))
	if sendErr != nil {
		t.Fatalf("unexpected error (should classify, not propagate): %v", sendErr)
	}
	if result.Outcome != notifyrelay.OutcomeTemporaryFailure {
		t.Fatalf("expected the untrusted certificate to classify as temporary_failure, got %q", result.Outcome)
	}
	if result.Code == nil || *result.Code != "network_error" {
		t.Fatalf("expected Code network_error, got %v", result.Code)
	}
}

// TestIntegrationTLSVerificationSucceedsWithTrustedTransport is the
// positive counterpart: the identical server succeeds once a transport
// that legitimately trusts its certificate is injected, proving transport
// injection -- not a relaxed verification default -- is what governs
// trust.
func TestIntegrationTLSVerificationSucceedsWithTrustedTransport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	s, err := New(validOptions(), server.Client().Transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := s.Send(context.Background(), requestForServer(server))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != notifyrelay.OutcomeAccepted {
		t.Fatalf("expected accepted, got %q", result.Outcome)
	}
}
