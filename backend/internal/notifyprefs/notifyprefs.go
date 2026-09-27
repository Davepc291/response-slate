// Package notifyprefs is the pure domain model for Step 8D-B Part 5 alert
// preferences (docs/notification-relay-amendment-v1.md Section 7, restating
// docs/alert-notification-engine-v1.md Section 8): the single, mutable,
// current set of delivery preferences an authenticated user configures for
// notification-relay eligibility -- enable/disable, channel/tone-set/
// keyword-list selection, minimum confidence, and a quiet-hours window in
// the user's own configured time zone.
//
// This package has no persistence (its own persistence adapter is the
// separate backend/internal/notifyprefsstore package, mirroring the
// notifydevices/notifydevicestore and notifyconsent/notifyconsentstore
// pairs), no HTTP handler, no route, no provider SDK, and no network
// dependency of any kind. It is deliberately named notifyprefs, not
// notifypreferences: docs/notification-relay-amendment-v1.md Section 25.1
// reserves "notifypreferences" for a different, not-yet-built component --
// server-side preference and quiet-hours *evaluation* at delivery time
// (a future notifyoutbox dependency) -- and this package must not collide
// with that name, since it is pure storage, not evaluation.
//
// Quiet-hours suppress-vs-queue behavior itself is explicitly unresolved
// (the amendment's own Section 29, item 10) and out of scope here: this
// package only stores the configured window and time zone. It never
// evaluates whether "now" falls inside it, never touches
// notification_outbox/notification_deliveries, and never decides anything
// about suppression.
package notifyprefs

import (
	"errors"
	"math"
	"regexp"
	"time"

	// Blank-imported so time.LoadLocation can resolve a real IANA zone name
	// from data embedded directly in this binary, rather than depending on
	// the deployment OS having its own zoneinfo files available (see
	// validateTimezone's own doc comment for why this matters on
	// production Windows specifically).
	_ "time/tzdata"

	"greenwich-fire-responder/backend/internal/identity"
)

// Sentinel errors. None of these, nor any error this package returns, ever
// echoes a caller-supplied value verbatim: only a safe, fixed, generic
// classification, mirroring notifydevices' own sentinel-per-validation-
// failure convention.
var (
	ErrInvalidUserID    = errors.New("notifyprefs: user id is required")
	ErrInvalidChannel   = errors.New("notifyprefs: channel is not one of CH1A, CH2B, CH3B, CH4C")
	ErrDuplicateChannel = errors.New("notifyprefs: channels must not repeat")
	// ErrInvalidID marks a tone-set or keyword-list id that does not match
	// the established id pattern (mirroring internal/alerts.idPattern,
	// duplicated locally rather than imported -- this package must never
	// depend on internal/alerts, enforced by its own depcheck_test.go).
	ErrInvalidID            = errors.New("notifyprefs: tone-set/keyword-list id contains unsupported characters or is too long")
	ErrDuplicateID          = errors.New("notifyprefs: tone-set/keyword-list ids must not repeat")
	ErrTooManyIDs           = errors.New("notifyprefs: at most 200 tone-set/keyword-list ids are allowed")
	ErrInvalidMinConfidence = errors.New("notifyprefs: min confidence must be between 0 and 1")
	// ErrQuietHoursUnpaired marks a quiet-hours window with only one of
	// start/end configured -- a half-configured window is never allowed to
	// exist, mirroring the migration's own paired-field CHECK constraint.
	ErrQuietHoursUnpaired         = errors.New("notifyprefs: quiet hours start and end must both be set or both be nil")
	ErrInvalidQuietHoursTime      = errors.New("notifyprefs: quiet hours start/end must be at least zero and less than 24h")
	ErrQuietHoursTimezoneRequired = errors.New("notifyprefs: quiet hours timezone is required when quiet hours are configured")
	ErrInvalidTimezone            = errors.New("notifyprefs: quiet hours timezone is not a valid IANA time zone name")
)

// Channel is exactly one of the four values migration 000010's
// alert_preferences.channels CHECK constraint allows (Section 7, restating
// Step 8A Section 8's CH1A/CH2B/CH3B/CH4C four-channel mapping). There is no
// fifth value and no free-form channel string.
type Channel string

const (
	ChannelCH1A Channel = "CH1A"
	ChannelCH2B Channel = "CH2B"
	ChannelCH3B Channel = "CH3B"
	ChannelCH4C Channel = "CH4C"
)

// Valid reports whether c is one of the four approved values. An unknown
// value is always invalid, never silently accepted.
func (c Channel) Valid() bool {
	switch c {
	case ChannelCH1A, ChannelCH2B, ChannelCH3B, ChannelCH4C:
		return true
	}
	return false
}

// maxIDLen/idPattern duplicate internal/alerts.idPattern's own established
// tone-set/keyword-list id shape, for the same reason
// notifydevicestore.decodeSubscriptionKey duplicates notifydevices' own
// decode logic: this package must validate before ever reaching SQL, and
// must never import internal/alerts to do so.
const maxIDListLen = 200

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// maxQuietHoursOffset is the exclusive upper bound for a quiet-hours
// clock offset: a full day has no valid 24:00 wall-clock value.
const maxQuietHoursOffset = 24 * time.Hour

// maxTimezoneLen mirrors the migration's own
// octet_length(quiet_hours_timezone) <= 64 CHECK.
const maxTimezoneLen = 64

// Preferences is the single, current set of delivery preferences for one
// user (migration 000010's alert_preferences row shape, Section 7). It is
// always a full snapshot: notifyprefsstore.Upsert always replaces every
// configurable field at once, never merges a partial change into an
// existing row (Section 25.3's proposed PUT route implies whole-resource
// replacement, not PATCH-style partial update).
type Preferences struct {
	UserID identity.UserID
	// Enabled is the global off switch (Section 7: "a global off switch
	// always wins over every other preference"). false is both the
	// migration's own column default and this package's zero value: a
	// user with no configured preferences at all is indistinguishable,
	// from a delivery-eligibility standpoint, from a user who has
	// explicitly disabled delivery -- both must fail closed to no
	// delivery. See notifyprefsstore.Get's own doc comment for how a
	// missing row is surfaced.
	Enabled bool
	// Channels is the selected CH1A/CH2B/CH3B/CH4C subset (Section 7's
	// "talkgroup/channel" gate, restating Step 8A Section 8). An empty
	// slice means no channel is selected, not "all channels."
	Channels []Channel
	// ToneSetIDs/KeywordListIDs are the user's selected tone-set/
	// keyword-list subscriptions (Section 7). tone_configurations/
	// keyword_configurations do not exist yet (gated on a future
	// administration contract, per migration 000010's own comment), so
	// these are bounded, format-validated label lists rather than a
	// foreign key -- the database bounds their size, this package
	// validates their content, exactly mirroring the migration's own
	// stated division of responsibility.
	ToneSetIDs     []string
	KeywordListIDs []string
	// MinConfidence is the minimum detector confidence below which a
	// match is not delivered (Step 8A Section 8). nil means no threshold
	// is configured.
	MinConfidence *float64
	// QuietHoursStart/QuietHoursEnd are wall-clock offsets since midnight
	// (migration 000010's quiet_hours_start/quiet_hours_end plain `time`
	// columns -- deliberately time.Duration, not time.Time: there is no
	// meaningful date component to a recurring daily quiet-hours window).
	// Both nil means no quiet-hours window is configured; a half-
	// configured window (only one of the two set) is invalid, mirroring
	// the migration's own paired-field CHECK constraint.
	//
	// This package does not evaluate whether "now" falls inside this
	// window, and does not decide suppress-vs-queue behavior: see this
	// package's own doc comment for why that is explicitly out of scope.
	QuietHoursStart *time.Duration
	QuietHoursEnd   *time.Duration
	// QuietHoursTimezone is the IANA time zone name the window above is
	// evaluated in (Section 7: "the user's own configured time zone, not
	// server-local time"). Required whenever QuietHoursStart/End are
	// configured -- stricter than the migration's own CHECK, which only
	// pairs start/end with each other, not with timezone; Section 7's own
	// language makes a timezone-less window semantically incomplete even
	// though the schema alone would accept it.
	QuietHoursTimezone *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Validate checks every field of p independently. It never trusts a caller
// to have validated first, and never touches a database: every rule here is
// checked entirely in memory before notifyprefsstore.Upsert ever reaches
// SQL.
func (p Preferences) Validate() error {
	if p.UserID <= 0 {
		return ErrInvalidUserID
	}
	if err := validateChannels(p.Channels); err != nil {
		return err
	}
	if err := validateIDList(p.ToneSetIDs); err != nil {
		return err
	}
	if err := validateIDList(p.KeywordListIDs); err != nil {
		return err
	}
	if err := validateMinConfidence(p.MinConfidence); err != nil {
		return err
	}
	if err := validateQuietHours(p.QuietHoursStart, p.QuietHoursEnd, p.QuietHoursTimezone); err != nil {
		return err
	}
	return nil
}

func validateChannels(channels []Channel) error {
	seen := make(map[Channel]bool, len(channels))
	for _, c := range channels {
		if !c.Valid() {
			return ErrInvalidChannel
		}
		if seen[c] {
			return ErrDuplicateChannel
		}
		seen[c] = true
	}
	return nil
}

func validateIDList(ids []string) error {
	if len(ids) > maxIDListLen {
		return ErrTooManyIDs
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !idPattern.MatchString(id) {
			return ErrInvalidID
		}
		if seen[id] {
			return ErrDuplicateID
		}
		seen[id] = true
	}
	return nil
}

func validateMinConfidence(c *float64) error {
	if c == nil {
		return nil
	}
	v := *c
	if math.IsNaN(v) || v < 0 || v > 1 {
		return ErrInvalidMinConfidence
	}
	return nil
}

func validateQuietHours(start, end *time.Duration, timezone *string) error {
	if (start == nil) != (end == nil) {
		return ErrQuietHoursUnpaired
	}
	configured := start != nil
	if configured {
		if err := validateClockOffset(*start); err != nil {
			return err
		}
		if err := validateClockOffset(*end); err != nil {
			return err
		}
	}
	if configured && timezone == nil {
		return ErrQuietHoursTimezoneRequired
	}
	if timezone != nil {
		if err := validateTimezone(*timezone); err != nil {
			return err
		}
	}
	return nil
}

func validateClockOffset(d time.Duration) error {
	if d < 0 || d >= maxQuietHoursOffset {
		return ErrInvalidQuietHoursTime
	}
	return nil
}

// validateTimezone requires tz to be a real, loadable IANA time zone name.
// The blank _ "time/tzdata" import above embeds the full IANA database
// directly in this binary, so this check behaves identically regardless of
// the deployment OS or its local zoneinfo availability -- in particular, on
// production Windows, where no /usr/share/zoneinfo equivalent exists and
// Go's fallback GOROOT-relative zoneinfo.zip lookup is not reliable for a
// deployed binary that may not carry its build-time GOROOT along with it.
//
// "Local" is rejected even though time.LoadLocation("Local") always
// succeeds: it resolves to whatever time zone the server process happens to
// be running in, not a real IANA zone the user actually chose, which would
// silently defeat Section 7's "the user's own configured time zone, not
// server-local time" requirement.
func validateTimezone(tz string) error {
	if tz == "" || len(tz) > maxTimezoneLen || tz == "Local" {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ErrInvalidTimezone
	}
	return nil
}
