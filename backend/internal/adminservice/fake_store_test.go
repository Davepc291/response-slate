package adminservice

// A minimal in-memory fake of this package's Store/AuditRecorder
// dependencies, used only to exercise adminservice's own authorization and
// orchestration logic without a live database, mirroring
// identityservice's/authhttp's own test-fake convention (each layer gets
// its own lightweight double).

import (
	"context"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
)

type fakeInvitation struct {
	userID    identity.UserID
	status    string
	expiresAt time.Time
}

type fakeStore struct {
	mu sync.Mutex

	nextUserID     int64
	users          map[identity.UserID]*identity.User
	invitations    map[string]*fakeInvitation
	activeSessions map[identity.UserID]int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:          make(map[identity.UserID]*identity.User),
		invitations:    make(map[string]*fakeInvitation),
		activeSessions: make(map[identity.UserID]int64),
	}
}

func digestKey(d []byte) string { return hex.EncodeToString(d) }

func (f *fakeStore) seedUser(u identity.User) identity.UserID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextUserID++
	u.ID = identity.UserID(f.nextUserID)
	f.users[u.ID] = &u
	return u.ID
}

func (f *fakeStore) seedSessions(userID identity.UserID, n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeSessions[userID] += n
}

func (f *fakeStore) activeSessionCount(userID identity.UserID) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeSessions[userID]
}

func (f *fakeStore) pendingInvitationCount(userID identity.UserID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, inv := range f.invitations {
		if inv.userID == userID && inv.status == "pending" {
			n++
		}
	}
	return n
}

func (f *fakeStore) CreateUserAndInvite(_ context.Context, params identitystore.NewUserParams) (identity.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.NormalizedEmail == params.Email {
			return 0, identitystore.ErrConflict
		}
	}
	f.nextUserID++
	id := identity.UserID(f.nextUserID)
	f.users[id] = &identity.User{
		ID: id, NormalizedEmail: params.Email, DisplayName: params.DisplayName,
		Role: params.Role, Scope: params.Scope, Status: identity.StateInvited,
		CreatedByID: params.CreatedBy,
	}
	f.invitations[digestKey(params.InvitationTokenDigest)] = &fakeInvitation{
		userID: id, status: "pending", expiresAt: params.InvitationExpiresAt,
	}
	return id, nil
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

func (f *fakeStore) ListUsers(_ context.Context, filter identitystore.ListUsersFilter) ([]identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []identity.User
	for _, u := range f.users {
		if filter.Scope != nil && u.Scope != *filter.Scope {
			continue
		}
		if filter.Role != nil && u.Role != *filter.Role {
			continue
		}
		if filter.Status != nil && u.Status != *filter.Status {
			continue
		}
		if filter.Search != "" {
			s := strings.ToLower(filter.Search)
			if !strings.Contains(strings.ToLower(u.NormalizedEmail), s) && !strings.Contains(strings.ToLower(u.DisplayName), s) {
				continue
			}
		}
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	limit := filter.Limit
	if limit <= 0 {
		limit = 25
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return nil, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (f *fakeStore) transition(userID identity.UserID, to identity.AccountState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	if !identity.AllowedTransition(u.Status, to) {
		return identitystore.ErrInvalidTransition
	}
	u.Status = to
	f.activeSessions[userID] = 0
	return nil
}

func (f *fakeStore) Suspend(_ context.Context, userID identity.UserID, _ time.Time) error {
	return f.transition(userID, identity.StateSuspended)
}

func (f *fakeStore) Disable(_ context.Context, userID identity.UserID, _ time.Time) error {
	return f.transition(userID, identity.StateDisabled)
}

func (f *fakeStore) Restore(_ context.Context, userID identity.UserID, now time.Time) (identity.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return "", identitystore.ErrNotFound
	}
	if u.Status != identity.StateSuspended && u.Status != identity.StateDisabled {
		return "", identitystore.ErrInvalidTransition
	}
	target := identity.StateExpired
	if u.HasPassword() {
		target = identity.StateActive
	} else {
		for _, inv := range f.invitations {
			if inv.userID == userID && inv.status == "pending" && now.Before(inv.expiresAt) {
				target = identity.StateInvited
			}
		}
	}
	u.Status = target
	return target, nil
}

func (f *fakeStore) Revoke(_ context.Context, userID identity.UserID, _ time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[userID]; !ok {
		return 0, identitystore.ErrNotFound
	}
	count := f.activeSessions[userID]
	f.activeSessions[userID] = 0
	return count, nil
}

func (f *fakeStore) ChangeRole(_ context.Context, params identitystore.ChangeRoleParams) (identitystore.PriorRoleScope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[params.UserID]
	if !ok {
		return identitystore.PriorRoleScope{}, identitystore.ErrNotFound
	}
	prior := identitystore.PriorRoleScope{Role: u.Role, Scope: u.Scope}
	u.Role = params.NewRole
	u.Scope = params.NewScope
	return prior, nil
}

func (f *fakeStore) ResendInvitation(_ context.Context, userID, _ identity.UserID, newTokenDigest []byte, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	if u.Status != identity.StateInvited && u.Status != identity.StateExpired {
		return identitystore.ErrInvalidTransition
	}
	for _, inv := range f.invitations {
		if inv.userID == userID && inv.status == "pending" {
			inv.status = "superseded"
		}
	}
	f.invitations[digestKey(newTokenDigest)] = &fakeInvitation{userID: userID, status: "pending", expiresAt: expiresAt}
	u.Status = identity.StateInvited
	return nil
}

func (f *fakeStore) AdminReset(_ context.Context, userID, _ identity.UserID, newTokenDigest []byte, expiresAt, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	switch u.Status {
	case identity.StateActive, identity.StateSuspended, identity.StatePasswordChangeRequired:
	default:
		return identitystore.ErrInvalidTransition
	}
	for _, inv := range f.invitations {
		if inv.userID == userID && inv.status == "pending" {
			inv.status = "superseded"
		}
	}
	f.invitations[digestKey(newTokenDigest)] = &fakeInvitation{userID: userID, status: "pending", expiresAt: expiresAt}
	u.Status = identity.StatePasswordChangeRequired
	u.PasswordHash = ""
	f.activeSessions[userID] = 0
	return nil
}

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
