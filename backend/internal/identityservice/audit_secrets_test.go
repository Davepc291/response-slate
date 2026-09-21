package identityservice

import (
	"context"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// TestAuditRecordsAcrossFullFlowNeverContainSecrets drives every
// security-significant operation this service exposes and then scans
// every recorded audit event's metadata for the raw values (password,
// tokens) involved, confirming none of them ever appear — the audit
// catalog's own allow-list already structurally prevents password/token
// -shaped keys (see identityaudit), but this test additionally confirms no
// value coincidentally leaks through an allowed key.
func TestAuditRecordsAcrossFullFlowNeverContainSecrets(t *testing.T) {
	svc, store, audit := newTestService(t)

	invitedID, rawInviteToken := seedInvitedUser(t, store, "fullflow@example.test", identity.RoleResponder, time.Hour)
	permanentPassword := "a-permanent-passphrase-value"
	if _, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawInviteToken, permanentPassword, fixedNow); err != nil {
		t.Fatal(err)
	}

	login, err := svc.Login(context.Background(), "fullflow@example.test", permanentPassword, "synthetic-device", "203.0.113.77", fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	rawResetToken, matched, err := svc.RequestPasswordReset(context.Background(), "fullflow@example.test", fixedNow)
	if err != nil || !matched {
		t.Fatal(err)
	}
	newPassword := "a-brand-new-replacement-passphrase"
	if _, err := svc.CompletePasswordReset(context.Background(), rawResetToken, newPassword, fixedNow); err != nil {
		t.Fatal(err)
	}

	secondLogin, err := svc.Login(context.Background(), "fullflow@example.test", newPassword, "second-device", "203.0.113.78", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(context.Background(), invitedID, secondLogin.SessionID, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LogoutAll(context.Background(), invitedID, fixedNow); err != nil {
		t.Fatal(err)
	}

	secrets := []string{
		permanentPassword, newPassword, rawInviteToken, rawResetToken,
		login.RawToken, secondLogin.RawToken,
	}

	for _, ev := range audit.snapshot() {
		if strings.Contains(ev.Reason, permanentPassword) {
			t.Errorf("event %s reason field leaked a secret", ev.Type)
		}
		for key, value := range ev.Metadata {
			lowerKey := strings.ToLower(key)
			for _, bad := range []string{"password", "token", "secret", "cookie", "hash", "credential"} {
				if strings.Contains(lowerKey, bad) {
					t.Errorf("event %s has a forbidden-shaped metadata key %q", ev.Type, key)
				}
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					t.Errorf("event %s metadata key %q leaked a secret value", ev.Type, key)
				}
			}
		}
	}
	if len(audit.snapshot()) == 0 {
		t.Fatal("expected at least one audit event across this flow")
	}
}
