// Package identity defines the provider-neutral account/identity domain
// approved by docs/authentication-authorization-v1.md Section 1
// (Account model). It has no persistence, no HTTP handler, no cookie, no
// password-hashing implementation, and no public self-registration path: a
// User value can only ever be constructed by this package's own
// administrator-driven functions, mirroring "every account is created by an
// administrator through the flow in Section 7."
package identity

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// UserID is the server-generated, immutable, opaque account identifier
// (Section 1). It is never client-supplied or email-derived.
type UserID int64

// Role is a first-class, audited field (Section 1, Section 6). Administrator
// roles are a disjoint, clearly labeled subset; role is never inferred from
// an email domain or naming convention.
type Role string

const (
	RoleSystemAdministrator     Role = "system_administrator"
	RoleDepartmentAdministrator Role = "department_administrator"
	RoleDispatcherOperator      Role = "dispatcher_operator"
	RoleResponder               Role = "responder"
	RoleReadOnlyAuditor         Role = "read_only_auditor"
)

// allRoles is the exhaustive, allow-listed set (Section 6's proposed five).
// An unrecognized role string is always invalid: default deny, never a
// permissive fallback.
var allRoles = map[Role]bool{
	RoleSystemAdministrator:     true,
	RoleDepartmentAdministrator: true,
	RoleDispatcherOperator:      true,
	RoleResponder:               true,
	RoleReadOnlyAuditor:         true,
}

// Valid reports whether r is one of the approved Section 6 roles. An unknown
// role is always invalid, never silently accepted.
func (r Role) Valid() bool { return allRoles[r] }

// IsAdministrator reports whether r is one of the two disjoint administrator
// roles (Section 1: "Administrator distinguishability").
func (r Role) IsAdministrator() bool {
	return r == RoleSystemAdministrator || r == RoleDepartmentAdministrator
}

// RequiresMFA reports whether r must complete MFA enrollment before leaving
// AccountStatePasswordChangeRequired (Section 5: "Administrators...must
// enroll strong MFA"). Whether a non-administrator role requires MFA is an
// explicitly unresolved contract question (Section 15, item 5); this
// package does not default that decision to true or false, and returns
// false only because "not required" is the contract's own stated absence of
// a decision, never a silent policy choice by this package.
func (r Role) RequiresMFA() bool { return r.IsAdministrator() }

// Scope is a placeholder department/scope label (Section 6: "'Own scope' is
// a placeholder for whatever department/talkgroup scoping model a future
// phase defines"). The exact scope model is an unresolved contract question
// (Section 15, item 3); this package treats Scope only as an opaque,
// case-sensitive label for equality comparison, never as a hierarchy,
// prefix, or wildcard. An empty Scope means "no scope boundary assigned"
// (used by RoleSystemAdministrator, whose authority is deployment-wide).
type Scope string

const maxScopeLen = 100

// Valid reports whether s is either empty (no scope) or a non-blank,
// bounded-length label.
func (s Scope) Valid() bool {
	if s == "" {
		return true
	}
	trimmed := strings.TrimSpace(string(s))
	return trimmed == string(s) && trimmed != "" && len(s) <= maxScopeLen
}

// AccountState is exactly one of the six states in Section 1's table. There
// is no seventh state and no free-form status string.
type AccountState string

const (
	StateInvited                AccountState = "invited"
	StatePasswordChangeRequired AccountState = "password_change_required"
	StateActive                 AccountState = "active"
	StateSuspended              AccountState = "suspended"
	StateDisabled               AccountState = "disabled"
	StateExpired                AccountState = "expired"
)

var allStates = map[AccountState]bool{
	StateInvited:                true,
	StatePasswordChangeRequired: true,
	StateActive:                 true,
	StateSuspended:              true,
	StateDisabled:               true,
	StateExpired:                true,
}

// Valid reports whether s is one of the six approved account states.
func (s AccountState) Valid() bool { return allStates[s] }

// allowedTransitions is the authoritative state-transition matrix. It is
// deliberately permissive about which *administrator-invoked, named*
// operation may reach a given target state (Suspend/Disable/Restore/Reset/
// ResendInvitation each call AllowedTransition themselves, so this table is
// the single source of truth for every one of them) while remaining
// impossible to satisfy for the transitions Section 1 forbids outright:
//
//   - Nothing transitions directly from invited/expired to active: "No
//     account reaches active without passing through
//     password-change-required at least once."
//   - Nothing transitions into invited except through an administrator
//     resend/restore operation (never self-service, never automatic).
//   - A terminal state (suspended/disabled/expired) never silently becomes
//     active again without an explicit Restore/Reset/ResendInvitation call.
var allowedTransitions = map[AccountState]map[AccountState]bool{
	StateInvited: {
		StatePasswordChangeRequired: true, // user redeems the invitation
		StateExpired:                true, // automatic timeout
		StateSuspended:              true, // administrator action
		StateDisabled:               true, // administrator action
	},
	StatePasswordChangeRequired: {
		StateActive:    true, // permanent password (and required MFA) established
		StateExpired:   true, // automatic timeout before completion
		StateSuspended: true, // administrator action
		StateDisabled:  true, // administrator action
	},
	StateActive: {
		StateSuspended:              true, // administrator action
		StateDisabled:               true, // administrator action
		StatePasswordChangeRequired: true, // administrator-initiated reset
	},
	StateSuspended: {
		StateActive:                 true, // administrator restore (already completed first-time login)
		StateInvited:                true, // administrator restore (never completed; invitation still valid)
		StateExpired:                true, // administrator restore (never completed; invitation lapsed)
		StateDisabled:               true, // administrator action
		StatePasswordChangeRequired: true, // administrator-initiated reset
	},
	StateDisabled: {
		StateActive:                 true, // administrator restore (already completed first-time login)
		StateInvited:                true, // administrator restore (never completed; invitation still valid)
		StateExpired:                true, // administrator restore (never completed; invitation lapsed)
		StateSuspended:              true, // administrator action
		StatePasswordChangeRequired: true, // administrator-initiated reset
	},
	StateExpired: {
		StateInvited:   true, // administrator resend
		StateSuspended: true, // administrator action
		StateDisabled:  true, // administrator action
	},
}

// AllowedTransition reports whether from -> to is a permitted direct state
// transition. Both an unknown state and an unlisted pair deny by default.
func AllowedTransition(from, to AccountState) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	return allowedTransitions[from][to]
}

// ErrInvalidTransition marks a rejected state change. It never echoes
// account-specific detail, only that the transition itself is not permitted.
var ErrInvalidTransition = errors.New("identity: state transition not permitted")

// Sentinel validation errors. None of these, nor any error this package
// returns, ever includes a password, token, or hash value: only a safe,
// fixed, generic classification.
var (
	ErrInvalidEmail       = errors.New("identity: invalid email address")
	ErrInvalidDisplayName = errors.New("identity: invalid display name")
	ErrInvalidRole        = errors.New("identity: invalid role")
	ErrInvalidScope       = errors.New("identity: invalid scope")
)

const (
	minEmailLen       = 3
	maxEmailLen       = 320
	maxDisplayNameLen = 200
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail lowercases, Unicode-NFC-normalizes, and grammar-validates
// raw, matching Section 1's "normalized email address" definition exactly
// (this must stay in lockstep with the users.normalized_email CHECK
// constraint in migration 000008). It never returns a value that could
// differ from what the database will accept.
func NormalizeEmail(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidEmail
	}
	normalized := norm.NFC.String(trimmed)
	lower := strings.ToLower(normalized)
	if len(lower) < minEmailLen || len(lower) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	if !emailPattern.MatchString(lower) {
		return "", ErrInvalidEmail
	}
	return lower, nil
}

// ValidateDisplayName enforces Section 1's bound: a non-blank, bounded
// administrator-assigned label. It carries no email-uniqueness meaning.
func ValidateDisplayName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len(name) > maxDisplayNameLen {
		return ErrInvalidDisplayName
	}
	return nil
}

// ValidateRole rejects any role not in the approved Section 6 set.
func ValidateRole(r Role) error {
	if !r.Valid() {
		return ErrInvalidRole
	}
	return nil
}

// ValidateScope rejects a blank-but-nonempty or oversized scope label.
func ValidateScope(s Scope) error {
	if !s.Valid() {
		return ErrInvalidScope
	}
	return nil
}

// User is the full internal account record. It is never serialized directly
// to a caller outside this package's trust boundary: PasswordHash is
// deliberately unexported-shaped (a raw string field so the identitystore
// package can populate it from a database row), but every consumer outside
// identitystore/passwordpolicy must use View, never User, to render account
// data.
type User struct {
	ID                UserID
	NormalizedEmail   string
	DisplayName       string
	Role              Role
	Scope             Scope
	Status            AccountState
	PasswordHash      string // PHC-encoded; empty until a permanent password is set
	PasswordUpdatedAt time.Time
	LastLoginAt       time.Time
	CreatedByID       UserID // 0 means "no creator recorded" (never true for a real account)
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// HasPassword reports whether a permanent password has been established.
func (u User) HasPassword() bool { return u.PasswordHash != "" }

// CanAuthenticate reports whether this account's current state permits
// starting a new authenticated session at all (Section 1, Section 8:
// "Sessions for suspended, disabled, expired, or
// password-change-required accounts cannot authorize protected access").
// This is a necessary, not sufficient, condition: role/scope authorization
// (see the authorization package) is always checked separately and
// additionally.
func (u User) CanAuthenticate() bool { return u.Status == StateActive }

// View is the safe, public representation of an account: exactly the
// fields Section 7's proposed user-detail screen names, and nothing a
// caller could use to authenticate as or impersonate this account. No
// PasswordHash field exists on View, structurally, not by convention.
type View struct {
	ID          UserID
	Email       string
	DisplayName string
	Role        Role
	Scope       Scope
	Status      AccountState
	LastLoginAt time.Time
	CreatedAt   time.Time
}

// Public renders u as its safe View. Every caller outside this package and
// identitystore must render a User this way before returning it anywhere a
// log, API response, or UI could observe it.
func (u User) Public() View {
	return View{
		ID:          u.ID,
		Email:       u.NormalizedEmail,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		Scope:       u.Scope,
		Status:      u.Status,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}
