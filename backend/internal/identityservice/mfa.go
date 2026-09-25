// Step 9F-3 MFA (WebAuthn passkey) enrollment orchestration: beginning and
// finishing a registration ceremony, the server-side in-flight ceremony
// state that bridges Begin/Finish, and the narrow bridging credential that
// lets an administrator who has just set a permanent password but not yet
// enrolled MFA reach this flow at all.
//
// # Why a second, narrower credential exists
//
// Both identitystore.CreateSession and identitystore.ResolveSession
// deliberately, hard-code that a session can only be created for, or
// resolved against, an account currently identity.StateActive (Section 1,
// Section 8: "Sessions for suspended, disabled, expired, or
// password-change-required accounts cannot authorize protected access").
// Login enforces the identical rule. This file does not touch, weaken, or
// add an exception to any of those three checks.
//
// But activateIfEligible (identitystore) only ever promotes an
// administrator role to active once a non-revoked MFA credential already
// exists — so a freshly invited administrator who has just redeemed their
// invitation and established a permanent password is left in
// password_change_required with literally no account state the existing
// session machinery will ever authenticate. Section 2 (steps 4-5) describes
// exactly this moment as one continuous flow ("Forces the user through a
// mandatory permanent-password establishment step (and MFA enrollment, if
// the role requires it)... before any protected route... is reachable...
// Once a permanent password (and required MFA) is established, the account
// moves to active and normal login applies going forward") — implying a
// bridging mechanism distinct from "normal login," which only "applies
// going forward" once both steps are done.
//
// This file supplies that bridge as its own, separate, far narrower
// credential (issued by RedeemInvitationAndSetPassword, resolved by
// ResolveMFAEnrollmentCredential): it authenticates identity for exactly
// one purpose (Begin/FinishMFAEnrollment below), carries a short, fixed
// TTL, is never a session.Session row, and is never accepted by
// ResolveSession or any /api/admin/* route. An already-active caller
// enrolling an additional passkey uses their ordinary session instead; both
// paths converge on the same Begin/FinishMFAEnrollment calls.
//
// # Step 9F-4: verifying an already-established session
//
// Enrollment (above) answers "can this account ever complete MFA."
// Section 5/AAX-07 also requires a second, independent fact before any
// /api/admin/* route is reachable: that the CURRENT SESSION itself
// completed a WebAuthn authentication ceremony — an enrolled credential's
// mere existence, or a correct password, is never sufficient on its own.
// Begin/FinishMFALogin implement that ceremony (a known-user WebAuthn
// assertion, never discoverable/passwordless) against an ALREADY
// authenticated, already-active caller's ordinary session — unlike
// enrollment, no bridging credential is needed here, because the account
// is already active and already holds a normal, valid session. On success,
// FinishMFALogin marks that one session row's sessions.mfa_verified_at
// (identitystore.MarkSessionMFAVerified): never every session for the
// account, and never the mfa_credentials table itself. Because
// ResolveSession's existing Usable() check already fails a revoked or
// expired session before requireAdminMFAVerified (authhttp) ever looks at
// this flag, verification automatically "expires" whenever the session
// itself does — no separate expiry bookkeeping exists or is needed.
package identityservice

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/mfa"
	"greenwich-fire-responder/backend/internal/session"
)

// MFAProvider is the subset of *mfa.Provider's capability this package
// depends on, so a test can substitute a fake that never performs a real
// cryptographic WebAuthn ceremony (that cryptography is backend/internal/mfa's
// own, separately tested responsibility). A *mfa.Provider satisfies this
// interface without any change to that package.
type MFAProvider interface {
	BeginEnrollment(user mfa.EnrollmentUser) (*protocol.CredentialCreation, *webauthn.SessionData, error)
	FinishEnrollment(user mfa.EnrollmentUser, session webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error)
	BeginLogin(user mfa.LoginUser) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
	FinishLogin(user mfa.LoginUser, session webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error)
}

// Sentinel errors for MFA enrollment. None of these, nor any value this
// file returns, ever includes a challenge, attestation object, cookie, or
// raw credential/token value.
var (
	// ErrMFAUnavailable marks that no MFAProvider has been configured
	// (SetMFAProvider was never called): the route exists but is
	// functionally inert, which is how this deployment keeps the feature
	// un-activated in production for now.
	ErrMFAUnavailable = errors.New("identityservice: MFA enrollment is not available")
	// ErrMFANotEligible marks an account whose current status is not
	// password_change_required or active (Section 5, AAX-07).
	ErrMFANotEligible = errors.New("identityservice: account is not eligible for MFA enrollment")
	// ErrMFACeremonyInvalid covers every reason a Finish call's in-flight
	// ceremony state could fail to apply — missing, expired, already
	// consumed (replayed), or bound to a different user or authenticated
	// context — never distinguished from one another to the caller.
	ErrMFACeremonyInvalid = errors.New("identityservice: MFA enrollment ceremony is missing, expired, or already used")
	// ErrMFAResponseInvalid marks a WebAuthn response this package could
	// not verify (malformed JSON, failed signature/origin/challenge
	// verification, or a credential already enrolled for this account).
	ErrMFAResponseInvalid = errors.New("identityservice: MFA enrollment response could not be verified")
)

const (
	// mfaCeremonyTTL bounds how long a Begin call's challenge remains
	// finishable — generous for a human completing a WebAuthn prompt,
	// tight enough that an abandoned ceremony does not linger.
	mfaCeremonyTTL = 5 * time.Minute
	// maxPendingMFACeremonies bounds this process's in-memory ceremony
	// state (Step 9F-3: "bounded, expiring" in-flight state).
	maxPendingMFACeremonies = 10000

	// mfaEnrollmentCredentialTTL bounds the bridging credential described
	// in this file's package doc comment: long enough to complete a
	// WebAuthn prompt right after setting a password, short enough that an
	// abandoned first-time-login cannot be resumed indefinitely (an
	// administrator who misses this window is still recoverable through
	// the existing AdminReset flow, which re-issues a fresh invitation).
	mfaEnrollmentCredentialTTL = 15 * time.Minute
	// maxPendingMFAEnrollmentCredentials bounds this process's in-memory
	// bridging-credential state.
	maxPendingMFAEnrollmentCredentials = 10000
)

// mfaCeremonyPurpose distinguishes a registration ceremony from an
// authentication (login-verification) ceremony sharing the same
// mfaCeremonyStore and the same binding-key scheme: without this tag, an
// admin's already-active session beginning an enrollment (adding a second
// passkey) and a login-verification ceremony in quick succession could
// silently overwrite one another's in-flight state and let Finish
// misinterpret which kind of response it is validating.
type mfaCeremonyPurpose string

const (
	mfaCeremonyPurposeEnroll mfaCeremonyPurpose = "enroll"
	mfaCeremonyPurposeLogin  mfaCeremonyPurpose = "login"
)

// mfaCeremony is the in-flight WebAuthn ceremony state bridging one Begin
// call to its Finish call. It is held only in process memory, never
// serialized to a store, log, or audit record.
type mfaCeremony struct {
	userID    identity.UserID
	purpose   mfaCeremonyPurpose
	data      webauthn.SessionData
	expiresAt time.Time
}

// mfaCeremonyStore is a bounded, expiring, in-process map of in-flight
// ceremonies keyed by an opaque binding key: this package uses the SHA-256
// digest of the caller's current authenticated raw token (session or
// bridging credential), so a ceremony begun under one authenticated context
// can never be found, let alone finished, under another. Every lookup is a
// take: found-or-not, a key is deleted the moment it is read, so a ceremony
// can be finished at most once (Step 9F-3: "one enrollment attempt," and
// the required replay test).
type mfaCeremonyStore struct {
	mu    sync.Mutex
	byKey map[string]mfaCeremony
}

func newMFACeremonyStore() *mfaCeremonyStore {
	return &mfaCeremonyStore{byKey: make(map[string]mfaCeremony)}
}

// put replaces any previous ceremony under key: only the most recently
// begun challenge for a given authenticated context is ever valid to
// finish.
func (st *mfaCeremonyStore) put(key string, c mfaCeremony, now time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked(now)
	if _, exists := st.byKey[key]; !exists && len(st.byKey) >= maxPendingMFACeremonies {
		return ErrInternal
	}
	st.byKey[key] = c
	return nil
}

// take retrieves and unconditionally deletes the ceremony for key, so a
// second call — whether a genuine replay of a successful finish or a retry
// after a malformed one — always misses, regardless of the first call's
// outcome.
func (st *mfaCeremonyStore) take(key string, now time.Time) (mfaCeremony, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	c, ok := st.byKey[key]
	delete(st.byKey, key)
	if !ok || !now.Before(c.expiresAt) {
		return mfaCeremony{}, false
	}
	return c, true
}

func (st *mfaCeremonyStore) sweepLocked(now time.Time) {
	for k, c := range st.byKey {
		if !now.Before(c.expiresAt) {
			delete(st.byKey, k)
		}
	}
}

// enrollmentCredentialEntry is the bridging credential described in this
// file's package doc comment: it proves identity for exactly one userID,
// nothing else — no role/scope claim is trusted from it, since
// ResolveMFAEnrollmentCredential re-derives those fresh from the store on
// every use.
type enrollmentCredentialEntry struct {
	userID    identity.UserID
	expiresAt time.Time
}

// enrollmentCredentialStore is a bounded, expiring, in-process map of
// pending bridging credentials, keyed by the SHA-256 digest of the raw
// token.
type enrollmentCredentialStore struct {
	mu    sync.Mutex
	byKey map[string]enrollmentCredentialEntry
}

func newEnrollmentCredentialStore() *enrollmentCredentialStore {
	return &enrollmentCredentialStore{byKey: make(map[string]enrollmentCredentialEntry)}
}

func (st *enrollmentCredentialStore) issue(key string, userID identity.UserID, expiresAt, now time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked(now)
	if _, exists := st.byKey[key]; !exists && len(st.byKey) >= maxPendingMFAEnrollmentCredentials {
		return ErrInternal
	}
	st.byKey[key] = enrollmentCredentialEntry{userID: userID, expiresAt: expiresAt}
	return nil
}

// resolve reports the credential's bound userID without consuming it: the
// bridging credential authorizes the entire enrollment window (Begin may be
// retried), not a single request.
func (st *enrollmentCredentialStore) resolve(key string, now time.Time) (identity.UserID, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked(now)
	e, ok := st.byKey[key]
	if !ok || !now.Before(e.expiresAt) {
		return 0, false
	}
	return e.userID, true
}

// revoke removes every pending credential for userID. Called once
// enrollment succeeds: a completed bridging credential has no further
// purpose and must not remain usable.
func (st *enrollmentCredentialStore) revoke(userID identity.UserID) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, e := range st.byKey {
		if e.userID == userID {
			delete(st.byKey, k)
		}
	}
}

func (st *enrollmentCredentialStore) sweepLocked(now time.Time) {
	for k, e := range st.byKey {
		if !now.Before(e.expiresAt) {
			delete(st.byKey, k)
		}
	}
}

// SetMFAProvider attaches the WebAuthn passkey provider. Until this is
// called, every MFA enrollment operation fails closed with
// ErrMFAUnavailable.
func (s *Service) SetMFAProvider(p MFAProvider) {
	s.mfaProvider = p
}

// mfaEnrollmentEligible mirrors identitystore.EnrollMFACredential's own
// gate: an account may enroll MFA only while password_change_required (the
// pending-first-enrollment case) or active (adding another passkey); every
// other status is refused.
func mfaEnrollmentEligible(u identity.User) bool {
	return u.Status == identity.StatePasswordChangeRequired || u.Status == identity.StateActive
}

// MFAEnrollmentPrincipal is the safe identity information
// ResolveMFAEnrollmentCredential returns. It carries no session.ID: no
// session.Session row backs this credential.
type MFAEnrollmentPrincipal struct {
	UserID      identity.UserID
	Role        identity.Role
	Scope       identity.Scope
	Email       string
	DisplayName string
	Status      identity.AccountState
}

// ResolveMFAEnrollmentCredential authenticates rawToken against the
// bridging credential store and re-verifies current eligibility fresh from
// the store on every call, never trusting a stale snapshot: an account
// suspended or disabled after the credential was issued fails closed here,
// identically to an unknown or expired token.
func (s *Service) ResolveMFAEnrollmentCredential(ctx context.Context, rawToken string, now time.Time) (MFAEnrollmentPrincipal, error) {
	if rawToken == "" {
		return MFAEnrollmentPrincipal{}, ErrSessionInvalid
	}
	userID, ok := s.mfaEnrollCreds.resolve(string(session.Digest(rawToken)), now)
	if !ok {
		return MFAEnrollmentPrincipal{}, ErrSessionInvalid
	}
	user, err := s.store.GetByID(ctx, userID)
	if err != nil || !mfaEnrollmentEligible(user) {
		return MFAEnrollmentPrincipal{}, ErrSessionInvalid
	}
	return MFAEnrollmentPrincipal{
		UserID: user.ID, Role: user.Role, Scope: user.Scope,
		Email: user.NormalizedEmail, DisplayName: user.DisplayName, Status: user.Status,
	}, nil
}

// issueMFAEnrollmentCredential is called only by
// RedeemInvitationAndSetPassword, only at the exact moment an
// administrator's permanent password was just established but MFA is still
// pending. Issuance failing (at capacity) is deliberately non-fatal to the
// caller: the password is already set either way, and an administrator can
// still be bridged later via AdminReset's fresh invitation.
func (s *Service) issueMFAEnrollmentCredential(userID identity.UserID, now time.Time) (rawToken string, expiresAt time.Time, err error) {
	rawToken, err = session.GenerateToken()
	if err != nil {
		return "", time.Time{}, ErrInternal
	}
	expiresAt = now.Add(mfaEnrollmentCredentialTTL)
	if err := s.mfaEnrollCreds.issue(string(session.Digest(rawToken)), userID, expiresAt, now); err != nil {
		return "", time.Time{}, err
	}
	return rawToken, expiresAt, nil
}

// existingMFACredentials decodes every currently non-revoked stored
// credential for userID into the form the WebAuthn library needs to build a
// registration exclusion list. A row that fails to decode is skipped rather
// than failing the whole call: it can never block a legitimate new
// enrollment attempt over one unreadable historical record.
func (s *Service) existingMFACredentials(ctx context.Context, userID identity.UserID) ([]webauthn.Credential, error) {
	rows, err := s.store.ListMFACredentials(ctx, userID)
	if err != nil {
		return nil, ErrInternal
	}
	out := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		decoded, decodeErr := mfa.UnmarshalCredential(row.CredentialData)
		if decodeErr != nil {
			continue
		}
		out = append(out, *decoded)
	}
	return out, nil
}

// BeginMFAEnrollment starts a WebAuthn passkey registration ceremony for
// userID, binding the resulting in-flight challenge to bindingRawToken —
// the caller's own currently authenticated raw token (session or bridging
// credential) — so Finish can only ever be reached from that identical
// authenticated context. Any previously begun, unfinished ceremony under
// the same bindingRawToken is discarded: only the most recent Begin's
// challenge is ever valid to finish.
func (s *Service) BeginMFAEnrollment(ctx context.Context, userID identity.UserID, bindingRawToken string, now time.Time) (*protocol.CredentialCreation, error) {
	if s.mfaProvider == nil {
		return nil, ErrMFAUnavailable
	}
	if bindingRawToken == "" {
		return nil, ErrMFACeremonyInvalid
	}
	user, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return nil, ErrInternal
	}
	if !mfaEnrollmentEligible(user) {
		return nil, ErrMFANotEligible
	}
	existing, err := s.existingMFACredentials(ctx, userID)
	if err != nil {
		return nil, err
	}

	creation, sessionData, err := s.mfaProvider.BeginEnrollment(mfa.EnrollmentUser{
		ID: user.ID, Email: user.NormalizedEmail, DisplayName: user.DisplayName, Credentials: existing,
	})
	if err != nil {
		return nil, ErrMFAResponseInvalid
	}
	key := string(session.Digest(bindingRawToken))
	if err := s.mfaCeremonies.put(key, mfaCeremony{
		userID: userID, purpose: mfaCeremonyPurposeEnroll, data: *sessionData, expiresAt: now.Add(mfaCeremonyTTL),
	}, now); err != nil {
		return nil, err
	}
	return creation, nil
}

// FinishMFAEnrollment verifies responseJSON against the ceremony begun
// under bindingRawToken, persists the resulting credential, records exactly
// one MFAEnrollment audit event using the fixed, safe metadata
// mfa.EnrollmentAuditMetadata provides, and returns the account's resulting
// status. activateIfEligible runs inside identitystore.EnrollMFACredential,
// so an administrator whose only remaining requirement was MFA becomes
// active in this same call.
func (s *Service) FinishMFAEnrollment(ctx context.Context, userID identity.UserID, bindingRawToken string, responseJSON []byte, now time.Time) (identity.AccountState, error) {
	if s.mfaProvider == nil {
		return "", ErrMFAUnavailable
	}
	if bindingRawToken == "" {
		return "", ErrMFACeremonyInvalid
	}
	key := string(session.Digest(bindingRawToken))
	ceremony, ok := s.mfaCeremonies.take(key, now)
	if !ok || ceremony.purpose != mfaCeremonyPurposeEnroll || ceremony.userID != userID {
		return "", ErrMFACeremonyInvalid
	}

	user, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return "", ErrInternal
	}
	if !mfaEnrollmentEligible(user) {
		return "", ErrMFANotEligible
	}

	cred, err := s.mfaProvider.FinishEnrollment(mfa.EnrollmentUser{
		ID: user.ID, Email: user.NormalizedEmail, DisplayName: user.DisplayName,
	}, ceremony.data, responseJSON)
	if err != nil {
		return "", ErrMFAResponseInvalid
	}
	encoded, err := mfa.MarshalCredential(cred)
	if err != nil {
		return "", ErrMFAResponseInvalid
	}

	newStatus, err := s.store.EnrollMFACredential(ctx, userID, mfa.CredentialTypePasskey, encoded, "")
	if err != nil {
		if errors.Is(err, identitystore.ErrConflict) {
			return "", ErrMFAResponseInvalid
		}
		if errors.Is(err, identitystore.ErrInvalidTransition) {
			return "", ErrMFANotEligible
		}
		return "", ErrInternal
	}

	s.recordAudit(ctx, identityaudit.MFAEnrollment, userID, 0, "", mfa.EnrollmentAuditMetadata(), now)
	s.mfaEnrollCreds.revoke(userID)
	return newStatus, nil
}

// BeginMFALogin starts a WebAuthn authentication ceremony proving
// possession of one of userID's already-enrolled credentials (Step 9F-4,
// AAX-07), binding the resulting in-flight challenge to bindingRawToken —
// the caller's own currently authenticated, already-established session
// token. This never itself authenticates the account (email/password
// already did that, per Section 3); it exists solely to let
// FinishMFALogin mark this one session verified. userID must currently be
// active and must have at least one enrolled credential (ErrMFANotEligible
// otherwise: there is nothing to prove possession of, and a
// password_change_required account has no session this ceremony could
// even be reached from — see this file's package doc comment).
func (s *Service) BeginMFALogin(ctx context.Context, userID identity.UserID, bindingRawToken string, now time.Time) (*protocol.CredentialAssertion, error) {
	if s.mfaProvider == nil {
		return nil, ErrMFAUnavailable
	}
	if bindingRawToken == "" {
		return nil, ErrMFACeremonyInvalid
	}
	user, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return nil, ErrInternal
	}
	if user.Status != identity.StateActive {
		return nil, ErrMFANotEligible
	}
	existing, err := s.existingMFACredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		return nil, ErrMFANotEligible
	}

	assertion, sessionData, err := s.mfaProvider.BeginLogin(mfa.LoginUser{
		ID: user.ID, Email: user.NormalizedEmail, DisplayName: user.DisplayName, Credentials: existing,
	})
	if err != nil {
		return nil, ErrMFAResponseInvalid
	}
	key := string(session.Digest(bindingRawToken))
	if err := s.mfaCeremonies.put(key, mfaCeremony{
		userID: userID, purpose: mfaCeremonyPurposeLogin, data: *sessionData, expiresAt: now.Add(mfaCeremonyTTL),
	}, now); err != nil {
		return nil, err
	}
	return assertion, nil
}

// FinishMFALogin verifies responseJSON against the login-verification
// ceremony begun under bindingRawToken and, only on success, marks
// sessionID itself as MFA-verified (identitystore.MarkSessionMFAVerified)
// and records exactly one MFAVerification audit event — never any other
// session belonging to the account, and never the account's
// mfa_credentials rows. sessionID is trusted as the caller's own current
// session (the HTTP layer resolves it via the identical requireSession
// path every other protected route uses); this function does not
// separately re-derive it from bindingRawToken. If sessionID was revoked,
// expired, or otherwise no longer exists by the time this call reaches the
// store (e.g. an admin-initiated revocation racing this exact request),
// MarkSessionMFAVerified reports that as identitystore.ErrNotFound, which
// this function maps to ErrSessionInvalid and returns BEFORE recording any
// audit event: a caller must never see a successful response, and the
// audit trail must never show a completed verification, for a session that
// was not actually updated.
func (s *Service) FinishMFALogin(ctx context.Context, userID identity.UserID, sessionID session.ID, bindingRawToken string, responseJSON []byte, now time.Time) error {
	if s.mfaProvider == nil {
		return ErrMFAUnavailable
	}
	if bindingRawToken == "" {
		return ErrMFACeremonyInvalid
	}
	key := string(session.Digest(bindingRawToken))
	ceremony, ok := s.mfaCeremonies.take(key, now)
	if !ok || ceremony.purpose != mfaCeremonyPurposeLogin || ceremony.userID != userID {
		return ErrMFACeremonyInvalid
	}

	user, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return ErrInternal
	}
	if user.Status != identity.StateActive {
		return ErrMFANotEligible
	}
	existing, err := s.existingMFACredentials(ctx, userID)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return ErrMFANotEligible
	}

	if _, err := s.mfaProvider.FinishLogin(mfa.LoginUser{
		ID: user.ID, Email: user.NormalizedEmail, DisplayName: user.DisplayName, Credentials: existing,
	}, ceremony.data, responseJSON); err != nil {
		return ErrMFAResponseInvalid
	}

	if err := s.store.MarkSessionMFAVerified(ctx, sessionID, now); err != nil {
		if errors.Is(err, identitystore.ErrNotFound) {
			return ErrSessionInvalid
		}
		return ErrInternal
	}
	s.recordAudit(ctx, identityaudit.MFAVerification, userID, 0, "", mfa.EnrollmentAuditMetadata(), now)
	return nil
}
