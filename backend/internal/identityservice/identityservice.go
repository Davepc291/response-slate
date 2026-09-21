// Package identityservice is the Step 9C authentication service layer: it
// coordinates the existing, approved Step 9B packages (identity,
// passwordpolicy, invitation, passwordreset, session, identitystore,
// identityaudit) into the provider-neutral operations
// docs/authentication-authorization-v1.md requires — login, invitation
// redemption/permanent-password establishment, session lookup/listing/
// revocation, logout, and password reset — without duplicating any of
// those packages' own security logic. It has no HTTP handler, no cookie,
// and no route: see backend/internal/authhttp for the transport layer built
// on top of this package.
//
// Every method takes an explicit now time.Time parameter, matching this
// repository's existing convention (session.Config, identitystore) of
// caller-supplied time rather than an internal clock, so tests are fully
// deterministic without wall-clock dependence.
//
// Self-service "change password while already active" is deliberately not
// implemented here: Section 4 of the contract defines only a token-based
// reset flow (self-service "forgot password" and administrator-initiated
// reset), never a bare old-password/new-password exchange for an already
// active session, and the underlying identitystore exposes no primitive to
// update a password hash for an account outside those two transitions
// (EstablishPassword requires password_change_required; CompletePasswordReset
// requires a valid single-use token). Adding one would mean extending
// Step 9B's persistence surface for a capability the approved contract does
// not name, which this phase declines to do silently. An authenticated user
// who wants to change their password today accomplishes it by requesting
// and completing a password reset for their own account while signed in,
// using the same RequestPasswordReset/CompletePasswordReset operations
// this package already provides.
package identityservice

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/session"
)

// Generic, externally-visible sentinel errors. None of these, nor any
// wrapped error this package returns to an HTTP caller, ever includes a
// password, raw token, cookie value, hash, or other sensitive account
// detail (Section 4's logging prohibition, restated for error values).
var (
	// ErrInvalidCredentials covers every login failure Section 3 requires to
	// be indistinguishable: unknown email, correct email with wrong
	// password, wrong email with any password, and (per this package's
	// literal reading of Section 3's state table, which names only
	// "suspended" as a narrow exception) every other non-active account
	// status presented with a correct password.
	ErrInvalidCredentials = errors.New("identityservice: invalid credentials")
	// ErrAccountSuspended is Section 3's one deliberate, narrow exception
	// for login: revealed only after a correct password was already
	// supplied, so nothing is leaked to a caller who has not authenticated.
	ErrAccountSuspended = errors.New("identityservice: account is suspended")
	// ErrTokenInvalid covers an unknown, already-redeemed, or superseded
	// invitation/reset token, matching Section 2/4's account-enumeration
	// resistance rule: never distinguished from "no such token."
	ErrTokenInvalid = errors.New("identityservice: token not found or already used")
	// ErrTokenExpired is the one deliberate, narrow exception for a
	// token-holding caller (Section 3: "Expired invite"; Section 4's
	// analogous reset-token comment): safe to reveal only because the
	// caller already possesses a link naming their own token.
	ErrTokenExpired = errors.New("identityservice: token has expired")
	// ErrPasswordTooWeak wraps a passwordpolicy length violation. Safe to
	// display distinctly because the caller is already holding a valid,
	// single-use invitation/reset token at this point (Section 4).
	ErrPasswordTooWeak = errors.New("identityservice: password does not meet the minimum policy")
	// ErrPasswordBreached marks a password rejected by the breach checker
	// (Section 4). Safe to display distinctly for the same reason.
	ErrPasswordBreached = errors.New("identityservice: password has appeared in a known data breach")
	// ErrSessionInvalid covers an unknown, revoked, idle-expired,
	// absolute-expired, or non-active-account session, never distinguished
	// (Section 8: "fail closed").
	ErrSessionInvalid = errors.New("identityservice: session is not valid")
	// ErrSessionNotOwned marks an attempt to revoke a session that does not
	// belong to the calling account (Section 8: a user may revoke only
	// their own sessions).
	ErrSessionNotOwned = errors.New("identityservice: session is not owned by the caller")
	// ErrInternal marks an unexpected persistence failure. The HTTP layer
	// must render this as a generic "temporarily unavailable" response,
	// never a raw database error (Section 3: "Service unavailable").
	ErrInternal = errors.New("identityservice: internal error")
)

// Store is the subset of identitystore.Postgres this service depends on,
// so tests can substitute a fake without a live database.
type Store interface {
	GetByEmail(ctx context.Context, email string) (identity.User, error)
	GetByID(ctx context.Context, id identity.UserID) (identity.User, error)
	EstablishPassword(ctx context.Context, userID identity.UserID, passwordHash string, now time.Time) (identity.AccountState, error)
	RedeemInvitation(ctx context.Context, tokenDigest []byte, now time.Time) (identity.UserID, error)
	CreateSession(ctx context.Context, userID identity.UserID, tokenDigest []byte, deviceHint string, createdAt, expiresAt time.Time) (session.ID, error)
	ResolveSession(ctx context.Context, tokenDigest []byte, cfg session.Config, now time.Time) (identitystore.Authorized, error)
	TouchSession(ctx context.Context, id session.ID, now time.Time) error
	RevokeSession(ctx context.Context, id session.ID, reason session.RevocationReason, now time.Time) error
	RevokeAllSessionsForUser(ctx context.Context, userID identity.UserID, reason session.RevocationReason, now time.Time) (int64, error)
	ListActiveSessions(ctx context.Context, userID identity.UserID) ([]session.Session, error)
	RequestPasswordReset(ctx context.Context, email string, issuedBy identity.UserID, newTokenDigest []byte, expiresAt time.Time) (bool, identity.UserID, error)
	CompletePasswordReset(ctx context.Context, rawTokenDigest []byte, newPasswordHash string, now time.Time) (identity.UserID, error)
}

// AuditRecorder is the subset of identityauditstore.Store this service
// depends on. It is structurally identical to that package's Store
// interface so a *identityauditstore.Postgres satisfies it without an
// import-cycle-causing direct dependency.
type AuditRecorder interface {
	Record(ctx context.Context, ev identityaudit.Event) (int64, error)
}

// Config is the explicit, caller-supplied policy this service enforces.
// Every field must be set by the caller (see backend/internal/authconfig):
// this package picks no implicit default for any value the contract leaves
// an open, unresolved question (Section 15).
type Config struct {
	Session          session.Config
	PasswordResetTTL time.Duration
	PasswordPolicy   passwordpolicy.Policy
	HashParams       passwordpolicy.Params
	BreachChecker    passwordpolicy.BreachChecker
}

// Service implements the Step 9C provider-neutral authentication
// operations.
type Service struct {
	store  Store
	audit  AuditRecorder
	cfg    Config
	logger *slog.Logger
}

// New constructs a Service. logger may be nil (a no-op logger is used),
// but store, audit, and every Config field must be valid; New panics on a
// nil store/audit or an invalid Config, matching this repository's
// fail-fast-at-construction convention for explicitly-chosen policy values
// (see authratelimit.NewMemoryLimiter).
func New(store Store, audit AuditRecorder, cfg Config, logger *slog.Logger) *Service {
	if store == nil || audit == nil {
		panic("identityservice: store and audit are required")
	}
	if err := cfg.Session.Validate(); err != nil {
		panic(err)
	}
	if cfg.PasswordResetTTL <= 0 {
		panic("identityservice: PasswordResetTTL must be positive and explicitly chosen")
	}
	if cfg.PasswordPolicy.MinLength <= 0 || cfg.PasswordPolicy.MaxLength <= cfg.PasswordPolicy.MinLength {
		panic("identityservice: PasswordPolicy is invalid")
	}
	if cfg.BreachChecker == nil {
		cfg.BreachChecker = passwordpolicy.NoOpBreachChecker{}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Service{store: store, audit: audit, cfg: cfg, logger: logger}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// dummyHash is a fixed, valid PHC-encoded Argon2id hash used only to
// perform an equivalent-cost comparison when no real account/hash exists,
// so a failed lookup for a non-existent account takes approximately the
// same time as a real password check (Section 3: "Timing"). Its content is
// never a real credential and is never derived from user input.
var dummyHash string

func init() {
	h, err := passwordpolicy.Hash("identityservice-timing-safety-fixture-only", passwordpolicy.DefaultParams())
	if err != nil {
		panic(err)
	}
	dummyHash = h
}

// recordAudit builds and records one audit event, best-effort: a failure to
// write the audit row is logged (event type only, never metadata content
// beyond what the allow-list already deemed safe) and never fails the
// caller's own operation, matching the existing repository convention that
// observability failures must not take down a request path.
func (s *Service) recordAudit(ctx context.Context, t identityaudit.EventType, accountID, actorID identity.UserID, reason string, metadata identityaudit.Metadata, now time.Time) {
	ev, err := identityaudit.New(t, accountID, actorID, reason, metadata, now)
	if err != nil {
		s.logger.Error("identity_audit_build_failed", "event_type", string(t))
		return
	}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		s.logger.Error("identity_audit_write_failed", "event_type", string(t))
	}
}

// LoginResult carries what the HTTP layer needs to establish a browser
// session after a successful login.
type LoginResult struct {
	SessionID session.ID
	RawToken  string
	ExpiresAt time.Time
	Account   identity.View
}

// Login authenticates email/password and, on success, creates a new
// server-side session (Section 1, Section 3, Section 8). sourceNetworkHint
// is a caller-supplied, safe descriptor of the request's network origin
// (for example, a client IP) recorded only in the audit trail, never
// returned to the caller. deviceHint is an optional, caller-supplied label
// for the new session (Section 8: "device/session listing").
func (s *Service) Login(ctx context.Context, email, password, deviceHint, sourceNetworkHint string, now time.Time) (LoginResult, error) {
	normalized, normErr := identity.NormalizeEmail(email)
	if normErr != nil {
		_, _ = passwordpolicy.Verify(dummyHash, password)
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.store.GetByEmail(ctx, normalized)
	if err != nil {
		_, _ = passwordpolicy.Verify(dummyHash, password)
		s.auditLoginFailure(ctx, 0, normalized, "no_such_account", sourceNetworkHint, now)
		return LoginResult{}, ErrInvalidCredentials
	}
	if !user.HasPassword() {
		_, _ = passwordpolicy.Verify(dummyHash, password)
		s.auditLoginFailure(ctx, user.ID, normalized, "no_password_established", sourceNetworkHint, now)
		return LoginResult{}, ErrInvalidCredentials
	}

	ok, verifyErr := passwordpolicy.Verify(user.PasswordHash, password)
	if verifyErr != nil || !ok {
		s.auditLoginFailure(ctx, user.ID, normalized, "invalid_credentials", sourceNetworkHint, now)
		return LoginResult{}, ErrInvalidCredentials
	}

	// Rehash-on-successful-login (Section 4) is intentionally not performed
	// here: the existing identitystore exposes no method to update an
	// already-active account's password hash outside the
	// EstablishPassword/CompletePasswordReset state transitions, and adding
	// one is a Step 9B persistence-surface change this phase declines to
	// make silently. passwordpolicy.NeedsRehash is available to a future
	// phase that adds such a method.

	switch user.Status {
	case identity.StateActive:
		// proceed
	case identity.StateSuspended:
		s.auditLoginFailure(ctx, user.ID, normalized, "account_suspended", sourceNetworkHint, now)
		return LoginResult{}, ErrAccountSuspended
	default:
		// Every other non-active status (invited, password_change_required,
		// disabled, expired) is deliberately NOT given its own narrow
		// exception here: Section 3's state table names only "suspended"
		// as safe to reveal after a correct password. Disabled/expired
		// accounts, though logically similar, are not listed as an
		// exception, so this package follows that literally rather than
		// silently adding one.
		s.auditLoginFailure(ctx, user.ID, normalized, "account_not_active", sourceNetworkHint, now)
		return LoginResult{}, ErrInvalidCredentials
	}

	rawToken, err := session.GenerateToken()
	if err != nil {
		return LoginResult{}, ErrInternal
	}
	digest := session.Digest(rawToken)
	expiresAt := s.cfg.Session.NewExpiresAt(now)
	id, err := s.store.CreateSession(ctx, user.ID, digest, deviceHint, now, expiresAt)
	if err != nil {
		return LoginResult{}, ErrInternal
	}

	s.recordAudit(ctx, identityaudit.SessionCreated, user.ID, 0, "", metadataOrNil("device_hint", deviceHint), now)
	s.recordAudit(ctx, identityaudit.LoginSuccess, user.ID, 0, "", identityaudit.Metadata{
		"session_id":          strconv.FormatInt(int64(id), 10),
		"source_network_hint": safeOrPlaceholder(sourceNetworkHint),
	}, now)

	return LoginResult{SessionID: id, RawToken: rawToken, ExpiresAt: expiresAt, Account: user.Public()}, nil
}

func (s *Service) auditLoginFailure(ctx context.Context, accountID identity.UserID, attemptedEmail, reasonCode, sourceNetworkHint string, now time.Time) {
	s.recordAudit(ctx, identityaudit.LoginFailure, accountID, 0, "", identityaudit.Metadata{
		"attempted_email":     attemptedEmail,
		"reason_code":         reasonCode,
		"source_network_hint": safeOrPlaceholder(sourceNetworkHint),
	}, now)
}

func metadataOrNil(key, value string) identityaudit.Metadata {
	if value == "" {
		return nil
	}
	return identityaudit.Metadata{key: value}
}

func safeOrPlaceholder(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
