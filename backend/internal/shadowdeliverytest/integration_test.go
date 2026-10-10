package shadowdeliverytest

// Optional real-database verification, mirroring the existing
// notifyoutcome/notifyworker/alertnotify convention exactly: skipped unless
// explicitly opted into a local rollback-only database. Everything here runs
// inside one outer transaction that is always rolled back; no fixture is
// left behind, and no network I/O of any kind ever occurs (notifyrelay.
// FakeSender performs none, and notifyworker.Config.Env is required to be
// notifywebpush.EnvDev).
//
// What this proves: the real chain --
//   synthetic tone PCM -> tonedetection.Registry.Detect ->
//   alertpipeline.Service.ProcessTone -> alertstore.Postgres.RecordDetection
//   (real record_alert_detection(), a real persisted alert_events row) ->
//   an independently-reconstructed alerts.Event (via alerts.Builder.New,
//   using the SAME Input fields alertpipeline itself would have used) ->
//   alertnotify.Service.EnqueueMatchedEvent -> real notifyoutboxstore.Enqueue
//   (real notification_outbox rows) -> a manually constructed, DEV-only
//   notifyworker.Worker -> real claim/evaluate/send/outcome cycle ->
//   notifyrelay.FakeSender -> real notifyoutcome.Store.RecordSent (a real
//   notification_deliveries row) --
// all against the real schema, all inside one shared pgx.Tx.
//
// What this explicitly does NOT prove: real eligibility evaluation. The
// Worker here is given a fixed, always-eligible fake notifyattempt.Evaluator
// (fakeEvaluator below), exactly mirroring every existing notifyworker test.
// notifypreferences.Evaluator -- the real implementation, which reads
// identitystore/notifydevicestore/notifyconsentstore/notifyprefsstore fresh
// on every call -- is never constructed or exercised here. Consent,
// preferences, and quiet-hours therefore remain unproven end-to-end after
// this part; see backend/internal/notifypreferences's own unit tests for
// that package's independent correctness proof.
//
// A note on claim scoping (Step 8D-C Part 2 design-lock Correction 1):
// notifyworker.Worker's public API (Run/RunOnce) offers no way to scope a
// claim to a specific event_id or outbox id -- RunOnce always calls
// Outbox.ClaimNextDue, which claims whatever pending row is "next due"
// system-wide, with no event/id filter parameter. (notifyoutboxstore.
// ClaimSpecific does exist and claims a known row directly, but Worker never
// calls it, and this package may not modify production code to add a path
// that does.) True, interface-level, guaranteed isolation from any other due
// row in this shared database is therefore NOT achievable through the
// public Worker API alone, and this test does not claim otherwise. Instead,
// before ever calling RunOnce, it VERIFIES -- via a direct count query
// against the real currently-due pending pool, visible within this test's
// own uncommitted transaction -- that every currently-due pending row
// belongs to this fixture's own fan-out (len(results) rows, all sharing this
// fixture's one fresh event_id). If that count check fails, the test fails
// loudly with an explanatory message rather than proceeding under an
// unverified assumption. The subsequent drain loop is bounded by that same,
// actual fan-out count (never a fixed constant), and a final assertion
// requires this fixture's own specific entry to have reached StateSent
// within that bound -- so a violation of the pre-loop invariant (an
// unrelated due row consuming one of the bounded iterations) still fails
// the test clearly, rather than silently passing or looping unboundedly.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand"
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
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifydevicestore"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
	"greenwich-fire-responder/backend/internal/notifyoutcome"
	"greenwich-fire-responder/backend/internal/notifypreferences"
	"greenwich-fire-responder/backend/internal/notifyprefs"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
	"greenwich-fire-responder/backend/internal/notifyworker"
	"greenwich-fire-responder/backend/internal/tonedetection"
)

// --- small, locally-duplicated helpers, mirroring existing precedent ---

func hex64(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

// tone/silence/noise/mix/concat/pcm duplicate tonedetection_test.go's own
// unexported identically-named helpers (not imported: they are test-internal
// to that package).
func tone(freqHz float64, durationMS int64, amplitude float64) []int16 {
	const sampleRateHz = 16000
	n := sampleRateHz * int(durationMS) / 1000
	out := make([]int16, n)
	for i := range out {
		v := amplitude * math.Sin(2*math.Pi*freqHz*float64(i)/sampleRateHz)
		out[i] = int16(v * 32767)
	}
	return out
}

// noise appends durationMS of deterministic (seeded) white noise, using
// math/rand with a fixed seed -- exactly matching tonedetection_test.go's
// own identical helper and the calibration experiment this file's
// non-matched scenario depends on for reproducibility.
func noise(durationMS int64, amplitude float64, seed int64) []int16 {
	const sampleRateHz = 16000
	n := sampleRateHz * int(durationMS) / 1000
	out := make([]int16, n)
	r := rand.New(rand.NewSource(seed))
	for i := range out {
		out[i] = int16(amplitude * (r.Float64()*2 - 1) * 32767)
	}
	return out
}

func mix(a, b []int16) []int16 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		v := float64(a[i]) + float64(b[i])
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

func concat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Shadow E2E Test', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	return id
}

func insertFixtureTestModeDevice(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, endpoint string) int64 {
	t.Helper()
	raw := make([]byte, 65)
	raw[0] = 0x04
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, test_mode, created_at, updated_at)
        VALUES ($1, $2, $3, $4, NULL, true, now(), now()) RETURNING id`,
		userID, endpoint, raw, make([]byte, 16)).Scan(&id); err != nil {
		t.Fatalf("fixture test_mode device insert failed: %v", err)
	}
	return id
}

func insertFixtureRadioTransmission(t *testing.T, ctx context.Context, tx pgx.Tx, audioFingerprint string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `INSERT INTO radio_transmissions (audio_fingerprint, source_filename) VALUES ($1, 'fixture.wav')`, audioFingerprint); err != nil {
		t.Fatalf("fixture radio_transmissions insert failed: %v", err)
	}
}

// fakeEvaluator duplicates notifyworker_test.go's own identical unexported
// type (test-internal to that package, so not importable here): a fixed,
// always-eligible notifyattempt.Evaluator. See this file's own top doc
// comment for exactly what this does, and does not, prove.
type fakeEvaluator struct{ decision notifypreferences.Decision }

func (f *fakeEvaluator) Evaluate(context.Context, time.Time, identity.UserID, notifydevices.DeviceID, notifypreferences.AlertContext) (notifypreferences.Decision, error) {
	return f.decision, nil
}

// toneReasonHasConfidence duplicates alertpipeline's own identical
// unexported function exactly (not importable): the detector leaves
// Confidence at its zero value for the two reasons that abort before any
// window is evaluated, so this must not forward a fabricated 0.0 for those.
func toneReasonHasConfidence(reason tonedetection.Reason) bool {
	switch reason {
	case tonedetection.ReasonMatched, tonedetection.ReasonLowConfidence, tonedetection.ReasonTooShort:
		return true
	default:
		return false
	}
}

// TestLiveShadowAlertDeliveryEndToEndRollsBack is the primary live proof
// (Step 8D-C Part 2). See this file's own top doc comment for full scope and
// the claim-scoping note (Correction 1).
func TestLiveShadowAlertDeliveryEndToEndRollsBack(t *testing.T) {
	if os.Getenv("GFR_SHADOW_DELIVERY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_SHADOW_DELIVERY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outboxDB, closeOutbox, err := notifyoutboxstore.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local database unavailable")
	}
	defer closeOutbox()
	pool := outboxDB.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tx.Rollback(cleanup)
		}
	}()

	// --- Fixtures: one user, one test_mode=true device, one minimal
	// radio_transmissions row for Evidence's own format requirement. ---
	userID := insertFixtureUser(t, ctx, tx, "shadowdeliverytest-live@example.com")
	deviceID := insertFixtureTestModeDevice(t, ctx, tx, userID, "https://push.example.invalid/shadowe2e/one")

	fingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64("shadowdeliverytest-primary-audio")
	insertFixtureRadioTransmission(t, ctx, tx, fingerprint)
	evidence := alerts.Evidence{AudioFingerprint: fingerprint}

	// --- Synthetic tone input: a clean two-tone "quick call" match, no
	// noise, comfortably above both PresenceThreshold and MinConfidence. ---
	toneSet := tonedetection.ToneSet{
		ID:      "shadow-e2e-quick-call",
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

	// Shared variables used BOTH to build the by-hand alerts.Input below AND
	// as the real ToneRequest fields passed to ProcessTone -- identical
	// values by construction, not merely by assertion.
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := base.Add(2 * time.Hour)
	channel := alerts.ChannelCH1A
	const tgid = 57201
	sourceKind := alerts.SourceSynthetic
	labels := map[string]string{"shadow-e2e-quick-call": "Shadow E2E test alert"}

	// Run the detector ourselves once: Detect is pure and deterministic, so
	// ProcessTone's own internal call (with the identical Registry+PCM)
	// below is guaranteed to reproduce this exact Detection.
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

	// Independently reconstruct the exact alerts.Event alertpipeline.
	// ProcessTone will itself construct internally from this same Input --
	// duplicating alertpipeline.process's own exact field mapping
	// (alertpipeline.go:226-240).
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

	// Now run the REAL pipeline against the REAL alertstore, over the SAME
	// shared tx.
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

	// --- Persistence-before-fan-out proof: read the real row back and
	// compare event_id/state/expires_at/display_summary against the
	// independently reconstructed Event, BEFORE calling alertnotify at all. ---
	var gotEventID, gotState, gotDisplaySummary string
	var gotExpiresAt time.Time
	if err := tx.QueryRow(ctx, `SELECT event_id, state, expires_at, display_summary FROM alert_events WHERE event_id = $1`, ev.EventID).
		Scan(&gotEventID, &gotState, &gotExpiresAt, &gotDisplaySummary); err != nil {
		t.Fatalf("persisted alert_events row not found for event_id %s before fan-out: %v", ev.EventID, err)
	}
	if gotEventID != ev.EventID || gotState != string(alerts.StateMatched) ||
		!gotExpiresAt.Equal(ev.ExpiresAt) || gotDisplaySummary != ev.DisplaySummary {
		t.Fatalf("persisted alert_events row does not match reconstructed Event before fan-out: got (event_id=%s state=%s expires_at=%v display_summary=%q), want (event_id=%s state=matched expires_at=%v display_summary=%q)",
			gotEventID, gotState, gotExpiresAt, gotDisplaySummary, ev.EventID, ev.ExpiresAt, ev.DisplaySummary)
	}

	// --- alertnotify fan-out ---
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
	ourEntryID := ourResult.Entry.ID

	// Capture ground truth for the fixture device directly from the same
	// store the Worker itself will use, so the later FakeSender.Calls()
	// comparison is against the real, store-decoded values, not a
	// hand-encoded guess.
	wantDevice, ok, err := deviceStore.Get(ctx, notifydevices.DeviceID(deviceID))
	if err != nil || !ok {
		t.Fatalf("expected to read back our own fixture device, got ok=%v err=%v", ok, err)
	}

	// --- Claim-scoping safety check (Correction 1): verify, before ever
	// calling RunOnce, that every currently-due pending row visible within
	// this transaction belongs to this fixture's own fan-out. If this count
	// does not match len(results) exactly, STOP here with a clear message:
	// the public Worker API cannot safely proceed past unverified,
	// unrelated due data. ---
	var dueCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE state = 'pending' AND next_attempt_at <= $1`, enqueueNow).Scan(&dueCount); err != nil {
		t.Fatal(err)
	}
	if dueCount != int64(len(results)) {
		t.Fatalf("claim-scoping safety check failed: expected exactly %d due pending outbox rows (this fixture's own fan-out), found %d -- "+
			"the shared database contains unrelated due entries outside this fixture's own event_id, and notifyworker.Worker's public "+
			"RunOnce/ClaimNextDue interface offers no way to scope a claim to a specific event or outbox id, so this test cannot safely "+
			"proceed without risking processing an unrelated entry", len(results), dueCount)
	}

	// --- Manually constructed DEV notifyworker.Worker ---
	sender := notifyrelay.NewFakeSender()
	evaluator := &fakeEvaluator{decision: notifypreferences.Decision{Eligible: true, Reason: notifypreferences.ReasonEligible}}
	outcomeStore := &notifyoutcome.Store{DB: tx}

	// Correction 2: the local AlertLookup closure must reject any eventID
	// other than this fixture's own persisted event, never silently
	// returning fixture AlertData for an unknown id.
	toneSetID := ev.ToneSetID
	alertLookup := func(_ context.Context, eventID string) (notifyworker.AlertData, error) {
		if eventID != ev.EventID {
			return notifyworker.AlertData{}, fmt.Errorf("shadowdeliverytest: AlertLookup called with unknown event id %q, want %q", eventID, ev.EventID)
		}
		return notifyworker.AlertData{
			Context: notifypreferences.AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: &toneSetID, Confidence: ev.Confidence},
			Payload: notifyrelay.Payload{Title: ev.DisplaySummary},
		}, nil
	}

	workerNow := enqueueNow.Add(90 * time.Second)
	w, err := notifyworker.New(notifyworker.Deps{
		Outbox: outboxStore, Evaluator: evaluator, Devices: deviceStore,
		Sender: sender, Outcomes: outcomeStore, Alerts: alertLookup,
		Now: func() time.Time { return workerNow },
	}, notifyworker.Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("notifyworker.New: %v", err)
	}

	// --- Drain loop, bounded by the ACTUAL fan-out count, never a fixed
	// constant. Each RunOnce call claims-and-resolves exactly one pending
	// row (to a terminal state here, since the fake evaluator is always
	// eligible and FakeSender defaults to accepted), strictly shrinking the
	// due pool, so our own fixture entry is guaranteed to be reached within
	// len(results) calls -- PROVIDED the pre-loop safety check above held. ---
	for i := 0; i < len(results); i++ {
		entry, ok, err := outboxStore.Get(ctx, ourEntryID)
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
			t.Fatalf("RunOnce reported no due entry remaining, but fixture entry %d has not reached StateSent", ourEntryID)
		}
	}

	finalEntry, ok, err := outboxStore.Get(ctx, ourEntryID)
	if err != nil || !ok || finalEntry.State != notifyoutbox.StateSent {
		t.Fatalf("expected fixture entry %d to reach StateSent within %d RunOnce calls, got ok=%v err=%v entry=%+v", ourEntryID, len(results), ok, err, finalEntry)
	}

	// --- Exactly one delivery-audit row for our own entry ---
	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'sent'`, int64(ourEntryID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf("expected exactly one real 'sent' delivery-audit row for entry %d, got %d", ourEntryID, deliveryCount)
	}

	// --- FakeSender recorded exactly our own fixture's subscription and
	// summary (membership, not an exact total count: other fanned-out rows
	// for pre-existing system-wide test_mode devices are also drained and
	// also call Send) ---
	var found *notifyrelay.Request
	for _, c := range sender.Calls() {
		if c.OutboxID == notifyrelay.OutboxID(ourEntryID) {
			cc := c
			found = &cc
		}
	}
	if found == nil {
		t.Fatalf("expected FakeSender to have recorded a call for entry %d", ourEntryID)
	}
	if found.Endpoint != wantDevice.Subscription.Endpoint || found.P256dh != wantDevice.Subscription.Keys.P256dh || found.Auth != wantDevice.Subscription.Keys.Auth {
		t.Fatalf("FakeSender call subscription does not match our fixture device: got %+v, want endpoint=%q p256dh=%q auth=%q",
			found, wantDevice.Subscription.Endpoint, wantDevice.Subscription.Keys.P256dh, wantDevice.Subscription.Keys.Auth)
	}
	if !found.TestMode {
		t.Fatal("expected the recorded request to carry TestMode=true")
	}
	if found.Payload.Title != ev.DisplaySummary {
		t.Fatalf("FakeSender call payload title = %q, want %q", found.Payload.Title, ev.DisplaySummary)
	}

	// --- Idempotency: a repeated EnqueueMatchedEvent must never create a
	// duplicate outbox row ---
	results2, err := notifySvc.EnqueueMatchedEvent(ctx, enqueueNow, ev)
	if err != nil {
		t.Fatalf("second EnqueueMatchedEvent: %v", err)
	}
	var ourResult2 *alertnotify.DeviceResult
	for i := range results2 {
		if results2[i].DeviceID == notifydevices.DeviceID(deviceID) {
			ourResult2 = &results2[i]
		}
	}
	if ourResult2 == nil {
		t.Fatal("expected a result for our own fixture device on the second EnqueueMatchedEvent call")
	}
	if ourResult2.Err != nil {
		t.Fatalf("unexpected error on second EnqueueMatchedEvent: %v", ourResult2.Err)
	}
	if ourResult2.Entry.ID != ourEntryID {
		t.Fatalf("expected idempotent re-enqueue to return the SAME entry id %d, got %d", ourEntryID, ourResult2.Entry.ID)
	}
	var outboxCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1 AND device_id = $2`, ev.EventID, deviceID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("expected exactly one outbox row after a repeated EnqueueMatchedEvent call, got %d", outboxCount)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveShadowAlertDeliveryNonMatchedEventNeverEnqueuesRollsBack is the
// optional non-matched scenario (Step 8D-C Part 2 design-lock §8). It uses a
// deliberately noisy two-tone signal, empirically calibrated (via a disposed
// scratch test against the real tonedetection package, never committed) to
// deterministically resolve to StateLowConfidence with a confidence
// comfortably inside (PresenceThreshold, MinConfidence) -- not a boundary
// value -- so this is a genuinely deterministic, not flaky, non-matched
// input. It proves the Part 1 ev.State == alerts.StateMatched gate correctly
// rejects a REAL pipeline-produced non-matched event (closing the gap left
// by Part 1's own unit test, which only exercised a hand-built Event), and
// re-confirms the EventCreated-is-not-matched-exclusive trap from Part 1's
// discovery using a real persisted row rather than a constructed one.
func TestLiveShadowAlertDeliveryNonMatchedEventNeverEnqueuesRollsBack(t *testing.T) {
	if os.Getenv("GFR_SHADOW_DELIVERY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_SHADOW_DELIVERY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outboxDB, closeOutbox, err := notifyoutboxstore.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local database unavailable")
	}
	defer closeOutbox()
	pool := outboxDB.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tx.Rollback(cleanup)
		}
	}()

	fingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64("shadowdeliverytest-nonmatched-audio")
	insertFixtureRadioTransmission(t, ctx, tx, fingerprint)
	evidence := alerts.Evidence{AudioFingerprint: fingerprint}

	toneSet := tonedetection.ToneSet{
		ID:      "shadow-e2e-quick-call",
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
	// Empirically calibrated: tone amplitude 0.4 mixed with noise amplitude
	// 0.7 (seeds 1 and 2) deterministically yields confidence ~0.491,
	// comfortably inside (0.15, 0.6) -- neither "absent" nor "matched".
	pcm := tonedetection.PCM{SampleRate: 16000, Samples: concat(
		mix(tone(800, 1000, 0.4), noise(1000, 0.7, 1)),
		mix(tone(1200, 1000, 0.4), noise(1000, 0.7, 2)),
	)}

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := base.Add(2 * time.Hour)
	channel := alerts.ChannelCH1A
	const tgid = 57201
	sourceKind := alerts.SourceSynthetic
	labels := map[string]string{"shadow-e2e-quick-call": "Shadow E2E non-matched test alert"}

	detections, err := registry.Detect(channel, pcm)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detections) != 1 {
		t.Fatalf("expected exactly one detection, got %d", len(detections))
	}
	det := detections[0]
	if det.State != alerts.StateLowConfidence || det.Reason != tonedetection.ReasonLowConfidence {
		t.Fatalf("calibration drifted: expected a deterministic low_confidence detection, got %+v (this scenario must not be weakened to force a pass -- if this fails, the calibrated PCM no longer produces the intended non-matched state and must be recalibrated, not asserted around)", det)
	}
	if det.Confidence <= 0.15 || det.Confidence >= 0.6 {
		t.Fatalf("calibration drifted: expected confidence strictly inside (0.15, 0.6), got %v", det.Confidence)
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
	// The critical Part 1 trap, re-proven against a real persisted row:
	// EventCreated is true for a non-matched (but non-rejected) state too.
	if len(outcomes) != 1 || outcomes[0].Rejected || outcomes[0].State != alerts.StateLowConfidence ||
		!outcomes[0].Result.EventCreated || outcomes[0].Result.Suppressed {
		t.Fatalf("expected one newly created low_confidence outcome (EventCreated=true despite not matched), got %+v", outcomes)
	}

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	deviceStore := &notifydevicestore.Postgres{DB: tx}
	notifySvc, err := alertnotify.NewService(outboxStore, deviceStore, alertnotify.Config{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("alertnotify.NewService: %v", err)
	}

	results, err := notifySvc.EnqueueMatchedEvent(ctx, base.Add(time.Minute), ev)
	if !errors.Is(err, alertnotify.ErrNotMatched) {
		t.Fatalf("expected ErrNotMatched for a real, persisted non-matched event, got results=%v err=%v", results, err)
	}
	if results != nil {
		t.Fatalf("expected nil results on ErrNotMatched, got %v", results)
	}

	var outboxCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1`, ev.EventID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected zero notification_outbox rows for a non-matched event, got %d", outboxCount)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}
