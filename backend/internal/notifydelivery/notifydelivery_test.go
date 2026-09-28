package notifydelivery

import (
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

func validEventID() string {
	return "evt_" + strings.Repeat("a", 64)
}

func strPtr(s string) *string { return &s }

func validDelivery() Delivery {
	return Delivery{
		OutboxID:      1,
		EventID:       validEventID(),
		DeviceID:      1,
		Outcome:       OutcomeSent,
		AttemptNumber: 1,
		CreatedAt:     time.Now(),
	}
}

func TestOutcomeValid(t *testing.T) {
	valid := []Outcome{OutcomeSent, OutcomeFailed, OutcomeExpired, OutcomeCanceled, OutcomeDeadLetter, OutcomeUnauthorized}
	for _, o := range valid {
		if !o.Valid() {
			t.Fatalf("expected %q to be valid", o)
		}
	}
	invalid := []Outcome{"", "pending", "SENT", "sent "}
	for _, o := range invalid {
		if o.Valid() {
			t.Fatalf("expected %q to be invalid", o)
		}
	}
}

func TestValidateMinimalDeliverySucceeds(t *testing.T) {
	if err := validDelivery().Validate(); err != nil {
		t.Fatalf("expected a minimal valid Delivery to validate, got %v", err)
	}
}

func TestValidateEveryOutcomeSucceeds(t *testing.T) {
	for _, o := range []Outcome{OutcomeSent, OutcomeFailed, OutcomeExpired, OutcomeCanceled, OutcomeDeadLetter, OutcomeUnauthorized} {
		d := validDelivery()
		d.Outcome = o
		if err := d.Validate(); err != nil {
			t.Fatalf("expected outcome %q to validate, got %v", o, err)
		}
	}
}

func TestValidateRejectsInvalidOutboxID(t *testing.T) {
	for _, id := range []notifyoutbox.OutboxID{0, -1} {
		d := validDelivery()
		d.OutboxID = id
		if err := d.Validate(); err != ErrInvalidOutboxID {
			t.Fatalf("expected ErrInvalidOutboxID for outbox id %v, got %v", id, err)
		}
	}
}

func TestValidateRejectsInvalidDeviceID(t *testing.T) {
	for _, id := range []notifydevices.DeviceID{0, -1} {
		d := validDelivery()
		d.DeviceID = id
		if err := d.Validate(); err != ErrInvalidDeviceID {
			t.Fatalf("expected ErrInvalidDeviceID for device id %v, got %v", id, err)
		}
	}
}

func TestValidateRejectsInvalidEventID(t *testing.T) {
	cases := []string{"", "not-an-event-id", "evt_abc", "evt_" + strings.Repeat("A", 64), "evt_" + strings.Repeat("a", 63), "evt_" + strings.Repeat("a", 65)}
	for _, eventID := range cases {
		d := validDelivery()
		d.EventID = eventID
		if err := d.Validate(); err != ErrInvalidEventID {
			t.Fatalf("expected ErrInvalidEventID for %q, got %v", eventID, err)
		}
	}
}

func TestValidateRejectsInvalidOutcome(t *testing.T) {
	for _, o := range []Outcome{"", "pending", "SENT"} {
		d := validDelivery()
		d.Outcome = o
		if err := d.Validate(); err != ErrInvalidOutcome {
			t.Fatalf("expected ErrInvalidOutcome for %q, got %v", o, err)
		}
	}
}

func TestValidateRejectsNonPositiveAttemptNumber(t *testing.T) {
	for _, n := range []int{0, -1} {
		d := validDelivery()
		d.AttemptNumber = n
		if err := d.Validate(); err != ErrInvalidAttemptNumber {
			t.Fatalf("expected ErrInvalidAttemptNumber for %d, got %v", n, err)
		}
	}
}

func TestValidateNilErrorCodeSucceeds(t *testing.T) {
	d := validDelivery()
	d.ErrorCode = nil
	if err := d.Validate(); err != nil {
		t.Fatalf("expected nil error_code to validate, got %v", err)
	}
}

func TestValidateValidErrorCodeSucceeds(t *testing.T) {
	d := validDelivery()
	d.ErrorCode = strPtr("quiet_hours_suppressed")
	if err := d.Validate(); err != nil {
		t.Fatalf("expected a valid error_code to validate, got %v", err)
	}
}

func TestValidateRejectsMalformedErrorCode(t *testing.T) {
	cases := []string{"", "UPPERCASE", "1starts_with_digit", "has space", "has-hyphen", strings.Repeat("a", 65)}
	for _, code := range cases {
		d := validDelivery()
		d.ErrorCode = strPtr(code)
		if err := d.Validate(); err != ErrInvalidErrorCode {
			t.Fatalf("expected ErrInvalidErrorCode for %q, got %v", code, err)
		}
	}
}

func TestValidateErrorCodeIndependentOfOutcome(t *testing.T) {
	// The schema imposes no outcome/error_code pairing, and this package
	// must not invent one: every outcome may carry any valid error_code,
	// or none at all.
	for _, o := range []Outcome{OutcomeSent, OutcomeFailed, OutcomeExpired, OutcomeCanceled, OutcomeDeadLetter, OutcomeUnauthorized} {
		d := validDelivery()
		d.Outcome = o
		d.ErrorCode = strPtr("some_code")
		if err := d.Validate(); err != nil {
			t.Fatalf("expected outcome %q with an error_code to validate, got %v", o, err)
		}
		d.ErrorCode = nil
		if err := d.Validate(); err != nil {
			t.Fatalf("expected outcome %q with no error_code to validate, got %v", o, err)
		}
	}
}

func TestValidateZeroCreatedAtDoesNotAffectValidation(t *testing.T) {
	// CreatedAt is database-generated (Record returns it), never
	// caller-validated input; Validate() intentionally does not check it.
	d := validDelivery()
	d.CreatedAt = time.Time{}
	if err := d.Validate(); err != nil {
		t.Fatalf("expected Validate to ignore CreatedAt, got %v", err)
	}
}
