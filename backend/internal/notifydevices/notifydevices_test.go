package notifydevices

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// validP256dh/validAuth are computed, not hand-typed literals, so the test
// itself documents exactly what shape a real Push API subscription's key
// material has (Section 4.1, RFC 8291): a 65-byte uncompressed P-256 point
// and a 16-byte secret, both base64url-encoded.
func validP256dh() string {
	b := make([]byte, p256dhDecodedLen)
	b[0] = 0x04 // uncompressed EC point marker
	return base64.RawURLEncoding.EncodeToString(b)
}

func validAuth() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, authDecodedLen))
}

func validSubscription(endpoint string) Subscription {
	if endpoint == "" {
		endpoint = "https://push.example.com/send/abc123"
	}
	return Subscription{
		Endpoint: endpoint,
		Keys:     Keys{P256dh: validP256dh(), Auth: validAuth()},
	}
}

func TestSubscriptionValidate(t *testing.T) {
	if err := validSubscription("").Validate(); err != nil {
		t.Fatalf("expected a well-formed subscription to validate, got %v", err)
	}
}

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantErr  error
	}{
		{"empty", "", ErrInvalidEndpoint},
		{"http not https", "http://push.example.com/send/abc", ErrInvalidEndpoint},
		{"no scheme", "push.example.com/send/abc", ErrInvalidEndpoint},
		{"malformed", "https://", ErrInvalidEndpoint},
		{"too long", "https://push.example.com/" + strings.Repeat("a", maxEndpointLen), ErrInvalidEndpoint},
		{"valid", "https://push.example.com/send/abc123", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Built directly, not through validSubscription's "" -> default
			// convenience substitution, so an intentionally empty endpoint
			// is actually exercised.
			sub := Subscription{
				Endpoint: c.endpoint,
				Keys:     Keys{P256dh: validP256dh(), Auth: validAuth()},
			}
			err := sub.Validate()
			if c.wantErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if c.wantErr != nil && err != c.wantErr {
				t.Fatalf("expected %v, got %v", c.wantErr, err)
			}
		})
	}
}

func TestValidateP256dh(t *testing.T) {
	tooShort := base64.RawURLEncoding.EncodeToString(make([]byte, p256dhDecodedLen-1))
	tooLong := base64.RawURLEncoding.EncodeToString(make([]byte, p256dhDecodedLen+1))
	padded := base64.URLEncoding.EncodeToString(make([]byte, p256dhDecodedLen))

	cases := []struct {
		name    string
		p256dh  string
		wantErr bool
	}{
		{"empty", "", true},
		{"not base64url", "not-valid-base64!!!", true},
		{"decodes too short", tooShort, true},
		{"decodes too long", tooLong, true},
		{"valid unpadded", validP256dh(), false},
		{"valid padded", padded, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := validSubscription("")
			sub.Keys.P256dh = c.p256dh
			err := sub.Validate()
			if c.wantErr && err != ErrInvalidP256dh {
				t.Fatalf("expected ErrInvalidP256dh, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidateAuth(t *testing.T) {
	tooShort := base64.RawURLEncoding.EncodeToString(make([]byte, authDecodedLen-1))
	tooLong := base64.RawURLEncoding.EncodeToString(make([]byte, authDecodedLen+1))
	padded := base64.URLEncoding.EncodeToString(make([]byte, authDecodedLen))

	cases := []struct {
		name    string
		auth    string
		wantErr bool
	}{
		{"empty", "", true},
		{"not base64url", "!!!not-valid", true},
		{"decodes too short", tooShort, true},
		{"decodes too long", tooLong, true},
		{"valid unpadded", validAuth(), false},
		{"valid padded", padded, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := validSubscription("")
			sub.Keys.Auth = c.auth
			err := sub.Validate()
			if c.wantErr && err != ErrInvalidAuth {
				t.Fatalf("expected ErrInvalidAuth, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidatePlatform(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		wantErr  bool
	}{
		{"empty is allowed (UX-only hint)", "", false},
		{"simple label", "android-chrome", false},
		{"label with spaces", "iOS Safari 17", false},
		{"unsupported characters", "android<script>", true},
		{"too long", strings.Repeat("a", maxPlatformLen+1), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePlatform(c.platform)
			if c.wantErr && err != ErrInvalidPlatform {
				t.Fatalf("expected ErrInvalidPlatform, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

const (
	alice identity.UserID = 1
	bob   identity.UserID = 2
)

func TestRegisterRejectsInvalidInput(t *testing.T) {
	store := NewStore()
	now := time.Now()

	if _, err := store.Register(now, 0, validSubscription(""), ""); err != ErrInvalidUserID {
		t.Fatalf("expected ErrInvalidUserID, got %v", err)
	}
	if _, err := store.Register(now, alice, Subscription{}, ""); err == nil {
		t.Fatal("expected an error for an empty subscription")
	}
	if _, err := store.Register(now, alice, validSubscription(""), "bad<platform>"); err != ErrInvalidPlatform {
		t.Fatalf("expected ErrInvalidPlatform, got %v", err)
	}
	if got := store.ListActive(alice); len(got) != 0 {
		t.Fatalf("expected no registrations to be created by rejected input, got %d", len(got))
	}
}

func TestRegisterCreatesActiveRegistration(t *testing.T) {
	store := NewStore()
	now := time.Now()
	sub := validSubscription("https://push.example.com/send/one")

	reg, err := store.Register(now, alice, sub, "android-chrome")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.ID == 0 {
		t.Fatal("expected a non-zero DeviceID")
	}
	if reg.UserID != alice {
		t.Fatalf("expected UserID %v, got %v", alice, reg.UserID)
	}
	if !reg.Active() {
		t.Fatal("expected a freshly registered device to be active")
	}
	if reg.Subscription != sub {
		t.Fatalf("expected subscription %+v, got %+v", sub, reg.Subscription)
	}
	if !reg.CreatedAt.Equal(now) || !reg.UpdatedAt.Equal(now) {
		t.Fatalf("expected CreatedAt/UpdatedAt to equal the caller-supplied now, got %v/%v", reg.CreatedAt, reg.UpdatedAt)
	}
}

func TestRegisterSameEndpointSameUserIsIdempotent(t *testing.T) {
	store := NewStore()
	t0 := time.Now()
	sub := validSubscription("https://push.example.com/send/one")

	first, err := store.Register(t0, alice, sub, "android-chrome")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t1 := t0.Add(time.Hour)
	second, err := store.Register(t1, alice, sub, "android-chrome-updated")
	if err != nil {
		t.Fatalf("unexpected error on re-registration: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("expected re-registering the same endpoint for the same user to update the existing row, got a new id %v vs %v", second.ID, first.ID)
	}
	if !second.UpdatedAt.Equal(t1) {
		t.Fatalf("expected UpdatedAt to advance to %v, got %v", t1, second.UpdatedAt)
	}
	if second.Platform != "android-chrome-updated" {
		t.Fatalf("expected the platform hint to refresh, got %q", second.Platform)
	}
	if got := store.ListActive(alice); len(got) != 1 {
		t.Fatalf("expected exactly one active registration, got %d", len(got))
	}
}

func TestRegisterSameEndpointDifferentUserIsForbidden(t *testing.T) {
	store := NewStore()
	now := time.Now()
	sub := validSubscription("https://push.example.com/send/shared")

	if _, err := store.Register(now, alice, sub, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := store.Register(now, bob, sub, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for a cross-user registration attempt, got %v", err)
	}

	aliceDevices := store.ListActive(alice)
	if len(aliceDevices) != 1 || aliceDevices[0].UserID != alice {
		t.Fatalf("expected alice's original registration to be untouched, got %+v", aliceDevices)
	}
	if got := store.ListActive(bob); len(got) != 0 {
		t.Fatalf("expected bob to have no registrations after a rejected attempt, got %d", len(got))
	}
}

func TestReplaceSupersedesOwnRegistration(t *testing.T) {
	store := NewStore()
	t0 := time.Now()
	oldSub := validSubscription("https://push.example.com/send/old")
	old, err := store.Register(t0, alice, oldSub, "ios-safari")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t1 := t0.Add(time.Minute)
	newSub := validSubscription("https://push.example.com/send/rotated")
	fresh, err := store.Replace(t1, alice, old.ID, newSub, "ios-safari")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fresh.ID == old.ID {
		t.Fatal("expected Replace to produce a new DeviceID, not reuse the old one")
	}
	if !fresh.Active() {
		t.Fatal("expected the replacement registration to be active")
	}
	if fresh.Subscription != newSub {
		t.Fatalf("expected the replacement to carry the new subscription, got %+v", fresh.Subscription)
	}

	oldAfter, ok := store.Get(old.ID)
	if !ok {
		t.Fatal("expected the old registration to still exist (revoked, not deleted)")
	}
	if oldAfter.Active() {
		t.Fatal("expected the old registration to no longer be active")
	}
	if oldAfter.SupersededBy != fresh.ID {
		t.Fatalf("expected SupersededBy to reference %v, got %v", fresh.ID, oldAfter.SupersededBy)
	}
	if oldAfter.RevokedAt == nil || !oldAfter.RevokedAt.Equal(t1) {
		t.Fatalf("expected RevokedAt to equal %v, got %v", t1, oldAfter.RevokedAt)
	}

	active := store.ListActive(alice)
	if len(active) != 1 || active[0].ID != fresh.ID {
		t.Fatalf("expected exactly one active registration (the replacement), got %+v", active)
	}
}

func TestReplaceRejectsWrongOwner(t *testing.T) {
	store := NewStore()
	now := time.Now()
	old, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = store.Replace(now, bob, old.ID, validSubscription("https://push.example.com/send/b"), "")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	after, _ := store.Get(old.ID)
	if !after.Active() {
		t.Fatal("expected the original registration to remain untouched and active")
	}
}

func TestReplaceRejectsUnknownOrInactiveID(t *testing.T) {
	store := NewStore()
	now := time.Now()

	if _, err := store.Replace(now, alice, DeviceID(999), validSubscription(""), ""); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for an unknown id, got %v", err)
	}

	old, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.Revoke(now, alice, old.ID); err != nil {
		t.Fatalf("unexpected error revoking: %v", err)
	}
	if _, err := store.Replace(now, alice, old.ID, validSubscription("https://push.example.com/send/b"), ""); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound when replacing an already-revoked id, got %v", err)
	}
}

func TestReplaceRejectsEndpointOwnedByAnotherUser(t *testing.T) {
	store := NewStore()
	now := time.Now()

	bobSub := validSubscription("https://push.example.com/send/bobs")
	if _, err := store.Register(now, bob, bobSub, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliceOld, err := store.Register(now, alice, validSubscription("https://push.example.com/send/alices"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := store.Replace(now, alice, aliceOld.ID, bobSub, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden when replacing onto another user's active endpoint, got %v", err)
	}
	after, _ := store.Get(aliceOld.ID)
	if !after.Active() {
		t.Fatal("expected alice's original registration to remain untouched on a rejected replace")
	}
}

// TestReplaceRejectsEndpointOwnedBySameUsersOtherDevice is a regression test
// for a confirmed bug: replacing device B with device A's own endpoint (both
// owned by the same user) previously succeeded and left two simultaneously
// active registrations bound to the same endpoint, silently duplicating
// delivery. Replace must reject this exactly as it rejects a different
// user's active endpoint, and must not mutate either existing row.
func TestReplaceRejectsEndpointOwnedBySameUsersOtherDevice(t *testing.T) {
	store := NewStore()
	now := time.Now()

	deviceA, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deviceB, err := store.Register(now, alice, validSubscription("https://push.example.com/send/b"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := store.Replace(now, alice, deviceB.ID, deviceA.Subscription, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden when replacing onto the caller's own other active endpoint, got %v", err)
	}

	active := store.ListActive(alice)
	if len(active) != 2 {
		t.Fatalf("expected exactly the original two active registrations, got %d: %+v", len(active), active)
	}
	seenEndpoints := map[string]int{}
	for _, r := range active {
		seenEndpoints[r.Subscription.Endpoint]++
	}
	for endpoint, count := range seenEndpoints {
		if count > 1 {
			t.Fatalf("endpoint %q has %d simultaneously active registrations; expected at most one", endpoint, count)
		}
	}
	afterA, _ := store.Get(deviceA.ID)
	afterB, _ := store.Get(deviceB.ID)
	if !afterA.Active() || !afterA.UpdatedAt.Equal(deviceA.UpdatedAt) {
		t.Fatal("expected device A to remain untouched by the rejected replace")
	}
	if !afterB.Active() || !afterB.UpdatedAt.Equal(deviceB.UpdatedAt) {
		t.Fatal("expected device B to remain untouched by the rejected replace")
	}
}

// TestReplaceWithSameEndpointIsIdempotent is a regression test for the
// decided behavior: Replace(oldID, sameEndpointAsOld) is not a real device
// replacement, so it must update the existing row in place (mirroring
// Register's own idempotent-update semantics for an unchanged endpoint)
// rather than churning a new, unnecessary DeviceID that supersedes the old
// one.
func TestReplaceWithSameEndpointIsIdempotent(t *testing.T) {
	store := NewStore()
	t0 := time.Now()
	sub := validSubscription("https://push.example.com/send/unchanged")

	original, err := store.Register(t0, alice, sub, "ios-safari")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t1 := t0.Add(time.Minute)
	result, err := store.Replace(t1, alice, original.ID, sub, "ios-safari-updated")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ID != original.ID {
		t.Fatalf("expected Replace with an unchanged endpoint to update the same DeviceID %v, got a new id %v", original.ID, result.ID)
	}
	if !result.Active() {
		t.Fatal("expected the updated registration to remain active")
	}
	if result.SupersededBy != 0 {
		t.Fatalf("expected no supersession to have occurred, got SupersededBy=%v", result.SupersededBy)
	}
	if !result.UpdatedAt.Equal(t1) {
		t.Fatalf("expected UpdatedAt to advance to %v, got %v", t1, result.UpdatedAt)
	}
	if result.Platform != "ios-safari-updated" {
		t.Fatalf("expected the platform hint to refresh, got %q", result.Platform)
	}
	if got := store.ListActive(alice); len(got) != 1 {
		t.Fatalf("expected exactly one active registration, got %d: %+v", len(got), got)
	}
}

// TestRegisterAfterRevokeCreatesFreshActiveRegistration confirms the
// intended behavior for re-registering a previously revoked/superseded
// endpoint: since activeByEndpoint only ever matches active rows, the old
// (now-inactive) row is left alone and a brand-new active registration is
// created, rather than the caller being blocked or the dead row being
// resurrected.
func TestRegisterAfterRevokeCreatesFreshActiveRegistration(t *testing.T) {
	store := NewStore()
	t0 := time.Now()
	sub := validSubscription("https://push.example.com/send/reused")

	original, err := store.Register(t0, alice, sub, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t1 := t0.Add(time.Minute)
	if err := store.Revoke(t1, alice, original.ID); err != nil {
		t.Fatalf("unexpected error revoking: %v", err)
	}

	t2 := t1.Add(time.Minute)
	fresh, err := store.Register(t2, alice, sub, "android-chrome")
	if err != nil {
		t.Fatalf("unexpected error re-registering a previously revoked endpoint: %v", err)
	}

	if fresh.ID == original.ID {
		t.Fatalf("expected a fresh DeviceID distinct from the revoked one %v, got %v", original.ID, fresh.ID)
	}
	if !fresh.Active() {
		t.Fatal("expected the fresh registration to be active")
	}
	if !fresh.CreatedAt.Equal(t2) {
		t.Fatalf("expected CreatedAt to equal %v, got %v", t2, fresh.CreatedAt)
	}

	oldAfter, ok := store.Get(original.ID)
	if !ok {
		t.Fatal("expected the original revoked registration to still exist")
	}
	if oldAfter.Active() {
		t.Fatal("expected the original registration to remain revoked, not resurrected")
	}
	if oldAfter.SupersededBy != 0 {
		t.Fatalf("expected the original registration to not be marked superseded (it was revoked, not replaced), got SupersededBy=%v", oldAfter.SupersededBy)
	}

	active := store.ListActive(alice)
	if len(active) != 1 || active[0].ID != fresh.ID {
		t.Fatalf("expected exactly one active registration (the fresh one), got %+v", active)
	}
}

func TestRevoke(t *testing.T) {
	store := NewStore()
	t0 := time.Now()
	reg, err := store.Register(t0, alice, validSubscription("https://push.example.com/send/a"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := store.Revoke(t0.Add(time.Minute), alice, reg.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after, ok := store.Get(reg.ID)
	if !ok || after.Active() {
		t.Fatal("expected the registration to exist and no longer be active")
	}

	// Idempotent: revoking again does not change the original RevokedAt.
	firstRevokedAt := *after.RevokedAt
	if err := store.Revoke(t0.Add(time.Hour), alice, reg.ID); err != nil {
		t.Fatalf("expected revoking an already-revoked id to be a no-op, got %v", err)
	}
	again, _ := store.Get(reg.ID)
	if !again.RevokedAt.Equal(firstRevokedAt) {
		t.Fatalf("expected RevokedAt to stay at %v, got %v", firstRevokedAt, again.RevokedAt)
	}
}

func TestRevokeRejectsWrongOwnerAndUnknownID(t *testing.T) {
	store := NewStore()
	now := time.Now()
	reg, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := store.Revoke(now, bob, reg.ID); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	after, _ := store.Get(reg.ID)
	if !after.Active() {
		t.Fatal("expected the registration to remain active after a rejected revoke")
	}

	if err := store.Revoke(now, alice, DeviceID(12345)); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for an unknown id, got %v", err)
	}
}

func TestListActiveScopesToOwnerAndExcludesRevoked(t *testing.T) {
	store := NewStore()
	now := time.Now()

	a1, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a1"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := store.Register(now, alice, validSubscription("https://push.example.com/send/a2"), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := store.Register(now, bob, validSubscription("https://push.example.com/send/b1"), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.Revoke(now, alice, a1.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	aliceActive := store.ListActive(alice)
	if len(aliceActive) != 1 {
		t.Fatalf("expected exactly one active registration for alice, got %d", len(aliceActive))
	}
	for _, r := range aliceActive {
		if r.UserID != alice {
			t.Fatalf("expected only alice's own registrations, got one owned by %v", r.UserID)
		}
		if r.ID == a1.ID {
			t.Fatal("expected the revoked registration to be excluded")
		}
	}

	bobActive := store.ListActive(bob)
	if len(bobActive) != 1 {
		t.Fatalf("expected exactly one active registration for bob, got %d", len(bobActive))
	}
}

func TestGetUnknownID(t *testing.T) {
	store := NewStore()
	if _, ok := store.Get(DeviceID(1)); ok {
		t.Fatal("expected an empty store to have no registrations")
	}
}
