package shadowdeliverytest

// Step 8D-C Part 3: real PostgreSQL-backed eligibility evaluation,
// integrated. Part 2 (integration_test.go, unchanged) proves the real
// alert -> outbox -> worker -> FakeSender pipeline mechanics using a fixed,
// always-eligible fakeEvaluator. This file proves the thing Part 2's own
// doc comment says is NOT proven: a real notifypreferences.Evaluator, wired
// to the real identitystore/notifydevicestore/notifyconsentstore/
// notifyprefsstore stores, correctly deciding account status, device
// ownership, consent, preferences, and quiet hours against real persisted
// rows -- and the real notifyworker.Worker correctly acting on that
// decision (send, cancel, or defer).
//
// Two tiers, because they prove different things:
//
//   - Tier A (TestLiveShadowAlertDelivery*RealEvaluator*RollsBack, 3 tests):
//     the real Evaluator plugged into notifyworker.Worker, reusing Part 2's
//     own synthetic quick-call tone fixture and real alertpipeline ->
//     alertstore -> alertnotify -> notifyoutboxstore chain unchanged (see
//     buildTierAFixture). Only the evaluator and the consent/preference
//     fixtures it needs are new.
//   - Tier B (TestLiveNotifyPreferencesEvaluateRealStoresRollsBack and
//     TestLiveNotifyPreferencesEvaluateCorruptedTimezoneFailsClosedRollsBack):
//     direct evaluator.Evaluate calls against real stores, no outbox, no
//     worker -- the cheapest, most deterministic way to cover every one of
//     notifypreferences.Reason's nine values plus the fail-closed malformed-
//     state path, including the one case (device ownership mismatch) that
//     cannot arise through alertnotify's own fan-out at all.
//
// Config.Env is notifywebpush.EnvDev in every worker construction here,
// exactly like Part 2: production and preview sends remain structurally
// unreachable (notifyworker.New refuses any other Env), notifyrelay.
// FakeSender performs zero network I/O, and every fixture device is
// test_mode=true. No real notification is ever sent by this file.
//
// Residual risk this part does NOT close (see also notifyworker.go's own
// Decision-10 crash-window comment for a related, separately-documented
// gap): notifypreferences.Evaluate performs four independent, non-
// transactional reads, and nothing re-checks eligibility between Evaluate
// returning Eligible=true and notifyworker.send's call to Sender.Send --
// there is no re-verification in between. A consent revocation or
// preference change landing in that window does not stop the send. This is
// not bounded by "the next attempt re-evaluates fresh": a successful
// first-attempt send has no next attempt at all, so for the common case
// (OutcomeAccepted on the first try) there is no re-check, ever. Part 3
// proves the evaluator's decision is correct for the state that exists at
// the instant of evaluation, and proves the worker correctly acts on
// whatever decision it receives -- it does not, and cannot safely (without
// a second real, non-rollback-only connection racing this suite's own
// uncommitted transaction, which would make every test flaky by
// construction), prove the decision is still valid at the instant of the
// actual send. Closing that gap -- for example, re-evaluating immediately
// before Sender.Send -- is a future, separately authorized production
// change, not something this test-only package can resolve.
import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/alertnotify"
	"greenwich-fire-responder/backend/internal/alertpipeline"
	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/alertstore"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/notifyconsent"
	"greenwich-fire-responder/backend/internal/notifyconsentstore"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifydevicestore"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
	"greenwich-fire-responder/backend/internal/notifyoutcome"
	"greenwich-fire-responder/backend/internal/notifypreferences"
	"greenwich-fire-responder/backend/internal/notifyprefs"
	"greenwich-fire-responder/backend/internal/notifyprefsstore"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
	"greenwich-fire-responder/backend/internal/notifyworker"
	"greenwich-fire-responder/backend/internal/tonedetection"
)

// --- small local helpers, additional to this package's existing ones ---

func f64Ptr(v float64) *float64              { return &v }
func strPtr2(s string) *string               { return &s }
func durPtr2(d time.Duration) *time.Duration { return &d }

// insertFixtureUserWithStatus generalizes insertFixtureUser (which is
// hardcoded to 'active') to any of migration 000008's six valid
// identity.AccountState values. A password_hash is always supplied
// regardless of status: migration 000008's own
// users_active_requires_password CHECK only requires it when status =
// 'active', but supplying it unconditionally keeps this helper simple and
// is harmless for every other status.
func insertFixtureUserWithStatus(t *testing.T, ctx context.Context, tx pgx.Tx, email, status string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Shadow Eligibility Test', 'responder', $2,
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email, status).Scan(&id); err != nil {
		t.Fatalf("fixture user (status=%s) insert failed: %v", status, err)
	}
	return id
}

// nonexistentUserID/nonexistentDeviceID compute an id guaranteed to match no
// row currently visible within tx, by reading the current max id and adding
// a large offset -- safe against collision with any real, already-persisted
// row (unlike a hardcoded constant, which a concurrent dev/test session's
// own data could coincidentally occupy).
func nonexistentUserID(t *testing.T, ctx context.Context, tx pgx.Tx) identity.UserID {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(id), 0) + 1000000 FROM users`).Scan(&id); err != nil {
		t.Fatalf("compute nonexistent user id: %v", err)
	}
	return identity.UserID(id)
}

func nonexistentDeviceID(t *testing.T, ctx context.Context, tx pgx.Tx) notifydevices.DeviceID {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(id), 0) + 1000000 FROM notification_devices`).Scan(&id); err != nil {
		t.Fatalf("compute nonexistent device id: %v", err)
	}
	return notifydevices.DeviceID(id)
}

// beginRollbackOnlyTx opens the real local database (skipping the test via
// t.Skip if GFR_SHADOW_DELIVERY_LIVE_TEST is not "true", identically to
// every other live test in this package) and returns a fresh pgx.Tx plus a
// rollback func the caller must defer immediately. Duplicates the open/begin
// boilerplate already present in integration_test.go's two existing tests,
// factored here since every test in this file needs it identically.
func beginRollbackOnlyTx(t *testing.T, ctx context.Context) (pgx.Tx, func()) {
	t.Helper()
	if os.Getenv("GFR_SHADOW_DELIVERY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_SHADOW_DELIVERY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	outboxDB, closeOutbox, err := notifyoutboxstore.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local database unavailable")
	}
	pool := outboxDB.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		closeOutbox()
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	cleanup := func() {
		if !rolledBack {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(c)
			rolledBack = true
		}
		closeOutbox()
	}
	return tx, cleanup
}

// --- Tier A: real Evaluator wired into notifyworker.Worker ---

// tierAFixture bundles everything buildTierAFixture creates, so each Tier A
// test can add its own consent/preference state and worker on top.
type tierAFixture struct {
	ev          alerts.Event
	userID      identity.UserID
	deviceID    notifydevices.DeviceID
	outboxStore *notifyoutboxstore.Postgres
	deviceStore *notifydevicestore.Postgres
	results     []alertnotify.DeviceResult
	ourEntryID  notifyoutbox.OutboxID
	wantDevice  notifydevices.Registration
	toneSetID   string
	enqueueNow  time.Time
	expiresAt   time.Time
}

// buildTierAFixture duplicates TestLiveShadowAlertDeliveryEndToEndRollsBack's
// own synthetic quick-call tone fixture and real alertpipeline ->
// alertstore -> alertnotify -> notifyoutboxstore chain exactly (see that
// test's own comments for why each step is shaped the way it is), including
// the identical claim-scoping safety check (Part 2 design-lock Correction
// 1): every currently-due pending row visible within tx must belong to this
// fixture's own fan-out before any caller of this function ever calls
// RunOnce. label must be unique per call (used in the fixture email/
// endpoint) so two Tier A tests sharing no transaction never collide.
func buildTierAFixture(t *testing.T, ctx context.Context, tx pgx.Tx, label string) tierAFixture {
	t.Helper()

	userID := insertFixtureUser(t, ctx, tx, "shadowdeliverytest-elig-"+label+"@example.com")
	deviceID := insertFixtureTestModeDevice(t, ctx, tx, userID, "https://push.example.invalid/eligibility/"+label)

	fingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64("shadowdeliverytest-eligibility-"+label)
	insertFixtureRadioTransmission(t, ctx, tx, fingerprint)
	evidence := alerts.Evidence{AudioFingerprint: fingerprint}

	toneSetID := "elig-quick-call-" + label
	toneSet := tonedetection.ToneSet{
		ID:      toneSetID,
		Channel: alerts.ChannelCH1A,
		Segments: []tonedetection.Segment{
			{FrequencyHz: 800, MinDurationMS: 800, MaxDurationMS: 1500},
			{FrequencyHz: 1200, MinDurationMS: 800, MaxDurationMS: 1500},
		},
		ToleranceHz: 30, MaxGapMS: 300, PresenceThreshold: 0.15, MinConfidence: 0.6,
	}
	registry, err := tonedetection.NewRegistry([]tonedetection.ToneSet{toneSet}, 100)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	pcm := tonedetection.PCM{SampleRate: 16000, Samples: concat(tone(800, 1000, 0.8), tone(1200, 1000, 0.8))}

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := base.Add(2 * time.Hour)
	channel := alerts.ChannelCH1A
	const tgid = 57201 // alerts.channelTGID's fixed, hardcoded CH1A mapping -- not a free choice
	sourceKind := alerts.SourceSynthetic
	labels := map[string]string{toneSetID: "Shadow eligibility test alert " + label}

	detections, err := registry.Detect(channel, pcm)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detections) != 1 {
		t.Fatalf("expected exactly one detection, got %d", len(detections))
	}
	det := detections[0]
	if det.State != alerts.StateMatched || det.Reason != tonedetection.ReasonMatched {
		t.Fatalf("expected a clean matched detection, got %+v", det)
	}

	var confidence *float64
	if toneReasonHasConfidence(det.Reason) {
		c := det.Confidence
		confidence = &c
	}

	limits := alerts.Limits{MaxExpiryWindow: 24 * time.Hour}
	builder, err := alerts.NewBuilder(limits)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	in := alerts.Input{
		CreatedAt: base, ExpiresAt: expiresAt,
		SourceKind: sourceKind, Channel: channel, TGID: tgid,
		DetectorKind: alerts.DetectorTone,
		Confidence:   confidence,
		State:        det.State,
		Evidence:     evidence,
		Label:        labels[det.ToneSetID],
		ToneSetID:    det.ToneSetID,
	}
	ev, err := builder.New(in)
	if err != nil {
		t.Fatalf("builder.New: %v", err)
	}

	alertStore := &alertstore.Postgres{DB: tx}
	pipelineSvc, err := alertpipeline.NewService(alertStore, alertpipeline.Limits{
		Alerts: limits, Cooldown: time.Minute, MaxBatchSize: 10,
	})
	if err != nil {
		t.Fatalf("alertpipeline.NewService: %v", err)
	}
	outcomes, err := pipelineSvc.ProcessTone(ctx, alertpipeline.ToneRequest{
		Registry: registry, Channel: channel, TGID: tgid, SourceKind: sourceKind,
		Evidence: evidence, PCM: pcm, Now: base, ExpiresAt: expiresAt, Labels: labels,
	})
	if err != nil {
		t.Fatalf("ProcessTone: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Rejected || outcomes[0].State != alerts.StateMatched ||
		!outcomes[0].Result.EventCreated || outcomes[0].Result.Suppressed {
		t.Fatalf("expected one newly created matched outcome, got %+v", outcomes)
	}

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	deviceStore := &notifydevicestore.Postgres{DB: tx}
	notifySvc, err := alertnotify.NewService(outboxStore, deviceStore, alertnotify.Config{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("alertnotify.NewService: %v", err)
	}

	enqueueNow := base.Add(time.Minute)
	results, err := notifySvc.EnqueueMatchedEvent(ctx, enqueueNow, ev)
	if err != nil {
		t.Fatalf("EnqueueMatchedEvent: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one fanned-out result (our own fixture device, system-wide test_mode scope)")
	}

	var ourResult *alertnotify.DeviceResult
	for i := range results {
		if results[i].DeviceID == notifydevices.DeviceID(deviceID) {
			ourResult = &results[i]
		}
	}
	if ourResult == nil {
		t.Fatalf("expected a result for our own fixture device %d among %d fanned-out results", deviceID, len(results))
	}
	if ourResult.Err != nil {
		t.Fatalf("unexpected error enqueueing our own fixture device: %v", ourResult.Err)
	}

	wantDevice, ok, err := deviceStore.Get(ctx, notifydevices.DeviceID(deviceID))
	if err != nil || !ok {
		t.Fatalf("expected to read back our own fixture device, got ok=%v err=%v", ok, err)
	}

	// Claim-scoping safety check (Part 2 design-lock Correction 1), repeated
	// here unchanged: this test must not proceed if the shared database
	// contains due rows outside this fixture's own fan-out, since the
	// public Worker API cannot scope a claim to a specific event/outbox id.
	var dueCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE state = 'pending' AND next_attempt_at <= $1`, enqueueNow).Scan(&dueCount); err != nil {
		t.Fatal(err)
	}
	if dueCount != int64(len(results)) {
		t.Fatalf("claim-scoping safety check failed: expected exactly %d due pending outbox rows (this fixture's own fan-out), found %d -- "+
			"the shared database contains unrelated due entries outside this fixture's own event_id", len(results), dueCount)
	}

	return tierAFixture{
		ev: ev, userID: identity.UserID(userID), deviceID: notifydevices.DeviceID(deviceID),
		outboxStore: outboxStore, deviceStore: deviceStore,
		results: results, ourEntryID: ourResult.Entry.ID, wantDevice: wantDevice,
		toneSetID: toneSetID, enqueueNow: enqueueNow, expiresAt: expiresAt,
	}
}

// realEvaluator constructs a notifypreferences.Evaluator wired to all four
// real Postgres stores over the same shared tx, mirroring exactly how a
// future production caller would wire it.
func realEvaluator(tx pgx.Tx, deviceStore *notifydevicestore.Postgres) *notifypreferences.Evaluator {
	return &notifypreferences.Evaluator{
		Devices:     deviceStore,
		Consents:    &notifyconsentstore.Postgres{DB: tx},
		Preferences: &notifyprefsstore.Postgres{DB: tx},
		Accounts:    &identitystore.Postgres{DB: tx},
	}
}

// alertLookupFor builds the notifyworker.AlertLookup closure Part 2 uses,
// rejecting any eventID other than fx.ev's own (Part 2 design-lock
// Correction 2), parameterized by channel so Tier A tests can reuse it.
func alertLookupFor(fx tierAFixture) notifyworker.AlertLookup {
	toneSetID := fx.toneSetID
	return func(_ context.Context, eventID string) (notifyworker.AlertData, error) {
		if eventID != fx.ev.EventID {
			return notifyworker.AlertData{}, errors.New("shadowdeliverytest: AlertLookup called with unknown event id")
		}
		return notifyworker.AlertData{
			Context: notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: &toneSetID, Confidence: fx.ev.Confidence},
			Payload: notifyrelay.Payload{Title: fx.ev.DisplaySummary},
		}, nil
	}
}

// TestLiveShadowAlertDeliveryEligibleRealEvaluatorSendsRollsBack proves a
// real alert, fanned out to a real outbox row, evaluated by the real
// notifypreferences.Evaluator (consent granted, preferences enabled with a
// matching channel and tone-set, no quiet hours), is actually sent.
func TestLiveShadowAlertDeliveryEligibleRealEvaluatorSendsRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, cleanup := beginRollbackOnlyTx(t, ctx)
	defer cleanup()

	fx := buildTierAFixture(t, ctx, tx, "eligible")

	consentStore := &notifyconsentstore.Postgres{DB: tx}
	if _, err := consentStore.Record(ctx, fx.enqueueNow, fx.userID, fx.deviceID, notifyconsent.EventGranted); err != nil {
		t.Fatalf("consent fixture: %v", err)
	}
	prefsStore := &notifyprefsstore.Postgres{DB: tx}
	if _, err := prefsStore.Upsert(ctx, fx.enqueueNow, notifyprefs.Preferences{
		UserID: fx.userID, Enabled: true,
		Channels:   []notifyprefs.Channel{notifyprefs.ChannelCH1A},
		ToneSetIDs: []string{fx.toneSetID},
	}); err != nil {
		t.Fatalf("preferences fixture: %v", err)
	}

	sender := notifyrelay.NewFakeSender()
	evaluator := realEvaluator(tx, fx.deviceStore)
	outcomeStore := &notifyoutcome.Store{DB: tx}

	workerNow := fx.enqueueNow.Add(90 * time.Second)
	w, err := notifyworker.New(notifyworker.Deps{
		Outbox: fx.outboxStore, Evaluator: evaluator, Devices: fx.deviceStore,
		Sender: sender, Outcomes: outcomeStore, Alerts: alertLookupFor(fx),
		Now: func() time.Time { return workerNow },
	}, notifyworker.Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("notifyworker.New: %v", err)
	}

	for i := 0; i < len(fx.results); i++ {
		entry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && entry.State == notifyoutbox.StateSent {
			break
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !processed {
			t.Fatalf("RunOnce reported no due entry remaining, but fixture entry %d has not reached StateSent", fx.ourEntryID)
		}
	}

	finalEntry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
	if err != nil || !ok || finalEntry.State != notifyoutbox.StateSent {
		t.Fatalf("expected fixture entry %d to reach StateSent within %d RunOnce calls, got ok=%v err=%v entry=%+v", fx.ourEntryID, len(fx.results), ok, err, finalEntry)
	}

	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'sent'`, int64(fx.ourEntryID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf("expected exactly one real 'sent' delivery-audit row for entry %d, got %d", fx.ourEntryID, deliveryCount)
	}

	var found *notifyrelay.Request
	for _, c := range sender.Calls() {
		if c.OutboxID == notifyrelay.OutboxID(fx.ourEntryID) {
			cc := c
			found = &cc
		}
	}
	if found == nil {
		t.Fatalf("expected FakeSender to have recorded a call for entry %d", fx.ourEntryID)
	}
	if found.Endpoint != fx.wantDevice.Subscription.Endpoint {
		t.Fatalf("FakeSender call endpoint = %q, want %q", found.Endpoint, fx.wantDevice.Subscription.Endpoint)
	}
}

// TestLiveShadowAlertDeliveryIneligibleRealEvaluatorCancelsRollsBack proves
// that once consent is revoked, the real Evaluator reports
// ReasonConsentNotGranted and the real worker cancels the entry -- never
// sending, and never consuming the retry budget a send would.
func TestLiveShadowAlertDeliveryIneligibleRealEvaluatorCancelsRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, cleanup := beginRollbackOnlyTx(t, ctx)
	defer cleanup()

	fx := buildTierAFixture(t, ctx, tx, "revoked")

	consentStore := &notifyconsentstore.Postgres{DB: tx}
	if _, err := consentStore.Record(ctx, fx.enqueueNow, fx.userID, fx.deviceID, notifyconsent.EventGranted); err != nil {
		t.Fatalf("consent grant fixture: %v", err)
	}
	if _, err := consentStore.Record(ctx, fx.enqueueNow.Add(time.Second), fx.userID, fx.deviceID, notifyconsent.EventRevoked); err != nil {
		t.Fatalf("consent revoke fixture: %v", err)
	}
	prefsStore := &notifyprefsstore.Postgres{DB: tx}
	if _, err := prefsStore.Upsert(ctx, fx.enqueueNow, notifyprefs.Preferences{
		UserID: fx.userID, Enabled: true,
		Channels:   []notifyprefs.Channel{notifyprefs.ChannelCH1A},
		ToneSetIDs: []string{fx.toneSetID},
	}); err != nil {
		t.Fatalf("preferences fixture: %v", err)
	}

	sender := notifyrelay.NewFakeSender()
	evaluator := realEvaluator(tx, fx.deviceStore)
	outcomeStore := &notifyoutcome.Store{DB: tx}

	workerNow := fx.enqueueNow.Add(90 * time.Second)
	w, err := notifyworker.New(notifyworker.Deps{
		Outbox: fx.outboxStore, Evaluator: evaluator, Devices: fx.deviceStore,
		Sender: sender, Outcomes: outcomeStore, Alerts: alertLookupFor(fx),
		Now: func() time.Time { return workerNow },
	}, notifyworker.Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("notifyworker.New: %v", err)
	}

	for i := 0; i < len(fx.results); i++ {
		entry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && entry.State == notifyoutbox.StateCanceled {
			break
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !processed {
			t.Fatalf("RunOnce reported no due entry remaining, but fixture entry %d has not reached StateCanceled", fx.ourEntryID)
		}
	}

	finalEntry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
	if err != nil || !ok || finalEntry.State != notifyoutbox.StateCanceled {
		t.Fatalf("expected fixture entry %d to reach StateCanceled within %d RunOnce calls, got ok=%v err=%v entry=%+v", fx.ourEntryID, len(fx.results), ok, err, finalEntry)
	}

	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'canceled'`, int64(fx.ourEntryID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf("expected exactly one real 'canceled' delivery-audit row for entry %d, got %d", fx.ourEntryID, deliveryCount)
	}

	for _, c := range sender.Calls() {
		if c.OutboxID == notifyrelay.OutboxID(fx.ourEntryID) {
			t.Fatalf("expected FakeSender to never be called for a canceled (consent-revoked) entry, but it was: %+v", c)
		}
	}
}

// TestLiveShadowAlertDeliveryQuietHoursRealEvaluatorDefersRollsBack proves
// that a real, currently-active quiet-hours window (not a fake asserting
// the reason by fiat) causes the real worker to defer -- never send, never
// cancel (Part 14A design lock Decision 1) -- and that the entry's own
// next_attempt_at is pushed forward while remaining pending.
func TestLiveShadowAlertDeliveryQuietHoursRealEvaluatorDefersRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, cleanup := beginRollbackOnlyTx(t, ctx)
	defer cleanup()

	fx := buildTierAFixture(t, ctx, tx, "quiethours")

	consentStore := &notifyconsentstore.Postgres{DB: tx}
	if _, err := consentStore.Record(ctx, fx.enqueueNow, fx.userID, fx.deviceID, notifyconsent.EventGranted); err != nil {
		t.Fatalf("consent fixture: %v", err)
	}
	prefsStore := &notifyprefsstore.Postgres{DB: tx}
	// workerNow below is fx.enqueueNow (12:01:00 UTC) + 90s = 12:02:30 UTC,
	// comfortably inside this noon-to-1pm UTC window.
	if _, err := prefsStore.Upsert(ctx, fx.enqueueNow, notifyprefs.Preferences{
		UserID: fx.userID, Enabled: true,
		Channels:           []notifyprefs.Channel{notifyprefs.ChannelCH1A},
		ToneSetIDs:         []string{fx.toneSetID},
		QuietHoursStart:    durPtr2(12 * time.Hour),
		QuietHoursEnd:      durPtr2(13 * time.Hour),
		QuietHoursTimezone: strPtr2("UTC"),
	}); err != nil {
		t.Fatalf("preferences fixture: %v", err)
	}

	sender := notifyrelay.NewFakeSender()
	evaluator := realEvaluator(tx, fx.deviceStore)
	outcomeStore := &notifyoutcome.Store{DB: tx}

	workerNow := fx.enqueueNow.Add(90 * time.Second)
	w, err := notifyworker.New(notifyworker.Deps{
		Outbox: fx.outboxStore, Evaluator: evaluator, Devices: fx.deviceStore,
		Sender: sender, Outcomes: outcomeStore, Alerts: alertLookupFor(fx),
		Now: func() time.Time { return workerNow },
	}, notifyworker.Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("notifyworker.New: %v", err)
	}

	var sawDeferred bool
	for i := 0; i < len(fx.results); i++ {
		entry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
		if err != nil {
			t.Fatal(err)
		}
		if ok && entry.State == notifyoutbox.StatePending && entry.ClaimToken == nil && entry.NextAttemptAt.After(workerNow) {
			sawDeferred = true
			break
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !processed {
			t.Fatalf("RunOnce reported no due entry remaining, but fixture entry %d was never observed deferred", fx.ourEntryID)
		}
	}
	if !sawDeferred {
		t.Fatalf("expected fixture entry %d to be deferred (still pending, unclaimed, next_attempt_at pushed forward) within %d RunOnce calls", fx.ourEntryID, len(fx.results))
	}

	finalEntry, ok, err := fx.outboxStore.Get(ctx, fx.ourEntryID)
	if err != nil || !ok {
		t.Fatalf("expected to read back fixture entry %d, got ok=%v err=%v", fx.ourEntryID, ok, err)
	}
	if finalEntry.State != notifyoutbox.StatePending {
		t.Fatalf("expected entry to remain StatePending (deferred, never canceled/sent), got %v", finalEntry.State)
	}
	if !finalEntry.NextAttemptAt.After(workerNow) {
		t.Fatalf("expected next_attempt_at to be pushed strictly after workerNow (%v), got %v", workerNow, finalEntry.NextAttemptAt)
	}
	if finalEntry.NextAttemptAt.After(fx.expiresAt) {
		t.Fatalf("expected next_attempt_at to be capped at expires_at (%v), got %v", fx.expiresAt, finalEntry.NextAttemptAt)
	}

	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1`, int64(fx.ourEntryID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 0 {
		t.Fatalf("expected zero delivery-audit rows for a deferred (never sent, never canceled) entry, got %d", deliveryCount)
	}

	for _, c := range sender.Calls() {
		if c.OutboxID == notifyrelay.OutboxID(fx.ourEntryID) {
			t.Fatalf("expected FakeSender to never be called for a quiet-hours-deferred entry, but it was: %+v", c)
		}
	}
}

// --- Tier B: direct evaluator.Evaluate calls against real stores ---

// evalFixtureNow is the fixed clock every Tier B case evaluates against
// unless a case explicitly needs a different "now" (the two quiet-hours
// cases below).
var evalFixtureNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

const evalBaselineToneSetID = "elig-table-tone"

// buildEligibleEvalFixture creates one fully eligible (userID, deviceID,
// AlertContext) triple: an active account, an active device it owns,
// granted consent, and enabled preferences matching the returned
// AlertContext's channel and tone-set, with no quiet-hours window
// configured. Every Tier B negative case starts from this and deliberately
// breaks exactly one thing.
func buildEligibleEvalFixture(t *testing.T, ctx context.Context, tx pgx.Tx, label string) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
	t.Helper()
	uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-"+label+"@example.com"))
	did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/"+label))

	consentStore := &notifyconsentstore.Postgres{DB: tx}
	if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
		t.Fatalf("consent fixture (%s): %v", label, err)
	}
	prefsStore := &notifyprefsstore.Postgres{DB: tx}
	if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
		UserID: uid, Enabled: true,
		Channels:   []notifyprefs.Channel{notifyprefs.ChannelCH1A},
		ToneSetIDs: []string{evalBaselineToneSetID},
	}); err != nil {
		t.Fatalf("preferences fixture (%s): %v", label, err)
	}
	return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
}

type evalCase struct {
	name         string
	now          time.Time // zero means use evalFixtureNow
	build        func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext)
	wantEligible bool
	wantReason   notifypreferences.Reason
}

// TestLiveNotifyPreferencesEvaluateRealStoresRollsBack is the direct-call
// counterpart to internal/notifypreferences's own unit tests: the identical
// table of outcomes, proven here against the real identitystore/
// notifydevicestore/notifyconsentstore/notifyprefsstore stores instead of
// fakes. Each case gets its own fresh transaction (opened and rolled back
// inside the t.Run below), not one shared across the whole table: under
// Postgres, a single failed statement aborts the entire transaction (SQLSTATE
// 25P02) until rollback, so sharing one tx across cases would let an
// unrelated early failure cascade into every later case failing too,
// masking the real signal -- exactly what was observed and corrected while
// first running this suite live.
func TestLiveNotifyPreferencesEvaluateRealStoresRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runCase := func(t *testing.T, c evalCase) {
		tx, cleanup := beginRollbackOnlyTx(t, ctx)
		defer cleanup()

		evaluator := &notifypreferences.Evaluator{
			Devices:     &notifydevicestore.Postgres{DB: tx},
			Consents:    &notifyconsentstore.Postgres{DB: tx},
			Preferences: &notifyprefsstore.Postgres{DB: tx},
			Accounts:    &identitystore.Postgres{DB: tx},
		}
		now := c.now
		if now.IsZero() {
			now = evalFixtureNow
		}
		userID, deviceID, alert := c.build(t, ctx, tx)
		decision, err := evaluator.Evaluate(ctx, now, userID, deviceID, alert)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Eligible != c.wantEligible || decision.Reason != c.wantReason {
			t.Fatalf("Evaluate() = %+v, want Eligible=%v Reason=%q", decision, c.wantEligible, c.wantReason)
		}
	}

	cases := []evalCase{
		{
			name: "eligible",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				return buildEligibleEvalFixture(t, ctx, tx, "eligible")
			},
			wantEligible: true, wantReason: notifypreferences.ReasonEligible,
		},
		{
			name: "account_missing",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := nonexistentUserID(t, ctx, tx)
				did := nonexistentDeviceID(t, ctx, tx)
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonAccountNotActive,
		},
		{
			name: "account_suspended",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUserWithStatus(t, ctx, tx, "shadowdeliverytest-eval-suspended@example.com", "suspended"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/suspended"))
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonAccountNotActive,
		},
		{
			name: "device_missing",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid, _, alert := buildEligibleEvalFixture(t, ctx, tx, "devicemissing")
				return uid, nonexistentDeviceID(t, ctx, tx), alert
			},
			wantReason: notifypreferences.ReasonDeviceNotActive,
		},
		{
			name: "device_revoked",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid, did, alert := buildEligibleEvalFixture(t, ctx, tx, "devicerevoked")
				deviceStore := &notifydevicestore.Postgres{DB: tx}
				if err := deviceStore.Revoke(ctx, evalFixtureNow, uid, did); err != nil {
					t.Fatalf("revoke fixture device: %v", err)
				}
				return uid, did, alert
			},
			wantReason: notifypreferences.ReasonDeviceNotActive,
		},
		{
			name: "device_ownership_mismatch",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				userA := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-ownera@example.com"))
				_, did, alert := buildEligibleEvalFixture(t, ctx, tx, "ownerb")
				// Evaluate for userA against a device that actually belongs
				// to userB: must never be treated as active for userA.
				return userA, did, alert
			},
			wantReason: notifypreferences.ReasonDeviceNotActive,
		},
		{
			name: "consent_never_recorded",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-noconsent@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/noconsent"))
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonConsentNotGranted,
		},
		{
			name: "consent_revoked",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid, did, alert := buildEligibleEvalFixture(t, ctx, tx, "consentrevoked")
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow.Add(time.Second), uid, did, notifyconsent.EventRevoked); err != nil {
					t.Fatalf("revoke consent fixture: %v", err)
				}
				return uid, did, alert
			},
			wantReason: notifypreferences.ReasonConsentNotGranted,
		},
		{
			name: "no_preferences_row",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-noprefs@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/noprefs"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonNotificationsDisabled,
		},
		{
			name: "preferences_explicitly_disabled",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-disabled@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/disabled"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: false,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{evalBaselineToneSetID},
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonNotificationsDisabled,
		},
		{
			name: "channel_not_selected",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-channel@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/channel"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH2B}, ToneSetIDs: []string{evalBaselineToneSetID},
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonChannelNotSelected,
		},
		{
			name: "tone_set_not_selected",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-tone@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/tone"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{"other-tone"},
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonToneSetNotSelected,
		},
		{
			name: "keyword_list_not_selected",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-keyword@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/keyword"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, KeywordListIDs: []string{"other-keyword"},
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, KeywordListID: strPtr2("elig-table-keyword")}
			},
			wantReason: notifypreferences.ReasonKeywordListNotSelected,
		},
		{
			name: "below_min_confidence",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-minconf@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/minconf"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{evalBaselineToneSetID},
					MinConfidence: f64Ptr(0.9),
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID), Confidence: f64Ptr(0.5)}
			},
			wantReason: notifypreferences.ReasonBelowMinConfidence,
		},
		{
			name: "below_min_confidence_nil_alert_confidence",
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-minconfnil@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/minconfnil"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{evalBaselineToneSetID},
					MinConfidence: f64Ptr(0.9),
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonBelowMinConfidence,
		},
		{
			name: "quiet_hours_normal_window",
			now:  evalFixtureNow, // 12:00:00 UTC, inside [11:00, 13:00)
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-quiet1@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/quiet1"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{evalBaselineToneSetID},
					QuietHoursStart: durPtr2(11 * time.Hour), QuietHoursEnd: durPtr2(13 * time.Hour), QuietHoursTimezone: strPtr2("UTC"),
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonQuietHours,
		},
		{
			name: "quiet_hours_overnight_window",
			now:  time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC), // 02:00 UTC, inside an overnight 22:00-06:00 window
			build: func(t *testing.T, ctx context.Context, tx pgx.Tx) (identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) {
				uid := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-quiet2@example.com"))
				did := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(uid), "https://push.example.invalid/eval/quiet2"))
				consentStore := &notifyconsentstore.Postgres{DB: tx}
				if _, err := consentStore.Record(ctx, evalFixtureNow, uid, did, notifyconsent.EventGranted); err != nil {
					t.Fatalf("consent fixture: %v", err)
				}
				prefsStore := &notifyprefsstore.Postgres{DB: tx}
				if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
					UserID: uid, Enabled: true,
					Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{evalBaselineToneSetID},
					QuietHoursStart: durPtr2(22 * time.Hour), QuietHoursEnd: durPtr2(6 * time.Hour), QuietHoursTimezone: strPtr2("UTC"),
				}); err != nil {
					t.Fatalf("preferences fixture: %v", err)
				}
				return uid, did, notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr2(evalBaselineToneSetID)}
			},
			wantReason: notifypreferences.ReasonQuietHours,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			runCase(t, c)
		})
	}
}

// TestLiveNotifyPreferencesEvaluateCorruptedTimezoneFailsClosedRollsBack
// proves Evaluate fails closed (ErrUnavailable, never a silent Eligible)
// against persisted quiet-hours state that notifyprefsstore.Upsert itself
// would refuse to write (notifyprefs.Validate rejects both shapes below),
// but which the database schema does NOT forbid: migration 000010's
// quiet_hours_timezone column has no NOT NULL and no CHECK pairing it with
// quiet_hours_start/quiet_hours_end (only alert_preferences_quiet_hours_paired,
// which pairs start with end, not either with timezone) -- so a nil or
// nonsense timezone alongside a valid paired window is schema-legal,
// simulating persisted legacy/corrupted data. The one shape this suite does
// NOT attempt to construct is a half-paired start/end: that IS rejected by
// the database itself (alert_preferences_quiet_hours_paired), not only by
// Validate, so no fixture can exist for it against this real schema.
func TestLiveNotifyPreferencesEvaluateCorruptedTimezoneFailsClosedRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, cleanup := beginRollbackOnlyTx(t, ctx)
	defer cleanup()

	userID := identity.UserID(insertFixtureUser(t, ctx, tx, "shadowdeliverytest-eval-corrupttz@example.com"))
	deviceID := notifydevices.DeviceID(insertFixtureTestModeDevice(t, ctx, tx, int64(userID), "https://push.example.invalid/eval/corrupttz"))

	consentStore := &notifyconsentstore.Postgres{DB: tx}
	if _, err := consentStore.Record(ctx, evalFixtureNow, userID, deviceID, notifyconsent.EventGranted); err != nil {
		t.Fatalf("consent fixture: %v", err)
	}
	prefsStore := &notifyprefsstore.Postgres{DB: tx}
	toneSetID := "corrupt-tz-tone"
	// A valid, fully-compliant row first, through the real Upsert (and
	// therefore through notifyprefs.Validate) -- establishing that this
	// starts as a legitimate row, not one invented to already violate
	// anything.
	if _, err := prefsStore.Upsert(ctx, evalFixtureNow, notifyprefs.Preferences{
		UserID: userID, Enabled: true,
		Channels: []notifyprefs.Channel{notifyprefs.ChannelCH1A}, ToneSetIDs: []string{toneSetID},
		QuietHoursStart: durPtr2(1 * time.Hour), QuietHoursEnd: durPtr2(2 * time.Hour), QuietHoursTimezone: strPtr2("UTC"),
	}); err != nil {
		t.Fatalf("baseline preferences fixture: %v", err)
	}

	evaluator := &notifypreferences.Evaluator{
		Devices:     &notifydevicestore.Postgres{DB: tx},
		Consents:    consentStore,
		Preferences: prefsStore,
		Accounts:    &identitystore.Postgres{DB: tx},
	}
	alert := notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: &toneSetID}

	t.Run("null_timezone_with_configured_window", func(t *testing.T) {
		// Raw SQL, deliberately bypassing Upsert/Validate: this exact shape
		// (paired, valid start/end; NULL timezone) is schema-legal, per this
		// test's own doc comment above.
		if _, err := tx.Exec(ctx, `UPDATE alert_preferences SET quiet_hours_timezone = NULL WHERE user_id = $1`, int64(userID)); err != nil {
			t.Fatalf("corrupt fixture (null timezone): %v", err)
		}
		_, err := evaluator.Evaluate(ctx, evalFixtureNow, userID, deviceID, alert)
		if !errors.Is(err, notifypreferences.ErrUnavailable) {
			t.Fatalf("expected ErrUnavailable for a configured window with no timezone, got %v", err)
		}
	})

	t.Run("invalid_timezone_string", func(t *testing.T) {
		if _, err := tx.Exec(ctx, `UPDATE alert_preferences SET quiet_hours_timezone = 'Not/AZone' WHERE user_id = $1`, int64(userID)); err != nil {
			t.Fatalf("corrupt fixture (invalid timezone): %v", err)
		}
		_, err := evaluator.Evaluate(ctx, evalFixtureNow, userID, deviceID, alert)
		if !errors.Is(err, notifypreferences.ErrUnavailable) {
			t.Fatalf("expected ErrUnavailable for an unloadable timezone, got %v", err)
		}
	})
}
