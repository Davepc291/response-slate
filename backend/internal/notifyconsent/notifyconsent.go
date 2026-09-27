// Package notifyconsent is the pure domain model for Step 8D-B Part 4
// device consent (docs/notification-relay-amendment-v1.md Section 8):
// explicit, timestamped consent/revocation events tied to an authenticated
// user and a specific notifydevices.DeviceID. It has no persistence, no
// HTTP handler, no provider SDK, and no network dependency of any kind --
// mirroring the exact notifydevices/notifydevicestore package-pair
// convention (a pure domain package kept separate from its Postgres
// adapter, backend/internal/notifyconsentstore, so this package's own
// depcheck_test.go can forbid a database import outright).
//
// This package models only what Section 8 calls the "explicit, timestamped
// consent event tied to the authenticated user and the specific device
// registration": a grant and a revoke are each their own event, append-only,
// never a mutable "current consent" flag. Deriving current consent state
// from the latest event is backend/internal/notifyconsentstore's job
// (Current), not this package's: unlike notifydevices, there is no
// in-memory Store here, because there is no meaningful invariant for a pure
// domain layer to enforce beyond the two-value Event type itself -- the
// "append one event, derive current state from the latest" logic only
// exists once persistence is involved.
package notifyconsent

import (
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// Event is exactly one of the two values migration 000010's
// notification_consents.event CHECK constraint allows. There is no third
// value and no free-form status string.
type Event string

const (
	EventGranted Event = "granted"
	EventRevoked Event = "revoked"
)

// Valid reports whether e is one of the two approved values. An unknown
// value is always invalid, never silently accepted.
func (e Event) Valid() bool {
	return e == EventGranted || e == EventRevoked
}

// Consent is one recorded consent/revocation event for a device
// (Section 8). It is always append-only: an existing Consent value is
// never updated or deleted, only ever superseded by a later one with a
// newer CreatedAt for the same DeviceID.
type Consent struct {
	ID        int64
	UserID    identity.UserID
	DeviceID  notifydevices.DeviceID
	Event     Event
	CreatedAt time.Time
}
