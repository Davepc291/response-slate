package identityaudit

import (
	"testing"
	"time"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNewRejectsUnknownEventType(t *testing.T) {
	if _, err := New(EventType("made_up_event"), 1, 0, "", nil, now); err != ErrUnknownEventType {
		t.Fatalf("expected ErrUnknownEventType, got %v", err)
	}
}

func TestNewRequiresAccountExceptLoginFailure(t *testing.T) {
	if _, err := New(InvitationCreated, 0, 1, "", Metadata{"role": "responder"}, now); err != ErrMissingAccountOrActor {
		t.Fatalf("expected ErrMissingAccountOrActor, got %v", err)
	}
	if _, err := New(LoginFailure, 0, 0, "no_such_account", nil, now); err != nil {
		t.Fatalf("expected login_failure with no account to be constructible, got %v", err)
	}
}

func TestValidateMetadataAllowList(t *testing.T) {
	if err := ValidateMetadata(InvitationCreated, Metadata{"role": "dispatcher_operator"}); err != nil {
		t.Errorf("expected allow-listed key to pass: %v", err)
	}
	if err := ValidateMetadata(InvitationCreated, Metadata{"unexpected_key": "value"}); err != ErrMetadataKeyNotAllowed {
		t.Errorf("expected non-allow-listed key to be rejected, got %v", err)
	}
	if err := ValidateMetadata(InvitationRedeemed, Metadata{"role": "x"}); err != ErrMetadataKeyNotAllowed {
		t.Errorf("expected key allowed for one event type to be rejected for another, got %v", err)
	}
}

func TestValidateMetadataForbidsSecretShapedKeys(t *testing.T) {
	forbidden := []string{"password", "session_token", "raw_token", "cookie_value", "mfa_secret_key", "api_key", "credential_blob"}
	// Use an event type whose allow-list is otherwise empty, so any
	// acceptance below can only be explained by the forbidden-key check
	// failing to fire, not by coincidentally matching an allow-listed key.
	for _, key := range forbidden {
		err := ValidateMetadata(InvitationRedeemed, Metadata{key: "somevalue"})
		if err != ErrForbiddenMetadataKey && err != ErrMetadataKeyNotAllowed {
			t.Errorf("expected key %q to be rejected, got %v", key, err)
		}
		if err != ErrForbiddenMetadataKey {
			t.Errorf("expected key %q to specifically trip the forbidden-key deny-list, got %v", key, err)
		}
	}
}

func TestValidateMetadataValueBounds(t *testing.T) {
	if err := ValidateMetadata(InvitationCreated, Metadata{"role": ""}); err != ErrMetadataValueInvalid {
		t.Errorf("expected empty value to be rejected, got %v", err)
	}
	oversized := make([]byte, maxMetadataValueLen+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	if err := ValidateMetadata(InvitationCreated, Metadata{"role": string(oversized)}); err != ErrMetadataValueInvalid {
		t.Errorf("expected oversized value to be rejected, got %v", err)
	}
	if err := ValidateMetadata(InvitationCreated, Metadata{"role": "line1\nline2"}); err != ErrMetadataValueInvalid {
		t.Errorf("expected control character in value to be rejected, got %v", err)
	}
}

func TestLoginFailureAttemptedEmailMustNormalize(t *testing.T) {
	if err := ValidateMetadata(LoginFailure, Metadata{"attempted_email": "not-an-email"}); err != ErrMetadataValueInvalid {
		t.Errorf("expected malformed attempted_email to be rejected, got %v", err)
	}
	if err := ValidateMetadata(LoginFailure, Metadata{"attempted_email": "synthetic@example.test"}); err != nil {
		t.Errorf("expected valid normalized attempted_email to pass: %v", err)
	}
}

func TestReasonCodePattern(t *testing.T) {
	if _, err := New(LoginFailure, 0, 0, "Not-Valid-Code", nil, now); err != ErrInvalidReason {
		t.Errorf("expected invalid reason code to be rejected, got %v", err)
	}
	if _, err := New(LoginFailure, 0, 0, "no_such_account", nil, now); err != nil {
		t.Errorf("expected valid reason code to pass: %v", err)
	}
}

func TestNewClonesMetadata(t *testing.T) {
	m := Metadata{"role": "responder"}
	ev, err := New(InvitationCreated, 1, 2, "", m, now)
	if err != nil {
		t.Fatal(err)
	}
	m["role"] = "mutated"
	if ev.Metadata["role"] != "responder" {
		t.Error("expected New to defensively copy metadata")
	}
}

func TestEventRequiresCreatedAt(t *testing.T) {
	if _, err := New(InvitationCreated, 1, 2, "", Metadata{"role": "responder"}, time.Time{}); err == nil {
		t.Error("expected zero-value created_at to be rejected")
	}
}
