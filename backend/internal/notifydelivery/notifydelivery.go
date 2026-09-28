// Package notifydelivery is the pure domain model for Step 8D-B Part 7 (
// docs/notification-relay-amendment-v1.md Section 17): one immutable
// delivery-attempt audit record. It has no persistence (its own persistence
// adapter is the separate backend/internal/notifydeliverystore package,
// mirroring the notifydevices/notifydevicestore, notifyconsent/
// notifyconsentstore, notifyprefs/notifyprefsstore, and notifyoutbox/
// notifyoutboxstore pairs), no HTTP handler, no route, no provider SDK, no
// network dependency, no worker/claim/lease logic, and no eligibility-
// evaluation logic of any kind.
//
// This package models only the delivery-audit row's own shape: it never
// decides whether a device is still active, whether consent is still
// granted, whether preferences still match, whether quiet hours currently
// apply, whether an account is still enabled, or whether an outbox entry
// should transition to any particular state -- all of that is the future,
// separately authorized notifypreferences (evaluation) and outbox-attempt
// orchestration components' job. This package must never import
// notifydevicestore, notifyconsentstore, notifyprefsstore, or
// notifyoutboxstore (enforced by its own depcheck_test.go): it stores audit
// bookkeeping only, and must not itself gain eligibility-evaluation or
// outbox-orchestration authority by accident.
//
// AttemptNumber is always caller-supplied. This package never derives it
// from, and never reads or modifies, notification_outbox.attempt_count --
// that correspondence (and the zero-real-attempt, unauthorized-retry, and
// dead-letter numbering questions the Step 8D-B Part 7 discovery report
// left explicitly unresolved) belongs to a future, separately authorized
// attempt-orchestration component, not to this pure audit-record model.
package notifydelivery

import (
	"errors"
	"regexp"
	"time"

	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// Sentinel errors. None of these, nor any error this package returns, ever
// echoes a caller-supplied value.
var (
	ErrInvalidOutboxID      = errors.New("notifydelivery: outbox id is required")
	ErrInvalidEventID       = errors.New("notifydelivery: event id does not match the established shape")
	ErrInvalidDeviceID      = errors.New("notifydelivery: device id is required")
	ErrInvalidOutcome       = errors.New("notifydelivery: outcome is not one of the six approved values")
	ErrInvalidAttemptNumber = errors.New("notifydelivery: attempt number must be a positive integer")
	ErrInvalidErrorCode     = errors.New("notifydelivery: error code contains unsupported characters or is too long")
)

// Outcome is exactly one of the six values migration 000010's
// notification_deliveries.outcome CHECK constraint allows. There is no
// seventh value and no free-form outcome string. Unlike
// notifyoutbox.State, this is not a state a row can currently be in: it is
// a fixed, historical fact about one already-completed delivery attempt,
// recorded once and never revisited (Section 17: "one row per relay
// delivery attempt").
type Outcome string

const (
	OutcomeSent         Outcome = "sent"
	OutcomeFailed       Outcome = "failed"
	OutcomeExpired      Outcome = "expired"
	OutcomeCanceled     Outcome = "canceled"
	OutcomeDeadLetter   Outcome = "dead_letter"
	OutcomeUnauthorized Outcome = "unauthorized"
)

// Valid reports whether o is one of the six approved values. An unknown
// value is always invalid, never silently accepted.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeSent, OutcomeFailed, OutcomeExpired, OutcomeCanceled, OutcomeDeadLetter, OutcomeUnauthorized:
		return true
	}
	return false
}

// eventIDPattern duplicates alert_events.event_id's own established shape
// (database/migrations/000007_alert_persistence.sql), identically to how
// notifyoutboxstore.eventIDPattern already duplicates it locally, for the
// same reason: this package must never import internal/alerts or
// internal/alertstore (enforced by its own depcheck_test.go), and must
// still validate before ever reaching SQL.
var eventIDPattern = regexp.MustCompile(`^evt_[0-9a-f]{64}$`)

// errorCodePattern duplicates notification_deliveries.error_code's own
// CHECK shape exactly (migration 000010, itself mirroring
// detection_audit.reason's identical convention). No closed set of
// error-code values is defined here, deliberately: this package is not the
// producer of error codes (a future attempt-orchestration component is),
// only their generic, format-validating persistence layer -- exactly the
// same producer/consumer split already established between
// tonedetection's own closed Reason enum and alertstore's format-only
// reasonPattern validation.
var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// DeliveryID is the server-generated, opaque delivery-record identifier. It
// is never client-supplied.
type DeliveryID int64

// Delivery is one immutable delivery-attempt audit record (migration
// 000010's notification_deliveries row shape, Section 17). It carries no
// user_id: that table has no such column, unlike notification_outbox.
//
// OutboxID and DeviceID reuse notifyoutbox.OutboxID and
// notifydevices.DeviceID directly, since they identify the exact same
// real-world outbox row/device this package's own rows reference, not a
// new concept. EventID stays a plain opaque string rather than
// alerts.Event's own EventID type, exactly mirroring
// notifyoutbox.Entry.EventID's identical precedent: this package must
// never import internal/alerts.
type Delivery struct {
	ID       DeliveryID
	OutboxID notifyoutbox.OutboxID
	EventID  string
	DeviceID notifydevices.DeviceID
	Outcome  Outcome
	// AttemptNumber is always caller-supplied (see this package's own doc
	// comment): it is never derived from, and never modifies,
	// notification_outbox.attempt_count.
	AttemptNumber int
	// ErrorCode is a fixed-shape, allow-listed-by-format-only code (see
	// errorCodePattern's own doc comment). nil means no error code is
	// recorded, which is valid for every Outcome value: the schema imposes
	// no outcome/error_code pairing requirement, and this package invents
	// none either.
	ErrorCode *string
	CreatedAt time.Time
}

// Validate checks every field of d independently. It never trusts a
// caller to have validated first, and never touches a database. It
// deliberately imposes no additional semantic coupling between fields
// beyond what migration 000010 itself requires: no outcome/error_code
// pairing rule, no cross-check against any other table's state.
func (d Delivery) Validate() error {
	if d.OutboxID <= 0 {
		return ErrInvalidOutboxID
	}
	if !eventIDPattern.MatchString(d.EventID) {
		return ErrInvalidEventID
	}
	if d.DeviceID <= 0 {
		return ErrInvalidDeviceID
	}
	if !d.Outcome.Valid() {
		return ErrInvalidOutcome
	}
	if d.AttemptNumber <= 0 {
		return ErrInvalidAttemptNumber
	}
	if d.ErrorCode != nil && !errorCodePattern.MatchString(*d.ErrorCode) {
		return ErrInvalidErrorCode
	}
	return nil
}
