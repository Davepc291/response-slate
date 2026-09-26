// Package notifydevices is the first, deliberately narrow substep of Step
// 8D-B (docs/notification-relay-amendment-v1.md Section 25.1). It models a
// browser's native W3C Push API subscription object, validates it, and binds
// it to the existing authenticated identity.UserID type — nothing else.
//
// This package has no persistence (its Store is in-memory only), no HTTP
// handler, no route, no database migration, no environment variable, no
// provider SDK, and no network dependency of any kind. It never sends a
// notification, never reads an alert_events row, and is never registered by
// backend/cmd/api: a caller (a test, or a future authorized integration)
// must construct and invoke it explicitly, mirroring the same dormant-by-
// construction pattern already established by
// backend/internal/alertpipeline and backend/internal/alertstore.
//
// Consent tracking, delivery preferences, the outbox, and the provider relay
// are explicitly out of scope here: they belong to the separate future
// notifypreferences/notifyoutbox/notifyrelay/notifyaudit packages named in
// the amendment's Section 25.1, none of which this package imports or
// depends on.
package notifydevices

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"sync"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// Web Push subscription key sizes, fixed by RFC 8291 (Message Encryption for
// Web Push): p256dh is an uncompressed P-256 public key (0x04 || X || Y), and
// auth is a 16-byte shared secret. These are structural facts about the Push
// API, not a policy value this package invents.
const (
	p256dhDecodedLen = 65
	authDecodedLen   = 16

	// maxEndpointLen is a resource-safety ceiling against a pathological
	// caller-supplied string, not a claim about any real push service's
	// actual endpoint URL length.
	maxEndpointLen = 2048
	// maxPlatformLen bounds the client-declared, UX-only platform hint
	// (Section 6: "never trusted for security decisions").
	maxPlatformLen = 64
)

// Sentinel errors. None of these, nor any error this package returns, ever
// echoes the subscription endpoint, key material, or another user's
// identity: only a safe, fixed, generic classification.
var (
	ErrInvalidUserID   = errors.New("notifydevices: user id is required")
	ErrInvalidEndpoint = errors.New("notifydevices: endpoint must be an absolute https URL")
	ErrInvalidP256dh   = errors.New("notifydevices: keys.p256dh must be a base64url-encoded 65-byte P-256 public key")
	ErrInvalidAuth     = errors.New("notifydevices: keys.auth must be a base64url-encoded 16-byte secret")
	ErrInvalidPlatform = errors.New("notifydevices: platform hint contains unsupported characters or is too long")

	// ErrNotFound marks a DeviceID this Store has no active registration for
	// (never created, already revoked, or already superseded). It never
	// distinguishes those three cases to a caller outside this package, so a
	// caller cannot use response shape alone to enumerate another user's
	// device IDs.
	ErrNotFound = errors.New("notifydevices: registration not found")
	// ErrForbidden marks an operation attempted against a registration that
	// belongs to a different user than the caller (Section 6: "rejected and
	// audited as a possible cross-user registration attempt"), or an
	// attempt to bind an endpoint that is already actively held by any
	// other registration at all — including another of the *same* caller's
	// own devices, which would otherwise leave two simultaneously active
	// rows for one endpoint and silently duplicate delivery. Audit
	// recording itself belongs to the future notifyaudit package; this
	// package only ever refuses the operation.
	ErrForbidden = errors.New("notifydevices: registration belongs to a different user")
)

var platformPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// Keys is the browser's native Push API subscription key material
// (PushSubscription.toJSON().keys). Neither field is a provider credential;
// both are opaque, per-subscription values the Push standard requires the
// application server to hold in order to encrypt a message to this specific
// browser subscription.
type Keys struct {
	P256dh string
	Auth   string
}

// Subscription is exactly the W3C Push API subscription object (Section 4.1,
// Section 6): endpoint, keys.p256dh, keys.auth, and nothing else. It never
// carries a raw device identifier, advertising ID, or any identifier the
// browser does not already expose to the Push API itself.
type Subscription struct {
	Endpoint string
	Keys     Keys
}

// Validate checks every field of s independently. It never trusts a caller
// to have validated first.
func (s Subscription) Validate() error {
	if err := validateEndpoint(s.Endpoint); err != nil {
		return err
	}
	if err := validateP256dh(s.Keys.P256dh); err != nil {
		return err
	}
	if err := validateAuth(s.Keys.Auth); err != nil {
		return err
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" || len(endpoint) > maxEndpointLen {
		return ErrInvalidEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ErrInvalidEndpoint
	}
	return nil
}

// decodeB64URL decodes a base64url string, accepting both the unpadded
// encoding every mainstream browser's PushSubscription.toJSON() produces and
// the padded variant, since padding presence is not a security property.
// Standard (non-URL-safe) base64 is never accepted: neither p256dh nor auth
// is ever produced in that alphabet by a real Push API subscription.
func decodeB64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func validateP256dh(p256dh string) error {
	b, err := decodeB64URL(p256dh)
	if err != nil || len(b) != p256dhDecodedLen {
		return ErrInvalidP256dh
	}
	return nil
}

func validateAuth(auth string) error {
	b, err := decodeB64URL(auth)
	if err != nil || len(b) != authDecodedLen {
		return ErrInvalidAuth
	}
	return nil
}

func validatePlatform(platform string) error {
	if platform == "" {
		return nil
	}
	if len(platform) > maxPlatformLen || !platformPattern.MatchString(platform) {
		return ErrInvalidPlatform
	}
	return nil
}

// DeviceID is the server-generated, opaque registration identifier. It is
// never client-supplied.
type DeviceID int64

// Registration is one authenticated user's bound device/subscription row
// (Section 6, Section 25.2's proposed notification_devices shape). A
// Registration is never constructed directly by a caller outside this
// package: use Store.Register or Store.Replace.
type Registration struct {
	ID           DeviceID
	UserID       identity.UserID
	Subscription Subscription
	// Platform is a client-declared hint (for example "ios-safari" or
	// "android-chrome"), for UX only, never trusted for security decisions.
	Platform  string
	CreatedAt time.Time
	UpdatedAt time.Time
	// RevokedAt is set once this row stops being active, whether by an
	// explicit Store.Revoke call or by Store.Replace superseding it. nil
	// means active.
	RevokedAt *time.Time
	// SupersededBy records the DeviceID that replaced this row via
	// Store.Replace (Section 6's "device replacement" case). Zero means this
	// row was never superseded; it may still be independently revoked.
	SupersededBy DeviceID
}

// Active reports whether r currently represents a live, deliverable
// registration. It is necessary but not sufficient authorization to deliver
// anything: per-delivery re-checks (consent, account state, preferences)
// belong to the future notifyoutbox/notifypreferences packages, not here.
func (r Registration) Active() bool { return r.RevokedAt == nil }

// Store is an in-memory registration store. It has no persistence: every
// Registration it holds is lost on process restart. It is never registered
// by the live server and calls no network or database dependency; a caller
// (a test, or a future authorized integration sitting behind Step 9
// authentication) constructs and uses it explicitly. Store is safe for
// concurrent use.
type Store struct {
	mu     sync.Mutex
	byID   map[DeviceID]Registration
	nextID DeviceID
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{byID: make(map[DeviceID]Registration)}
}

// activeByEndpoint returns the active registration currently bound to
// endpoint, if any. Callers must hold s.mu. A full-map scan is deliberate: at
// this package's expected scale (one row per registered browser/device) a
// secondary index would add bookkeeping-drift risk (an index falling out of
// sync with Revoke/Replace) for no measurable benefit.
func (s *Store) activeByEndpoint(endpoint string) (Registration, bool) {
	for _, r := range s.byID {
		if r.Active() && r.Subscription.Endpoint == endpoint {
			return r, true
		}
	}
	return Registration{}, false
}

// Register validates sub and platform, then creates or idempotently updates
// the caller's registration for that subscription (Section 6: "Re-
// registering the same endpoint for the same user updates the existing row
// (idempotent)"). now is the caller-supplied clock value; this package never
// calls time.Now() itself, so every timestamp it produces is deterministic
// and test-controlled.
//
// Registering an endpoint another user already actively holds is rejected
// with ErrForbidden and mutates nothing (Section 6: "re-registering the same
// endpoint for a different user is rejected... never silently accepted or
// reassigned").
func (s *Store) Register(now time.Time, userID identity.UserID, sub Subscription, platform string) (Registration, error) {
	if userID <= 0 {
		return Registration{}, ErrInvalidUserID
	}
	if err := sub.Validate(); err != nil {
		return Registration{}, err
	}
	if err := validatePlatform(platform); err != nil {
		return Registration{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.activeByEndpoint(sub.Endpoint); ok {
		if existing.UserID != userID {
			return Registration{}, ErrForbidden
		}
		existing.Subscription = sub
		existing.Platform = platform
		existing.UpdatedAt = now
		s.byID[existing.ID] = existing
		return existing, nil
	}

	s.nextID++
	reg := Registration{
		ID:           s.nextID,
		UserID:       userID,
		Subscription: sub,
		Platform:     platform,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.byID[reg.ID] = reg
	return reg, nil
}

// Replace supersedes an existing, caller-owned, still-active registration
// with a freshly validated subscription (Section 6: "device replacement...
// supersedes the prior row for that logical device; it does not silently
// duplicate delivery"). The caller must already know its own prior DeviceID
// (returned by an earlier Register/Replace call): a rotated subscription
// endpoint has no relationship to the old one that this package can detect
// unassisted, and Section 6 forbids inferring identity from anything beyond
// what the Push API itself exposes.
//
// Replace fails closed with ErrNotFound if oldID does not exist or is no
// longer active, and with ErrForbidden if oldID belongs to a different user.
// If sub's endpoint is unchanged from oldID's current endpoint, Replace is
// an idempotent update of that same row (identical to how Register treats
// re-registering an unchanged endpoint) rather than churning a new DeviceID
// that supersedes a row for no functional reason. Otherwise, the new
// endpoint must not already be actively held by any other registration —
// including one owned by this same caller — or Replace fails closed with
// ErrForbidden; allowing that would leave two simultaneously active rows
// bound to one endpoint. Neither row is mutated on any error path.
func (s *Store) Replace(now time.Time, userID identity.UserID, oldID DeviceID, sub Subscription, platform string) (Registration, error) {
	if userID <= 0 {
		return Registration{}, ErrInvalidUserID
	}
	if err := sub.Validate(); err != nil {
		return Registration{}, err
	}
	if err := validatePlatform(platform); err != nil {
		return Registration{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.byID[oldID]
	if !ok || !old.Active() {
		return Registration{}, ErrNotFound
	}
	if old.UserID != userID {
		return Registration{}, ErrForbidden
	}

	if sub.Endpoint == old.Subscription.Endpoint {
		old.Subscription = sub
		old.Platform = platform
		old.UpdatedAt = now
		s.byID[old.ID] = old
		return old, nil
	}

	if existing, ok := s.activeByEndpoint(sub.Endpoint); ok && existing.ID != old.ID {
		return Registration{}, ErrForbidden
	}

	s.nextID++
	fresh := Registration{
		ID:           s.nextID,
		UserID:       userID,
		Subscription: sub,
		Platform:     platform,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.byID[fresh.ID] = fresh

	revokedAt := now
	old.RevokedAt = &revokedAt
	old.SupersededBy = fresh.ID
	s.byID[old.ID] = old

	return fresh, nil
}

// Revoke marks id revoked as of now, so it is no longer Active (Section 8:
// "Lost device: the user...must be able to revoke a specific device
// registration without affecting the user's other devices"). Revoke is
// idempotent: revoking an already-revoked or already-superseded id succeeds
// without changing its original RevokedAt. Revoking an id owned by a
// different user, or an id that never existed, fails closed with
// ErrForbidden/ErrNotFound and changes nothing.
func (s *Store) Revoke(now time.Time, userID identity.UserID, id DeviceID) error {
	if userID <= 0 {
		return ErrInvalidUserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	reg, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	if reg.UserID != userID {
		return ErrForbidden
	}
	if !reg.Active() {
		return nil
	}
	reg.RevokedAt = &now
	s.byID[id] = reg
	return nil
}

// Get returns the registration for id regardless of its active state, and
// whether it exists at all. It never checks ownership: a caller
// authorizing a read against the requesting user's identity is the caller's
// own responsibility, exactly as Replace/Revoke require an explicit userID
// for their own ownership checks.
func (s *Store) Get(id DeviceID) (Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.byID[id]
	return reg, ok
}

// ListActive returns every currently active registration owned by userID,
// in unspecified order (Section 6: "a user may hold more than one registered
// device... each is a distinct row, independently revocable").
func (s *Store) ListActive(userID identity.UserID) []Registration {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Registration
	for _, r := range s.byID {
		if r.UserID == userID && r.Active() {
			out = append(out, r)
		}
	}
	return out
}
