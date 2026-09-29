// Package notifyoutbox is the pure domain model for Step 8D-B Part 6 (
// docs/notification-relay-amendment-v1.md Section 10): the durable,
// bounded-retry outbox record that fans an approved (matched) alert event
// out to one specific device. It has no persistence (its own persistence
// adapter is the separate backend/internal/notifyoutboxstore package,
// mirroring the notifydevices/notifydevicestore, notifyconsent/
// notifyconsentstore, and notifyprefs/notifyprefsstore pairs), no HTTP
// handler, no route, no provider SDK, no network dependency, no worker loop,
// no provider-sending logic, and no eligibility-evaluation logic of any
// kind.
//
// This package models the outbox row's own shape, its five-state machine
// (State, Entry), and -- as of Step 8D-B Part 10 -- the fenced claim/lease
// fields a future worker uses to safely reserve one row for one delivery
// attempt at a time (see Entry.ClaimToken's own doc comment). It never
// decides whether a device is still active, whether consent is still
// granted, whether preferences still match, whether quiet hours currently
// apply, or whether an account is still enabled -- all of that is the
// future, separately authorized notifypreferences (evaluation) package's
// job, consuming this package's sibling store fresh at delivery-attempt
// time, never cached here. This package must never import
// notifydevicestore, notifyconsentstore, or notifyprefsstore (enforced by
// its own depcheck_test.go): it stores outbox bookkeeping only, and must
// not itself gain eligibility-evaluation authority by accident.
package notifyoutbox

import (
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// State is exactly one of the five values migration 000010's
// notification_outbox.state CHECK constraint allows. There is no sixth
// value and no free-form status string.
type State string

const (
	// StatePending is the only non-terminal value: newly enqueued, and
	// every retry that has not yet exhausted its attempts or reached
	// expires_at, stays in this state.
	StatePending State = "pending"
	// StateSent, StateExpired, StateCanceled, and StateDeadLetter are all
	// terminal: once reached, an entry never transitions again (see
	// Terminal below, and notifyoutboxstore's own transition methods,
	// which enforce this at the database level too).
	StateSent       State = "sent"
	StateExpired    State = "expired"
	StateCanceled   State = "canceled"
	StateDeadLetter State = "dead_letter"
)

// Valid reports whether s is one of the five approved values. An unknown
// value is always invalid, never silently accepted.
func (s State) Valid() bool {
	switch s {
	case StatePending, StateSent, StateExpired, StateCanceled, StateDeadLetter:
		return true
	}
	return false
}

// Terminal reports whether s is a terminal state: everything except
// StatePending. No transition method ever accepts a terminal entry as its
// starting state -- a terminal outcome is permanent, mirroring
// notification_outbox's own CHECK-constrained state machine (Section 10:
// "never retried indefinitely," and no state value or column exists in the
// migration to resurrect a terminal row into a fresh attempt).
func (s State) Terminal() bool {
	return s.Valid() && s != StatePending
}

// OutboxID is the server-generated, opaque outbox row identifier. It is
// never client-supplied.
type OutboxID int64

// Entry is one outbox row: exactly one (EventID, DeviceID) fan-out target
// for a matched alert event (migration 000010's notification_outbox row
// shape, Section 10). EventID duplicates alerts.Event.EventID as a plain
// opaque string rather than importing internal/alerts, exactly mirroring
// how notifyconsent/notifyprefs already avoid a sibling-package dependency
// for a value they only need to carry, not interpret. DeviceID and UserID
// reuse notifydevices.DeviceID and identity.UserID directly, since those
// identify the exact same real-world device/user this package's own rows
// reference, not a new concept.
type Entry struct {
	ID       OutboxID
	EventID  string
	DeviceID notifydevices.DeviceID
	UserID   identity.UserID
	State    State
	// AttemptCount is the number of delivery attempts already made,
	// bounded by MaxAttempts (migration 000010's own
	// notification_outbox_attempt_count_bounded CHECK:
	// attempt_count <= max_attempts).
	AttemptCount int
	// MaxAttempts is a required, caller-chosen ceiling (Section 10:
	// "an explicit caller/configuration value, never assumed"). This
	// package assumes no default of its own.
	MaxAttempts int
	// NextAttemptAt is when this entry next becomes eligible for a
	// delivery attempt. Required while State is StatePending (migration
	// 000010's own notification_outbox_pending_has_next_attempt CHECK);
	// meaningless, and not guaranteed cleared, once terminal.
	//
	// As of migration 000011 (Step 8D-B Part 10), this field is
	// deliberately dual-purpose, exactly mirroring
	// radio_transmissions.transcription_next_at's own documented dual
	// meaning: while ClaimToken is nil, it means "next retry-due time"
	// (the original Part 6 meaning, unchanged); while ClaimToken is
	// non-nil, the same field means "this claim's lease expiry" instead.
	// No second timestamp field exists or is needed: "this entry is
	// actionable right now" is always exactly
	// State == StatePending && !NextAttemptAt.After(now), regardless of
	// which meaning currently applies.
	NextAttemptAt *time.Time
	// ExpiresAt is inherited from the alert_events row this entry targets
	// (Section 10). An entry that reaches this time without a successful
	// send is marked expired, never sent late.
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	// ClaimToken is the fencing token for the delivery-attempt claim this
	// entry currently carries, if any (migration 000011's own
	// claim_token column). nil means unclaimed. A non-nil value means some
	// worker currently holds this entry reserved for one delivery attempt;
	// see notifyoutboxstore.ClaimNextDue/ClaimSpecific/ReleaseClaim and
	// MarkSent/RecordFailedAttempt's own doc comments for the full fencing
	// contract this field participates in. This package itself performs no
	// claim logic -- it only carries the value.
	ClaimToken *string
}

// Claimed reports whether e currently carries an active delivery-attempt
// claim (ClaimToken is non-nil). Mirrors notifydevices.Registration.Active's
// own nil-means-unset convention.
func (e Entry) Claimed() bool { return e.ClaimToken != nil }
