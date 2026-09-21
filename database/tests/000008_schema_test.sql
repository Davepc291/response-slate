\set ON_ERROR_STOP on
BEGIN;
CREATE FUNCTION pg_temp.check_identity(ok boolean, label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %', label; END IF; RAISE NOTICE 'PASS: %', label; END; $$;

-- Every fixture below is synthetic and rolls back. No real name, email, or
-- credential appears anywhere in this file. This migration inserts no
-- default administrator; the bootstrap admin row created here exists only
-- inside this rolled-back transaction.

DO $$
DECLARE
    admin_id bigint;
    target_user_id bigint;
    inv1_id bigint;
    reset1_id bigint;
    session1_id bigint;
    dummy_digest bytea := sha256('synthetic-token-1'::bytea);
    dummy_digest2 bytea := sha256('synthetic-token-2'::bytea);
    audit_id bigint;
    sql text;
BEGIN
    PERFORM pg_temp.check_identity(EXISTS(SELECT FROM schema_migrations WHERE version = '000008'), 'migration recorded');

    -- 1. Bootstrap: a self-referencing-free administrator row (created_by NULL is allowed).
    INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
    VALUES ('synthetic-admin@example.test', 'Synthetic Admin', 'system_administrator', 'active',
        '$argon2id$v=19$m=65536,t=3,p=2$c29tZXNhbHQ$aGFzaHZhbHVl', now())
    RETURNING id INTO admin_id;
    PERFORM pg_temp.check_identity(admin_id IS NOT NULL, 'bootstrap administrator inserted');

    -- 2. Normal invited user, created_by the bootstrap admin.
    INSERT INTO users (normalized_email, display_name, role, created_by)
    VALUES ('synthetic-user@example.test', 'Synthetic User', 'dispatcher_operator', admin_id)
    RETURNING id INTO target_user_id;
    PERFORM pg_temp.check_identity(
        (SELECT status FROM users WHERE id = target_user_id) = 'invited', 'new user defaults to invited status');
    PERFORM pg_temp.check_identity(
        (SELECT password_hash FROM users WHERE id = target_user_id) IS NULL, 'invited user has no password hash');

    -- 3. Duplicate normalized_email is rejected, including case difference (must be pre-lowercased).
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, created_by)
        VALUES ('synthetic-user@example.test', 'Duplicate', 'responder', admin_id);
        RAISE EXCEPTION 'FAIL: duplicate normalized_email accepted';
    EXCEPTION WHEN unique_violation THEN RAISE NOTICE 'PASS: normalized_email unique across accounts'; END;

    -- 4. Non-lowercase or malformed email is rejected by the CHECK constraint.
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, created_by)
        VALUES ('Mixed-Case@example.test', 'Bad Email', 'responder', admin_id);
        RAISE EXCEPTION 'FAIL: non-lowercase normalized_email accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: normalized_email must already be lowercased'; END;
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, created_by)
        VALUES ('not-an-email', 'Bad Email', 'responder', admin_id);
        RAISE EXCEPTION 'FAIL: malformed email accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: email grammar enforced'; END;

    -- 5. Unknown role is rejected; default-deny extends to the schema layer.
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, created_by)
        VALUES ('synthetic-badrole@example.test', 'Bad Role', 'superuser', admin_id);
        RAISE EXCEPTION 'FAIL: unknown role accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: unknown role rejected'; END;

    -- 6. An account cannot be marked active without a password hash.
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, status, created_by)
        VALUES ('synthetic-noactivepw@example.test', 'No Password', 'responder', 'active', admin_id);
        RAISE EXCEPTION 'FAIL: active status without password_hash accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: active status requires a password hash'; END;

    -- 7. Malformed (non-PHC) password hash is rejected.
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, status, password_hash, created_by)
        VALUES ('synthetic-badhash@example.test', 'Bad Hash', 'responder', 'active', 'plaintext-not-a-hash', admin_id);
        RAISE EXCEPTION 'FAIL: malformed password hash accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: password hash must be PHC-encoded argon2id'; END;

    -- 8. Deleting a referenced user is restricted (foreign key protects audit/lineage integrity).
    BEGIN
        DELETE FROM users WHERE id = admin_id;
        RAISE EXCEPTION 'FAIL: referenced administrator deleted';
    EXCEPTION WHEN foreign_key_violation THEN RAISE NOTICE 'PASS: referenced user cannot be deleted (created_by FK)'; END;

    -- 9. Invitation issuance: hash-only, single pending invitation per user.
    INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
    VALUES (target_user_id, dummy_digest, admin_id, now() + interval '1 day')
    RETURNING id INTO inv1_id;
    PERFORM pg_temp.check_identity(inv1_id IS NOT NULL, 'invitation issued with digest only');
    BEGIN
        INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
        VALUES (target_user_id, dummy_digest2, admin_id, now() + interval '1 day');
        RAISE EXCEPTION 'FAIL: second pending invitation for the same user accepted';
    EXCEPTION WHEN unique_violation THEN RAISE NOTICE 'PASS: at most one pending invitation per user'; END;

    -- 10. Resend supersedes the prior invitation, then a new pending invitation is allowed.
    UPDATE invitations SET status = 'superseded' WHERE id = inv1_id;
    INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
    VALUES (target_user_id, dummy_digest2, admin_id, now() + interval '1 day');
    PERFORM pg_temp.check_identity(
        (SELECT count(*) FROM invitations i WHERE i.user_id = target_user_id AND i.status = 'pending') = 1,
        'exactly one pending invitation after supersede-and-reissue');

    -- 11. Redeemed status requires redeemed_at, and vice versa.
    BEGIN
        UPDATE invitations SET status = 'redeemed' WHERE id = inv1_id;
        RAISE EXCEPTION 'FAIL: redeemed status without redeemed_at accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: redeemed status requires redeemed_at'; END;

    -- 12. Password reset tokens follow the identical hash-only, single-pending pattern.
    INSERT INTO password_resets (user_id, token_digest, expires_at)
    VALUES (target_user_id, sha256('synthetic-reset-1'::bytea), now() + interval '1 hour')
    RETURNING id INTO reset1_id;
    PERFORM pg_temp.check_identity(reset1_id IS NOT NULL, 'password reset issued with digest only, self-service (issued_by NULL)');
    BEGIN
        INSERT INTO password_resets (user_id, token_digest, expires_at)
        VALUES (target_user_id, sha256('synthetic-reset-2'::bytea), now() + interval '1 hour');
        RAISE EXCEPTION 'FAIL: second pending password reset for the same user accepted';
    EXCEPTION WHEN unique_violation THEN RAISE NOTICE 'PASS: at most one pending password reset per user'; END;

    -- 13. Sessions: hash-only, expiry must be after creation, revocation fields are paired.
    INSERT INTO sessions (user_id, token_digest, expires_at)
    VALUES (target_user_id, sha256('synthetic-session-1'::bytea), now() + interval '1 hour')
    RETURNING id INTO session1_id;
    PERFORM pg_temp.check_identity(session1_id IS NOT NULL, 'session created with digest only');
    BEGIN
        INSERT INTO sessions (user_id, token_digest, expires_at)
        VALUES (target_user_id, sha256('synthetic-session-bad'::bytea), now() - interval '1 minute');
        RAISE EXCEPTION 'FAIL: expires_at before created_at accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: session expiry must be after creation'; END;
    BEGIN
        UPDATE sessions SET revoked_at = now() WHERE id = session1_id;
        RAISE EXCEPTION 'FAIL: revoked_at without revocation_reason accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: revocation requires a reason code'; END;
    UPDATE sessions SET revoked_at = now(), revocation_reason = 'logout' WHERE id = session1_id;
    PERFORM pg_temp.check_identity(
        (SELECT revoked_at FROM sessions WHERE id = session1_id) IS NOT NULL, 'session revocation recorded');

    -- 14. MFA credential metadata: provider-neutral, bounded size, no plaintext secret column exists.
    INSERT INTO mfa_credentials (user_id, credential_type, credential_data)
    VALUES (target_user_id, 'passkey', '\x0102030405'::bytea);
    BEGIN
        INSERT INTO mfa_credentials (user_id, credential_type, credential_data)
        VALUES (target_user_id, 'yubikey-otp', '\x0102'::bytea);
        RAISE EXCEPTION 'FAIL: unknown credential_type accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: unknown MFA credential type rejected'; END;
    BEGIN
        INSERT INTO mfa_credentials (user_id, credential_type, credential_data)
        VALUES (target_user_id, 'totp', ''::bytea);
        RAISE EXCEPTION 'FAIL: empty credential_data accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: empty MFA credential data rejected'; END;

    -- 15. Audit log: allow-listed event types only, bounded metadata, and no free-text secret column.
    INSERT INTO identity_audit_log (event_type, account_id, actor_id, reason, metadata)
    VALUES ('invitation_created', target_user_id, admin_id, 'invited', '{"role": "dispatcher_operator"}'::jsonb)
    RETURNING id INTO audit_id;
    PERFORM pg_temp.check_identity(audit_id IS NOT NULL, 'audit event recorded with allow-listed metadata');
    BEGIN
        INSERT INTO identity_audit_log (event_type, account_id) VALUES ('user_deleted_permanently', target_user_id);
        RAISE EXCEPTION 'FAIL: unknown event_type accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: unknown audit event_type rejected (default deny)'; END;
    BEGIN
        INSERT INTO identity_audit_log (event_type, account_id, metadata)
        VALUES ('login_failure', NULL, jsonb_build_object('blob', repeat('x', 5000)));
        RAISE EXCEPTION 'FAIL: oversized metadata blob accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: audit metadata size bounded'; END;
    -- login_failure against an unmatched email has no account: account_id NULL is allowed.
    INSERT INTO identity_audit_log (event_type, account_id, reason) VALUES ('login_failure', NULL, 'no_such_account');
    PERFORM pg_temp.check_identity(true, 'login_failure with no matched account is recordable (account_id NULL)');

    -- 16. Append-only enforcement on identity_audit_log.
    FOREACH sql IN ARRAY ARRAY[
        'UPDATE identity_audit_log SET reason=''rewritten''', 'DELETE FROM identity_audit_log', 'TRUNCATE identity_audit_log'
    ] LOOP
        BEGIN EXECUTE sql; RAISE EXCEPTION 'FAIL: audit log mutation allowed (%)', sql;
        EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: append-only enforcement %', split_part(sql, ' ', 1);
        END;
    END LOOP;

    -- 17. SQL-injection-shaped values remain inert: rejected by CHECK/type system, never executed.
    BEGIN
        INSERT INTO users (normalized_email, display_name, role, created_by)
        VALUES ($q$x'; DROP TABLE users; --@example.test$q$, 'Injector', 'responder', admin_id);
        RAISE EXCEPTION 'FAIL: injection-shaped email accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: injection-shaped email rejected inertly'; END;
    PERFORM pg_temp.check_identity(EXISTS(SELECT FROM information_schema.tables WHERE table_name = 'users'),
        'users table still exists: injection-shaped input was never executed as SQL');
END; $$;
ROLLBACK;
