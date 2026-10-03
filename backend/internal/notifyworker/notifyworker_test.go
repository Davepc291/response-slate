package notifyworker

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyattempt"
	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
	"greenwich-fire-responder/backend/internal/notifypreferences"
	"greenwich-fire-responder/backend/internal/notifyprefs"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func validEventID() string { return "evt_" + strings.Repeat("a", 64) }

func claimToken() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }

func claimedEntry() notifyoutbox.Entry {
	tok := claimToken()
	return notifyoutbox.Entry{
		ID:           1,
		EventID:      validEventID(),
		DeviceID:     7,
		UserID:       3,
		State:        notifyoutbox.StatePending,
		AttemptCount: 0,
		MaxAttempts:  3,
		ExpiresAt:    fixedNow.Add(time.Hour),
		ClaimToken:   &tok,
	}
}

func validP256dh() string {
	b := make([]byte, 65)
	b[0] = 0x04
	return base64.RawURLEncoding.EncodeToString(b)
}

func validAuth() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 16))
}

func testModeDevice() notifydevices.Registration {
	return notifydevices.Registration{
		ID:     7,
		UserID: 3,
		Subscription: notifydevices.Subscription{
			Endpoint: "https://push.example.invalid/send/worker-test",
			Keys:     notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()},
		},
		TestMode: true,
	}
}

func validAlertData() AlertData {
	toneSet := "engine-1"
	return AlertData{
		Context: notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: &toneSet},
		Payload: notifyrelay.Payload{Title: "Test alert"},
	}
}

func eligibleDecision() notifypreferences.Decision {
	return notifypreferences.Decision{Eligible: true, Reason: notifypreferences.ReasonEligible}
}

func quietHoursDecision() notifypreferences.Decision {
	return notifypreferences.Decision{Eligible: false, Reason: notifypreferences.ReasonQuietHours}
}

func deviceNotActiveDecision() notifypreferences.Decision {
	return notifypreferences.Decision{Eligible: false, Reason: notifypreferences.ReasonDeviceNotActive}
}

// deliveryOutcomeForStateTest mirrors notifyoutcome's own unexported
// deliveryOutcomeForState mapping, duplicated locally so fakeOutcomes can
// simulate what the real atomic RecordFailedAttempt would derive, without
// this test package depending on notifyoutcome's internals.
func deliveryOutcomeForStateTest(state notifyoutbox.State) notifydelivery.Outcome {
	switch state {
	case notifyoutbox.StateDeadLetter:
		return notifydelivery.OutcomeDeadLetter
	case notifyoutbox.StateExpired:
		return notifydelivery.OutcomeExpired
	default:
		return notifydelivery.OutcomeFailed
	}
}

// --- fakeEvaluator ---

type fakeEvaluator struct {
	decision notifypreferences.Decision
	err      error
	calls    int
}

func (f *fakeEvaluator) Evaluate(context.Context, time.Time, identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) (notifypreferences.Decision, error) {
	f.calls++
	return f.decision, f.err
}

// --- fakeDeviceStore ---

type fakeDeviceStore struct {
	reg      notifydevices.Registration
	ok       bool
	getErr   error
	getCalls int
}

func (f *fakeDeviceStore) Get(context.Context, notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	f.getCalls++
	return f.reg, f.ok, f.getErr
}

// --- fakeAlerts ---

func fixedAlerts(data AlertData, err error) AlertLookup {
	return func(context.Context, string) (AlertData, error) { return data, err }
}

// --- fakeOutbox (non-transactional operations only, post-Part 14B) ---

type rescheduleCall struct {
	id    notifyoutbox.OutboxID
	token string
	next  time.Time
}
type releaseCall struct {
	id    notifyoutbox.OutboxID
	token string
}

type fakeOutbox struct {
	claimEntry notifyoutbox.Entry
	claimOK    bool
	claimErr   error
	claimCalls int

	rescheduleErr   error
	rescheduleCalls []rescheduleCall

	releaseErr   error
	releaseCalls []releaseCall

	markExpiredErr   error
	markExpiredCalls []notifyoutbox.OutboxID
}

func (f *fakeOutbox) ClaimNextDue(context.Context, time.Time, time.Duration) (notifyoutbox.Entry, bool, error) {
	f.claimCalls++
	return f.claimEntry, f.claimOK, f.claimErr
}

func (f *fakeOutbox) RescheduleClaim(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string, next time.Time) (notifyoutbox.Entry, error) {
	f.rescheduleCalls = append(f.rescheduleCalls, rescheduleCall{id, token, next})
	if f.rescheduleErr != nil {
		return notifyoutbox.Entry{}, f.rescheduleErr
	}
	e := f.claimEntry
	e.ClaimToken = nil
	e.NextAttemptAt = &next
	return e, nil
}

func (f *fakeOutbox) ReleaseClaim(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string) (notifyoutbox.Entry, error) {
	f.releaseCalls = append(f.releaseCalls, releaseCall{id, token})
	if f.releaseErr != nil {
		return notifyoutbox.Entry{}, f.releaseErr
	}
	e := f.claimEntry
	e.ClaimToken = nil
	return e, nil
}

func (f *fakeOutbox) MarkExpired(_ context.Context, _ time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error) {
	f.markExpiredCalls = append(f.markExpiredCalls, id)
	if f.markExpiredErr != nil {
		return notifyoutbox.Entry{}, f.markExpiredErr
	}
	e := f.claimEntry
	e.ClaimToken = nil
	e.State = notifyoutbox.StateExpired
	return e, nil
}

// --- fakeOutcomes (OutcomeRecorder, Step 8D-B Part 14B) ---

type sentCall struct {
	id    notifyoutbox.OutboxID
	token string
	d     notifydelivery.Delivery
}
type canceledCall = sentCall
type deadLetterCall = sentCall

type failedAttemptCall struct {
	id    notifyoutbox.OutboxID
	next  time.Time
	token string
	d     notifydelivery.Delivery
}

type unauthorizedCall struct {
	id       notifyoutbox.OutboxID
	token    string
	d        notifydelivery.Delivery
	userID   identity.UserID
	deviceID notifydevices.DeviceID
}

type fakeOutcomes struct {
	sentErr   error
	sentCalls []sentCall

	canceledErr   error
	canceledCalls []canceledCall

	// failedAttemptResultState, when non-empty, is used as the simulated
	// real outbox state RecordFailedAttempt would have returned (so a test
	// can exercise the pending/dead_letter/expired outcome-mapping paths);
	// defaults to StatePending.
	failedAttemptResultState notifyoutbox.State
	failedAttemptErr         error
	failedAttemptCalls       []failedAttemptCall

	deadLetterErr   error
	deadLetterCalls []deadLetterCall

	unauthorizedErr   error
	unauthorizedCalls []unauthorizedCall
}

func (f *fakeOutcomes) RecordSent(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	f.sentCalls = append(f.sentCalls, sentCall{id, token, d})
	if f.sentErr != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, f.sentErr
	}
	return notifyoutbox.Entry{ID: id, State: notifyoutbox.StateSent}, d, nil
}

func (f *fakeOutcomes) RecordCanceled(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	f.canceledCalls = append(f.canceledCalls, canceledCall{id, token, d})
	if f.canceledErr != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, f.canceledErr
	}
	return notifyoutbox.Entry{ID: id, State: notifyoutbox.StateCanceled}, d, nil
}

func (f *fakeOutcomes) RecordFailedAttempt(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, next time.Time, token string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if f.failedAttemptErr != nil {
		f.failedAttemptCalls = append(f.failedAttemptCalls, failedAttemptCall{id, next, token, d})
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, f.failedAttemptErr
	}
	state := f.failedAttemptResultState
	if state == "" {
		state = notifyoutbox.StatePending
	}
	// d.Outcome is resolved here, mirroring the real notifyoutcome.Store's
	// own behavior (it derives and overwrites Outcome from the transition's
	// actual result), and the call is recorded with that resolved value so
	// a test can assert on what was actually "persisted."
	d.Outcome = deliveryOutcomeForStateTest(state)
	f.failedAttemptCalls = append(f.failedAttemptCalls, failedAttemptCall{id, next, token, d})
	return notifyoutbox.Entry{ID: id, State: state}, d, nil
}

func (f *fakeOutcomes) RecordDeadLetter(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	f.deadLetterCalls = append(f.deadLetterCalls, deadLetterCall{id, token, d})
	if f.deadLetterErr != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, f.deadLetterErr
	}
	return notifyoutbox.Entry{ID: id, State: notifyoutbox.StateDeadLetter}, d, nil
}

func (f *fakeOutcomes) RecordUnauthorized(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string, d notifydelivery.Delivery, userID identity.UserID, deviceID notifydevices.DeviceID) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	f.unauthorizedCalls = append(f.unauthorizedCalls, unauthorizedCall{id, token, d, userID, deviceID})
	if f.unauthorizedErr != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, f.unauthorizedErr
	}
	return notifyoutbox.Entry{ID: id, State: notifyoutbox.StateDeadLetter}, d, nil
}

// --- shared test wiring ---

type harness struct {
	outbox    *fakeOutbox
	evaluator *fakeEvaluator
	devices   *fakeDeviceStore
	sender    *notifyrelay.FakeSender
	outcomes  *fakeOutcomes
}

func newHarness() *harness {
	return &harness{
		outbox:    &fakeOutbox{claimEntry: claimedEntry(), claimOK: true},
		evaluator: &fakeEvaluator{decision: eligibleDecision()},
		devices:   &fakeDeviceStore{reg: testModeDevice(), ok: true},
		sender:    notifyrelay.NewFakeSender(),
		outcomes:  &fakeOutcomes{},
	}
}

func (h *harness) worker(t *testing.T) *Worker {
	t.Helper()
	w, err := New(Deps{
		Outbox:    h.outbox,
		Evaluator: h.evaluator,
		Devices:   h.devices,
		Sender:    h.sender,
		Outcomes:  h.outcomes,
		Alerts:    fixedAlerts(validAlertData(), nil),
		Now:       func() time.Time { return fixedNow },
	}, Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return w
}

// --- New ---

func TestNewRefusesMissingDependencies(t *testing.T) {
	h := newHarness()
	base := Deps{
		Outbox: h.outbox, Evaluator: h.evaluator, Devices: h.devices,
		Sender: h.sender, Outcomes: h.outcomes, Alerts: fixedAlerts(validAlertData(), nil),
	}
	cfg := Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second}

	cases := []struct {
		name string
		mut  func(d *Deps)
	}{
		{"nil outbox", func(d *Deps) { d.Outbox = nil }},
		{"nil evaluator", func(d *Deps) { d.Evaluator = nil }},
		{"nil devices", func(d *Deps) { d.Devices = nil }},
		{"nil sender", func(d *Deps) { d.Sender = nil }},
		{"nil outcomes", func(d *Deps) { d.Outcomes = nil }},
		{"nil alerts", func(d *Deps) { d.Alerts = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := base
			c.mut(&d)
			if w, err := New(d, cfg); err != ErrUnconfigured || w != nil {
				t.Fatalf("expected (nil, ErrUnconfigured), got (%v, %v)", w, err)
			}
		})
	}
}

func TestNewRefusesNonDevEnvironment(t *testing.T) {
	h := newHarness()
	deps := Deps{
		Outbox: h.outbox, Evaluator: h.evaluator, Devices: h.devices,
		Sender: h.sender, Outcomes: h.outcomes, Alerts: fixedAlerts(validAlertData(), nil),
	}
	for _, env := range []notifywebpush.Env{notifywebpush.EnvPreview, notifywebpush.EnvProduction, ""} {
		w, err := New(deps, Config{Env: env, SendTimeout: 10 * time.Second})
		if err != ErrEnvironmentNotSupported || w != nil {
			t.Fatalf("env %q: expected (nil, ErrEnvironmentNotSupported), got (%v, %v)", env, w, err)
		}
	}
	// Relay-disabled / misconfigured-worker fail-closed proof: a refused
	// Worker is never usable, and the outbox it would have used recorded
	// zero interactions -- there is no way to call Run/RunOnce without a
	// valid *Worker from a successful New call.
	if h.outbox.claimCalls != 0 {
		t.Fatalf("expected zero ClaimNextDue calls when New refuses construction, got %d", h.outbox.claimCalls)
	}
}

func TestNewRefusesNonPositiveSendTimeout(t *testing.T) {
	h := newHarness()
	deps := Deps{
		Outbox: h.outbox, Evaluator: h.evaluator, Devices: h.devices,
		Sender: h.sender, Outcomes: h.outcomes, Alerts: fixedAlerts(validAlertData(), nil),
	}
	for _, st := range []time.Duration{0, -time.Second} {
		if w, err := New(deps, Config{Env: notifywebpush.EnvDev, SendTimeout: st}); err != ErrInput || w != nil {
			t.Fatalf("SendTimeout %v: expected (nil, ErrInput), got (%v, %v)", st, w, err)
		}
	}
}

func TestNewAppliesDefaultsAndDerivesLeaseDuration(t *testing.T) {
	h := newHarness()
	w, err := New(Deps{
		Outbox: h.outbox, Evaluator: h.evaluator, Devices: h.devices,
		Sender: h.sender, Outcomes: h.outcomes, Alerts: fixedAlerts(validAlertData(), nil),
	}, Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.cfg.PollInterval != DefaultPollInterval || w.cfg.DeferralRecheckInterval != DefaultDeferralRecheckInterval ||
		w.cfg.BaseRetryDelay != DefaultBaseRetryDelay || w.cfg.MaxRetryDelay != DefaultMaxRetryDelay {
		t.Fatalf("expected defaults applied, got %+v", w.cfg)
	}
	if got, want := w.leaseDuration(), 10*time.Second+leaseSafetyMargin; got != want {
		t.Fatalf("expected lease duration derived from SendTimeout+margin = %v, got %v", want, got)
	}
}

// --- RunOnce: empty queue / claim error ---

func TestRunOnceNoDueEntry(t *testing.T) {
	h := newHarness()
	h.outbox.claimOK = false
	w := h.worker(t)
	processed, err := w.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("expected (false, nil), got (%v, %v)", processed, err)
	}
	if h.devices.getCalls != 0 || len(h.sender.Calls()) != 0 {
		t.Fatal("expected no further work when nothing is due")
	}
}

func TestRunOnceClaimNextDueErrorPropagates(t *testing.T) {
	h := newHarness()
	h.outbox.claimErr = errors.New("db unavailable")
	w := h.worker(t)
	processed, err := w.RunOnce(context.Background())
	if err == nil || processed {
		t.Fatalf("expected (false, error), got (%v, %v)", processed, err)
	}
}

// --- Non-test_mode / missing device: defer, never send or cancel ---

func TestRunOnceNonTestModeDeviceReschedules(t *testing.T) {
	h := newHarness()
	h.devices.reg.TestMode = false
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.rescheduleCalls) != 1 {
		t.Fatalf("expected exactly one RescheduleClaim call, got %d", len(h.outbox.rescheduleCalls))
	}
	if len(h.sender.Calls()) != 0 {
		t.Fatal("expected a non-test_mode device to never be sent to")
	}
	if len(h.outcomes.canceledCalls) != 0 {
		t.Fatal("expected a non-test_mode device's entry to never be canceled")
	}
	want := fixedNow.Add(DefaultDeferralRecheckInterval)
	if got := h.outbox.rescheduleCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected requeue time %v, got %v", want, got)
	}
}

func TestRunOnceDeviceNotFoundReschedules(t *testing.T) {
	h := newHarness()
	h.devices.ok = false
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.rescheduleCalls) != 1 || len(h.sender.Calls()) != 0 {
		t.Fatal("expected a reschedule and zero sends when the device cannot be found")
	}
}

func TestRunOnceDeferralCappedAtExpiresAt(t *testing.T) {
	h := newHarness()
	h.devices.reg.TestMode = false
	nearExpiry := fixedNow.Add(5 * time.Minute) // well inside DefaultDeferralRecheckInterval (15m)
	h.outbox.claimEntry.ExpiresAt = nearExpiry
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.rescheduleCalls) != 1 {
		t.Fatalf("expected exactly one RescheduleClaim call, got %d", len(h.outbox.rescheduleCalls))
	}
	if got := h.outbox.rescheduleCalls[0].next; !got.Equal(nearExpiry) {
		t.Fatalf("expected the requeue time capped at expires_at (%v), got %v", nearExpiry, got)
	}
}

func TestRunOnceDeferralAtOrPastExpiryExpiresInstead(t *testing.T) {
	h := newHarness()
	h.devices.reg.TestMode = false
	h.outbox.claimEntry.ExpiresAt = fixedNow // already due to expire at exactly now
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.rescheduleCalls) != 0 {
		t.Fatal("expected RescheduleClaim to never be called when the capped requeue time would not be after now")
	}
	if len(h.outbox.releaseCalls) != 1 || len(h.outbox.markExpiredCalls) != 1 {
		t.Fatalf("expected release+expire instead, got releases=%d expires=%d", len(h.outbox.releaseCalls), len(h.outbox.markExpiredCalls))
	}
}

// --- Quiet hours: queue, never send or cancel ---

func TestRunOnceQuietHoursReschedules(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = quietHoursDecision()
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.rescheduleCalls) != 1 {
		t.Fatalf("expected exactly one RescheduleClaim call, got %d", len(h.outbox.rescheduleCalls))
	}
	if len(h.sender.Calls()) != 0 {
		t.Fatal("expected quiet hours to never send")
	}
	if len(h.outcomes.canceledCalls) != 0 {
		t.Fatal("expected quiet hours to never cancel")
	}
}

// --- Already past ExpiresAt at plan time ---

func TestRunOnceAlreadyExpiredAtPlanTimeReleasesAndExpiresWithoutEvaluating(t *testing.T) {
	h := newHarness()
	h.outbox.claimEntry.ExpiresAt = fixedNow // not strictly after now -> ErrNotPlannable, evaluator never consulted
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.evaluator.calls != 0 {
		t.Fatal("expected the evaluator to never be consulted for an already-expired entry")
	}
	if len(h.outbox.releaseCalls) != 1 || len(h.outbox.markExpiredCalls) != 1 {
		t.Fatal("expected release+expire")
	}
}

// --- Ineligible (non-quiet-hours): cancel, atomic, no ReleaseClaim ---

func TestRunOnceIneligibleCancelsAtomicallyWithNoReleaseClaim(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = deviceNotActiveDecision()
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outcomes.canceledCalls) != 1 {
		t.Fatalf("expected exactly one atomic RecordCanceled call, got %d", len(h.outcomes.canceledCalls))
	}
	call := h.outcomes.canceledCalls[0]
	if call.token != claimToken() {
		t.Fatalf("expected the claim token to be passed through, got %q", call.token)
	}
	if call.d.Outcome != notifydelivery.OutcomeCanceled || call.d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", call.d)
	}
	// The whole point of Part 14B's CancelClaimed primitive: cancellation
	// must never go through ReleaseClaim at all.
	if len(h.outbox.releaseCalls) != 0 {
		t.Fatalf("expected ZERO ReleaseClaim calls on the cancel path, got %d", len(h.outbox.releaseCalls))
	}
	if len(h.sender.Calls()) != 0 {
		t.Fatal("expected no send for an ineligible entry")
	}
}

func TestRunOnceCancelFencingLostWritesNoAudit(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = deviceNotActiveDecision()
	h.outcomes.canceledErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	// The single atomic call already failed internally (simulating a lost
	// race inside the transaction); nothing else should happen.
	if len(h.outbox.releaseCalls) != 0 {
		t.Fatal("expected ZERO ReleaseClaim calls on the cancel path even on a fencing conflict")
	}
}

// --- Send: accepted ---

func TestRunOnceSendAcceptedRecordsAtomically(t *testing.T) {
	h := newHarness()
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.sender.Calls()) != 1 {
		t.Fatalf("expected exactly one send, got %d", len(h.sender.Calls()))
	}
	if h.sender.Calls()[0].TestMode != true {
		t.Fatal("expected the request's TestMode to be sourced from the stored device registration")
	}
	if len(h.outcomes.sentCalls) != 1 {
		t.Fatalf("expected exactly one atomic RecordSent call, got %d", len(h.outcomes.sentCalls))
	}
	call := h.outcomes.sentCalls[0]
	if call.token != claimToken() {
		t.Fatalf("expected MarkSent's fencing token to be passed through, got %q", call.token)
	}
	if call.d.Outcome != notifydelivery.OutcomeSent || call.d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", call.d)
	}
}

func TestRunOnceSendAcceptedFencingLostSkipsAudit(t *testing.T) {
	h := newHarness()
	h.outcomes.sentErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
}

// --- Send: temporary failure (existing retry/backoff path, unchanged) ---

func TestRunOnceTemporaryFailureUsesBaseBackoffOnFirstAttempt(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outcomes.failedAttemptCalls) != 1 {
		t.Fatalf("expected exactly one atomic RecordFailedAttempt call, got %d", len(h.outcomes.failedAttemptCalls))
	}
	want := fixedNow.Add(DefaultBaseRetryDelay)
	if got := h.outcomes.failedAttemptCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected next_attempt_at %v (base delay), got %v", want, got)
	}
	if h.outcomes.failedAttemptCalls[0].d.AttemptNumber != 1 {
		t.Fatalf("unexpected attempt number: %+v", h.outcomes.failedAttemptCalls[0].d)
	}
	// Permanent-failure path must never be used for a temporary failure.
	if len(h.outcomes.deadLetterCalls) != 0 {
		t.Fatal("expected RecordDeadLetter to never be called for a temporary failure")
	}
}

func TestRunOnceTemporaryFailureHonorsRetryAfterFloor(t *testing.T) {
	h := newHarness()
	retryAfter := 5 * time.Minute
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure, RetryAfter: &retryAfter})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fixedNow.Add(5 * time.Minute)
	if got := h.outcomes.failedAttemptCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected RetryAfter honored as a floor (%v), got %v", want, got)
	}
}

func TestRunOnceTemporaryFailureShorterRetryAfterIgnored(t *testing.T) {
	h := newHarness()
	retryAfter := 5 * time.Second // shorter than the 30s base delay
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure, RetryAfter: &retryAfter})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fixedNow.Add(DefaultBaseRetryDelay)
	if got := h.outcomes.failedAttemptCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected the shorter RetryAfter to be ignored, want %v got %v", want, got)
	}
}

func TestRunOnceTemporaryFailureNeverSchedulesBeyondExpiresAt(t *testing.T) {
	h := newHarness()
	soon := fixedNow.Add(10 * time.Second) // well inside the 30s base delay
	h.outbox.claimEntry.ExpiresAt = soon
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := h.outcomes.failedAttemptCalls[0].next; !got.Equal(soon) {
		t.Fatalf("expected next_attempt_at capped at expires_at (%v), got %v", soon, got)
	}
}

func TestRunOnceTemporaryFailureFencingLostSwallowed(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	h.outcomes.failedAttemptErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
}

func TestRunOnceTemporaryFailureOutcomeMappingFollowsActualReturnedState(t *testing.T) {
	cases := []struct {
		name    string
		state   notifyoutbox.State
		outcome notifydelivery.Outcome
	}{
		{"stays pending", notifyoutbox.StatePending, notifydelivery.OutcomeFailed},
		{"exhausts to dead_letter", notifyoutbox.StateDeadLetter, notifydelivery.OutcomeDeadLetter},
		{"reaches expired", notifyoutbox.StateExpired, notifydelivery.OutcomeExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness()
			h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
			h.outcomes.failedAttemptResultState = c.state
			w := h.worker(t)
			if _, err := w.RunOnce(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(h.outcomes.failedAttemptCalls) != 1 || h.outcomes.failedAttemptCalls[0].d.Outcome != c.outcome {
				t.Fatalf("expected outcome %q, got %+v", c.outcome, h.outcomes.failedAttemptCalls)
			}
		})
	}
}

// --- Send: permanent failure (NEW separate path, immediate dead-letter) ---

func TestRunOncePermanentFailureImmediatelyDeadLetters(t *testing.T) {
	h := newHarness()
	code := "malformed_request"
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomePermanentFailure, Code: &code})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outcomes.deadLetterCalls) != 1 {
		t.Fatalf("expected exactly one atomic RecordDeadLetter call, got %d", len(h.outcomes.deadLetterCalls))
	}
	call := h.outcomes.deadLetterCalls[0]
	if call.d.Outcome != notifydelivery.OutcomeDeadLetter || call.d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", call.d)
	}
	if call.d.ErrorCode == nil || *call.d.ErrorCode != code {
		t.Fatalf("expected the provider's error code to be carried through, got %+v", call.d.ErrorCode)
	}
	// Permanent failure must NEVER go through the temporary-failure
	// (retry/backoff) path -- this is the exact distinction Part 14B
	// introduces.
	if len(h.outcomes.failedAttemptCalls) != 0 {
		t.Fatal("expected RecordFailedAttempt to never be called for a permanent failure")
	}
}

func TestRunOncePermanentFailureFencingLostSwallowed(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomePermanentFailure})
	h.outcomes.deadLetterErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
}

// --- Send: unauthorized (atomic, single-cycle, no next-cycle cleanup) ---

func TestRunOnceUnauthorizedAtomicallyTerminalizesAndRevokesInOneCycle(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outcomes.unauthorizedCalls) != 1 {
		t.Fatalf("expected exactly one atomic RecordUnauthorized call, got %d", len(h.outcomes.unauthorizedCalls))
	}
	call := h.outcomes.unauthorizedCalls[0]
	if call.userID != 3 || call.deviceID != 7 {
		t.Fatalf("unexpected revoke target: %+v", call)
	}
	if call.d.Outcome != notifydelivery.OutcomeUnauthorized || call.d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", call.d)
	}
	// No ReleaseClaim step at all -- the whole point of using
	// DeadLetterClaimed directly.
	if len(h.outbox.releaseCalls) != 0 {
		t.Fatalf("expected ZERO ReleaseClaim calls on the unauthorized path, got %d", len(h.outbox.releaseCalls))
	}
}

func TestRunOnceUnauthorizedFencingLostWritesNothing(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	h.outcomes.unauthorizedErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
}

// TestRunOnceUnauthorizedIsAlreadyTerminalNoSecondCycleNeeded proves the
// Part 14B guarantee directly: after one RunOnce call resolves an
// unauthorized send, the claimed-entry fixture this fake outbox always
// returns is no longer meaningfully reclaimable in practice (in the real
// store it is now dead_letter, not pending) -- the single atomic call
// already did everything RecordUnauthorized's own contract promises (no
// next-cycle cleanup). This is asserted here by confirming notifyworker
// itself made exactly the one call and nothing else, never a second
// ReleaseClaim/Cancel-shaped follow-up within the same cycle.
func TestRunOnceUnauthorizedIsAlreadyTerminalNoSecondCycleNeeded(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outcomes.unauthorizedCalls) != 1 {
		t.Fatalf("expected exactly one call total, got %d", len(h.outcomes.unauthorizedCalls))
	}
	if len(h.outcomes.canceledCalls) != 0 || len(h.outbox.releaseCalls) != 0 || len(h.outbox.rescheduleCalls) != 0 {
		t.Fatal("expected no follow-up cancel/release/reschedule calls within the same cycle")
	}
}

// --- BackoffDelay ---

func TestBackoffDelaySchedule(t *testing.T) {
	base := DefaultBaseRetryDelay
	max := DefaultMaxRetryDelay
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{5, 480 * time.Second},
	}
	for _, c := range cases {
		if got := BackoffDelay(c.attempt, base, max, nil); got != c.want {
			t.Fatalf("attempt %d: expected %v, got %v", c.attempt, c.want, got)
		}
	}
}

func TestBackoffDelayCapsAtMax(t *testing.T) {
	base := DefaultBaseRetryDelay
	max := DefaultMaxRetryDelay
	if got := BackoffDelay(6, base, max, nil); got != max {
		t.Fatalf("expected the schedule to cap at max (%v), got %v", max, got)
	}
	if got := BackoffDelay(50, base, max, nil); got != max {
		t.Fatalf("expected a very large attempt number to still cap at max, got %v", got)
	}
}

func TestBackoffDelayRetryAfterFloorAndIgnoredShorterValue(t *testing.T) {
	base := DefaultBaseRetryDelay
	max := DefaultMaxRetryDelay
	longer := 10 * time.Minute
	if got := BackoffDelay(1, base, max, &longer); got != longer {
		t.Fatalf("expected RetryAfter to win as a floor, got %v want %v", got, longer)
	}
	shorter := 5 * time.Second
	if got := BackoffDelay(1, base, max, &shorter); got != base {
		t.Fatalf("expected a shorter RetryAfter to be ignored, got %v want %v", got, base)
	}
}

func TestBackoffDelayZeroOrNegativeAttemptTreatedAsFirst(t *testing.T) {
	base := DefaultBaseRetryDelay
	max := DefaultMaxRetryDelay
	if got := BackoffDelay(0, base, max, nil); got != base {
		t.Fatalf("expected attempt 0 to behave like attempt 1, got %v", got)
	}
	if got := BackoffDelay(-5, base, max, nil); got != base {
		t.Fatalf("expected a negative attempt number to behave like attempt 1, got %v", got)
	}
}

// --- notifyattempt.Evaluator / Plan sanity: confirm Deps.Evaluator is used ---

var _ notifyattempt.Evaluator = (*fakeEvaluator)(nil)
