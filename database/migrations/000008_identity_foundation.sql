BEGIN;

-- Step 9B: authentication/authorization persistence surface approved by
-- docs/authentication-authorization-v1.md Section 1, 2, 4, 5, 8, 10, 11.4.
-- This migration creates exactly the tables named there: users, invitations,
-- password_resets, sessions, mfa_credentials, identity_audit_log. It creates
-- no default administrator, no HTTP endpoint, no cookie, and no MFA provider
-- integration. Every token column stores a digest only; no plaintext
-- password, invitation token, reset token, or session token column exists.

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Lowercased, NFC-normalized, unique across every status forever: an
    -- email is never reused by a second account (Section 1).
    normalized_email text NOT NULL UNIQUE CHECK (
        normalized_email = lower(normalized_email)
        AND octet_length(normalized_email) BETWEEN 3 AND 320
        AND normalized_email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'
    ),
    display_name text NOT NULL CHECK (btrim(display_name) <> '' AND octet_length(display_name) <= 200),
    role text NOT NULL CHECK (role IN (
        'system_administrator', 'department_administrator',
        'dispatcher_operator', 'responder', 'read_only_auditor'
    )),
    -- Placeholder department/scope label. Section 15 (unresolved question 3)
    -- leaves the exact department-scope model undecided; this column is a
    -- plain, unconstrained-format string so a future phase can define the
    -- real scope model without a new column. NULL means "no scope boundary
    -- assigned" (used by system_administrator, whose Section 6 authority is
    -- deployment-wide, not scoped).
    scope text CHECK (scope IS NULL OR (btrim(scope) <> '' AND octet_length(scope) <= 100)),
    status text NOT NULL DEFAULT 'invited' CHECK (status IN (
        'invited', 'password_change_required', 'active', 'suspended', 'disabled', 'expired'
    )),
    -- PHC-style encoded Argon2id hash. NULL until a permanent password is
    -- established; never a temporary/invitation credential, which lives only
    -- in the invitations table below, hashed there.
    password_hash text CHECK (
        password_hash IS NULL OR
        password_hash ~ '^\$argon2id\$v=[0-9]+\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+=*\$[A-Za-z0-9+/]+=*$'
    ),
    password_updated_at timestamptz,
    last_login_at timestamptz,
    created_by bigint REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Section 1: "No account reaches active without passing through
    -- password-change-required at least once," restated here as "active
    -- always has a permanent password hash."
    CONSTRAINT users_active_requires_password CHECK (status <> 'active' OR password_hash IS NOT NULL)
);

CREATE INDEX users_role_status_idx ON users (role, status);
CREATE INDEX users_scope_idx ON users (scope) WHERE scope IS NOT NULL;

CREATE TRIGGER users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Append-only-per-issuance: a resend inserts a new row and supersedes the
-- prior one; it never rewrites the prior row's status (Section 2).
CREATE TABLE invitations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- SHA-256 digest of the single-use raw token; the raw token itself is
    -- never persisted anywhere.
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'redeemed', 'expired', 'superseded')),
    issued_by bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    expires_at timestamptz NOT NULL,
    redeemed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT invitations_redeemed_at_matches_status CHECK (
        (status = 'redeemed') = (redeemed_at IS NOT NULL)
    )
);

CREATE INDEX invitations_user_idx ON invitations (user_id, created_at DESC);
-- Enforces "at most one active (pending) invitation per user" at the
-- database level, independent of application-level supersede discipline.
CREATE UNIQUE INDEX invitations_user_pending_idx ON invitations (user_id) WHERE status = 'pending';

-- Append-only-per-issuance, mirroring invitations exactly (Section 4).
CREATE TABLE password_resets (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'redeemed', 'expired', 'superseded')),
    -- NULL for a self-service "forgot password" request; set for an
    -- administrator-initiated reset (Section 2 "Reset" action).
    issued_by bigint REFERENCES users (id) ON DELETE RESTRICT,
    expires_at timestamptz NOT NULL,
    redeemed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT password_resets_redeemed_at_matches_status CHECK (
        (status = 'redeemed') = (redeemed_at IS NOT NULL)
    )
);

CREATE INDEX password_resets_user_idx ON password_resets (user_id, created_at DESC);
CREATE UNIQUE INDEX password_resets_user_pending_idx ON password_resets (user_id) WHERE status = 'pending';

-- Server-side session records (Section 8). Cookie construction is not part
-- of this migration or this phase: see docs/authentication-authorization-v1.md
-- Section 8, not explicitly assigned to Step 9B.
CREATE TABLE sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    device_hint text CHECK (device_hint IS NULL OR octet_length(device_hint) <= 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    -- Absolute lifetime; idle expiration is evaluated by the application
    -- against last_seen_at, since the exact idle-timeout duration is a
    -- caller-supplied, unresolved policy value (Section 15, item 6).
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    revoked_at timestamptz,
    revocation_reason text CHECK (
        revocation_reason IS NULL OR revocation_reason IN (
            'logout', 'logout_all', 'password_reset', 'account_disabled',
            'account_suspended', 'admin_revoke', 'superseded'
        )
    ),
    CONSTRAINT sessions_revocation_matches_reason CHECK (
        (revoked_at IS NULL) = (revocation_reason IS NULL)
    )
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires_idx ON sessions (expires_at) WHERE revoked_at IS NULL;

-- Provider-neutral MFA credential metadata (Section 5, 11.4). No WebAuthn or
-- TOTP provider integration is authorized by Step 9B: this table exists so a
-- future, separately authorized phase can enroll credentials without a new
-- migration. No secret key material for passkeys is ever stored; a TOTP
-- secret, if this method is ever used, is stored only encrypted/hashed, never
-- in plaintext.
CREATE TABLE mfa_credentials (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    credential_type text NOT NULL CHECK (credential_type IN ('passkey', 'totp')),
    -- Passkey: public key material only (no private/secret key material).
    -- TOTP: an encrypted/hashed secret blob, never plaintext.
    credential_data bytea NOT NULL CHECK (octet_length(credential_data) BETWEEN 1 AND 4096),
    label text CHECK (label IS NULL OR octet_length(label) <= 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    UNIQUE (user_id, credential_type, credential_data)
);

CREATE INDEX mfa_credentials_user_idx ON mfa_credentials (user_id) WHERE revoked_at IS NULL;

-- Immutable, append-only audit log (Section 10). Structured, allow-listed
-- metadata only; no password, token, cookie, MFA secret, full notification
-- payload, or unnecessary protected-call content can be stored here, because
-- the application layer (identityaudit package) rejects forbidden keys
-- before this table is ever written, and this table itself has no free-form
-- blob column wide enough for one.
CREATE TABLE identity_audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type text NOT NULL CHECK (event_type IN (
        'login_success', 'login_failure', 'invitation_created', 'invitation_redeemed',
        'invitation_redemption_failed', 'invitation_expired', 'password_reset_requested',
        'password_reset_completed', 'mfa_enrollment', 'mfa_recovery',
        'role_or_scope_change', 'account_state_change', 'protected_call_access',
        'notification_device_event', 'session_created', 'session_revoked',
        'administrative_action'
    )),
    -- The account the event is about; NULL only for a login_failure against
    -- an email that matched no account (Section 10: "attempted email...not
    -- proof an account exists").
    account_id bigint REFERENCES users (id) ON DELETE RESTRICT,
    -- The administrator who performed the action, when the event is
    -- administrator-driven; NULL for a user's own action.
    actor_id bigint REFERENCES users (id) ON DELETE RESTRICT,
    -- Fixed, safe, allow-listed reason/outcome code, mirroring
    -- detection_audit.reason's convention elsewhere in this schema.
    reason text CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    -- Structured, allow-listed metadata only (identityaudit package
    -- enforces the allow-list before insert); bounded size defends against a
    -- pathological blob even if that enforcement is ever bypassed.
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (octet_length(metadata::text) <= 4096),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX identity_audit_log_account_idx ON identity_audit_log (account_id, created_at DESC);
CREATE INDEX identity_audit_log_actor_idx ON identity_audit_log (actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX identity_audit_log_type_created_idx ON identity_audit_log (event_type, created_at DESC);
CREATE INDEX identity_audit_log_created_idx ON identity_audit_log (created_at DESC, id DESC);

CREATE TRIGGER identity_audit_log_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON identity_audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_audit_mutation();
ALTER TABLE identity_audit_log ENABLE ALWAYS TRIGGER identity_audit_log_immutable;

COMMENT ON TABLE users IS 'Step 9B account records (docs/authentication-authorization-v1.md Section 1). No default administrator is inserted by this migration.';
COMMENT ON TABLE invitations IS 'Append-only-per-issuance single-use invitation tokens, hash only (Section 2).';
COMMENT ON TABLE password_resets IS 'Append-only-per-issuance single-use password-reset tokens, hash only (Section 4).';
COMMENT ON TABLE sessions IS 'Server-side session records, hash only (Section 8). No cookie is constructed by this migration.';
COMMENT ON TABLE mfa_credentials IS 'Provider-neutral MFA credential metadata (Section 5). No WebAuthn/TOTP provider integration exists yet.';
COMMENT ON TABLE identity_audit_log IS 'Immutable, append-only identity/authorization audit trail (Section 10). No password, token, or secret column exists.';

INSERT INTO schema_migrations (version) VALUES ('000008');

COMMIT;
