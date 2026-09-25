BEGIN;

-- Step 9F-4: MFA-verified current sessions
-- (docs/authentication-authorization-v1.md Section 5, Section 6; AAX-07).
--
-- An enrolled mfa_credentials row proves an account CAN complete MFA; it
-- has never proven that the CURRENT session did. This migration adds the
-- one fact needed to distinguish them: whether a specific session row has
-- completed a WebAuthn authentication ceremony, and when. It creates no
-- new table, seeds nothing, and does not touch mfa_credentials.

ALTER TABLE sessions ADD COLUMN mfa_verified_at timestamptz;
COMMENT ON COLUMN sessions.mfa_verified_at IS
    'Set only when THIS session completed a WebAuthn authentication ceremony (Section 5, AAX-07). NULL is the default for every existing row (backfilled automatically by adding the column) and every newly created row; application code must treat NULL as fail-closed (not MFA-verified), never as "verification not required."';

-- Extend the audit catalog with a distinct event for a per-session MFA
-- verification ceremony, kept separate from mfa_enrollment (registering a
-- new credential) so the two different security events are never
-- conflated in the audit trail.
ALTER TABLE identity_audit_log DROP CONSTRAINT identity_audit_log_event_type_check;
ALTER TABLE identity_audit_log ADD CONSTRAINT identity_audit_log_event_type_check CHECK (event_type IN (
    'login_success', 'login_failure', 'invitation_created', 'invitation_redeemed',
    'invitation_redemption_failed', 'invitation_expired', 'password_reset_requested',
    'password_reset_completed', 'mfa_enrollment', 'mfa_verification', 'mfa_recovery',
    'role_or_scope_change', 'account_state_change', 'protected_call_access',
    'notification_device_event', 'session_created', 'session_revoked',
    'administrative_action'
));

INSERT INTO schema_migrations (version) VALUES ('000009');

COMMIT;
