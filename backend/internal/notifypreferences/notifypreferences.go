// Package notifypreferences is the Step 8D-B Part 8 delivery-eligibility
// evaluator (docs/notification-relay-amendment-v1.md Section 25.1: "Server-
// side preference and quiet-hours evaluation against a user's current,
// non-cached settings"). It answers exactly one question -- "is this
// (user, device, alert) combination currently eligible for a delivery
// attempt right now?" -- by freshly reading account, device, consent, and
// preference state through the existing identitystore/notifydevicestore/
// notifyconsentstore/notifyprefsstore packages, every single call, never
// cached between evaluations (Section 9: "Every individual delivery
// attempt -- not only registration -- re-checks... the alert event's
// channel/tone-set/keyword-list matches the user's current, not cached,
// preferences").
//
// This package is deliberately named notifypreferences, matching the
// amendment's own Section 25.1 reservation exactly -- Part 5's
// notifyprefs package intentionally avoided this name so it would stay
// free for this evaluation-layer component.
//
// This package has no persistence of its own (no migration, no new
// table), no HTTP handler, no route, no provider SDK, and no network
// dependency of any kind. Critically, it never acts on its own decision:
// Evaluate returns a Decision, and does nothing else. It never claims,
// cancels, or otherwise mutates a notification_outbox row, never writes a
// notification_deliveries row, and never sends anything -- all of that
// remains the future, separately authorized attempt-orchestration
// component's job, which is expected to call Evaluate and then act on the
// result itself.
//
// Two of Section 29's still-open questions are deliberately NOT resolved
// here: whether quiet-hours behavior suppresses or queues a matching
// alert (item 10), and what caller action any given ineligibility Reason
// should imply (cancel the outbox entry, leave it pending for retry,
// write an unauthorized delivery-audit row, or something else). Evaluate
// reports only the fact; deciding what to do about it is left entirely to
// the caller.
package notifypreferences

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

// Sentinel errors. An error return from Evaluate means eligibility could
// not safely be determined at all -- never "not eligible," and never
// "eligible." Only two categories exist: ErrInput for a caller-supplied
// shape violation caught before any store is touched, and ErrUnavailable
// for everything else that prevents a safe determination (an underlying
// store's own failure, or persisted state too malformed to evaluate
// safely -- see currentlyQuietHours' own doc comment). Store/database/
// corrupt-data failures must never be silently converted into
// Decision{Eligible: true}.
var (
	ErrInput       = errors.New("notifypreferences: invalid input")
	ErrUnavailable = errors.New("notifypreferences: evaluation unavailable")
)

// Reason is exactly one of the ten values below. There is no free-form
// reason string: every Decision this package returns carries one of
// these, and Evaluate itself is the only place that constructs a Decision
// at all.
type Reason string

const (
	ReasonEligible               Reason = "eligible"
	ReasonAccountNotActive       Reason = "account_not_active"
	ReasonDeviceNotActive        Reason = "device_not_active"
	ReasonConsentNotGranted      Reason = "consent_not_granted"
	ReasonNotificationsDisabled  Reason = "notifications_disabled"
	ReasonChannelNotSelected     Reason = "channel_not_selected"
	ReasonToneSetNotSelected     Reason = "tone_set_not_selected"
	ReasonKeywordListNotSelected Reason = "keyword_list_not_selected"
	ReasonBelowMinConfidence     Reason = "below_min_confidence"
	ReasonQuietHours             Reason = "quiet_hours"
)

// Valid reports whether r is one of the ten approved values.
func (r Reason) Valid() bool {
	switch r {
	case ReasonEligible, ReasonAccountNotActive, ReasonDeviceNotActive, ReasonConsentNotGranted,
		ReasonNotificationsDisabled, ReasonChannelNotSelected, ReasonToneSetNotSelected,
		ReasonKeywordListNotSelected, ReasonBelowMinConfidence, ReasonQuietHours:
		return true
	}
	return false
}

// Decision is a pure fact, never an action. A future caller decides what
// to do about a non-eligible Decision (cancel the outbox entry, leave it
// pending for retry, audit it as unauthorized, or something else) -- this
// package does not, and structurally cannot, do any of those things
// itself.
type Decision struct {
	Eligible bool
	Reason   Reason
}

// AlertContext carries the small set of immutable alert_events fields
// (migration 000007_alert_persistence.sql) this evaluator needs, supplied
// directly by the caller rather than read from a store: unlike account/
// device/consent/preference state, these facts are fixed forever once the
// alert_events row exists (that table is hard immutable), so accepting
// them as plain input is not "caching" in the sense this package's own
// live checks must avoid -- there is nothing here that could go stale.
// This also means this package never needs an alertstore read method
// (none exists today) and never imports internal/alerts or
// internal/alertstore.
type AlertContext struct {
	Channel notifyprefs.Channel
	// Exactly one of ToneSetID/KeywordListID must be set, mirroring
	// alert_events' own alert_events_detector_config CHECK constraint
	// exactly (a real alert_events row can never have both or neither).
	ToneSetID     *string
	KeywordListID *string
	// Confidence is the detector's own scoring for this alert, nil when
	// the detector defines no numeric confidence.
	Confidence *float64
}

// idPattern duplicates notifyprefs' own established tone-set/keyword-list
// id shape (itself duplicated from internal/alerts.idPattern), for the
// same reason every notify-family package duplicates this small rule
// locally rather than importing internal/alerts: this package must
// validate before ever reaching a store, and must never import
// internal/alerts to do so.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// DeviceStore is the minimal notifydevicestore surface this evaluator
// needs.
type DeviceStore interface {
	Get(ctx context.Context, id notifydevices.DeviceID) (notifydevices.Registration, bool, error)
}

// ConsentStore is the minimal notifyconsentstore surface this evaluator
// needs.
type ConsentStore interface {
	Current(ctx context.Context, deviceID notifydevices.DeviceID) (bool, bool, error)
}

// PreferencesStore is the minimal notifyprefsstore surface this evaluator
// needs.
type PreferencesStore interface {
	Get(ctx context.Context, userID identity.UserID) (notifyprefs.Preferences, bool, error)
}

// AccountStore is the minimal identitystore surface this evaluator needs.
// Unlike DeviceStore/ConsentStore/PreferencesStore, a "not found" result
// here is signaled by a real error (identitystore.ErrNotFound), not an ok
// bool -- Evaluate itself accounts for that difference (see its own doc
// comment).
type AccountStore interface {
	GetByID(ctx context.Context, id identity.UserID) (identity.User, error)
}

// Evaluator holds the four store dependencies Evaluate reads fresh on
// every call. These are declared as narrow, locally-defined interfaces
// (rather than importing the concrete *notifydevicestore.Postgres, etc.
// types) so this package can be unit-tested with simple fakes and never
// needs a live database of its own -- it has none.
type Evaluator struct {
	Devices     DeviceStore
	Consents    ConsentStore
	Preferences PreferencesStore
	Accounts    AccountStore
}

func validateInput(userID identity.UserID, deviceID notifydevices.DeviceID, alert AlertContext) error {
	if userID <= 0 || deviceID <= 0 {
		return ErrInput
	}
	if !alert.Channel.Valid() {
		return ErrInput
	}
	hasTone := alert.ToneSetID != nil
	hasKeyword := alert.KeywordListID != nil
	if hasTone == hasKeyword {
		// Exactly one must be present: both set or neither set is a
		// caller bug, since a real alert_events row can never have
		// either shape (see AlertContext's own doc comment).
		return ErrInput
	}
	if hasTone && !idPattern.MatchString(*alert.ToneSetID) {
		return ErrInput
	}
	if hasKeyword && !idPattern.MatchString(*alert.KeywordListID) {
		return ErrInput
	}
	if alert.Confidence != nil {
		c := *alert.Confidence
		if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
			return ErrInput
		}
	}
	return nil
}

func containsChannel(list []notifyprefs.Channel, c notifyprefs.Channel) bool {
	for _, v := range list {
		if v == c {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// currentlyQuietHours reports whether now falls inside prefs' configured
// quiet-hours window, evaluated in prefs' own configured IANA time zone
// (never the machine-local zone). A nil error with a false result means
// either no quiet-hours window is configured, or now falls outside it; a
// non-nil error means the persisted quiet-hours state is malformed enough
// (half-configured start/end, a missing timezone, or a timezone that no
// longer loads) that evaluation cannot safely proceed -- this must fail
// evaluation, never silently permit delivery by assuming "not quiet."
func currentlyQuietHours(now time.Time, prefs notifyprefs.Preferences) (bool, error) {
	hasStart := prefs.QuietHoursStart != nil
	hasEnd := prefs.QuietHoursEnd != nil
	if hasStart != hasEnd {
		return false, errors.New("notifypreferences: quiet hours start/end are not both configured")
	}
	if !hasStart {
		return false, nil
	}
	if prefs.QuietHoursTimezone == nil {
		return false, errors.New("notifypreferences: quiet hours configured without a time zone")
	}
	loc, err := time.LoadLocation(*prefs.QuietHoursTimezone)
	if err != nil {
		return false, err
	}

	start := *prefs.QuietHoursStart
	end := *prefs.QuietHoursEnd
	if start == end {
		// An empty interval: never quiet, regardless of now.
		return false, nil
	}

	local := now.In(loc)
	// Wall-clock time of day, deliberately computed from Clock()/
	// Nanosecond() rather than local.Sub(midnight): the latter would
	// measure real elapsed duration, which is distorted by exactly one
	// hour on any calendar day containing a DST transition (a
	// spring-forward day is 23 real hours long; a fall-back day is 25),
	// silently producing the wrong wall-clock offset for any "now" after
	// the transition on that day. Clock()/Nanosecond() always reports
	// the actual displayed wall-clock time, which is what a user's
	// configured quiet-hours window actually means.
	h, m, s := local.Clock()
	nowOfDay := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(s)*time.Second + time.Duration(local.Nanosecond())

	if start < end {
		return nowOfDay >= start && nowOfDay < end, nil
	}
	// Overnight window (start > end): quiet from start through midnight,
	// and from midnight through end.
	return nowOfDay >= start || nowOfDay < end, nil
}

// Evaluate answers "is (userID, deviceID) currently eligible for a
// delivery attempt of alert, right now?" by freshly reading account,
// device, consent, and preference state -- every call, never cached
// between evaluations, exactly as Section 9 requires. It performs no
// write of any kind: no outbox claim, no outbox mutation, no
// notification_deliveries write, no send.
//
// (Decision{Eligible: false, Reason: ...}, nil) means evaluation
// successfully determined the target is currently ineligible, and why.
// (Decision{}, error) means eligibility could not safely be determined at
// all -- the caller must not treat this as either eligible or ineligible.
//
// Checks run in a fixed priority order and this method returns on the
// first one that fails: account -> device -> consent -> notifications
// enabled -> channel -> detector-specific tone/keyword selection ->
// minimum confidence -> quiet hours -> eligible.
func (e *Evaluator) Evaluate(ctx context.Context, now time.Time, userID identity.UserID, deviceID notifydevices.DeviceID, alert AlertContext) (Decision, error) {
	if e == nil || e.Devices == nil || e.Consents == nil || e.Preferences == nil || e.Accounts == nil {
		return Decision{}, ErrUnavailable
	}
	if now.IsZero() {
		return Decision{}, ErrInput
	}
	if err := validateInput(userID, deviceID, alert); err != nil {
		return Decision{}, ErrInput
	}

	// 1. Account: missing, or not currently able to authenticate, is
	// ineligible. identitystore.GetByID signals "not found" as a real
	// error (identitystore.ErrNotFound), unlike the notify-family stores'
	// own (value, ok, error) shape, so that specific error is folded into
	// a Decision here rather than propagated as an evaluation failure.
	user, err := e.Accounts.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, identitystore.ErrNotFound) {
			return Decision{Eligible: false, Reason: ReasonAccountNotActive}, nil
		}
		return Decision{}, ErrUnavailable
	}
	if !user.CanAuthenticate() {
		return Decision{Eligible: false, Reason: ReasonAccountNotActive}, nil
	}

	// 2. Device: missing, revoked, or belonging to a different user than
	// requested, is ineligible. A device registered to a different user
	// than the one this evaluation is for must never be treated as
	// active for THIS user, regardless of its own Active() state.
	device, ok, err := e.Devices.Get(ctx, deviceID)
	if err != nil {
		return Decision{}, ErrUnavailable
	}
	if !ok || !device.Active() || device.UserID != userID {
		return Decision{Eligible: false, Reason: ReasonDeviceNotActive}, nil
	}

	// 3. Consent: no history at all, or the latest recorded event is a
	// revocation, is ineligible.
	granted, ok, err := e.Consents.Current(ctx, deviceID)
	if err != nil {
		return Decision{}, ErrUnavailable
	}
	if !ok || !granted {
		return Decision{Eligible: false, Reason: ReasonConsentNotGranted}, nil
	}

	// 4. Preferences / global enable switch: no preferences row at all,
	// or an explicit disable, is ineligible.
	prefs, ok, err := e.Preferences.Get(ctx, userID)
	if err != nil {
		return Decision{}, ErrUnavailable
	}
	if !ok || !prefs.Enabled {
		return Decision{Eligible: false, Reason: ReasonNotificationsDisabled}, nil
	}

	// 5. Channel: an empty Channels list selects nothing, never "all
	// channels" (notifyprefs.Preferences' own documented semantics).
	if !containsChannel(prefs.Channels, alert.Channel) {
		return Decision{Eligible: false, Reason: ReasonChannelNotSelected}, nil
	}

	// 6. Detector-specific tone-set/keyword-list selection: an empty list
	// selects nothing, identically to Channels above.
	if alert.ToneSetID != nil {
		if !containsString(prefs.ToneSetIDs, *alert.ToneSetID) {
			return Decision{Eligible: false, Reason: ReasonToneSetNotSelected}, nil
		}
	} else {
		if !containsString(prefs.KeywordListIDs, *alert.KeywordListID) {
			return Decision{Eligible: false, Reason: ReasonKeywordListNotSelected}, nil
		}
	}

	// 7. Minimum confidence: nil means no threshold configured. A
	// configured threshold with no alert confidence to compare against
	// fails closed identically to a confidence that is merely too low.
	if prefs.MinConfidence != nil {
		threshold := *prefs.MinConfidence
		if alert.Confidence == nil || *alert.Confidence < threshold {
			return Decision{Eligible: false, Reason: ReasonBelowMinConfidence}, nil
		}
	}

	// 8. Quiet hours: reports only the fact. This package does not decide
	// suppress-vs-queue or any other consequence (Section 29 item 10
	// remains unresolved) -- see currentlyQuietHours' own doc comment for
	// why a malformed persisted window fails evaluation rather than
	// silently permitting delivery.
	quiet, err := currentlyQuietHours(now, prefs)
	if err != nil {
		return Decision{}, ErrUnavailable
	}
	if quiet {
		return Decision{Eligible: false, Reason: ReasonQuietHours}, nil
	}

	return Decision{Eligible: true, Reason: ReasonEligible}, nil
}
