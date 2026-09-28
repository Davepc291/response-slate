package notifypreferences

import (
	"context"
	"errors"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }
func durPtr(d time.Duration) *time.Duration {
	return &d
}

// --- fakes ---

type fakeDeviceStore struct {
	reg notifydevices.Registration
	ok  bool
	err error
}

func (f *fakeDeviceStore) Get(context.Context, notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	return f.reg, f.ok, f.err
}

type fakeConsentStore struct {
	granted bool
	ok      bool
	err     error
}

func (f *fakeConsentStore) Current(context.Context, notifydevices.DeviceID) (bool, bool, error) {
	return f.granted, f.ok, f.err
}

type fakePreferencesStore struct {
	prefs notifyprefs.Preferences
	ok    bool
	err   error
}

func (f *fakePreferencesStore) Get(context.Context, identity.UserID) (notifyprefs.Preferences, bool, error) {
	return f.prefs, f.ok, f.err
}

type fakeAccountStore struct {
	user identity.User
	err  error
}

func (f *fakeAccountStore) GetByID(context.Context, identity.UserID) (identity.User, error) {
	return f.user, f.err
}

// --- fixtures ---

const testUserID identity.UserID = 1
const testDeviceID notifydevices.DeviceID = 1

func activeUser() identity.User {
	return identity.User{ID: testUserID, Status: identity.StateActive}
}

func activeDevice() notifydevices.Registration {
	return notifydevices.Registration{ID: testDeviceID, UserID: testUserID}
}

func enabledPrefs() notifyprefs.Preferences {
	return notifyprefs.Preferences{
		UserID:         testUserID,
		Enabled:        true,
		Channels:       []notifyprefs.Channel{notifyprefs.ChannelCH1A},
		ToneSetIDs:     []string{"engine-1"},
		KeywordListIDs: []string{"structure-fire"},
	}
}

func toneAlert() AlertContext {
	return AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-1")}
}

func keywordAlert() AlertContext {
	return AlertContext{Channel: notifyprefs.ChannelCH1A, KeywordListID: strPtr("structure-fire")}
}

type harness struct {
	eval     *Evaluator
	devices  *fakeDeviceStore
	consents *fakeConsentStore
	prefs    *fakePreferencesStore
	accounts *fakeAccountStore
}

func newHarness() *harness {
	h := &harness{
		devices:  &fakeDeviceStore{reg: activeDevice(), ok: true},
		consents: &fakeConsentStore{granted: true, ok: true},
		prefs:    &fakePreferencesStore{prefs: enabledPrefs(), ok: true},
		accounts: &fakeAccountStore{user: activeUser()},
	}
	h.eval = &Evaluator{Devices: h.devices, Consents: h.consents, Preferences: h.prefs, Accounts: h.accounts}
	return h
}

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// --- Reason.Valid ---

func TestReasonValid(t *testing.T) {
	valid := []Reason{
		ReasonEligible, ReasonAccountNotActive, ReasonDeviceNotActive, ReasonConsentNotGranted,
		ReasonNotificationsDisabled, ReasonChannelNotSelected, ReasonToneSetNotSelected,
		ReasonKeywordListNotSelected, ReasonBelowMinConfidence, ReasonQuietHours,
	}
	for _, r := range valid {
		if !r.Valid() {
			t.Fatalf("expected %q to be valid", r)
		}
	}
	if (Reason("bogus")).Valid() {
		t.Fatal("expected an unknown reason to be invalid")
	}
}

// --- fully eligible ---

func TestEvaluateFullyEligibleToneAlert(t *testing.T) {
	h := newHarness()
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible || d.Reason != ReasonEligible {
		t.Fatalf("expected eligible, got %+v", d)
	}
}

func TestEvaluateFullyEligibleKeywordAlert(t *testing.T) {
	h := newHarness()
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, keywordAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible || d.Reason != ReasonEligible {
		t.Fatalf("expected eligible, got %+v", d)
	}
}

// --- account ---

func TestEvaluateAccountNotFound(t *testing.T) {
	h := newHarness()
	h.accounts.err = identitystore.ErrNotFound
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonAccountNotActive {
		t.Fatalf("expected ReasonAccountNotActive, got %+v", d)
	}
}

func TestEvaluateAccountEveryNonActiveState(t *testing.T) {
	for _, s := range []identity.AccountState{
		identity.StateInvited, identity.StatePasswordChangeRequired,
		identity.StateSuspended, identity.StateDisabled, identity.StateExpired,
	} {
		t.Run(string(s), func(t *testing.T) {
			h := newHarness()
			h.accounts.user = identity.User{ID: testUserID, Status: s}
			d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Eligible || d.Reason != ReasonAccountNotActive {
				t.Fatalf("expected ReasonAccountNotActive for status %q, got %+v", s, d)
			}
		})
	}
}

func TestEvaluateAccountStoreErrorFailsClosed(t *testing.T) {
	h := newHarness()
	h.accounts.err = errors.New("connection reset")
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

// --- device ---

func TestEvaluateDeviceMissing(t *testing.T) {
	h := newHarness()
	h.devices.ok = false
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonDeviceNotActive {
		t.Fatalf("expected ReasonDeviceNotActive, got %+v", d)
	}
}

func TestEvaluateDeviceRevoked(t *testing.T) {
	h := newHarness()
	revokedAt := fixedNow.Add(-time.Hour)
	reg := activeDevice()
	reg.RevokedAt = &revokedAt
	h.devices.reg = reg
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonDeviceNotActive {
		t.Fatalf("expected ReasonDeviceNotActive, got %+v", d)
	}
}

func TestEvaluateDeviceBelongsToAnotherUser(t *testing.T) {
	h := newHarness()
	reg := activeDevice()
	reg.UserID = testUserID + 1 // a different user than requested
	h.devices.reg = reg
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonDeviceNotActive {
		t.Fatalf("expected ReasonDeviceNotActive, got %+v", d)
	}
}

func TestEvaluateDeviceStoreErrorFailsClosed(t *testing.T) {
	h := newHarness()
	h.devices.err = errors.New("connection reset")
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

// --- consent ---

func TestEvaluateNoConsent(t *testing.T) {
	h := newHarness()
	h.consents.ok = false
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonConsentNotGranted {
		t.Fatalf("expected ReasonConsentNotGranted, got %+v", d)
	}
}

func TestEvaluateConsentRevoked(t *testing.T) {
	h := newHarness()
	h.consents.granted = false
	h.consents.ok = true
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonConsentNotGranted {
		t.Fatalf("expected ReasonConsentNotGranted, got %+v", d)
	}
}

func TestEvaluateConsentStoreErrorFailsClosed(t *testing.T) {
	h := newHarness()
	h.consents.err = errors.New("connection reset")
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

// --- preferences / enabled ---

func TestEvaluateNoPreferences(t *testing.T) {
	h := newHarness()
	h.prefs.ok = false
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonNotificationsDisabled {
		t.Fatalf("expected ReasonNotificationsDisabled, got %+v", d)
	}
}

func TestEvaluateNotificationsDisabled(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.Enabled = false
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonNotificationsDisabled {
		t.Fatalf("expected ReasonNotificationsDisabled, got %+v", d)
	}
}

func TestEvaluatePreferencesStoreErrorFailsClosed(t *testing.T) {
	h := newHarness()
	h.prefs.err = errors.New("connection reset")
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

// --- channel / detector-list matching ---

func TestEvaluateEmptyChannels(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.Channels = nil
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonChannelNotSelected {
		t.Fatalf("expected ReasonChannelNotSelected, got %+v", d)
	}
}

func TestEvaluateChannelMismatch(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.Channels = []notifyprefs.Channel{notifyprefs.ChannelCH2B}
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonChannelNotSelected {
		t.Fatalf("expected ReasonChannelNotSelected, got %+v", d)
	}
}

func TestEvaluateEmptyToneSets(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.ToneSetIDs = nil
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonToneSetNotSelected {
		t.Fatalf("expected ReasonToneSetNotSelected, got %+v", d)
	}
}

func TestEvaluateEmptyKeywordLists(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.KeywordListIDs = nil
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, keywordAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonKeywordListNotSelected {
		t.Fatalf("expected ReasonKeywordListNotSelected, got %+v", d)
	}
}

func TestEvaluateToneSetMismatch(t *testing.T) {
	h := newHarness()
	alert := AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-2")}
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonToneSetNotSelected {
		t.Fatalf("expected ReasonToneSetNotSelected, got %+v", d)
	}
}

func TestEvaluateKeywordListMismatch(t *testing.T) {
	h := newHarness()
	alert := AlertContext{Channel: notifyprefs.ChannelCH1A, KeywordListID: strPtr("mvc")}
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonKeywordListNotSelected {
		t.Fatalf("expected ReasonKeywordListNotSelected, got %+v", d)
	}
}

// --- minimum confidence ---

func TestEvaluateNilMinConfidence(t *testing.T) {
	h := newHarness()
	// MinConfidence is nil by default in enabledPrefs(); alert carries no
	// confidence either. Must still be eligible.
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible {
		t.Fatalf("expected eligible with nil min confidence, got %+v", d)
	}
}

func TestEvaluateBelowThreshold(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.MinConfidence = floatPtr(0.8)
	h.prefs.prefs = p
	alert := toneAlert()
	alert.Confidence = floatPtr(0.5)
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonBelowMinConfidence {
		t.Fatalf("expected ReasonBelowMinConfidence, got %+v", d)
	}
}

func TestEvaluateExactlyAtThreshold(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.MinConfidence = floatPtr(0.8)
	h.prefs.prefs = p
	alert := toneAlert()
	alert.Confidence = floatPtr(0.8)
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible {
		t.Fatalf("expected eligible at exactly the threshold (inclusive), got %+v", d)
	}
}

func TestEvaluateMissingConfidenceWithThreshold(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.MinConfidence = floatPtr(0.8)
	h.prefs.prefs = p
	alert := toneAlert() // no Confidence set
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonBelowMinConfidence {
		t.Fatalf("expected ReasonBelowMinConfidence for missing confidence with a threshold set, got %+v", d)
	}
}

// --- quiet hours ---

func quietPrefs(start, end time.Duration, tz string) notifyprefs.Preferences {
	p := enabledPrefs()
	p.QuietHoursStart = durPtr(start)
	p.QuietHoursEnd = durPtr(end)
	p.QuietHoursTimezone = strPtr(tz)
	return p
}

func TestEvaluateSameDayQuietHours(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(9*time.Hour, 17*time.Hour, "UTC") // 9am-5pm quiet
	cases := []struct {
		name  string
		now   time.Time
		quiet bool
	}{
		{"before window", time.Date(2026, 6, 15, 8, 0, 0, 0, time.UTC), false},
		{"inside window", time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), true},
		{"after window", time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := h.eval.Evaluate(context.Background(), c.now, testUserID, testDeviceID, toneAlert())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantEligible := !c.quiet
			if d.Eligible != wantEligible {
				t.Fatalf("expected eligible=%v, got %+v", wantEligible, d)
			}
			if c.quiet && d.Reason != ReasonQuietHours {
				t.Fatalf("expected ReasonQuietHours, got %+v", d)
			}
		})
	}
}

func TestEvaluateOvernightQuietHours(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(22*time.Hour, 6*time.Hour, "UTC") // 10pm-6am quiet
	cases := []struct {
		name  string
		now   time.Time
		quiet bool
	}{
		{"late evening", time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC), true},
		{"early morning", time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC), true},
		{"midday", time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := h.eval.Evaluate(context.Background(), c.now, testUserID, testDeviceID, toneAlert())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantEligible := !c.quiet
			if d.Eligible != wantEligible {
				t.Fatalf("expected eligible=%v, got %+v", wantEligible, d)
			}
		})
	}
}

func TestEvaluateExactStartBoundaryIsQuiet(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(22*time.Hour, 6*time.Hour, "UTC")
	now := time.Date(2026, 6, 15, 22, 0, 0, 0, time.UTC) // exactly start
	d, err := h.eval.Evaluate(context.Background(), now, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonQuietHours {
		t.Fatalf("expected the start boundary to be inclusive (quiet), got %+v", d)
	}
}

func TestEvaluateExactEndBoundaryIsNotQuiet(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(22*time.Hour, 6*time.Hour, "UTC")
	now := time.Date(2026, 6, 16, 6, 0, 0, 0, time.UTC) // exactly end
	d, err := h.eval.Evaluate(context.Background(), now, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible {
		t.Fatalf("expected the end boundary to be exclusive (not quiet), got %+v", d)
	}
}

func TestEvaluateStartEqualsEndIsNeverQuiet(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(10*time.Hour, 10*time.Hour, "UTC")
	for _, now := range []time.Time{
		time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 23, 59, 59, 0, time.UTC),
	} {
		d, err := h.eval.Evaluate(context.Background(), now, testUserID, testDeviceID, toneAlert())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !d.Eligible {
			t.Fatalf("expected start==end to never be quiet, got %+v at %v", d, now)
		}
	}
}

// TestEvaluateDSTSpringForwardUsesWallClockNotElapsedDuration proves quiet
// hours are evaluated against the actual displayed local wall-clock time,
// not the real elapsed duration since local midnight -- these two
// disagree by exactly one hour on any day containing a US DST
// spring-forward transition. 2026-03-08 is the US spring-forward date in
// America/New_York (clocks jump from 02:00 to 03:00). Local midnight that
// day is 00:00 EST (UTC-5); requesting local wall-clock 03:30 lands in
// EDT (UTC-4), which is only 2h30m of REAL elapsed time after midnight,
// even though the wall clock reads 3:30. A quiet-hours window of
// [01:00, 03:00) must NOT be quiet at wall-clock 03:30 (correct, using
// Clock()) even though naively subtracting durations would compute 2:30,
// which IS inside [01:00, 03:00) (the bug this test guards against).
func TestEvaluateDSTSpringForwardUsesWallClockNotElapsedDuration(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York zone data unavailable: %v", err)
	}
	h := newHarness()
	h.prefs.prefs = quietPrefs(1*time.Hour, 3*time.Hour, "America/New_York")

	now := time.Date(2026, 3, 8, 3, 30, 0, 0, loc)
	if _, offset := now.Zone(); offset != -4*60*60 {
		t.Fatalf("test fixture assumption failed: expected EDT (UTC-4) at %v, got offset %ds", now, offset)
	}
	d, err := h.eval.Evaluate(context.Background(), now, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Eligible {
		t.Fatalf("expected NOT quiet at true local wall-clock 03:30 (window ends at 03:00), got %+v", d)
	}
}

// TestEvaluateDSTFallBackAmbiguousHourStillEvaluates proves the evaluator
// does not error or misbehave for a "now" that falls on the repeated
// wall-clock hour of a fall-back transition (2026-11-01 in
// America/New_York, 01:00-02:00 occurs twice). Evaluate only ever
// converts an unambiguous absolute instant TO local time (never the
// reverse), so this case has no real ambiguity for this evaluator at all.
func TestEvaluateDSTFallBackAmbiguousHourStillEvaluates(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York zone data unavailable: %v", err)
	}
	h := newHarness()
	h.prefs.prefs = quietPrefs(22*time.Hour, 6*time.Hour, "America/New_York")

	// The second (post-fall-back, EST) occurrence of 01:30 local time.
	// This correctly falls inside the configured 22:00-06:00 overnight
	// quiet window (01:30 < 06:00), so the point of this test is not a
	// particular eligibility outcome -- it is that evaluation completes
	// cleanly, with no error/panic, for a "now" landing on an hour that
	// occurs twice in local wall-clock terms (something only the reverse
	// local-time-to-instant direction could ever be ambiguous about, and
	// this evaluator never performs that conversion).
	now := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC).In(loc)
	d, err := h.eval.Evaluate(context.Background(), now, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("expected quiet-hours evaluation to succeed cleanly across a fall-back transition, got error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonQuietHours {
		t.Fatalf("expected wall-clock 01:30 EST to correctly fall inside the 22:00-06:00 window, got %+v", d)
	}
}

func TestEvaluateMalformedQuietHoursHalfConfiguredFailsClosed(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.QuietHoursStart = durPtr(time.Hour) // End deliberately left nil: corrupt/bypassed state
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for half-configured quiet hours, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

func TestEvaluateMalformedQuietHoursMissingTimezoneFailsClosed(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.QuietHoursStart = durPtr(1 * time.Hour)
	p.QuietHoursEnd = durPtr(2 * time.Hour)
	// QuietHoursTimezone deliberately left nil: corrupt/bypassed state.
	h.prefs.prefs = p
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for a missing time zone, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

func TestEvaluateMalformedQuietHoursInvalidTimezoneFailsClosed(t *testing.T) {
	h := newHarness()
	h.prefs.prefs = quietPrefs(1*time.Hour, 2*time.Hour, "Not/AZone")
	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for an unloadable time zone, got %v", err)
	}
	if d != (Decision{}) {
		t.Fatalf("expected a zero-value Decision on error, got %+v", d)
	}
}

// --- invalid caller input ---

func TestEvaluateInvalidInputBeforeAnyStoreAccess(t *testing.T) {
	cases := []struct {
		name   string
		userID identity.UserID
		device notifydevices.DeviceID
		now    time.Time
		alert  AlertContext
	}{
		{"zero user id", 0, testDeviceID, fixedNow, toneAlert()},
		{"negative user id", -1, testDeviceID, fixedNow, toneAlert()},
		{"zero device id", testUserID, 0, fixedNow, toneAlert()},
		{"negative device id", testUserID, -1, fixedNow, toneAlert()},
		{"zero now", testUserID, testDeviceID, time.Time{}, toneAlert()},
		{"invalid channel", testUserID, testDeviceID, fixedNow, AlertContext{Channel: "CH5D", ToneSetID: strPtr("engine-1")}},
		{"both tone and keyword set", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-1"), KeywordListID: strPtr("mvc")}},
		{"neither tone nor keyword set", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A}},
		{"malformed tone set id", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("bad id!")}},
		{"malformed keyword list id", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, KeywordListID: strPtr("bad id!")}},
		{"NaN confidence", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-1"), Confidence: floatPtr(nan())}},
		{"confidence below zero", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-1"), Confidence: floatPtr(-0.1)}},
		{"confidence above one", testUserID, testDeviceID, fixedNow, AlertContext{Channel: notifyprefs.ChannelCH1A, ToneSetID: strPtr("engine-1"), Confidence: floatPtr(1.1)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness()
			d, err := h.eval.Evaluate(context.Background(), c.now, c.userID, c.device, c.alert)
			if err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if d != (Decision{}) {
				t.Fatalf("expected a zero-value Decision, got %+v", d)
			}
		})
	}
}

func nan() float64 {
	var zero float64
	return zero / zero
}

func TestEvaluateUnconfiguredEvaluatorFailsClosed(t *testing.T) {
	cases := []*Evaluator{
		{},
		{Devices: &fakeDeviceStore{}},
		{Devices: &fakeDeviceStore{}, Consents: &fakeConsentStore{}},
		{Devices: &fakeDeviceStore{}, Consents: &fakeConsentStore{}, Preferences: &fakePreferencesStore{}},
	}
	for i, e := range cases {
		d, err := e.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
		if err != ErrUnavailable {
			t.Fatalf("case %d: expected ErrUnavailable, got %v", i, err)
		}
		if d != (Decision{}) {
			t.Fatalf("case %d: expected a zero-value Decision, got %+v", i, d)
		}
	}
}

// --- deterministic priority ---

func TestEvaluateDeterministicReasonPriority(t *testing.T) {
	// Break every single check simultaneously; the account check must win.
	h := newHarness()
	h.accounts.user = identity.User{ID: testUserID, Status: identity.StateDisabled}
	revokedAt := fixedNow.Add(-time.Hour)
	badDevice := activeDevice()
	badDevice.RevokedAt = &revokedAt
	h.devices.reg = badDevice
	h.consents.granted = false
	h.consents.ok = true
	p := enabledPrefs()
	p.Enabled = false
	p.Channels = nil
	p.ToneSetIDs = nil
	h.prefs.prefs = p

	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonAccountNotActive {
		t.Fatalf("expected account to take priority over every other failure, got %+v", d)
	}
}

func TestEvaluateDeterministicReasonPriorityDeviceBeforeConsent(t *testing.T) {
	h := newHarness()
	revokedAt := fixedNow.Add(-time.Hour)
	badDevice := activeDevice()
	badDevice.RevokedAt = &revokedAt
	h.devices.reg = badDevice
	h.consents.granted = false
	h.consents.ok = true

	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonDeviceNotActive {
		t.Fatalf("expected device to take priority over consent, got %+v", d)
	}
}

func TestEvaluateDeterministicReasonPriorityChannelBeforeConfidence(t *testing.T) {
	h := newHarness()
	p := enabledPrefs()
	p.Channels = nil
	p.MinConfidence = floatPtr(0.9)
	h.prefs.prefs = p
	alert := toneAlert()
	alert.Confidence = floatPtr(0.1)

	d, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Eligible || d.Reason != ReasonChannelNotSelected {
		t.Fatalf("expected channel to take priority over confidence, got %+v", d)
	}
}

// --- freshness / no caching ---

func TestEvaluateRepeatedCallsReadFreshState(t *testing.T) {
	h := newHarness()
	first, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.Eligible {
		t.Fatalf("expected the first evaluation to be eligible, got %+v", first)
	}

	// Mutate every underlying fake's state directly (simulating consent
	// being revoked, the device being revoked, preferences changing, and
	// the account being disabled between two calls) without going through
	// the Evaluator at all.
	h.consents.granted = false

	second, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.Eligible {
		t.Fatal("expected the second evaluation to reflect the freshly revoked consent, not a cached eligible result")
	}
	if second.Reason != ReasonConsentNotGranted {
		t.Fatalf("expected ReasonConsentNotGranted, got %+v", second)
	}

	// Revert, and confirm a third call reflects that too.
	h.consents.granted = true
	third, err := h.eval.Evaluate(context.Background(), fixedNow, testUserID, testDeviceID, toneAlert())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !third.Eligible {
		t.Fatalf("expected the third evaluation to reflect the freshly restored consent, got %+v", third)
	}
}
