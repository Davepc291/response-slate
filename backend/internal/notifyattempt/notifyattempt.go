// Package notifyattempt is the Step 8D-B Part 9 delivery-attempt planner.
// It answers exactly one question -- "given this pending outbox entry and
// its current, freshly re-evaluated eligibility, what should the next
// provider attempt number be, and should a future caller send, cancel, or
// hold off pending an unresolved policy decision?" -- and returns that as
// a plain, inert Plan value. It performs no side effect of any kind.
//
// This package is still NO-SEND architecture, exactly like Parts 6-8
// before it. PlanAttempt never claims, leases, or mutates a
// notification_outbox row, never increments attempt_count, never writes a
// notification_deliveries row, never calls a provider, and never performs
// any network I/O. It calls the Part 8 evaluator (backend/internal/
// notifypreferences) exactly once per call, fresh, and maps the result to
// a proposed Action -- nothing more. A future, separately authorized
// orchestration/worker component is expected to call PlanAttempt and then
// act on its Plan by calling the appropriate notifyoutboxstore/
// notifydeliverystore methods (and, eventually, a real provider client)
// itself; this package must never gain that authority.
//
// AttemptNumber is always entry.AttemptCount + 1: the number the NEXT
// real provider attempt would carry if a future caller actually sends and
// then calls notifyoutboxstore.RecordFailedAttempt (which itself
// increments attempt_count by exactly one). Merely planning -- evaluating
// eligibility, or arriving at Cancel or ErrPolicyUnresolved -- never
// consumes an attempt: this package never touches attempt_count, and a
// future caller must not either, for any outcome but an actual send
// attempt.
package notifyattempt

import (
	"context"
	"errors"
	"math"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifypreferences"
)

// Sentinel errors.
var (
	// ErrInput marks an Entry (or evaluator) whose own fields are
	// internally inconsistent regardless of timing -- a non-positive
	// OutboxID/UserID/DeviceID, a negative AttemptCount, or a non-positive
	// MaxAttempts. migration 000010's own CHECK constraints
	// (notification_outbox_attempt_count_bounded, max_attempts > 0) make
	// such a row impossible to actually persist, so a caller passing one
	// of these shapes is passing a value that could never have come from
	// a real notifyoutboxstore.Get/Enqueue/... result -- a caller bug, not
	// a stale read.
	ErrInput = errors.New("notifyattempt: invalid input")
	// ErrNotPlannable marks a structurally valid Entry that is simply not
	// currently eligible for attempt planning: not in notifyoutbox.
	// StatePending, already at or past its own ExpiresAt (matching
	// notifyoutboxstore.MarkExpired's own expires_at <= now guard
	// exactly), or already at its own AttemptCount >= MaxAttempts ceiling
	// (matching notifyoutboxstore.RecordFailedAttempt's own dead_letter
	// transition point). Every one of these three conditions is entirely
	// reachable in practice from an ordinary stale read -- a caller
	// fetched this Entry a moment before a concurrent
	// RecordFailedAttempt/MarkExpired/MarkSent/Cancel call already moved
	// it out of pending -- so this is deliberately distinct from ErrInput:
	// it means "re-read this row's current state," not "this value is
	// malformed."
	ErrNotPlannable = errors.New("notifyattempt: entry is not currently plannable")
	// ErrPolicyUnresolved marks a fresh evaluation whose Reason is
	// notifypreferences.ReasonQuietHours. Whether quiet hours should
	// suppress (cancel-shaped) or queue (retry-shaped) a matching alert
	// remains an explicitly unresolved policy question (Section 29 item
	// 10 of docs/notification-relay-amendment-v1.md, restated by the
	// Step 8D-B Part 8 and Part 9 discovery reports). PlanAttempt refuses
	// to guess: it returns neither ActionSend nor ActionCancel for this
	// case, specifically so a future caller cannot accidentally treat an
	// unresolved policy question as an executable action just because a
	// Plan value happened to come back.
	ErrPolicyUnresolved = errors.New("notifyattempt: quiet-hours policy is not yet resolved")
)

// Action is exactly one of the two values below. There is no third value:
// an evaluation that resolves to quiet hours never produces an Action at
// all (see ErrPolicyUnresolved's own doc comment) -- Action is only ever
// populated on a genuinely resolved Plan.
type Action string

const (
	// ActionSend means the caller-supplied alert is currently eligible
	// for delivery to this device: a future caller may proceed to attempt
	// the real provider call using AttemptNumber.
	ActionSend Action = "send"
	// ActionCancel means one of the eight non-quiet-hours ineligibility
	// reasons applies right now: a future caller should cancel the
	// outbox entry (notifyoutboxstore.Cancel) rather than attempt a send.
	// Cancellation does not consume an attempt (notifyoutboxstore.Cancel
	// never touches attempt_count).
	ActionCancel Action = "cancel"
)

// Plan is a plain, inert fact -- never an action already taken. It carries
// no method that performs a side effect, because this package has none to
// offer: a future caller reads Plan.Action and Plan.AttemptNumber and
// decides what to do next itself.
type Plan struct {
	Action        Action
	Reason        notifypreferences.Reason
	AttemptNumber int
}

// Evaluator is the minimal notifypreferences.Evaluator surface PlanAttempt
// needs. Declaring it locally (rather than depending on the concrete
// *notifypreferences.Evaluator type) keeps this package testable with a
// simple fake and avoids coupling it to notifypreferences' own internal
// wiring (its four store dependencies) -- PlanAttempt only ever needs to
// call Evaluate, never to construct or configure an Evaluator itself.
type Evaluator interface {
	Evaluate(ctx context.Context, now time.Time, userID identity.UserID, deviceID notifydevices.DeviceID, alert notifypreferences.AlertContext) (notifypreferences.Decision, error)
}

// DeriveAttemptNumber computes the number the next real provider attempt
// would carry for entry, without mutating entry or anything else: always
// entry.AttemptCount + 1. It fails closed rather than silently overflow or
// accept a negative count: a negative AttemptCount can never come from a
// real notification_outbox row (migration 000010's own attempt_count >= 0
// CHECK), and an AttemptCount already at the maximum representable int
// would silently wrap to a negative, nonsensical attempt number if not
// caught explicitly first.
func DeriveAttemptNumber(entry notifyoutbox.Entry) (int, error) {
	if entry.AttemptCount < 0 {
		return 0, ErrInput
	}
	if entry.AttemptCount == math.MaxInt {
		return 0, ErrInput
	}
	return entry.AttemptCount + 1, nil
}

// validateEntry checks entry's own fields for internal consistency
// (ErrInput), then checks whether entry is currently plannable at all
// (ErrNotPlannable), entirely before any evaluation is attempted.
//
// The pending/expiry/attempt-ceiling checks deliberately mirror Part 6's
// own existing state-machine guards exactly, rather than inventing new
// boundary semantics:
//   - notifyoutboxstore's own transition methods only ever operate
//     "WHERE state = 'pending'" -- a terminal entry is never re-planned.
//   - notifyoutboxstore.MarkExpired's own guard is
//     "WHERE ... AND expires_at <= $now" -- the identical boundary
//     (inclusive) is used here: an entry due to expire at exactly now is
//     treated as already expired, never planned for a send.
//   - notifyoutboxstore.RecordFailedAttempt transitions an entry to
//     dead_letter the moment attempt_count + 1 >= max_attempts, so a
//     still-pending entry should never actually have
//     AttemptCount >= MaxAttempts already -- but a caller's stale read
//     could still observe exactly that for a brief window, and this must
//     fail closed rather than plan a Send that would push attempt_count
//     past max_attempts.
func validateEntry(now time.Time, entry notifyoutbox.Entry) error {
	if entry.ID <= 0 {
		return ErrInput
	}
	if entry.UserID <= 0 || entry.DeviceID <= 0 {
		return ErrInput
	}
	if entry.AttemptCount < 0 {
		return ErrInput
	}
	if entry.MaxAttempts <= 0 {
		return ErrInput
	}
	if entry.State != notifyoutbox.StatePending {
		return ErrNotPlannable
	}
	if !entry.ExpiresAt.After(now) {
		return ErrNotPlannable
	}
	if entry.AttemptCount >= entry.MaxAttempts {
		return ErrNotPlannable
	}
	return nil
}

// actionFor maps a Part 8 Reason to an Action under the locked mapping:
// eligible -> Send; every one of the eight non-quiet-hours ineligibility
// reasons -> Cancel; quiet hours -> ErrPolicyUnresolved (never an Action
// at all). Any Reason value outside this known set (which a well-behaved
// notifypreferences.Evaluator implementation never produces) fails closed
// as ErrInput rather than silently defaulting to either Action.
func actionFor(reason notifypreferences.Reason) (Action, error) {
	switch reason {
	case notifypreferences.ReasonEligible:
		return ActionSend, nil
	case notifypreferences.ReasonQuietHours:
		return "", ErrPolicyUnresolved
	case notifypreferences.ReasonAccountNotActive,
		notifypreferences.ReasonDeviceNotActive,
		notifypreferences.ReasonConsentNotGranted,
		notifypreferences.ReasonNotificationsDisabled,
		notifypreferences.ReasonChannelNotSelected,
		notifypreferences.ReasonToneSetNotSelected,
		notifypreferences.ReasonKeywordListNotSelected,
		notifypreferences.ReasonBelowMinConfidence:
		return ActionCancel, nil
	default:
		return "", ErrInput
	}
}

// PlanAttempt derives the next attempt number for entry and freshly
// re-evaluates eligibility (via evaluator, exactly once, never cached),
// then returns a deterministic Plan describing what a future caller
// should do next. It performs no side effect: no claim, no lease, no
// outbox mutation, no attempt_count change, no notification_deliveries
// write, no provider call, no network I/O.
//
// entry is validated first (validateEntry), entirely before evaluator is
// ever called -- an invalid or not-currently-plannable entry never
// reaches evaluation at all, so a caller cannot burn a live eligibility
// check on a value that was already known to be unusable.
//
// A quiet-hours result never produces a Plan: PlanAttempt returns
// (Plan{}, ErrPolicyUnresolved) instead, so a caller can never mistake an
// unresolved policy question for an executable ActionSend or
// ActionCancel.
func PlanAttempt(ctx context.Context, evaluator Evaluator, now time.Time, entry notifyoutbox.Entry, alert notifypreferences.AlertContext) (Plan, error) {
	if evaluator == nil {
		return Plan{}, ErrInput
	}
	if now.IsZero() {
		return Plan{}, ErrInput
	}
	if err := validateEntry(now, entry); err != nil {
		return Plan{}, err
	}
	attemptNumber, err := DeriveAttemptNumber(entry)
	if err != nil {
		return Plan{}, err
	}

	decision, err := evaluator.Evaluate(ctx, now, entry.UserID, entry.DeviceID, alert)
	if err != nil {
		return Plan{}, err
	}

	action, err := actionFor(decision.Reason)
	if err != nil {
		return Plan{}, err
	}

	return Plan{Action: action, Reason: decision.Reason, AttemptNumber: attemptNumber}, nil
}
