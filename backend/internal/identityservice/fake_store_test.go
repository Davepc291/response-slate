package identityservice

// A minimal, in-memory fake of the Store and AuditRecorder interfaces, so
// this package's own logic can be unit-tested without a live database. It
// deliberately mirrors identitystore's documented semantics only as far as
// this package's tests need (single-user-at-a-time invitation/reset state,
// no concurrent-transaction races), never claiming to be a full
// reimplementation of identitystore's own, separately tested behavior.

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/session"
)

type fakeInvitation struct {
	userID    identity.UserID
	status    string // pending, redeemed, expired
	expiresAt time.Time
}

type fakeReset struct {
	userID    identity.UserID
	status    string
	expiresAt time.Time
}

type fakeSession struct {
	session.Session
}

type fakeMFACredential struct {
	credentialType string
	credentialData []byte
	label          string
	revoked        bool
}

type fakeStore struct {
	mu sync.Mutex

	nextUserID    int64
	nextSessionID int64
	users         map[identity.UserID]*identity.User
	invitations   map[string]*fakeInvitation
	resets        map[string]*fakeReset
	sessions      map[session.ID]*fakeSession
	mfaCreds      map[identity.UserID][]*fakeMFACredential
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:       make(map[identity.UserID]*identity.User),
		invitations: make(map[string]*fakeInvitation),
		resets:      make(map[string]*fakeReset),
		sessions:    make(map[session.ID]*fakeSession),
		mfaCreds:    make(map[identity.UserID][]*fakeMFACredential),
	}
}

func digestKey(d []byte) string { return hex.EncodeToString(d) }

// seedUser creates a user directly (bypassing invitation issuance, which is
// out of this service's HTTP scope) and returns its id.
func (f *fakeStore) seedUser(u identity.User) identity.UserID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextUserID++
	u.ID = identity.UserID(f.nextUserID)
	f.users[u.ID] = &u
	return u.ID
}

// seedInvitation registers a pending invitation for userID under
// tokenDigest, for a test to later redeem via the service.
func (f *fakeStore) seedInvitation(userID identity.UserID, tokenDigest []byte, expiresAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invitations[digestKey(tokenDigest)] = &fakeInvitation{userID: userID, status: "pending", expiresAt: expiresAt}
}

func (f *fakeStore) GetByEmail(_ context.Context, email string) (identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.NormalizedEmail == email {
			return *u, nil
		}
	}
	return identity.User{}, identitystore.ErrNotFound
}

func (f *fakeStore) GetByID(_ context.Context, id identity.UserID) (identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return identity.User{}, identitystore.ErrNotFound
	}
	return *u, nil
}

func (f *fakeStore) EstablishPassword(_ context.Context, userID identity.UserID, passwordHash string, now time.Time) (identity.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok || u.Status != identity.StatePasswordChangeRequired {
		return "", identitystore.ErrInvalidTransition
	}
	u.PasswordHash = passwordHash
	u.PasswordUpdatedAt = now
	if u.Role.RequiresMFA() {
		return identity.StatePasswordChangeRequired, nil
	}
	u.Status = identity.StateActive
	return identity.StateActive, nil
}

func (f *fakeStore) RedeemInvitation(_ context.Context, tokenDigest []byte, now time.Time) (identity.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.invitations[digestKey(tokenDigest)]
	if !ok || inv.status != "pending" {
		return 0, identitystore.ErrTokenInvalid
	}
	if !now.Before(inv.expiresAt) {
		inv.status = "expired"
		return 0, identitystore.ErrTokenExpired
	}
	inv.status = "redeemed"
	if u, ok := f.users[inv.userID]; ok {
		u.Status = identity.StatePasswordChangeRequired
	}
	return inv.userID, nil
}

func (f *fakeStore) CreateSession(_ context.Context, userID identity.UserID, tokenDigest []byte, deviceHint string, createdAt, expiresAt time.Time) (session.ID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok || u.Status != identity.StateActive {
		return 0, identitystore.ErrAccountNotActive
	}
	f.nextSessionID++
	id := session.ID(f.nextSessionID)
	f.sessions[id] = &fakeSession{session.Session{
		ID: id, UserID: int64(userID), TokenDigest: tokenDigest, DeviceHint: deviceHint,
		CreatedAt: createdAt, LastSeenAt: createdAt, ExpiresAt: expiresAt,
	}}
	return id, nil
}

func (f *fakeStore) ResolveSession(_ context.Context, tokenDigest []byte, cfg session.Config, now time.Time) (identitystore.Authorized, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if digestKey(s.TokenDigest) != digestKey(tokenDigest) {
			continue
		}
		if !cfg.Usable(s.Session, now) {
			return identitystore.Authorized{}, identitystore.ErrTokenInvalid
		}
		u, ok := f.users[identity.UserID(s.UserID)]
		if !ok || u.Status != identity.StateActive {
			return identitystore.Authorized{}, identitystore.ErrTokenInvalid
		}
		return identitystore.Authorized{Session: s.Session, Account: u.Public(), Role: u.Role, Scope: u.Scope}, nil
	}
	return identitystore.Authorized{}, identitystore.ErrTokenInvalid
}

// MarkSessionMFAVerified mirrors identitystore's real Postgres semantics
// (Step 9F-4/9F-7K): unrevoked AND unexpired-as-of-now only, returning
// identitystore.ErrNotFound — never a silent success — for a revoked,
// expired, or unknown session, so identityservice tests can exercise the
// exact fail-closed contract FinishMFALogin depends on without a live
// database.
func (f *fakeStore) MarkSessionMFAVerified(_ context.Context, id session.ID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || s.RevokedAt != nil || !now.Before(s.ExpiresAt) {
		return identitystore.ErrNotFound
	}
	t := now
	s.MFAVerifiedAt = &t
	return nil
}

func (f *fakeStore) TouchSession(_ context.Context, id session.ID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || s.RevokedAt != nil {
		return identitystore.ErrNotFound
	}
	s.LastSeenAt = now
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, id session.ID, reason session.RevocationReason, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || s.RevokedAt != nil {
		return nil // idempotent, matching identitystore.RevokeSession
	}
	t := now
	s.RevokedAt = &t
	s.RevocationReason = reason
	return nil
}

func (f *fakeStore) RevokeAllSessionsForUser(_ context.Context, userID identity.UserID, reason session.RevocationReason, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, s := range f.sessions {
		if identity.UserID(s.UserID) != userID || s.RevokedAt != nil {
			continue
		}
		t := now
		s.RevokedAt = &t
		s.RevocationReason = reason
		count++
	}
	return count, nil
}

func (f *fakeStore) ListActiveSessions(_ context.Context, userID identity.UserID) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []session.Session
	for _, s := range f.sessions {
		if identity.UserID(s.UserID) == userID && s.RevokedAt == nil {
			out = append(out, s.Session)
		}
	}
	return out, nil
}

func (f *fakeStore) RequestPasswordReset(_ context.Context, email string, issuedBy identity.UserID, newTokenDigest []byte, expiresAt time.Time) (bool, identity.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		return false, 0, identitystore.ErrInput
	}
	var target *identity.User
	for _, u := range f.users {
		if u.NormalizedEmail == normalized {
			target = u
			break
		}
	}
	if target == nil {
		return false, 0, nil
	}
	switch target.Status {
	case identity.StateActive, identity.StatePasswordChangeRequired:
	default:
		return false, 0, nil
	}
	for _, r := range f.resets {
		if r.userID == target.ID && r.status == "pending" {
			r.status = "superseded"
		}
	}
	f.resets[digestKey(newTokenDigest)] = &fakeReset{userID: target.ID, status: "pending", expiresAt: expiresAt}
	return true, target.ID, nil
}

func (f *fakeStore) CompletePasswordReset(_ context.Context, rawTokenDigest []byte, newPasswordHash string, now time.Time) (identity.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.resets[digestKey(rawTokenDigest)]
	if !ok || r.status != "pending" {
		return 0, identitystore.ErrTokenInvalid
	}
	if !now.Before(r.expiresAt) {
		r.status = "expired"
		return 0, identitystore.ErrTokenExpired
	}
	u, ok := f.users[r.userID]
	if !ok {
		return 0, identitystore.ErrTokenInvalid
	}
	switch u.Status {
	case identity.StateActive, identity.StatePasswordChangeRequired:
	default:
		return 0, identitystore.ErrInvalidTransition
	}
	r.status = "redeemed"
	u.PasswordHash = newPasswordHash
	u.PasswordUpdatedAt = now
	if !u.Role.RequiresMFA() {
		u.Status = identity.StateActive
	}
	for _, s := range f.sessions {
		if identity.UserID(s.UserID) == r.userID && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
			s.RevocationReason = session.RevokedPasswordReset
		}
	}
	return r.userID, nil
}

func (f *fakeStore) EnrollMFACredential(_ context.Context, userID identity.UserID, credentialType string, credentialData []byte, label string) (identity.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if userID == 0 || credentialType != "passkey" || len(credentialData) == 0 {
		return "", identitystore.ErrInput
	}
	u, ok := f.users[userID]
	if !ok {
		return "", identitystore.ErrInvalidTransition
	}
	if u.Status != identity.StatePasswordChangeRequired && u.Status != identity.StateActive {
		return "", identitystore.ErrInvalidTransition
	}
	for _, c := range f.mfaCreds[userID] {
		if !c.revoked && c.credentialType == credentialType && string(c.credentialData) == string(credentialData) {
			return "", identitystore.ErrConflict
		}
	}
	f.mfaCreds[userID] = append(f.mfaCreds[userID], &fakeMFACredential{
		credentialType: credentialType, credentialData: credentialData, label: label,
	})
	if u.Status == identity.StatePasswordChangeRequired && u.Role.RequiresMFA() {
		u.Status = identity.StateActive
	}
	return u.Status, nil
}

func (f *fakeStore) ListMFACredentials(_ context.Context, userID identity.UserID) ([]identitystore.MFACredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []identitystore.MFACredential
	for i, c := range f.mfaCreds[userID] {
		if c.revoked {
			continue
		}
		out = append(out, identitystore.MFACredential{
			ID: int64(i + 1), CredentialType: c.credentialType, CredentialData: c.credentialData, Label: c.label,
		})
	}
	return out, nil
}

// fakeAudit records every event in memory for assertions, never touching a
// database.
type fakeAudit struct {
	mu     sync.Mutex
	events []identityaudit.Event
}

func (f *fakeAudit) Record(_ context.Context, ev identityaudit.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return int64(len(f.events)), nil
}

func (f *fakeAudit) snapshot() []identityaudit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]identityaudit.Event, len(f.events))
	copy(out, f.events)
	return out
}
