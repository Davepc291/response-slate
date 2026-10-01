package notifyrelay

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// validP256dh returns a well-formed, base64url-encoded 65-byte value: the
// exact shape a real RFC 8291 P-256 public key has, but not a real key.
func validP256dh() string {
	b := make([]byte, p256dhDecodedLen)
	for i := range b {
		b[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// validAuth returns a well-formed, base64url-encoded 16-byte value: the
// exact shape a real RFC 8291 shared secret has, but not a real secret.
func validAuth() string {
	b := make([]byte, authDecodedLen)
	for i := range b {
		b[i] = byte(i + 100)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validRequest() Request {
	return Request{
		OutboxID: 1,
		Endpoint: "https://push.example.com/subscription/abc123",
		P256dh:   validP256dh(),
		Auth:     validAuth(),
		Payload: Payload{
			Title: "Dispatch alert",
			Body:  "Engine 3 dispatched",
			URL:   "https://app.example.com/incidents/42",
		},
	}
}

func TestRequestValidateAcceptsWellFormedRequest(t *testing.T) {
	if err := validRequest().Validate(); err != nil {
		t.Fatalf("expected a well-formed request to validate, got %v", err)
	}
}

func TestRequestValidateAcceptsMinimalPayload(t *testing.T) {
	req := validRequest()
	req.Payload = Payload{Title: "Dispatch alert"}
	if err := req.Validate(); err != nil {
		t.Fatalf("expected a title-only payload to validate, got %v", err)
	}
}

func TestRequestValidateRejectsInvalidShapes(t *testing.T) {
	base := validRequest()

	cases := []struct {
		name    string
		mutate  func(Request) Request
		wantErr error
	}{
		{
			name:    "zero outbox id",
			mutate:  func(r Request) Request { r.OutboxID = 0; return r },
			wantErr: ErrInvalidOutboxID,
		},
		{
			name:    "negative outbox id",
			mutate:  func(r Request) Request { r.OutboxID = -5; return r },
			wantErr: ErrInvalidOutboxID,
		},
		{
			name:    "empty endpoint",
			mutate:  func(r Request) Request { r.Endpoint = ""; return r },
			wantErr: ErrInvalidEndpoint,
		},
		{
			name:    "non-https endpoint",
			mutate:  func(r Request) Request { r.Endpoint = "http://push.example.com/abc"; return r },
			wantErr: ErrInvalidEndpoint,
		},
		{
			name: "endpoint too long",
			mutate: func(r Request) Request {
				r.Endpoint = "https://push.example.com/" + strings.Repeat("a", maxEndpointLen)
				return r
			},
			wantErr: ErrInvalidEndpoint,
		},
		{
			name:    "malformed base64 p256dh",
			mutate:  func(r Request) Request { r.P256dh = "not-valid-base64!!!"; return r },
			wantErr: ErrInvalidP256dh,
		},
		{
			name: "wrong length p256dh",
			mutate: func(r Request) Request {
				r.P256dh = base64.RawURLEncoding.EncodeToString([]byte("too-short"))
				return r
			},
			wantErr: ErrInvalidP256dh,
		},
		{
			name:    "malformed base64 auth",
			mutate:  func(r Request) Request { r.Auth = "not-valid-base64!!!"; return r },
			wantErr: ErrInvalidAuth,
		},
		{
			name: "wrong length auth",
			mutate: func(r Request) Request {
				r.Auth = base64.RawURLEncoding.EncodeToString([]byte("waytoolongtobeavalidauthsecret"))
				return r
			},
			wantErr: ErrInvalidAuth,
		},
		{
			name:    "empty title",
			mutate:  func(r Request) Request { r.Payload.Title = ""; return r },
			wantErr: ErrInvalidTitle,
		},
		{
			name:    "title too long",
			mutate:  func(r Request) Request { r.Payload.Title = strings.Repeat("a", maxTitleLen+1); return r },
			wantErr: ErrInvalidTitle,
		},
		{
			name:    "body too long",
			mutate:  func(r Request) Request { r.Payload.Body = strings.Repeat("a", maxBodyLen+1); return r },
			wantErr: ErrInvalidBody,
		},
		{
			name:    "non-https click url",
			mutate:  func(r Request) Request { r.Payload.URL = "http://app.example.com/x"; return r },
			wantErr: ErrInvalidURL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.mutate(base)
			err := req.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFakeSenderSendAcceptsValidRequestByDefault(t *testing.T) {
	sender := NewFakeSender()
	result, err := sender.Send(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != OutcomeAccepted {
		t.Fatalf("expected default outcome %q, got %q", OutcomeAccepted, result.Outcome)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("expected a valid Result, got %v", err)
	}
}

func TestFakeSenderSendRejectsInvalidRequest(t *testing.T) {
	sender := NewFakeSender()
	req := validRequest()
	req.Endpoint = "not-a-url"

	_, err := sender.Send(context.Background(), req)
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("expected %v, got %v", ErrInvalidEndpoint, err)
	}
	if calls := sender.Calls(); len(calls) != 0 {
		t.Fatalf("expected an invalid request to never be recorded, got %d calls", len(calls))
	}
}

// TestFakeSenderRepresentsEveryOutcomeClass proves each of the four
// provider-neutral Outcome values (accepted, temporary failure, permanent
// failure, unauthorized) is representable through the Sender contract, by
// configuring FakeSender to return each one in turn for a distinct
// OutboxID.
func TestFakeSenderRepresentsEveryOutcomeClass(t *testing.T) {
	sender := NewFakeSender()
	code := "provider_rejected"

	cases := []struct {
		id     OutboxID
		result Result
	}{
		{id: 1, result: Result{Outcome: OutcomeAccepted}},
		{id: 2, result: Result{Outcome: OutcomeTemporaryFailure, Code: &code}},
		{id: 3, result: Result{Outcome: OutcomePermanentFailure, Code: &code}},
		{id: 4, result: Result{Outcome: OutcomeUnauthorized}},
	}

	for _, tc := range cases {
		sender.SetResult(tc.id, tc.result)
	}

	for _, tc := range cases {
		req := validRequest()
		req.OutboxID = tc.id

		got, err := sender.Send(context.Background(), req)
		if err != nil {
			t.Fatalf("outbox id %d: unexpected error: %v", tc.id, err)
		}
		if got.Outcome != tc.result.Outcome {
			t.Fatalf("outbox id %d: expected outcome %q, got %q", tc.id, tc.result.Outcome, got.Outcome)
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("outbox id %d: expected a valid Result, got %v", tc.id, err)
		}
	}
}

func TestFakeSenderSetErrorOverridesResult(t *testing.T) {
	sender := NewFakeSender()
	sender.SetResult(9, Result{Outcome: OutcomeAccepted})
	sender.SetError(9, ErrSenderUnavailable)

	req := validRequest()
	req.OutboxID = 9

	_, err := sender.Send(context.Background(), req)
	if !errors.Is(err, ErrSenderUnavailable) {
		t.Fatalf("expected %v, got %v", ErrSenderUnavailable, err)
	}
}

func TestFakeSenderCallsReturnsIndependentCopy(t *testing.T) {
	sender := NewFakeSender()
	if _, err := sender.Send(context.Background(), validRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := sender.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(calls))
	}
	calls[0].OutboxID = 999

	again := sender.Calls()
	if again[0].OutboxID != 1 {
		t.Fatalf("mutating a returned Calls() slice must not affect the FakeSender's own state")
	}
}

func TestOutcomeValid(t *testing.T) {
	valid := []Outcome{OutcomeAccepted, OutcomeTemporaryFailure, OutcomePermanentFailure, OutcomeUnauthorized}
	for _, o := range valid {
		if !o.Valid() {
			t.Fatalf("expected %q to be valid", o)
		}
	}
	if Outcome("delivered").Valid() {
		t.Fatal("expected an unknown outcome string to be invalid")
	}
	if Outcome("").Valid() {
		t.Fatal("expected an empty outcome to be invalid")
	}
}

func TestResultValidate(t *testing.T) {
	if err := (Result{Outcome: OutcomeAccepted}).Validate(); err != nil {
		t.Fatalf("expected a nil-code accepted result to validate, got %v", err)
	}
	code := "temporary_provider_error"
	if err := (Result{Outcome: OutcomeTemporaryFailure, Code: &code}).Validate(); err != nil {
		t.Fatalf("expected a well-formed code to validate, got %v", err)
	}
	if err := (Result{Outcome: "delivered"}).Validate(); err == nil {
		t.Fatal("expected an unknown outcome to fail validation")
	}
	badCode := "Not Valid! 123"
	if err := (Result{Outcome: OutcomeAccepted, Code: &badCode}).Validate(); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expected %v, got %v", ErrInvalidCode, err)
	}
}

// TestRequestTestModeIsPlainOpaqueField proves TestMode round-trips as a
// plain bool with no validation of its own (both values are always legal
// shapes for Validate) -- this package never interprets it, it only
// carries it (Step 8D-B Part 12B).
func TestRequestTestModeIsPlainOpaqueField(t *testing.T) {
	for _, tm := range []bool{true, false} {
		req := validRequest()
		req.TestMode = tm
		if err := req.Validate(); err != nil {
			t.Fatalf("TestMode=%v: expected an otherwise well-formed request to validate, got %v", tm, err)
		}
	}
}

// TestResultValidateRetryAfter proves RetryAfter is optional (nil is
// always valid), accepts a non-negative duration, and rejects a negative
// one (Step 8D-B Part 12B).
func TestResultValidateRetryAfter(t *testing.T) {
	if err := (Result{Outcome: OutcomeTemporaryFailure}).Validate(); err != nil {
		t.Fatalf("expected a nil RetryAfter to validate, got %v", err)
	}

	zero := time.Duration(0)
	if err := (Result{Outcome: OutcomeTemporaryFailure, RetryAfter: &zero}).Validate(); err != nil {
		t.Fatalf("expected a zero RetryAfter to validate, got %v", err)
	}

	positive := 30 * time.Second
	if err := (Result{Outcome: OutcomeTemporaryFailure, RetryAfter: &positive}).Validate(); err != nil {
		t.Fatalf("expected a positive RetryAfter to validate, got %v", err)
	}

	negative := -time.Second
	if err := (Result{Outcome: OutcomeTemporaryFailure, RetryAfter: &negative}).Validate(); err == nil {
		t.Fatal("expected a negative RetryAfter to fail validation")
	}
}

// TestValidationErrorsNeverExposeSecrets proves that rejecting a
// malformed/secret-shaped subscription value never echoes that value back
// in the returned error: every validation error is a fixed sentinel
// string, never a dynamically constructed message containing caller input.
func TestValidationErrorsNeverExposeSecrets(t *testing.T) {
	secretEndpoint := "https://push.example.com/subscription/super-secret-endpoint-id-should-never-leak"
	secretP256dh := "this-looks-like-a-leaked-p256dh-key-value-but-is-the-wrong-length"
	secretAuth := "this-looks-like-a-leaked-auth-secret-but-is-the-wrong-length"

	req := validRequest()
	req.Endpoint = "ftp://" + secretEndpoint // wrong scheme forces ErrInvalidEndpoint
	req.P256dh = secretP256dh
	req.Auth = secretAuth

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation to fail for a malformed request")
	}
	msg := err.Error()
	for _, secret := range []string{secretEndpoint, secretP256dh, secretAuth} {
		if strings.Contains(msg, secret) {
			t.Fatalf("validation error %q must never contain caller-supplied secret-shaped input %q", msg, secret)
		}
	}
}

// TestFakeSenderNeverExposesSecretsInError proves the same property holds
// through the Sender contract itself, not only the raw Validate call.
func TestFakeSenderNeverExposesSecretsInError(t *testing.T) {
	sender := NewFakeSender()
	secretAuth := "leaked-auth-secret-value-that-must-never-appear-in-an-error"

	req := validRequest()
	req.Auth = secretAuth

	_, err := sender.Send(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a malformed auth value")
	}
	if strings.Contains(err.Error(), secretAuth) {
		t.Fatalf("Sender error %q must never contain the caller-supplied secret value", err.Error())
	}
}
