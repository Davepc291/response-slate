package mfa

import (
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

func validConfig() Config {
	return Config{
		RPID:          "localhost",
		RPDisplayName: "Greenwich Fire Responder",
		RPOrigins:     []string{"http://localhost:4300"},
	}
}

// TestConfigValidation covers Step 9F requirement B: strict validation of
// RP ID, RP display name, and allowed RP origins.
func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(Config) Config
		wantErr bool
	}{
		{"valid local config", func(c Config) Config { return c }, false},
		{"empty RPID", func(c Config) Config { c.RPID = ""; return c }, true},
		{"RPID with scheme", func(c Config) Config { c.RPID = "https://localhost"; return c }, true},
		{"RPID with port", func(c Config) Config { c.RPID = "localhost:4300"; return c }, true},
		{"RPID is an IP address", func(c Config) Config { c.RPID = "127.0.0.1"; return c }, true},
		{"RPID with leading/trailing space", func(c Config) Config { c.RPID = " localhost "; return c }, true},
		{"empty display name", func(c Config) Config { c.RPDisplayName = ""; return c }, true},
		{"blank display name", func(c Config) Config { c.RPDisplayName = "   "; return c }, true},
		{"no origins", func(c Config) Config { c.RPOrigins = nil; return c }, true},
		{"too many origins", func(c Config) Config {
			origins := make([]string, maxOrigins+1)
			for i := range origins {
				origins[i] = "https://example.test"
			}
			c.RPOrigins = origins
			return c
		}, true},
		{"duplicate origins", func(c Config) Config {
			c.RPOrigins = []string{"https://example.test", "https://example.test"}
			return c
		}, true},
		{"origin missing scheme", func(c Config) Config { c.RPOrigins = []string{"example.test"}; return c }, true},
		{"origin with a path", func(c Config) Config { c.RPOrigins = []string{"https://example.test/app"}; return c }, true},
		{"http origin on a non-loopback host", func(c Config) Config {
			c.RPOrigins = []string{"http://example.test"}
			return c
		}, true},
		{"https origin on a real domain", func(c Config) Config {
			c.RPOrigins = []string{"https://example.test"}
			return c
		}, false},
		{"http origin on loopback IP", func(c Config) Config {
			c.RPOrigins = []string{"http://127.0.0.1:4300"}
			return c
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.mutate(validConfig())
			err := cfg.validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error for config %+v, got nil", cfg)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected a valid config %+v, got %v", cfg, err)
			}
		})
	}
}

func TestNewProviderRejectsInvalidConfig(t *testing.T) {
	if _, err := NewProvider(Config{}); err == nil {
		t.Fatal("expected an error constructing a Provider from an empty config")
	}
}

func TestNewProviderAcceptsValidLocalConfig(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatalf("expected a valid local config to construct a Provider, got %v", err)
	}
	if p == nil || p.rp == nil {
		t.Fatal("expected a non-nil Provider wrapping a non-nil webauthn.WebAuthn")
	}
}

func validUser() EnrollmentUser {
	return EnrollmentUser{ID: 42, Email: "admin@example.test", DisplayName: "Synthetic Administrator"}
}

// TestBeginEnrollmentProducesChallengeAndSession exercises the begin half
// of the registration ceremony end-to-end against the real library: the
// returned options must be addressed to the configured Relying Party and
// carry a fresh, sufficiently long, single-use challenge, and the session
// state that must be persisted server-side must be bound to this user.
func TestBeginEnrollmentProducesChallengeAndSession(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	user := validUser()

	creation, session, err := p.BeginEnrollment(user)
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if creation == nil || session == nil {
		t.Fatal("expected non-nil creation options and session")
	}
	if creation.Response.RelyingParty.ID != "localhost" {
		t.Fatalf("expected RP ID localhost, got %q", creation.Response.RelyingParty.ID)
	}
	if len(creation.Response.Challenge) < 16 {
		t.Fatalf("expected a challenge of at least 16 bytes, got %d", len(creation.Response.Challenge))
	}
	if string(session.UserID) != string(user.WebAuthnID()) {
		t.Fatalf("expected session to be bound to the enrolling user's WebAuthn id")
	}

	// A second call must never reuse the same challenge (Step 9F
	// requirement D: no reused/predictable challenge material).
	creation2, _, err := p.BeginEnrollment(user)
	if err != nil {
		t.Fatal(err)
	}
	if creation.Response.Challenge.String() == creation2.Response.Challenge.String() {
		t.Fatal("expected two BeginEnrollment calls to produce distinct challenges")
	}
}

func TestBeginEnrollmentRejectsInvalidUser(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	cases := []EnrollmentUser{
		{ID: 0, Email: "admin@example.test", DisplayName: "Name"},
		{ID: 1, Email: "not-an-email", DisplayName: "Name"},
		{ID: 1, Email: "admin@example.test", DisplayName: ""},
	}
	for _, u := range cases {
		if _, _, err := p.BeginEnrollment(u); err != ErrInvalidUser {
			t.Fatalf("expected ErrInvalidUser for %+v, got %v", u, err)
		}
	}
}

// TestFinishEnrollmentRejectsMalformedResponse exercises the wrapper's
// error path for a malformed client response, without needing a real
// authenticator/browser ceremony (that cryptographic verification is the
// underlying library's own, separately tested responsibility).
func TestFinishEnrollmentRejectsMalformedResponse(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	user := validUser()
	_, session, err := p.BeginEnrollment(user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishEnrollment(user, *session, []byte("not valid json")); err == nil {
		t.Fatal("expected an error finishing enrollment with a malformed response body")
	}
}

func TestMarshalCredentialRejectsNilAndOversized(t *testing.T) {
	if _, err := MarshalCredential(nil); err != ErrInvalidCredential {
		t.Fatalf("expected ErrInvalidCredential for a nil credential, got %v", err)
	}
	huge := &webauthn.Credential{
		ID:        []byte(strings.Repeat("a", maxCredentialDataLen)),
		PublicKey: []byte(strings.Repeat("b", maxCredentialDataLen)),
	}
	if _, err := MarshalCredential(huge); err != ErrInvalidCredential {
		t.Fatalf("expected ErrInvalidCredential for an oversized credential, got %v", err)
	}
}

func TestUnmarshalCredentialRoundTrip(t *testing.T) {
	original := &webauthn.Credential{
		ID:        []byte("synthetic-credential-id"),
		PublicKey: []byte("synthetic-public-key-bytes"),
	}
	data, err := MarshalCredential(original)
	if err != nil {
		t.Fatalf("MarshalCredential: %v", err)
	}
	decoded, err := UnmarshalCredential(data)
	if err != nil {
		t.Fatalf("UnmarshalCredential: %v", err)
	}
	if string(decoded.ID) != string(original.ID) || string(decoded.PublicKey) != string(original.PublicKey) {
		t.Fatalf("expected round-tripped credential to match, got %+v", decoded)
	}
}

func TestUnmarshalCredentialRejectsEmptyAndOversized(t *testing.T) {
	if _, err := UnmarshalCredential(nil); err != ErrInvalidCredential {
		t.Fatalf("expected ErrInvalidCredential for empty data, got %v", err)
	}
	if _, err := UnmarshalCredential(make([]byte, maxCredentialDataLen+1)); err != ErrInvalidCredential {
		t.Fatalf("expected ErrInvalidCredential for oversized data, got %v", err)
	}
}

// TestEnrollmentAuditMetadataIsSafe covers Step 9F requirement G: no raw
// credential secret, challenge, or token is ever written to
// mfa_enrollment audit metadata. EnrollmentAuditMetadata is the only value
// this package ever proposes for that event type, and it must both satisfy
// identityaudit's allow-list and never carry a forbidden key.
func TestEnrollmentAuditMetadataIsSafe(t *testing.T) {
	metadata := EnrollmentAuditMetadata()
	if len(metadata) != 1 || metadata["method"] != CredentialTypePasskey {
		t.Fatalf("expected exactly {method: passkey}, got %+v", metadata)
	}
	if err := identityaudit.ValidateMetadata(identityaudit.MFAEnrollment, metadata); err != nil {
		t.Fatalf("expected EnrollmentAuditMetadata to pass validation, got %v", err)
	}
	ev, err := identityaudit.New(identityaudit.MFAEnrollment, identity.UserID(1), 0, "", metadata, time.Now())
	if err != nil {
		t.Fatalf("expected a valid MFAEnrollment audit event, got %v", err)
	}
	if len(ev.Metadata) != 1 {
		t.Fatalf("expected the constructed event to carry exactly one metadata field, got %+v", ev.Metadata)
	}

	// A caller attempting to smuggle a challenge, credential, or token into
	// this event type must be rejected outright, both because those keys
	// are not on MFAEnrollment's allow-list and because "challenge" is not
	// a forbidden substring by name alone (defense in depth relies on the
	// allow-list here, not just the deny-list).
	unsafe := []identityaudit.Metadata{
		{"challenge": "not-actually-secret-but-still-not-allowed"},
		{"credential_data": "AAAA"},
		{"session_token": "AAAA"},
		{"method": "passkey", "extra": "not-allowed"},
	}
	for _, m := range unsafe {
		if err := identityaudit.ValidateMetadata(identityaudit.MFAEnrollment, m); err == nil {
			t.Fatalf("expected metadata %+v to be rejected for MFAEnrollment", m)
		}
	}
}

func validCredential() webauthn.Credential {
	return webauthn.Credential{
		ID:        []byte("synthetic-login-credential-id"),
		PublicKey: []byte("synthetic-login-public-key"),
	}
}

// TestBeginLoginRequiresCredentials covers Step 9F-4: a known-user
// authentication ceremony can never begin for a user with zero enrolled
// credentials — there is nothing to prove possession of, and this package
// must fail rather than silently produce an unusable assertion.
func TestBeginLoginRequiresCredentials(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	user := validUser()
	if _, _, err := p.BeginLogin(user); err == nil {
		t.Fatal("expected an error beginning a login ceremony with no enrolled credentials")
	}
}

// TestBeginLoginProducesChallengeAndSession exercises the begin half of
// the authentication ceremony end-to-end against the real library: the
// returned options must be addressed to the configured Relying Party,
// list the user's enrolled credential as allowed, and carry a fresh,
// sufficiently long, single-use challenge distinct across calls.
func TestBeginLoginProducesChallengeAndSession(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	user := validUser()
	user.Credentials = []webauthn.Credential{validCredential()}

	assertion, session, err := p.BeginLogin(user)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if assertion == nil || session == nil {
		t.Fatal("expected non-nil assertion options and session")
	}
	if assertion.Response.RelyingPartyID != "localhost" {
		t.Fatalf("expected RP ID localhost, got %q", assertion.Response.RelyingPartyID)
	}
	if len(assertion.Response.Challenge) < 16 {
		t.Fatalf("expected a challenge of at least 16 bytes, got %d", len(assertion.Response.Challenge))
	}
	if len(assertion.Response.AllowedCredentials) != 1 || string(assertion.Response.AllowedCredentials[0].CredentialID) != string(user.Credentials[0].ID) {
		t.Fatalf("expected the enrolled credential to be listed as allowed, got %+v", assertion.Response.AllowedCredentials)
	}

	assertion2, _, err := p.BeginLogin(user)
	if err != nil {
		t.Fatal(err)
	}
	if assertion.Response.Challenge.String() == assertion2.Response.Challenge.String() {
		t.Fatal("expected two BeginLogin calls to produce distinct challenges")
	}
}

func TestBeginLoginRejectsInvalidUser(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	u := LoginUser{ID: 0, Email: "admin@example.test", DisplayName: "Name", Credentials: []webauthn.Credential{validCredential()}}
	if _, _, err := p.BeginLogin(u); err != ErrInvalidUser {
		t.Fatalf("expected ErrInvalidUser, got %v", err)
	}
}

// TestFinishLoginRejectsMalformedResponse exercises the wrapper's error
// path for a malformed client response, without needing a real
// authenticator/browser ceremony (that cryptographic verification is the
// underlying library's own, separately tested responsibility).
func TestFinishLoginRejectsMalformedResponse(t *testing.T) {
	p, err := NewProvider(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	user := validUser()
	user.Credentials = []webauthn.Credential{validCredential()}
	_, session, err := p.BeginLogin(user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishLogin(user, *session, []byte("not valid json")); err == nil {
		t.Fatal("expected an error finishing login with a malformed response body")
	}
}
