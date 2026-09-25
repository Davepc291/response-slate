// Package mfa is the Step 9F WebAuthn passkey provider foundation approved
// by docs/authentication-authorization-v1.md Section 5 and 11.4:
// administrators (system_administrator, department_administrator) must
// enroll strong MFA before their account can leave password_change_required,
// and cannot reach any /api/admin/* route without an MFA-verified current
// session (AAX-07); passkeys/WebAuthn are the contract's preferred method.
// This package wraps github.com/go-webauthn/webauthn with strict Relying
// Party configuration validation and the minimal ceremony surface a future,
// separately authorized HTTP handler needs: registration
// (BeginEnrollment/FinishEnrollment, Step 9F-3) and authentication
// (BeginLogin/FinishLogin, Step 9F-4) against a known, already-identified
// user — never a discoverable/passwordless/usernameless login, which this
// package does not expose. It defines no HTTP route, no session or cookie
// handling, no TOTP, and no SMS: TOTP is contract-designated fallback-only
// and explicitly out of this task's scope, and SMS is never implemented.
//
// This package persists nothing itself; see identitystore's
// EnrollMFACredential and ListMFACredentials for the mfa_credentials table
// integration. Every value this package hands a caller to store is public
// credential material only — webauthn.Credential has no private-key,
// challenge, or token field, so MarshalCredential cannot emit one even by
// mistake.
package mfa

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

// CredentialTypePasskey is the only credential type this package produces,
// matching migration 000008's mfa_credentials.credential_type CHECK
// constraint and the contract's preferred method (Section 5). TOTP is a
// distinct, separately authorized value this package does not implement.
const CredentialTypePasskey = "passkey"

// maxCredentialDataLen mirrors mfa_credentials.credential_data's CHECK
// constraint (migration 000008: octet_length BETWEEN 1 AND 4096), so an
// oversized encoded credential is rejected here, before any database call.
const maxCredentialDataLen = 4096

// Sentinel errors. None of these, nor any value this package returns, ever
// includes a private key, challenge, or session/bearer token.
var (
	ErrInvalidConfig     = errors.New("mfa: invalid provider configuration")
	ErrInvalidUser       = errors.New("mfa: invalid enrollment user")
	ErrInvalidCredential = errors.New("mfa: invalid or oversized credential")
)

const (
	maxRPIDLen          = 253
	maxRPDisplayNameLen = 200
	maxOrigins          = 20
)

// Config is the strict, provider-neutral Relying Party configuration this
// package requires (Step 9F requirement B). Every field is mandatory:
// WebAuthn registration is RP-ID- and origin-bound by design (the client
// only ever signs a credential over one of these origins), so a missing or
// permissive value here would let a different site enroll a passkey on
// this Relying Party's behalf.
type Config struct {
	// RPID is the Relying Party's effective domain (e.g. "example.org"):
	// never a full origin — no scheme, no port, no path.
	RPID string
	// RPDisplayName is the human-readable name shown to the user during
	// the enrollment ceremony.
	RPDisplayName string
	// RPOrigins is the exhaustive, non-empty list of origins a client's
	// signed response may declare. An http origin is accepted only for a
	// loopback host (local development); every other origin must be https.
	RPOrigins []string
}

func (c Config) validate() error {
	if trimmed := strings.TrimSpace(c.RPID); trimmed == "" || trimmed != c.RPID || len(c.RPID) > maxRPIDLen {
		return ErrInvalidConfig
	}
	if strings.ContainsAny(c.RPID, "/:@ \t\r\n") {
		return ErrInvalidConfig
	}
	if err := protocol.ValidateRPID(c.RPID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	if trimmed := strings.TrimSpace(c.RPDisplayName); trimmed == "" || trimmed != c.RPDisplayName || len(c.RPDisplayName) > maxRPDisplayNameLen {
		return ErrInvalidConfig
	}

	if len(c.RPOrigins) == 0 || len(c.RPOrigins) > maxOrigins {
		return ErrInvalidConfig
	}
	seen := make(map[string]bool, len(c.RPOrigins))
	for _, origin := range c.RPOrigins {
		if err := validateOrigin(origin); err != nil {
			return err
		}
		if seen[origin] {
			return ErrInvalidConfig
		}
		seen[origin] = true
	}
	return nil
}

// validateOrigin rejects anything but a bare "scheme://host[:port]" origin
// — no path, query, fragment, or userinfo — and accepts http only for a
// loopback host, so a production deployment cannot be misconfigured with an
// unencrypted origin by accident.
func validateOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return ErrInvalidConfig
	}
	host := u.Hostname()
	switch u.Scheme {
	case "https":
		if host == "" {
			return ErrInvalidConfig
		}
	case "http":
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return ErrInvalidConfig
		}
	default:
		return ErrInvalidConfig
	}
	return nil
}

// Provider is a clean abstraction over github.com/go-webauthn/webauthn: it
// exposes only the registration- and known-user-authentication-ceremony
// operations Step 9F needs and hides every other capability the underlying
// library offers (discoverable/passwordless login, FIDO Metadata Service
// trust validation, and so on), so this task cannot accidentally grow
// beyond passkey enrollment and per-session verification.
type Provider struct {
	rp *webauthn.WebAuthn
}

// NewProvider validates cfg and constructs a Provider. It never returns a
// partially valid Provider: any error means cfg must be corrected.
func NewProvider(cfg Config) (*Provider, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	rp, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     append([]string(nil), cfg.RPOrigins...),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return &Provider{rp: rp}, nil
}

// EnrollmentUser adapts one account to the go-webauthn library's User
// interface for exactly one ceremony — registration or authentication.
// For a registration ceremony, Credentials must contain only that
// account's already-enrolled, non-revoked passkeys (decoded via
// UnmarshalCredential); it is used solely to build the exclusion list, so
// the same physical authenticator cannot be registered twice. For an
// authentication ceremony, Credentials must contain every currently
// enrolled, non-revoked passkey: BeginLogin/FinishLogin verify the
// response against exactly this set. It is never itself persisted by this
// package. LoginUser is an alias of this type for readability at
// authentication-ceremony call sites.
type EnrollmentUser struct {
	ID          identity.UserID
	Email       string
	DisplayName string
	Credentials []webauthn.Credential
}

// WebAuthnID returns the account's server-generated numeric id, encoded as
// a fixed-width 8-byte big-endian value. This is not a secret (Section 1:
// the id is already an opaque, non-enumerable handle the account is known
// by internally); WebAuthn only requires the handle be stable and bounded
// to 64 bytes, which this trivially satisfies.
func (u EnrollmentUser) WebAuthnID() []byte {
	id := uint64(u.ID)
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(id)
		id >>= 8
	}
	return b
}

// WebAuthnName satisfies webauthn.User.
func (u EnrollmentUser) WebAuthnName() string { return u.Email }

// WebAuthnDisplayName satisfies webauthn.User.
func (u EnrollmentUser) WebAuthnDisplayName() string { return u.DisplayName }

// WebAuthnCredentials satisfies webauthn.User.
func (u EnrollmentUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// LoginUser is EnrollmentUser under the name authentication-ceremony call
// sites read more naturally with; it is the identical type.
type LoginUser = EnrollmentUser

func (u EnrollmentUser) validate() error {
	if u.ID == 0 {
		return ErrInvalidUser
	}
	if _, err := identity.NormalizeEmail(u.Email); err != nil {
		return ErrInvalidUser
	}
	if err := identity.ValidateDisplayName(u.DisplayName); err != nil {
		return ErrInvalidUser
	}
	return nil
}

// BeginEnrollment starts a WebAuthn registration ceremony for user. The
// returned *protocol.CredentialCreation is safe to serialize directly to
// the client — its Challenge is a public, single-use nonce, not a secret.
// session must be persisted server-side only (never in a client-visible
// cookie or response body) and supplied unmodified to FinishEnrollment.
func (p *Provider) BeginEnrollment(user EnrollmentUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	if p == nil || p.rp == nil {
		return nil, nil, ErrInvalidConfig
	}
	if err := user.validate(); err != nil {
		return nil, nil, err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(user.Credentials))
	for _, cred := range user.Credentials {
		exclude = append(exclude, cred.Descriptor())
	}
	creation, session, err := p.rp.BeginRegistration(user, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, nil, fmt.Errorf("mfa: begin enrollment: %w", err)
	}
	return creation, session, nil
}

// FinishEnrollment verifies a client's registration response JSON against
// session and returns the resulting Credential: public key material only
// (Section 5, migration 000008: "No secret key material for passkeys is
// ever stored"). Callers must persist the result via MarshalCredential and
// must never place any field of it, or of session, in an audit-log
// metadata value — see EnrollmentAuditMetadata.
func (p *Provider) FinishEnrollment(user EnrollmentUser, session webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	if p == nil || p.rp == nil {
		return nil, ErrInvalidConfig
	}
	if err := user.validate(); err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("mfa: parse registration response: %w", err)
	}
	cred, err := p.rp.CreateCredential(user, session, parsed)
	if err != nil {
		return nil, fmt.Errorf("mfa: finish enrollment: %w", err)
	}
	return cred, nil
}

// BeginLogin starts a WebAuthn authentication ceremony proving possession
// of one of user's already-enrolled credentials (Step 9F-4, AAX-07): this
// is never a login by itself (the caller already authenticated by
// email/password beforehand; see docs/authentication-authorization-v1.md
// Section 3/5) and never a discoverable/passwordless ceremony — user's
// identity, and its Credentials, must already be known. The returned
// *protocol.CredentialAssertion is safe to serialize directly to the
// client; session must be persisted server-side only and supplied
// unmodified to FinishLogin. Fails if user has no enrolled credentials:
// there is nothing to prove possession of.
func (p *Provider) BeginLogin(user LoginUser) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if p == nil || p.rp == nil {
		return nil, nil, ErrInvalidConfig
	}
	if err := user.validate(); err != nil {
		return nil, nil, err
	}
	assertion, session, err := p.rp.BeginLogin(user)
	if err != nil {
		return nil, nil, fmt.Errorf("mfa: begin login: %w", err)
	}
	return assertion, session, nil
}

// FinishLogin verifies a client's authentication response JSON against
// session and returns the credential that was used (its authenticator
// sign-count bookkeeping updated) — callers that track sign counts must
// persist it via MarshalCredential; callers that only need proof of
// possession (Step 9F-4 marks a session verified and stores nothing new)
// may discard it. Never returns any challenge, clientDataJSON, or
// signature value: the returned Credential carries only what
// webauthn.Credential itself structurally can (public key and
// authenticator bookkeeping).
func (p *Provider) FinishLogin(user LoginUser, session webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	if p == nil || p.rp == nil {
		return nil, ErrInvalidConfig
	}
	if err := user.validate(); err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("mfa: parse login response: %w", err)
	}
	cred, err := p.rp.ValidateLogin(user, session, parsed)
	if err != nil {
		return nil, fmt.Errorf("mfa: finish login: %w", err)
	}
	return cred, nil
}

// MarshalCredential encodes cred for storage in
// mfa_credentials.credential_data. The encoding is exactly
// webauthn.Credential's own JSON representation: public key, attestation
// metadata, and authenticator bookkeeping only — structurally, no private
// key, challenge, or token field exists on that type for this function to
// ever emit.
func MarshalCredential(cred *webauthn.Credential) ([]byte, error) {
	if cred == nil {
		return nil, ErrInvalidCredential
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return nil, fmt.Errorf("mfa: encode credential: %w", err)
	}
	if len(data) == 0 || len(data) > maxCredentialDataLen {
		return nil, ErrInvalidCredential
	}
	return data, nil
}

// UnmarshalCredential decodes a credential_data value previously produced
// by MarshalCredential.
func UnmarshalCredential(data []byte) (*webauthn.Credential, error) {
	if len(data) == 0 || len(data) > maxCredentialDataLen {
		return nil, ErrInvalidCredential
	}
	var cred webauthn.Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("mfa: decode credential: %w", err)
	}
	return &cred, nil
}

// EnrollmentAuditMetadata is the one place this package builds
// identity_audit_log metadata for an mfa_enrollment or mfa_verification
// event — both carry the identical, single safe fact ("which method"), so
// one function serves both event types rather than duplicating it. It is
// deliberately a fixed value with no caller-supplied field, so no future
// call site can smuggle a credential, challenge, or session value into an
// audit record even by accident; identityaudit's own allow-list
// (MFAEnrollment/MFAVerification -> {"method"}) and forbidden-substring
// check both still apply as defense in depth.
func EnrollmentAuditMetadata() identityaudit.Metadata {
	return identityaudit.Metadata{"method": CredentialTypePasskey}
}
