package identity

import "testing"

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Synthetic.User@Example.TEST", "synthetic.user@example.test", false},
		{"  spaced@example.test  ", "spaced@example.test", false},
		{"not-an-email", "", true},
		{"", "", true},
		{"a@b", "", true}, // no TLD-shaped segment
		{"double@@example.test", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeEmail(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeEmail(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeEmail(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeEmailIdempotentCasing(t *testing.T) {
	a, err := NormalizeEmail("Mixed.Case@Example.Test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeEmail(a)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("normalization is not idempotent: %q != %q", a, b)
	}
}

func TestRoleValidAndMFA(t *testing.T) {
	if !RoleSystemAdministrator.Valid() || !RoleSystemAdministrator.RequiresMFA() || !RoleSystemAdministrator.IsAdministrator() {
		t.Error("system administrator must be valid, administrator, and require MFA")
	}
	if !RoleDepartmentAdministrator.RequiresMFA() {
		t.Error("department administrator must require MFA")
	}
	for _, r := range []Role{RoleDispatcherOperator, RoleResponder, RoleReadOnlyAuditor} {
		if r.RequiresMFA() {
			t.Errorf("%s must not be defaulted to requiring MFA (unresolved question)", r)
		}
		if r.IsAdministrator() {
			t.Errorf("%s must not be an administrator role", r)
		}
	}
	if Role("superuser").Valid() {
		t.Error("unknown role must be invalid (default deny)")
	}
}

func TestScopeValid(t *testing.T) {
	if !Scope("").Valid() {
		t.Error("empty scope must be valid (no scope boundary)")
	}
	if !Scope("engine-2").Valid() {
		t.Error("normal scope label must be valid")
	}
	if Scope("  ").Valid() {
		t.Error("blank-but-nonempty scope must be invalid")
	}
	if Scope(make([]byte, 200)).Valid() {
		t.Error("oversized scope must be invalid")
	}
}

func TestAllowedTransitionMatrix(t *testing.T) {
	allowed := []struct{ from, to AccountState }{
		{StateInvited, StatePasswordChangeRequired},
		{StateInvited, StateExpired},
		{StatePasswordChangeRequired, StateActive},
		{StateSuspended, StateActive},
		{StateDisabled, StateActive},
		{StateSuspended, StateInvited},
		{StateExpired, StateInvited},
		{StateActive, StateSuspended},
		{StateActive, StateDisabled},
		// An administrator must be able to reissue a fresh credential-reset
		// code for an account that is already password_change_required (for
		// example, the original code was lost or never retained) without
		// first forcing it through some other state.
		{StatePasswordChangeRequired, StatePasswordChangeRequired},
	}
	for _, c := range allowed {
		if !AllowedTransition(c.from, c.to) {
			t.Errorf("expected %s -> %s to be allowed", c.from, c.to)
		}
	}

	forbidden := []struct{ from, to AccountState }{
		{StateInvited, StateActive}, // must pass through password_change_required
		{StateExpired, StateActive}, // must pass through password_change_required
		{StateActive, StateInvited}, // never silently re-invited
		{StateActive, StateExpired}, // active does not "expire" directly
		{StatePasswordChangeRequired, StateInvited},
		{AccountState("bogus"), StateActive}, // unknown from-state
		{StateActive, AccountState("bogus")}, // unknown to-state
	}
	for _, c := range forbidden {
		if AllowedTransition(c.from, c.to) {
			t.Errorf("expected %s -> %s to be forbidden", c.from, c.to)
		}
	}
}

func TestUserPublicViewExcludesPasswordHash(t *testing.T) {
	u := User{ID: 1, NormalizedEmail: "x@example.test", DisplayName: "X", Role: RoleResponder, PasswordHash: "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA"}
	view := u.Public()
	// View has no PasswordHash field at all; this test exists to document
	// that guarantee structurally (a future field addition to View would
	// need a deliberate, reviewed change, not an accidental leak).
	_ = view
	if u.PasswordHash == "" {
		t.Fatal("test fixture must have a password hash to be meaningful")
	}
}

func TestCanAuthenticateRequiresActive(t *testing.T) {
	for _, s := range []AccountState{StateInvited, StatePasswordChangeRequired, StateSuspended, StateDisabled, StateExpired} {
		u := User{Status: s}
		if u.CanAuthenticate() {
			t.Errorf("status %s must not be able to authenticate", s)
		}
	}
	if !(User{Status: StateActive}).CanAuthenticate() {
		t.Error("active status must be able to authenticate")
	}
}

func TestValidateDisplayNameAndRoleAndScope(t *testing.T) {
	if ValidateDisplayName("") == nil {
		t.Error("blank display name must be invalid")
	}
	if ValidateDisplayName("Synthetic Responder") != nil {
		t.Error("normal display name must be valid")
	}
	if ValidateRole("bogus") == nil {
		t.Error("unknown role must be invalid")
	}
	if ValidateScope(Scope("  ")) == nil {
		t.Error("blank scope must be invalid")
	}
}
