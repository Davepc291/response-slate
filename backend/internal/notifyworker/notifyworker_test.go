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

	revokeErr   error
	revokeCalls []struct {
		userID identity.UserID
		id     notifydevices.DeviceID
	}
}

func (f *fakeDeviceStore) Get(context.Context, notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	f.getCalls++
	return f.reg, f.ok, f.getErr
}

func (f *fakeDeviceStore) Revoke(_ context.Context, _ time.Time, userID identity.UserID, id notifydevices.DeviceID) error {
	f.revokeCalls = append(f.revokeCalls, struct {
		userID identity.UserID
		id     notifydevices.DeviceID
	}{userID, id})
	return f.revokeErr
}

// --- fakeDeliveryStore ---

type fakeDeliveryStore struct {
	err     error
	records []notifydelivery.Delivery
}

func (f *fakeDeliveryStore) Record(_ context.Context, d notifydelivery.Delivery) (notifydelivery.Delivery, error) {
	f.records = append(f.records, d)
	return d, f.err
}

// --- fakeAlerts ---

func fixedAlerts(data AlertData, err error) AlertLookup {
	return func(context.Context, string) (AlertData, error) { return data, err }
}

// --- fakeOutbox ---

type rescheduleCall struct {
	id    notifyoutbox.OutboxID
	token string
	next  time.Time
}
type releaseCall struct {
	id    notifyoutbox.OutboxID
	token string
}
type markSentCall struct {
	id    notifyoutbox.OutboxID
	token string
}
type recordFailedCall struct {
	id    notifyoutbox.OutboxID
	next  time.Time
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

	markSentErr   error
	markSentCalls []markSentCall

	recordFailedResult notifyoutbox.Entry
	recordFailedErr    error
	recordFailedCalls  []recordFailedCall

	cancelErr   error
	cancelCalls []notifyoutbox.OutboxID

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

func (f *fakeOutbox) MarkSent(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, token string) (notifyoutbox.Entry, error) {
	f.markSentCalls = append(f.markSentCalls, markSentCall{id, token})
	if f.markSentErr != nil {
		return notifyoutbox.Entry{}, f.markSentErr
	}
	e := f.claimEntry
	e.ClaimToken = nil
	e.State = notifyoutbox.StateSent
	return e, nil
}

func (f *fakeOutbox) RecordFailedAttempt(_ context.Context, _ time.Time, id notifyoutbox.OutboxID, next time.Time, token string) (notifyoutbox.Entry, error) {
	f.recordFailedCalls = append(f.recordFailedCalls, recordFailedCall{id, next, token})
	if f.recordFailedErr != nil {
		return notifyoutbox.Entry{}, f.recordFailedErr
	}
	if f.recordFailedResult.ID != 0 {
		return f.recordFailedResult, nil
	}
	e := f.claimEntry
	e.ClaimToken = nil
	e.AttemptCount++
	e.State = notifyoutbox.StatePending
	e.NextAttemptAt = &next
	return e, nil
}

func (f *fakeOutbox) Cancel(_ context.Context, _ time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error) {
	f.cancelCalls = append(f.cancelCalls, id)
	if f.cancelErr != nil {
		return notifyoutbox.Entry{}, f.cancelErr
	}
	e := f.claimEntry
	e.ClaimToken = nil
	e.State = notifyoutbox.StateCanceled
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

// --- shared test wiring ---

type harness struct {
	outbox     *fakeOutbox
	evaluator  *fakeEvaluator
	devices    *fakeDeviceStore
	sender     *notifyrelay.FakeSender
	deliveries *fakeDeliveryStore
}

func newHarness() *harness {
	return &harness{
		outbox:     &fakeOutbox{claimEntry: claimedEntry(), claimOK: true},
		evaluator:  &fakeEvaluator{decision: eligibleDecision()},
		devices:    &fakeDeviceStore{reg: testModeDevice(), ok: true},
		sender:     notifyrelay.NewFakeSender(),
		deliveries: &fakeDeliveryStore{},
	}
}

func (h *harness) worker(t *testing.T) *Worker {
	t.Helper()
	w, err := New(Deps{
		Outbox:     h.outbox,
		Evaluator:  h.evaluator,
		Devices:    h.devices,
		Sender:     h.sender,
		Deliveries: h.deliveries,
		Alerts:     fixedAlerts(validAlertData(), nil),
		Now:        func() time.Time { return fixedNow },
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
		Sender: h.sender, Deliveries: h.deliveries, Alerts: fixedAlerts(validAlertData(), nil),
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
		{"nil deliveries", func(d *Deps) { d.Deliveries = nil }},
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
		Sender: h.sender, Deliveries: h.deliveries, Alerts: fixedAlerts(validAlertData(), nil),
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
		Sender: h.sender, Deliveries: h.deliveries, Alerts: fixedAlerts(validAlertData(), nil),
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
		Sender: h.sender, Deliveries: h.deliveries, Alerts: fixedAlerts(validAlertData(), nil),
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
	if len(h.outbox.cancelCalls) != 0 {
		t.Fatal("expected a non-test_mode device's entry to never be canceled")
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected no audit row for a deferred entry")
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
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected no audit row for an expiry with no real attempt")
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
	if len(h.outbox.cancelCalls) != 0 {
		t.Fatal("expected quiet hours to never cancel")
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected no audit row for a quiet-hours deferral")
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
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected no audit row")
	}
}

// --- Ineligible (non-quiet-hours): cancel ---

func TestRunOnceIneligibleCancelsAndRecordsAudit(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = deviceNotActiveDecision()
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.releaseCalls) != 1 || len(h.outbox.cancelCalls) != 1 {
		t.Fatalf("expected release-then-cancel, got releases=%d cancels=%d", len(h.outbox.releaseCalls), len(h.outbox.cancelCalls))
	}
	if len(h.deliveries.records) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(h.deliveries.records))
	}
	d := h.deliveries.records[0]
	if d.Outcome != notifydelivery.OutcomeCanceled || d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", d)
	}
	if len(h.sender.Calls()) != 0 {
		t.Fatal("expected no send for an ineligible entry")
	}
}

func TestRunOnceCancelFencingLostOnReleaseWritesNoAudit(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = deviceNotActiveDecision()
	h.outbox.releaseErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	if len(h.outbox.cancelCalls) != 0 {
		t.Fatal("expected Cancel to never be called when the release itself lost fencing")
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected zero audit writes from a stale worker")
	}
}

func TestRunOnceCancelFencingLostOnCancelWritesNoAudit(t *testing.T) {
	h := newHarness()
	h.evaluator.decision = deviceNotActiveDecision()
	h.outbox.cancelErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected zero audit writes when Cancel itself loses the race")
	}
}

// --- Send: accepted ---

func TestRunOnceSendAcceptedMarksSentAndRecords(t *testing.T) {
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
	if len(h.outbox.markSentCalls) != 1 || h.outbox.markSentCalls[0].token != claimToken() {
		t.Fatalf("expected MarkSent called with the claim token, got %+v", h.outbox.markSentCalls)
	}
	if len(h.deliveries.records) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(h.deliveries.records))
	}
	d := h.deliveries.records[0]
	if d.Outcome != notifydelivery.OutcomeSent || d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", d)
	}
}

func TestRunOnceSendAcceptedFencingLostSkipsAudit(t *testing.T) {
	h := newHarness()
	h.outbox.markSentErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected zero audit writes from a stale worker, even though the push was physically sent")
	}
}

// --- Send: temporary / permanent failure ---

func TestRunOnceTemporaryFailureUsesBaseBackoffOnFirstAttempt(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.recordFailedCalls) != 1 {
		t.Fatalf("expected exactly one RecordFailedAttempt call, got %d", len(h.outbox.recordFailedCalls))
	}
	want := fixedNow.Add(DefaultBaseRetryDelay)
	if got := h.outbox.recordFailedCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected next_attempt_at %v (base delay), got %v", want, got)
	}
	if len(h.deliveries.records) != 1 || h.deliveries.records[0].Outcome != notifydelivery.OutcomeFailed {
		t.Fatalf("expected one OutcomeFailed audit row, got %+v", h.deliveries.records)
	}
}

func TestRunOncePermanentFailureUsesIdenticalMechanics(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomePermanentFailure})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.recordFailedCalls) != 1 {
		t.Fatalf("expected RecordFailedAttempt to be used for a permanent failure too, got %d calls", len(h.outbox.recordFailedCalls))
	}
	want := fixedNow.Add(DefaultBaseRetryDelay)
	if got := h.outbox.recordFailedCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected the identical backoff schedule, got %v want %v", got, want)
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
	if got := h.outbox.recordFailedCalls[0].next; !got.Equal(want) {
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
	if got := h.outbox.recordFailedCalls[0].next; !got.Equal(want) {
		t.Fatalf("expected the shorter RetryAfter to be ignored, want %v got %v", want, got)
	}
}

func TestRunOnceFailureNeverSchedulesBeyondExpiresAt(t *testing.T) {
	h := newHarness()
	soon := fixedNow.Add(10 * time.Second) // well inside the 30s base delay
	h.outbox.claimEntry.ExpiresAt = soon
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := h.outbox.recordFailedCalls[0].next; !got.Equal(soon) {
		t.Fatalf("expected next_attempt_at capped at expires_at (%v), got %v", soon, got)
	}
}

func TestRunOnceFailureFencingLostSkipsAudit(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	h.outbox.recordFailedErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected zero audit writes from a stale worker")
	}
}

func TestRunOnceOutcomeMappingFollowsActualReturnedState(t *testing.T) {
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
			result := h.outbox.claimEntry
			result.State = c.state
			result.ClaimToken = nil
			h.outbox.recordFailedResult = result
			w := h.worker(t)
			if _, err := w.RunOnce(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(h.deliveries.records) != 1 || h.deliveries.records[0].Outcome != c.outcome {
				t.Fatalf("expected outcome %q, got %+v", c.outcome, h.deliveries.records)
			}
		})
	}
}

// --- Send: unauthorized ---

func TestRunOnceUnauthorizedRevokesAndRecords(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.outbox.releaseCalls) != 1 {
		t.Fatalf("expected exactly one ReleaseClaim call, got %d", len(h.outbox.releaseCalls))
	}
	if len(h.devices.revokeCalls) != 1 {
		t.Fatalf("expected exactly one device revocation, got %d", len(h.devices.revokeCalls))
	}
	if h.devices.revokeCalls[0].userID != 3 || h.devices.revokeCalls[0].id != 7 {
		t.Fatalf("unexpected revoke target: %+v", h.devices.revokeCalls[0])
	}
	if len(h.deliveries.records) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(h.deliveries.records))
	}
	d := h.deliveries.records[0]
	if d.Outcome != notifydelivery.OutcomeUnauthorized || d.AttemptNumber != 1 {
		t.Fatalf("unexpected delivery record: %+v", d)
	}
}

func TestRunOnceUnauthorizedFencingLostSkipsRevokeAndAudit(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	h.outbox.releaseErr = notifyoutboxstore.ErrConflict
	w := h.worker(t)
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("expected a fencing conflict to be swallowed, got %v", err)
	}
	if len(h.devices.revokeCalls) != 0 {
		t.Fatal("expected zero device mutation from a stale worker")
	}
	if len(h.deliveries.records) != 0 {
		t.Fatal("expected zero audit writes from a stale worker")
	}
}

// TestRunOnceUnauthorizedThenNextCycleNeverResends is the "subscription can
// never be sent to again" proof the Part 14A design lock requires. The
// second cycle's fakeEvaluator decision is set to ReasonDeviceNotActive
// directly, modeling what the real notifypreferences.Evaluator (already
// separately tested for exactly this case) would report once it consults the
// device this same worker revoked in cycle one -- this test's own job is to
// prove notifyworker's handling of that reported decision, not to
// re-verify notifypreferences' own correctness.
func TestRunOnceUnauthorizedThenNextCycleNeverResends(t *testing.T) {
	h := newHarness()
	h.sender.SetResult(notifyrelay.OutboxID(1), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	w := h.worker(t)

	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("cycle 1: unexpected error: %v", err)
	}
	if len(h.sender.Calls()) != 1 {
		t.Fatalf("cycle 1: expected exactly one send, got %d", len(h.sender.Calls()))
	}
	if len(h.devices.revokeCalls) != 1 {
		t.Fatal("cycle 1: expected the device to be revoked")
	}

	// Cycle 2: same entry reclaimed (immediately due after ReleaseClaim);
	// the evaluator now reports the device inactive.
	h.evaluator.decision = deviceNotActiveDecision()
	if _, err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("cycle 2: unexpected error: %v", err)
	}
	if len(h.sender.Calls()) != 1 {
		t.Fatalf("cycle 2: expected NO additional send to the revoked subscription, total calls = %d", len(h.sender.Calls()))
	}
	if len(h.outbox.cancelCalls) != 1 {
		t.Fatal("cycle 2: expected the entry to be canceled via the standard ActionCancel path")
	}
	outcomes := make([]notifydelivery.Outcome, len(h.deliveries.records))
	for i, d := range h.deliveries.records {
		outcomes[i] = d.Outcome
	}
	if len(outcomes) != 2 || outcomes[0] != notifydelivery.OutcomeUnauthorized || outcomes[1] != notifydelivery.OutcomeCanceled {
		t.Fatalf("expected [unauthorized, canceled] audit outcomes, got %v", outcomes)
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
		{6, 960 * time.Second}, // 16m > 15m cap would apply at attempt 6 if uncapped (960s=16m)... see cap test below
	}
	for _, c := range cases[:5] { // attempts 1-5 stay under the 15m cap
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
