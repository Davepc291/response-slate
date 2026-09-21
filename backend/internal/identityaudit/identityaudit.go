// Package identityaudit implements the Section 10 (Audit and security
// events) immutable audit-event domain: the fixed event-type catalog and
// structured, allow-listed metadata validation. It has no persistence (see
// identityauditstore) and never accepts a password, raw token, cookie
// value, MFA secret, full notification payload, or unnecessary protected
// call content, by construction: metadata keys not on an event type's
// allow-list are rejected before an Event can even be built, and a fixed
// set of forbidden key names is rejected for every event type as defense
// in depth.
package identityaudit

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// EventType is exactly one of the Section 10 catalog values (plus
// InvitationRedemptionFailed, the "invitation redeemed-adjacent failure
// event" AAX-06 requires for a reused-token attempt). It must stay in
// lockstep with the identity_audit_log.event_type CHECK constraint in
// migration 000008.
type EventType string

const (
	LoginSuccess               EventType = "login_success"
	LoginFailure               EventType = "login_failure"
	InvitationCreated          EventType = "invitation_created"
	InvitationRedeemed         EventType = "invitation_redeemed"
	InvitationRedemptionFailed EventType = "invitation_redemption_failed"
	InvitationExpired          EventType = "invitation_expired"
	PasswordResetRequested     EventType = "password_reset_requested"
	PasswordResetCompleted     EventType = "password_reset_completed"
	MFAEnrollment              EventType = "mfa_enrollment"
	MFARecovery                EventType = "mfa_recovery"
	RoleOrScopeChange          EventType = "role_or_scope_change"
	AccountStateChange         EventType = "account_state_change"
	ProtectedCallAccess        EventType = "protected_call_access"
	NotificationDeviceEvent    EventType = "notification_device_event"
	SessionCreated             EventType = "session_created"
	SessionRevoked             EventType = "session_revoked"
	AdministrativeAction       EventType = "administrative_action"
)

// Metadata is structured, string-valued key/value data only: never a
// nested object, never a raw blob. Every key must be in EventType's
// allow-list; every value is bounded and control-character-free.
type Metadata map[string]string

// allowedKeys is the per-event-type metadata allow-list. A key not listed
// here for a given EventType is rejected, regardless of its content.
var allowedKeys = map[EventType]map[string]bool{
	LoginSuccess:               {"session_id": true, "source_network_hint": true},
	LoginFailure:               {"attempted_email": true, "reason_code": true, "source_network_hint": true},
	InvitationCreated:          {"role": true},
	InvitationRedeemed:         {},
	InvitationRedemptionFailed: {"reason_code": true},
	InvitationExpired:          {},
	PasswordResetRequested:     {},
	PasswordResetCompleted:     {"revoked_session_count": true},
	MFAEnrollment:              {"method": true},
	MFARecovery:                {},
	RoleOrScopeChange:          {"prior_role": true, "new_role": true, "prior_scope": true, "new_scope": true},
	AccountStateChange:         {"prior_status": true, "new_status": true},
	ProtectedCallAccess:        {"resource_id": true},
	NotificationDeviceEvent:    {"device_id": true, "action": true},
	SessionCreated:             {"device_hint": true},
	SessionRevoked:             {"reason": true},
	AdministrativeAction:       {"action": true},
}

// forbiddenKeySubstrings is a defense-in-depth deny-list checked against
// every metadata key regardless of event type or allow-list membership, so
// a future editing mistake that adds an unsafe key to allowedKeys still
// cannot smuggle a secret through. Matching is case-insensitive and by
// substring, deliberately broad.
var forbiddenKeySubstrings = []string{
	"password", "token", "secret", "cookie", "hash", "credential",
	"private_key", "privatekey", "payload", "transcript", "auth_header",
	"apikey", "api_key",
}

// ErrForbiddenMetadataKey and ErrMetadataKeyNotAllowed are distinct so a
// caller/test can tell "this key is always unsafe" apart from "this key is
// merely not on this event type's list," but both must be treated
// identically by any code that decides whether to reject: never persisted.
var (
	ErrUnknownEventType      = errors.New("identityaudit: unknown event type")
	ErrForbiddenMetadataKey  = errors.New("identityaudit: metadata key is never permitted in an audit event")
	ErrMetadataKeyNotAllowed = errors.New("identityaudit: metadata key is not allow-listed for this event type")
	ErrMetadataValueInvalid  = errors.New("identityaudit: metadata value is empty, oversized, or contains control characters")
	ErrInvalidReason         = errors.New("identityaudit: reason code must match ^[a-z][a-z0-9_]{0,63}$")
	ErrMissingAccountOrActor = errors.New("identityaudit: event requires at least one of account or actor")
)

const maxMetadataValueLen = 512

var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// ValidateMetadata checks every key against t's allow-list and the global
// forbidden-key deny-list, and every value against a bounded, printable
// content rule. It never inspects values for the *pattern* of a secret
// (that is unreliable); safety instead comes entirely from restricting
// which keys may appear at all.
func ValidateMetadata(t EventType, m Metadata) error {
	allowed, ok := allowedKeys[t]
	if !ok {
		return ErrUnknownEventType
	}
	for key, value := range m {
		lower := strings.ToLower(key)
		for _, bad := range forbiddenKeySubstrings {
			if strings.Contains(lower, bad) {
				return ErrForbiddenMetadataKey
			}
		}
		if !allowed[key] {
			return ErrMetadataKeyNotAllowed
		}
		if value == "" || len(value) > maxMetadataValueLen || containsControl(value) {
			return ErrMetadataValueInvalid
		}
	}
	if t == LoginFailure {
		if email, ok := m["attempted_email"]; ok {
			if _, err := identity.NormalizeEmail(email); err != nil {
				return ErrMetadataValueInvalid
			}
		}
	}
	return nil
}

// Event is one immutable audit record. Construct with New; a zero-value
// Event must never be persisted directly.
type Event struct {
	Type      EventType
	AccountID identity.UserID // 0 means "no account" (only valid for an unmatched login_failure)
	ActorID   identity.UserID // 0 means "no distinct actor" (a user's own action)
	Reason    string          // optional; "" means no reason code recorded
	Metadata  Metadata
	CreatedAt time.Time
}

// New validates and constructs an Event. accountID and/or actorID may be 0
// (identity.UserID's zero value) when not applicable, except that a nil
// account is permitted only for LoginFailure (Section 10: "attempted
// email...not proof an account exists"); every other event type requires a
// resolvable account.
func New(t EventType, accountID, actorID identity.UserID, reason string, metadata Metadata, now time.Time) (Event, error) {
	if _, ok := allowedKeys[t]; !ok {
		return Event{}, ErrUnknownEventType
	}
	if accountID == 0 && t != LoginFailure {
		return Event{}, ErrMissingAccountOrActor
	}
	if reason != "" && !reasonPattern.MatchString(reason) {
		return Event{}, ErrInvalidReason
	}
	if err := ValidateMetadata(t, metadata); err != nil {
		return Event{}, err
	}
	if now.IsZero() {
		return Event{}, errors.New("identityaudit: created_at is required")
	}
	cloned := make(Metadata, len(metadata))
	for k, v := range metadata {
		cloned[k] = v
	}
	return Event{
		Type:      t,
		AccountID: accountID,
		ActorID:   actorID,
		Reason:    reason,
		Metadata:  cloned,
		CreatedAt: now,
	}, nil
}
