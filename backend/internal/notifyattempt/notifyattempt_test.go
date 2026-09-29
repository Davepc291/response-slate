package notifyattempt

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifypreferences"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// fakeEvaluator records how many times, and with what arguments, Evaluate
// was called, and returns a fixed, pre-programmed result.
type fakeEvaluator struct {
	decision notifypreferences.Decision
	err      error
	calls    int
	lastArgs struct {
		now      time.Time
		userID   identity.UserID
		deviceID notifydevices.DeviceID
		alert    notifypreferences.AlertContext
	}
}

func (f *fakeEvaluator) Evaluate(_ context.Context, now time.Time, userID identity.UserID, deviceID notifydevices.DeviceID, alert notifypreferences.AlertContext) (notifypreferences.Decision, error) {
	f.calls++
	f.lastArgs.now = now
	f.lastArgs.userID = userID
	f.lastArgs.deviceID = deviceID
	f.lastArgs.alert = alert
	return f.decision, f.err
}

func strPtr(s string) *string { return &s }

func validEventID() string { return "evt_" + strings.Repeat("a", 64) }

func validEntry() notifyoutbox.Entry {
	return notifyoutbox.Entry{
		ID:           1,
		EventID:      validEventID(),
		DeviceID:     1,
		UserID:       1,
		State:        notifyoutbox.StatePending,
		AttemptCount: 0,
		MaxAttempts:  3,
		ExpiresAt:    fixedNow.Add(time.Hour),
		CreatedAt:    fixedNow.Add(-time.Minute),
		UpdatedAt:    fixedNow.Add(-time.Minute),
	}
}

func validAlert() notifypreferences.AlertContext {
	return notifypreferences.AlertContext{
		Channel:   notifyprefs.ChannelCH1A,
		ToneSetID: strPtr("engine-1"),
	}
}

func eligibleDecision() notifypreferences.Decision {
	return notifypreferences.Decision{Eligible: true, Reason: notifypreferences.ReasonEligible}
}

// --- eligible -> Send ---

func TestPlanAttemptEligibleProducesSend(t *testing.T) {
	ev := &fakeEvaluator{decision: eligibleDecision()}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, validEntry(), validAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Action != ActionSend || plan.Reason != notifypreferences.ReasonEligible {
		t.Fatalf("expected ActionSend, got %+v", plan)
	}
	if plan.AttemptNumber != 1 {
		t.Fatalf("expected AttemptNumber 1 for AttemptCount 0, got %d", plan.AttemptNumber)
	}
}

// --- every non-quiet-hours ineligibility reason -> Cancel ---

func TestPlanAttemptEveryNonQuietHoursReasonProducesCancel(t *testing.T) {
	reasons := []notifypreferences.Reason{
		notifypreferences.ReasonAccountNotActive,
		notifypreferences.ReasonDeviceNotActive,
		notifypreferences.ReasonConsentNotGranted,
		notifypreferences.ReasonNotificationsDisabled,
		notifypreferences.ReasonChannelNotSelected,
		notifypreferences.ReasonToneSetNotSelected,
		notifypreferences.ReasonKeywordListNotSelected,
		notifypreferences.ReasonBelowMinConfidence,
	}
	for _, r := range reasons {
		t.Run(string(r), func(t *testing.T) {
			ev := &fakeEvaluator{decision: notifypreferences.Decision{Eligible: false, Reason: r}}
			plan, err := PlanAttempt(context.Background(), ev, fixedNow, validEntry(), validAlert())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Action != ActionCancel || plan.Reason != r {
				t.Fatalf("expected ActionCancel with reason %q, got %+v", r, plan)
			}
			// Cancellation must not consume an attempt: AttemptNumber is
			// still reported (it is always computed), but this package
			// never acts on it, and it must reflect AttemptCount+1 exactly
			// as it would for a Send plan -- the number itself is neutral
			// information, not evidence of anything having been consumed.
			if plan.AttemptNumber != 1 {
				t.Fatalf("expected AttemptNumber 1, got %d", plan.AttemptNumber)
			}
		})
	}
}

// --- quiet hours -> ErrPolicyUnresolved, zero Plan ---

func TestPlanAttemptQuietHoursReturnsErrPolicyUnresolved(t *testing.T) {
	ev := &fakeEvaluator{decision: notifypreferences.Decision{Eligible: false, Reason: notifypreferences.ReasonQuietHours}}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, validEntry(), validAlert())
	if err != ErrPolicyUnresolved {
		t.Fatalf("expected ErrPolicyUnresolved, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
}

func TestPlanAttemptUnknownReasonFailsClosed(t *testing.T) {
	ev := &fakeEvaluator{decision: notifypreferences.Decision{Eligible: false, Reason: notifypreferences.Reason("bogus")}}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, validEntry(), validAlert())
	if err != ErrInput {
		t.Fatalf("expected ErrInput for an unknown reason, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
}

// --- attempt number derivation ---

func TestDeriveAttemptNumberFromZero(t *testing.T) {
	e := validEntry()
	e.AttemptCount = 0
	n, err := DeriveAttemptNumber(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
}

func TestDeriveAttemptNumberFromOne(t *testing.T) {
	e := validEntry()
	e.AttemptCount = 1
	e.MaxAttempts = 5
	n, err := DeriveAttemptNumber(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestDeriveAttemptNumberLargerValidCount(t *testing.T) {
	e := validEntry()
	e.AttemptCount = 41
	e.MaxAttempts = 42
	n, err := DeriveAttemptNumber(e)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 42 {
		t.Fatalf("expected 42, got %d", n)
	}
}

func TestDeriveAttemptNumberRejectsNegativeCount(t *testing.T) {
	e := validEntry()
	e.AttemptCount = -1
	if _, err := DeriveAttemptNumber(e); err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
}

func TestDeriveAttemptNumberOverflowBoundary(t *testing.T) {
	e := validEntry()
	e.AttemptCount = math.MaxInt
	if _, err := DeriveAttemptNumber(e); err != ErrInput {
		t.Fatalf("expected ErrInput at the int overflow boundary, got %v", err)
	}
}

func TestDeriveAttemptNumberJustBelowOverflowBoundary(t *testing.T) {
	e := validEntry()
	e.AttemptCount = math.MaxInt - 1
	n, err := DeriveAttemptNumber(e)
	if err != nil {
		t.Fatalf("unexpected error one below the overflow boundary: %v", err)
	}
	if n != math.MaxInt {
		t.Fatalf("expected math.MaxInt, got %d", n)
	}
}

// --- exhausted attempts / non-pending / expired ---

func TestPlanAttemptExhaustedAttemptsFailsClosed(t *testing.T) {
	e := validEntry()
	e.AttemptCount = 3
	e.MaxAttempts = 3
	ev := &fakeEvaluator{decision: eligibleDecision()}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert())
	if err != ErrNotPlannable {
		t.Fatalf("expected ErrNotPlannable, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
	if ev.calls != 0 {
		t.Fatal("expected the evaluator to never be called for an exhausted entry")
	}
}

func TestPlanAttemptNonPendingEntryFailsClosed(t *testing.T) {
	for _, s := range []notifyoutbox.State{notifyoutbox.StateSent, notifyoutbox.StateExpired, notifyoutbox.StateCanceled, notifyoutbox.StateDeadLetter} {
		t.Run(string(s), func(t *testing.T) {
			e := validEntry()
			e.State = s
			ev := &fakeEvaluator{decision: eligibleDecision()}
			plan, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert())
			if err != ErrNotPlannable {
				t.Fatalf("expected ErrNotPlannable, got %v", err)
			}
			if plan != (Plan{}) {
				t.Fatalf("expected a zero-value Plan, got %+v", plan)
			}
			if ev.calls != 0 {
				t.Fatal("expected the evaluator to never be called for a non-pending entry")
			}
		})
	}
}

func TestPlanAttemptExpiredEntryFailsClosed(t *testing.T) {
	e := validEntry()
	e.ExpiresAt = fixedNow // exactly at now: expires_at <= now, matching MarkExpired's own inclusive guard
	ev := &fakeEvaluator{decision: eligibleDecision()}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert())
	if err != ErrNotPlannable {
		t.Fatalf("expected ErrNotPlannable, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
	if ev.calls != 0 {
		t.Fatal("expected the evaluator to never be called for an already-expired entry")
	}
}

func TestPlanAttemptNotYetExpiredEntrySucceeds(t *testing.T) {
	e := validEntry()
	e.ExpiresAt = fixedNow.Add(time.Nanosecond) // one nanosecond in the future: not yet expired
	ev := &fakeEvaluator{decision: eligibleDecision()}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Action != ActionSend {
		t.Fatalf("expected ActionSend, got %+v", plan)
	}
}

// --- invalid Entry input (ErrInput, before evaluation) ---

func TestPlanAttemptRejectsInvalidEntryBeforeEvaluation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*notifyoutbox.Entry)
	}{
		{"zero outbox id", func(e *notifyoutbox.Entry) { e.ID = 0 }},
		{"negative outbox id", func(e *notifyoutbox.Entry) { e.ID = -1 }},
		{"zero user id", func(e *notifyoutbox.Entry) { e.UserID = 0 }},
		{"zero device id", func(e *notifyoutbox.Entry) { e.DeviceID = 0 }},
		{"negative attempt count", func(e *notifyoutbox.Entry) { e.AttemptCount = -1 }},
		{"zero max attempts", func(e *notifyoutbox.Entry) { e.MaxAttempts = 0 }},
		{"negative max attempts", func(e *notifyoutbox.Entry) { e.MaxAttempts = -1 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := validEntry()
			c.mutate(&e)
			ev := &fakeEvaluator{decision: eligibleDecision()}
			plan, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert())
			if err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if plan != (Plan{}) {
				t.Fatalf("expected a zero-value Plan, got %+v", plan)
			}
			if ev.calls != 0 {
				t.Fatal("expected the evaluator to never be called for invalid input")
			}
		})
	}
}

func TestPlanAttemptRejectsZeroNow(t *testing.T) {
	ev := &fakeEvaluator{decision: eligibleDecision()}
	plan, err := PlanAttempt(context.Background(), ev, time.Time{}, validEntry(), validAlert())
	if err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
	if ev.calls != 0 {
		t.Fatal("expected the evaluator to never be called with a zero now")
	}
}

func TestPlanAttemptRejectsNilEvaluator(t *testing.T) {
	plan, err := PlanAttempt(context.Background(), nil, fixedNow, validEntry(), validAlert())
	if err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
}

// --- evaluator error propagation ---

func TestPlanAttemptPropagatesEvaluatorError(t *testing.T) {
	wantErr := errors.New("notifypreferences: evaluation unavailable")
	ev := &fakeEvaluator{err: wantErr}
	plan, err := PlanAttempt(context.Background(), ev, fixedNow, validEntry(), validAlert())
	if err != wantErr {
		t.Fatalf("expected the evaluator's own error to propagate unchanged, got %v", err)
	}
	if plan != (Plan{}) {
		t.Fatalf("expected a zero-value Plan, got %+v", plan)
	}
}

// --- evaluator called exactly once, with the right arguments ---

func TestPlanAttemptCallsEvaluatorExactlyOnceWithEntryFields(t *testing.T) {
	e := validEntry()
	e.UserID = 42
	e.DeviceID = 99
	alert := validAlert()
	ev := &fakeEvaluator{decision: eligibleDecision()}
	if _, err := PlanAttempt(context.Background(), ev, fixedNow, e, alert); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.calls != 1 {
		t.Fatalf("expected exactly one Evaluate call, got %d", ev.calls)
	}
	if ev.lastArgs.userID != 42 || ev.lastArgs.deviceID != 99 {
		t.Fatalf("expected Evaluate to be called with the entry's own user/device ids, got userID=%v deviceID=%v", ev.lastArgs.userID, ev.lastArgs.deviceID)
	}
	if !ev.lastArgs.now.Equal(fixedNow) {
		t.Fatalf("expected Evaluate to be called with the supplied now, got %v", ev.lastArgs.now)
	}
	if ev.lastArgs.alert != alert {
		t.Fatalf("expected Evaluate to be called with the supplied alert, got %+v", ev.lastArgs.alert)
	}
}

// --- no mutation / no side effects of any kind ---

func TestPlanAttemptDoesNotMutateEntry(t *testing.T) {
	e := validEntry()
	original := e
	ev := &fakeEvaluator{decision: notifypreferences.Decision{Eligible: false, Reason: notifypreferences.ReasonNotificationsDisabled}}
	if _, err := PlanAttempt(context.Background(), ev, fixedNow, e, validAlert()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e != original {
		t.Fatalf("expected entry to be passed by value and never mutated, got %+v (original %+v)", e, original)
	}
}

// --- dependency-boundary sanity: this test file itself proves no
// provider/store/network interaction is possible, since fakeEvaluator is
// the ONLY dependency PlanAttempt is ever given in every test above, and
// it exposes nothing but the single Evaluate method. There is no *Postgres,
// no pgx import, and no HTTP client anywhere in this package (see
// depcheck_test.go for the static, exhaustive version of this claim).
