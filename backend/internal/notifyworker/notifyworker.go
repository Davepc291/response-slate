// Package notifyworker is the Step 8D-B Part 14A notification delivery
// orchestrator: the single new component that composes every already-built,
// separately-authorized notify-family package (notifyoutboxstore,
// notifyattempt, notifypreferences, notifyrelay, notifywebpush,
// notifydeliverystore, notifydevicestore) into one claim -> evaluate ->
// plan -> send -> record-outcome -> release cycle, per the approved Step
// 8D-B Part 14A design lock.
//
// This part is locked to a single, deliberately narrow scope, enforced
// structurally, not only by caller discipline:
//
//   - New refuses to construct a Worker unless Config.Env is exactly
//     notifywebpush.EnvDev -- production and preview are out of scope for
//     this part entirely.
//   - RunOnce/Run never send to, or decide the terminal fate of (cancel), any
//     outbox entry whose target device is not confirmed
//     notifydevices.Registration.TestMode == true, read fresh from Deps.Devices
//     every cycle, never cached or assumed.
//   - A Worker is a single sequential loop: Run processes at most one claimed
//     entry per tick. This package neither starts nor expects more than one
//     goroutine to call Run/RunOnce against the same OutboxStore concurrently
//     within this part (the underlying fencing in notifyoutboxstore would
//     tolerate it, but this package does not yet exercise or claim that).
//   - This package is never registered by backend/cmd/api: a caller (a test,
//     or a future authorized integration) must construct and invoke it
//     explicitly, mirroring every other notify-family package's own
//     dormant-by-construction convention.
//   - New fails closed: it refuses to construct a Worker at all (nil, error)
//     unless every dependency is non-nil and Config is well-formed, so a
//     misconfigured or relay-disabled caller can never start a loop that
//     claims outbox rows with nothing able to act on them. A refused Worker
//     claims zero rows, ever.
//   - This package imports no provider SDK, no HTTP route, and -- enforced by
//     its own depcheck_test.go -- none of internal/alertpipeline,
//     internal/alerts, or internal/alertstore: it never reads a live
//     radio/CAD/alert event itself. The AlertLookup dependency is injected by
//     the caller specifically so this package never gains that import.
//
// This package does not claim exactly-once Web Push delivery: see Send's own
// doc comment in backend/internal/notifywebpush and this package's own
// recordAccepted for the accepted residual crash-window risk the Part 14A
// design lock explicitly accepted rather than solved.
package notifyworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyattempt"
	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
	"greenwich-fire-responder/backend/internal/notifypreferences"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

// Default tuning values, used by New whenever the corresponding Config field
// is left at its zero value. None of these is a claim about what a future,
// wider-scoped part should use -- they are this dev-only slice's own
// deliberately conservative defaults.
const (
	// DefaultBaseRetryDelay and DefaultMaxRetryDelay are the bounded
	// exponential backoff schedule's own floor and ceiling (Part 14A design
	// lock Section 4): 30s, 60s, 120s, ... capped at 15m.
	DefaultBaseRetryDelay = 30 * time.Second
	DefaultMaxRetryDelay  = 15 * time.Minute
	// DefaultPollInterval is how often Run attempts a fresh ClaimNextDue
	// after finding no due row, or after finishing one claimed row.
	DefaultPollInterval = 5 * time.Second
	// DefaultDeferralRecheckInterval is the single bounded delay used for
	// BOTH quiet-hours and non-test_mode-device deferral (Part 14A design
	// lock Sections 1 and 6): a deferred entry is touched once per interval,
	// never once per poll tick, which is what prevents a hot reclaim loop.
	DefaultDeferralRecheckInterval = 15 * time.Minute
	// leaseSafetyMargin is added to Config.SendTimeout to derive the claim
	// lease duration (Part 14A design lock Section 5 / Decision 10): never an
	// independently configured value, so the lease can never silently drift
	// out of sync with however long a real send attempt is actually allowed
	// to take.
	leaseSafetyMargin = 30 * time.Second
)

// Sentinel errors. New returns one of these (refusing construction
// entirely) rather than ever returning a partially-usable Worker.
var (
	// ErrUnconfigured marks a Deps value missing a required dependency.
	ErrUnconfigured = errors.New("notifyworker: one or more required dependencies are nil")
	// ErrEnvironmentNotSupported marks a Config.Env other than
	// notifywebpush.EnvDev. Part 14A supports dev only; production and
	// preview delivery are explicitly out of scope for this part.
	ErrEnvironmentNotSupported = errors.New("notifyworker: only the dev environment is supported in this part")
	// ErrInput marks any other malformed Config field (currently: a
	// non-positive SendTimeout, which the lease duration is derived from and
	// so cannot be defaulted silently).
	ErrInput = errors.New("notifyworker: invalid configuration")
)

// OutboxStore is the minimal notifyoutboxstore surface a Worker needs.
// Declared locally (rather than depending on the concrete
// *notifyoutboxstore.Postgres type) so this package is testable with a
// simple fake, exactly mirroring notifyattempt.Evaluator's own convention.
type OutboxStore interface {
	ClaimNextDue(ctx context.Context, now time.Time, lease time.Duration) (notifyoutbox.Entry, bool, error)
	RescheduleClaim(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, nextAttemptAt time.Time) (notifyoutbox.Entry, error)
	ReleaseClaim(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string) (notifyoutbox.Entry, error)
	MarkSent(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string) (notifyoutbox.Entry, error)
	RecordFailedAttempt(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, nextAttemptAt time.Time, claimToken string) (notifyoutbox.Entry, error)
	Cancel(ctx context.Context, now time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error)
	MarkExpired(ctx context.Context, now time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error)
}

// DeviceStore is the minimal notifydevicestore surface a Worker needs: a
// fresh, never-cached TestMode/Active read before ever considering a send,
// and the ability to revoke a device whose subscription a push service has
// reported as unauthorized/gone.
type DeviceStore interface {
	Get(ctx context.Context, id notifydevices.DeviceID) (notifydevices.Registration, bool, error)
	Revoke(ctx context.Context, now time.Time, userID identity.UserID, id notifydevices.DeviceID) error
}

// DeliveryStore is the minimal notifydeliverystore surface a Worker needs.
type DeliveryStore interface {
	Record(ctx context.Context, d notifydelivery.Delivery) (notifydelivery.Delivery, error)
}

// AlertData carries both the eligibility-evaluation context and the
// notification payload for one outbox entry's EventID. Neither
// notifyoutbox.Entry nor notifypreferences.AlertContext carries any display
// text, and notifyrelay.Request cannot be constructed without one, so
// AlertLookup resolves both together from whatever the caller's own
// alert-event source is.
type AlertData struct {
	Context notifypreferences.AlertContext
	Payload notifyrelay.Payload
}

// AlertLookup resolves the AlertData for an outbox entry's EventID.
// Injected rather than imported: this package must never import
// internal/alerts or internal/alertstore (depcheck-enforced) -- it never
// reads a live radio/CAD/alert event itself. A caller (a test, or a future
// authorized integration) supplies this from whatever alert-event store it
// already has authorized access to.
type AlertLookup func(ctx context.Context, eventID string) (AlertData, error)

// Deps carries every dependency a Worker needs. Every field is required;
// New refuses to construct a Worker if any is nil (ErrUnconfigured).
type Deps struct {
	Outbox     OutboxStore
	Evaluator  notifyattempt.Evaluator
	Devices    DeviceStore
	Sender     notifyrelay.Sender
	Deliveries DeliveryStore
	Alerts     AlertLookup
	// Now returns the current time. Defaults to time.Now when nil; a test
	// supplies a fake clock instead.
	Now func() time.Time
}

// Config is a Worker's tuning configuration.
type Config struct {
	// Env must be exactly notifywebpush.EnvDev; New refuses construction
	// otherwise (ErrEnvironmentNotSupported). This is a structural restatement
	// of the same dev/test_mode pairing notifywebpush.Send itself enforces,
	// not a replacement for it -- both gates exist, defense in depth.
	Env notifywebpush.Env
	// SendTimeout must equal the Sender's own configured send timeout
	// (notifywebpush.Options.SendTimeout). Required, must be positive: the
	// claim lease duration is derived from it (SendTimeout + a fixed 30s
	// safety margin), deliberately never an independently configured value,
	// so the lease can never silently drift out of sync with how long a real
	// send is actually allowed to take.
	SendTimeout time.Duration
	// PollInterval, DeferralRecheckInterval, BaseRetryDelay, and
	// MaxRetryDelay all default to the package's own Default* constants when
	// left at zero.
	PollInterval            time.Duration
	DeferralRecheckInterval time.Duration
	BaseRetryDelay          time.Duration
	MaxRetryDelay           time.Duration
}

// Worker is the Part 14A orchestrator. Construct with New.
type Worker struct {
	deps Deps
	cfg  Config
}

// New validates deps and cfg and returns a ready-to-use Worker, or refuses
// construction entirely (nil, error) -- fail closed, exactly mirroring
// notifywebpush.New's own locked design. A refused Worker is never usable:
// Run/RunOnce are methods on *Worker, so there is no way to call either
// without first obtaining one from a successful New call, and a caller that
// fails to construct a Sender upstream (relay disabled or misconfigured)
// therefore cannot start a Worker at all -- zero outbox rows are ever
// claimed in that state.
func New(deps Deps, cfg Config) (*Worker, error) {
	if deps.Outbox == nil || deps.Evaluator == nil || deps.Devices == nil ||
		deps.Sender == nil || deps.Deliveries == nil || deps.Alerts == nil {
		return nil, ErrUnconfigured
	}
	if cfg.Env != notifywebpush.EnvDev {
		return nil, ErrEnvironmentNotSupported
	}
	if cfg.SendTimeout <= 0 {
		return nil, ErrInput
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.DeferralRecheckInterval <= 0 {
		cfg.DeferralRecheckInterval = DefaultDeferralRecheckInterval
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = DefaultBaseRetryDelay
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = DefaultMaxRetryDelay
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Worker{deps: deps, cfg: cfg}, nil
}

// leaseDuration derives the claim lease duration from the configured send
// timeout (Part 14A design lock Decision 10): always safely longer than the
// configured provider send timeout, never an independently configured value.
func (w *Worker) leaseDuration() time.Duration {
	return w.cfg.SendTimeout + leaseSafetyMargin
}

// Run polls until ctx is canceled, processing at most one claimed outbox
// entry per tick. It returns nil on clean shutdown (ctx.Done()). Errors from
// individual RunOnce calls are deliberately swallowed here (a transient
// database blip must not silently kill the whole loop); this package wires
// no structured logging of its own in this part.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		_, _ = w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce performs exactly one claim attempt and, if an entry was claimed,
// one full handling cycle for it. processed reports whether an entry was
// claimed at all (true even if handling it ultimately did nothing, for
// example a quiet-hours deferral) -- it is not a claim that a send was
// attempted. This exists so a test can drive the Worker deterministically,
// without sleeping for Run's own ticker.
func (w *Worker) RunOnce(ctx context.Context) (processed bool, err error) {
	now := w.deps.Now()
	entry, ok, err := w.deps.Outbox.ClaimNextDue(ctx, now, w.leaseDuration())
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return true, w.handleClaimed(ctx, now, entry)
}

// handleClaimed processes one freshly claimed entry through to some
// resolution: deferral, cancellation, or a send attempt and its outcome.
func (w *Worker) handleClaimed(ctx context.Context, now time.Time, entry notifyoutbox.Entry) error {
	if entry.ClaimToken == nil {
		// Unreachable in practice: ClaimNextDue only ever returns a claimed
		// entry. Fail closed rather than panic on a nil dereference if this
		// invariant is ever broken by a future edit.
		return errors.New("notifyworker: claimed entry carries no claim token")
	}
	claimToken := *entry.ClaimToken

	device, ok, err := w.deps.Devices.Get(ctx, entry.DeviceID)
	if err != nil {
		return err
	}
	if !ok || !device.TestMode {
		// Dev-only worker scope (Step 8D-B Part 14A): this worker must never
		// send to, or decide the terminal fate of, any entry whose device is
		// not confirmed test_mode=true -- including a device this worker's
		// own Devices.Get cannot even find, which is treated identically
		// (cannot confirm TestMode, so never proceed). Device
		// activity/revocation is deliberately NOT checked here: a test_mode
		// device that is later revoked (for example by this same worker's
		// own Unauthorized handling, see recordUnauthorized) must still flow
		// through to evaluation below, so the standard ActionCancel path
		// (ReasonDeviceNotActive) correctly terminates it -- a real
		// (non-test_mode) device's entry, by contrast, is left alone
		// entirely here, for a future, separately authorized worker to
		// resolve.
		return w.rescheduleDeferred(ctx, now, entry, claimToken)
	}

	alert, err := w.deps.Alerts(ctx, entry.EventID)
	if err != nil {
		return err
	}

	plan, err := notifyattempt.PlanAttempt(ctx, w.deps.Evaluator, now, entry, alert.Context)
	switch {
	case errors.Is(err, notifyattempt.ErrPolicyUnresolved):
		// Quiet hours: queue, never send or cancel (Part 14A design lock
		// Decision 1).
		return w.rescheduleDeferred(ctx, now, entry, claimToken)
	case errors.Is(err, notifyattempt.ErrNotPlannable):
		// Already past ExpiresAt (or otherwise no longer plannable) by the
		// time evaluation ran -- a stale read, not an error this worker
		// caused. Resolve via the expiry path, never a send or cancel.
		return w.handleNotPlannable(ctx, now, entry, claimToken)
	case err != nil:
		return err
	}

	switch plan.Action {
	case notifyattempt.ActionCancel:
		return w.cancel(ctx, now, entry, claimToken, plan)
	case notifyattempt.ActionSend:
		return w.send(ctx, now, entry, claimToken, plan, device, alert.Payload)
	default:
		return fmt.Errorf("notifyworker: unrecognized plan action %q", plan.Action)
	}
}

// rescheduleDeferred requeues entry for later re-evaluation (quiet hours or
// a non-test_mode device), capped at entry.ExpiresAt so expiration still
// ultimately prevents stale delivery regardless of how long the deferred
// condition persists (Part 14A design lock Decision 1). If the capped
// requeue time would not actually be after now (the entry is already at or
// past its own expiry), this defers to the same stale-at-plan-time handling
// RunOnce uses for ErrNotPlannable, rather than attempting an invalid
// (not-strictly-after-now) RescheduleClaim call.
//
// A fencing conflict here (another worker already reclaimed this entry)
// causes zero further action: no device mutation, no audit write, exactly
// like every other outcome path in this package.
func (w *Worker) rescheduleDeferred(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string) error {
	requeueAt := now.Add(w.cfg.DeferralRecheckInterval)
	if requeueAt.After(entry.ExpiresAt) {
		requeueAt = entry.ExpiresAt
	}
	if !requeueAt.After(now) {
		return w.handleNotPlannable(ctx, now, entry, claimToken)
	}
	if _, err := w.deps.Outbox.RescheduleClaim(ctx, now, entry.ID, claimToken, requeueAt); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}
	return nil
}

// handleNotPlannable resolves an entry discovered to already be past its own
// ExpiresAt (or otherwise non-plannable) at evaluation time: release the
// claim, then best-effort MarkExpired. No delivery-audit row is written for
// this path -- there was no real send attempt to audit (Part 14A design
// lock Section 3 / flag 5), carrying forward, for this one specific case,
// the zero-real-attempt question Part 7's own discovery left open.
//
// A lost race on either call (another worker already reclaimed, or already
// resolved, this entry) is treated as benign: MarkExpired's own fencing
// (claim_token IS NULL AND expires_at <= now) means it either succeeds
// exactly once or fails closed, never double-expiring or mutating a row it
// does not own.
func (w *Worker) handleNotPlannable(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string) error {
	if _, err := w.deps.Outbox.ReleaseClaim(ctx, now, entry.ID, claimToken); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}
	if _, err := w.deps.Outbox.MarkExpired(ctx, now, entry.ID); err != nil &&
		!errors.Is(err, notifyoutboxstore.ErrConflict) && !errors.Is(err, notifyoutboxstore.ErrNotFound) {
		return err
	}
	return nil
}

// cancel resolves an ActionCancel plan: release the claim, then Cancel (which
// structurally requires an unclaimed row), then record the audit outcome.
// Both outbox-store calls are fenced; the audit write happens only if both
// succeed (Part 14A design lock Section 9): a fencing conflict on either
// call causes zero further action from this worker.
func (w *Worker) cancel(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string, plan notifyattempt.Plan) error {
	if _, err := w.deps.Outbox.ReleaseClaim(ctx, now, entry.ID, claimToken); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}
	if _, err := w.deps.Outbox.Cancel(ctx, now, entry.ID); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			// Narrow, accepted race: a different reclaim landed between this
			// worker's own ReleaseClaim and Cancel calls. The entry's
			// eventual terminal state is still correct -- whoever now holds
			// it will reach its own conclusion -- but this worker writes no
			// audit row for it. Unreachable under this part's single-worker
			// scope; see the Part 14A design lock's own flagged limitation.
			return nil
		}
		return err
	}
	_, err := w.deps.Deliveries.Record(ctx, notifydelivery.Delivery{
		OutboxID:      entry.ID,
		EventID:       entry.EventID,
		DeviceID:      entry.DeviceID,
		Outcome:       notifydelivery.OutcomeCanceled,
		AttemptNumber: plan.AttemptNumber,
	})
	return err
}

// send resolves an ActionSend plan: construct the relay Request from the
// already-fresh device registration and alert payload, call Sender.Send
// exactly once (no retry within this call -- retry is the outbox's own
// bounded-attempt mechanism, driven by a future claim cycle), and dispatch
// on the returned Outcome.
func (w *Worker) send(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string, plan notifyattempt.Plan, device notifydevices.Registration, payload notifyrelay.Payload) error {
	req := notifyrelay.Request{
		OutboxID: notifyrelay.OutboxID(entry.ID),
		Endpoint: device.Subscription.Endpoint,
		P256dh:   device.Subscription.Keys.P256dh,
		Auth:     device.Subscription.Keys.Auth,
		Payload:  payload,
		TestMode: device.TestMode,
	}
	result, err := w.deps.Sender.Send(ctx, req)
	if err != nil {
		// notifyrelay.Sender.Send only ever errors for a caller/Sender
		// misconfiguration (an invalid Request, a disabled Sender, or an
		// environment/test-mode mismatch) -- never for an ordinary
		// provider-facing outcome, which always comes back as a valid
		// Result instead. Propagate as-is; this is not a send-outcome
		// classification this worker can resolve itself.
		return err
	}

	switch result.Outcome {
	case notifyrelay.OutcomeAccepted:
		return w.recordAccepted(ctx, now, entry, claimToken, plan)
	case notifyrelay.OutcomeTemporaryFailure, notifyrelay.OutcomePermanentFailure:
		return w.recordFailedAttempt(ctx, now, entry, claimToken, plan, result)
	case notifyrelay.OutcomeUnauthorized:
		return w.recordUnauthorized(ctx, now, entry, claimToken, plan)
	default:
		return fmt.Errorf("notifyworker: unrecognized relay outcome %q", result.Outcome)
	}
}

// recordAccepted resolves an OutcomeAccepted send: MarkSent, then record the
// audit row. This package does not claim exactly-once Web Push delivery
// (Part 14A design lock Decision 10 / flag 1): if this process crashes after
// Sender.Send has already returned OutcomeAccepted but before MarkSent (or
// the following Record call) commits, the claim's lease eventually expires
// and a future ClaimNextDue reclaims the same entry, which can cause a
// genuine duplicate physical push with no corresponding duplicate-detection
// in this schema. leaseDuration only bounds how long this window can last;
// it does not close it.
func (w *Worker) recordAccepted(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string, plan notifyattempt.Plan) error {
	if _, err := w.deps.Outbox.MarkSent(ctx, now, entry.ID, claimToken); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}
	_, err := w.deps.Deliveries.Record(ctx, notifydelivery.Delivery{
		OutboxID:      entry.ID,
		EventID:       entry.EventID,
		DeviceID:      entry.DeviceID,
		Outcome:       notifydelivery.OutcomeSent,
		AttemptNumber: plan.AttemptNumber,
	})
	return err
}

// recordFailedAttempt resolves an OutcomeTemporaryFailure or
// OutcomePermanentFailure send identically (Part 14A design lock Decision
// 7): both call RecordFailedAttempt with the same bounded backoff schedule,
// letting the outbox's own existing attempt-exhaustion machinery decide
// pending/dead_letter/expired. This package deliberately adds no fast
// dead-letter path for a permanent failure: doing so would require a new,
// unfenced-adjacent mutation this design lock explicitly declined to add.
func (w *Worker) recordFailedAttempt(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string, plan notifyattempt.Plan, result notifyrelay.Result) error {
	nextAttemptAt := now.Add(BackoffDelay(plan.AttemptNumber, w.cfg.BaseRetryDelay, w.cfg.MaxRetryDelay, result.RetryAfter))
	if nextAttemptAt.After(entry.ExpiresAt) {
		nextAttemptAt = entry.ExpiresAt
	}

	updated, err := w.deps.Outbox.RecordFailedAttempt(ctx, now, entry.ID, nextAttemptAt, claimToken)
	if err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}

	outcome, ok := deliveryOutcomeFor(updated.State)
	if !ok {
		return fmt.Errorf("notifyworker: unexpected outbox state %q after RecordFailedAttempt", updated.State)
	}
	_, err = w.deps.Deliveries.Record(ctx, notifydelivery.Delivery{
		OutboxID:      entry.ID,
		EventID:       entry.EventID,
		DeviceID:      entry.DeviceID,
		Outcome:       outcome,
		AttemptNumber: plan.AttemptNumber,
	})
	return err
}

// recordUnauthorized resolves an OutcomeUnauthorized send: release the
// claim, revoke the device immediately (idempotent, best-effort), then
// record the audit row (Part 14A design lock Decision 8 / Section 3). The
// entry itself is left pending and immediately reclaimable: no-resend is
// guaranteed not by a special outbox transition but by
// notifypreferences.Evaluate's own fresh-every-call device-active check --
// once Revoke commits, every future Evaluate call for this device observes
// ReasonDeviceNotActive before Sender.Send could ever be reached again, and
// the standard ActionCancel path (see cancel) terminates the entry on its
// next claim cycle.
func (w *Worker) recordUnauthorized(ctx context.Context, now time.Time, entry notifyoutbox.Entry, claimToken string, plan notifyattempt.Plan) error {
	if _, err := w.deps.Outbox.ReleaseClaim(ctx, now, entry.ID, claimToken); err != nil {
		if errors.Is(err, notifyoutboxstore.ErrConflict) {
			return nil
		}
		return err
	}
	revokeErr := w.deps.Devices.Revoke(ctx, now, entry.UserID, entry.DeviceID)
	_, recordErr := w.deps.Deliveries.Record(ctx, notifydelivery.Delivery{
		OutboxID:      entry.ID,
		EventID:       entry.EventID,
		DeviceID:      entry.DeviceID,
		Outcome:       notifydelivery.OutcomeUnauthorized,
		AttemptNumber: plan.AttemptNumber,
	})
	if recordErr != nil {
		return recordErr
	}
	return revokeErr
}

// deliveryOutcomeFor maps the notifyoutbox.State RecordFailedAttempt
// actually returned to the matching notifydelivery.Outcome. The caller must
// use the ACTUAL returned state, never assume which of the three branches
// RecordFailedAttempt's own CASE expression took.
func deliveryOutcomeFor(state notifyoutbox.State) (notifydelivery.Outcome, bool) {
	switch state {
	case notifyoutbox.StatePending:
		return notifydelivery.OutcomeFailed, true
	case notifyoutbox.StateDeadLetter:
		return notifydelivery.OutcomeDeadLetter, true
	case notifyoutbox.StateExpired:
		return notifydelivery.OutcomeExpired, true
	default:
		return "", false
	}
}

// BackoffDelay computes the Part 14A design lock's bounded exponential
// backoff schedule (Decision 6/7): base, 2*base, 4*base, ..., capped at max.
// retryAfter, when present, is honored as a floor (never a ceiling
// override): a provider-supplied Retry-After hint longer than the computed
// schedule wins; a shorter one is ignored, never shortening the schedule
// below its own deterministic minimum. Exported so it can be tested directly
// as a pure function, and so a future caller can reuse the exact same
// formula without re-deriving it.
func BackoffDelay(attemptNumber int, base, max time.Duration, retryAfter *time.Duration) time.Duration {
	shift := attemptNumber - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 20 {
		// Overflow guard: time.Duration is an int64 count of nanoseconds, and
		// any realistic base/max already caps the real-world result long
		// before this bound matters.
		shift = 20
	}
	delay := base * time.Duration(uint64(1)<<uint(shift))
	if delay <= 0 || delay > max {
		delay = max
	}
	if retryAfter != nil && *retryAfter > delay {
		delay = *retryAfter
	}
	return delay
}
