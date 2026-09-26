BEGIN;

-- Step 8D-B Part 2: notification-relay persistence foundation
-- (docs/notification-relay-amendment-v1.md Section 25.2, gated on Section 3's
-- Step 9 authentication prerequisite, which docs/authentication-authorization-v1.md
-- and migrations 000008-000009 now satisfy). This migration creates exactly
-- the five tables that document names: notification_devices,
-- notification_consents, notification_outbox, alert_preferences,
-- notification_deliveries. It creates no HTTP route, no provider SDK, no
-- environment variable, no relay code, and enables no delivery: schema alone
-- never sends anything. The backend/internal/notifydevices Go domain
-- package (already reviewed and committed) is not modified by this
-- migration and is not wired to this schema yet.
--
-- Six schema decisions were resolved before this migration was written and
-- are applied here exactly as recorded:
--   1. notification_consents is hard immutable/append-only, reusing the
--      existing reject_alert_audit_mutation() trigger function (defined in
--      000007, already reused by identity_audit_log in 000008) rather than
--      defining a new one.
--   2. notification_devices.test_mode is reserved now
--      (NOT NULL DEFAULT false); the Go Registration struct is not updated
--      by this migration.
--   3. alert_preferences is one mutable current row per user, keyed
--      directly on user_id, with no preference-history versioning.
--   4. Quiet-hours suppression requires no schema of its own: it reuses
--      notification_outbox/notification_deliveries' existing 'canceled'
--      terminal state (Section 10), distinguished only by an application-
--      supplied error_code value on the delivery row.
--   5. Device/account-level audit (registration, revocation, cross-user
--      registration attempts) reuses the existing
--      identity_audit_log.notification_device_event event type, already
--      reserved by migration 000008 and left untouched here. No new audit
--      table is created for this purpose; only notification_deliveries is
--      new, and it exists solely for per-delivery-attempt outcome history.
--   6. notification_deliveries carries outbox_id, event_id, and device_id
--      together, tied by one composite foreign key so a delivery row can
--      never reference an outbox row's id while disagreeing with that same
--      outbox row's own event_id/device_id.
--
-- Evidence-state gating (Section 11, NRA-06): a BEFORE INSERT trigger on
-- notification_outbox rejects any row whose alert_events.state is not
-- 'matched', enforced by the database itself, not only by a future caller's
-- discipline.

-- notification_devices: one authenticated user's bound Push API
-- subscription (Section 4.1, Section 6). Mutable, like sessions/
-- mfa_credentials: state is "active" (revoked_at IS NULL) or not, never a
-- separate status enum. endpoint/p256dh/auth are Web Push subscription
-- values, never a provider (OneSignal or successor) credential, app ID, or
-- webhook secret -- no column in this table, or any table in this
-- migration, ever holds one.
CREATE TABLE notification_devices (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    endpoint text NOT NULL CHECK (
        octet_length(endpoint) BETWEEN 1 AND 2048
        AND endpoint ~ '^https://'
    ),
    -- RFC 8291 fixed sizes: p256dh is an uncompressed P-256 public key
    -- (0x04 || X || Y = 65 bytes); auth is a 16-byte shared secret. Stored
    -- as bytea (decoded), matching this schema's existing convention for
    -- binary secret-shaped values (invitations.token_digest,
    -- password_resets.token_digest, sessions.token_digest,
    -- mfa_credentials.credential_data all follow this same bytea +
    -- octet_length CHECK pattern) rather than as base64 text.
    p256dh bytea NOT NULL CHECK (octet_length(p256dh) = 65),
    auth bytea NOT NULL CHECK (octet_length(auth) = 16),
    -- Client-declared UX hint only (Section 6: "never trusted for security
    -- decisions"); never used for any authorization or delivery decision.
    platform text CHECK (
        platform IS NULL OR (
            octet_length(platform) <= 64
            AND platform ~ '^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$'
        )
    ),
    -- Decision 2: reserved now, read by no code yet. Test-mode delivery
    -- targeting/gating is future notifyoutbox/notifyrelay work, not
    -- authorized or implemented by this migration.
    test_mode boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    -- Section 6 "device replacement": the DeviceID that superseded this row
    -- via a future Replace-shaped write path. Zero/absent rows are never
    -- superseded; a superseded row is always also revoked.
    superseded_by bigint REFERENCES notification_devices (id) ON DELETE RESTRICT,
    CONSTRAINT notification_devices_superseded_implies_revoked CHECK (
        superseded_by IS NULL OR revoked_at IS NOT NULL
    ),
    -- Composite target so notification_consents/notification_outbox can
    -- enforce "this device really belongs to this user" via a composite
    -- foreign key, the same technique migration 000006 already uses for
    -- transcript_dataset_items/transcript_reviews.
    UNIQUE (id, user_id)
);

-- Active notification endpoint uniqueness, enforced at the database level:
-- at most one currently-active registration may ever hold a given endpoint,
-- across every user, not only within one user's own rows. This is the same
-- invariant backend/internal/notifydevices.Store enforces in memory today;
-- this index makes it a structural database guarantee too, independent of
-- any future persistence adapter's own correctness.
CREATE UNIQUE INDEX notification_devices_endpoint_active_idx
    ON notification_devices (endpoint) WHERE revoked_at IS NULL;
CREATE INDEX notification_devices_user_active_idx
    ON notification_devices (user_id) WHERE revoked_at IS NULL;
CREATE INDEX notification_devices_user_idx
    ON notification_devices (user_id, created_at DESC);

CREATE TRIGGER notification_devices_updated_at BEFORE UPDATE ON notification_devices
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- notification_consents: hard immutable, append-only consent/revocation
-- event log (Decision 1). Grant and revoke are each their own inserted row;
-- an existing row is never updated, and "current consent" is always
-- derived by reading the latest row for a device, never cached or trusted
-- from an in-place status column. The composite foreign key below
-- transitively guarantees device_id both exists and belongs to user_id (and,
-- via notification_devices.user_id's own foreign key, that user_id is a
-- valid account); no separate direct users foreign key is needed here,
-- mirroring migration 000006's transcript_reviews composite-FK-only
-- pattern.
CREATE TABLE notification_consents (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL,
    device_id bigint NOT NULL,
    event text NOT NULL CHECK (event IN ('granted', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (device_id, user_id) REFERENCES notification_devices (id, user_id) ON DELETE RESTRICT
);

CREATE INDEX notification_consents_device_idx ON notification_consents (device_id, created_at DESC);
CREATE INDEX notification_consents_user_idx ON notification_consents (user_id, created_at DESC);

-- Decision 1: reuse the existing immutable-audit trigger function rather
-- than defining a new one, exactly as migration 000008 already reused it
-- for identity_audit_log.
CREATE TRIGGER notification_consents_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON notification_consents
    FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_audit_mutation();
ALTER TABLE notification_consents ENABLE ALWAYS TRIGGER notification_consents_immutable;

-- alert_preferences: one mutable current-settings row per user (Decision
-- 3), keyed directly on user_id -- never a surrogate id, never a history of
-- prior versions. A preference change is an ordinary UPDATE to this single
-- row; nothing here retroactively alters an alert_events row or an
-- already-queued notification_outbox entry's original evaluation record
-- (Section 7). Role-based eligibility, if a future phase adds it, is
-- evaluated by joining users.role at delivery time, never duplicated into a
-- column here. Quiet-hours suppress-vs-queue (Decision 4) needs no column:
-- suppression is expressed later as notification_outbox/
-- notification_deliveries' existing 'canceled' terminal state.
CREATE TABLE alert_preferences (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE RESTRICT,
    enabled boolean NOT NULL DEFAULT false,
    channels text[] NOT NULL DEFAULT '{}'::text[]
        CHECK (channels <@ ARRAY['CH1A', 'CH2B', 'CH3B', 'CH4C']::text[]),
    -- tone_configurations/keyword_configurations do not exist yet (they
    -- remain gated on a future administration contract per migration
    -- 000007's own comment), so these are bounded, unconstrained-format
    -- label arrays rather than a foreign key; the existing idPattern format
    -- (`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`) is enforced by the Go alerts
    -- package at input time, not re-validated per-element here -- the same
    -- division of responsibility this schema already uses for
    -- detection_audit.measured_hz (database bounds size, application
    -- validates content).
    tone_set_ids text[] NOT NULL DEFAULT '{}'::text[] CHECK (cardinality(tone_set_ids) <= 200),
    keyword_list_ids text[] NOT NULL DEFAULT '{}'::text[] CHECK (cardinality(keyword_list_ids) <= 200),
    min_confidence numeric CHECK (min_confidence IS NULL OR min_confidence BETWEEN 0 AND 1),
    -- Section 7 "the user's own configured time zone, not server-local
    -- time"; suppress-vs-queue behavior itself (Decision 4) needs no
    -- column here, only the timezone the window is evaluated in.
    quiet_hours_start time,
    quiet_hours_end time,
    quiet_hours_timezone text CHECK (quiet_hours_timezone IS NULL OR octet_length(quiet_hours_timezone) <= 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Paired-field convention already used throughout this schema
    -- (sessions_revocation_matches_reason, invitations_redeemed_at_matches_status):
    -- a half-configured quiet-hours window is never allowed to exist.
    CONSTRAINT alert_preferences_quiet_hours_paired CHECK (
        (quiet_hours_start IS NULL) = (quiet_hours_end IS NULL)
    )
);

CREATE TRIGGER alert_preferences_updated_at BEFORE UPDATE ON alert_preferences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- notification_outbox: a genuinely mutable state machine (state,
-- attempt_count, next_attempt_at all change in place), NOT an append-only
-- audit table -- unlike most of this table's siblings. UNIQUE(event_id,
-- device_id) is the exact idempotency key Section 10 specifies: at most one
-- outbox row may ever exist for one (alert_events.event_id,
-- notification_devices.id) pair. The composite foreign key on
-- (device_id, user_id) enforces device/user ownership consistency the same
-- way notification_consents does.
CREATE TABLE notification_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id text NOT NULL REFERENCES alert_events (event_id) ON DELETE RESTRICT,
    device_id bigint NOT NULL,
    user_id bigint NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'expired', 'canceled', 'dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    -- Explicit, caller-chosen ceiling; this schema assumes no default retry
    -- policy of its own, mirroring alerts.Limits/alertpipeline.Limits'
    -- "never an implicit default" convention.
    max_attempts integer NOT NULL CHECK (max_attempts > 0),
    next_attempt_at timestamptz,
    -- Inherited from alert_events.expires_at (Section 10): an entry that
    -- exhausts attempts or reaches expiration without success is expired,
    -- never sent late.
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_outbox_pending_has_next_attempt CHECK (
        state <> 'pending' OR next_attempt_at IS NOT NULL
    ),
    -- Mirrors alert_events.expires_at's own CHECK (expires_at > created_at):
    -- an outbox entry can never be born already expired.
    CONSTRAINT notification_outbox_expires_after_created CHECK (expires_at > created_at),
    -- Defense in depth against the retry loop's own bounded-attempt policy:
    -- the counter can never silently exceed the ceiling it is bounded by.
    CONSTRAINT notification_outbox_attempt_count_bounded CHECK (attempt_count <= max_attempts),
    UNIQUE (event_id, device_id),
    -- Composite target for notification_deliveries' own outbox_id-tied
    -- foreign key below.
    UNIQUE (id, event_id, device_id),
    FOREIGN KEY (device_id, user_id) REFERENCES notification_devices (id, user_id) ON DELETE RESTRICT
);

CREATE INDEX notification_outbox_pending_idx ON notification_outbox (next_attempt_at) WHERE state = 'pending';
CREATE INDEX notification_outbox_device_idx ON notification_outbox (device_id, created_at DESC);
CREATE INDEX notification_outbox_event_idx ON notification_outbox (event_id);

CREATE TRIGGER notification_outbox_updated_at BEFORE UPDATE ON notification_outbox
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Evidence-state gating (Section 11, NRA-06), database-enforced: a
-- notification_outbox row may only ever be created for, or repointed onto,
-- an alert_events row whose state is 'matched'. 'ambiguous', 'partial',
-- 'low_confidence' events remain fully visible in detection_audit/
-- alert_events for human review but can never produce or capture an outbox
-- entry, structurally, not only by whatever a future caller chooses to
-- check. The trigger fires on UPDATE OF event_id specifically (not on every
-- UPDATE) so ordinary state/attempt_count/next_attempt_at mutation is
-- unaffected; only an attempt to change which event this entry targets is
-- re-validated.
CREATE FUNCTION validate_notification_outbox_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM alert_events WHERE event_id = NEW.event_id AND state = 'matched' FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'notification_outbox may only reference an alert_events row whose state is matched'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER notification_outbox_requires_matched_event
    BEFORE INSERT OR UPDATE OF event_id ON notification_outbox
    FOR EACH ROW EXECUTE FUNCTION validate_notification_outbox_event();

-- notification_deliveries: hard immutable, append-only, one row per relay
-- delivery attempt (Section 17). Decision 6: outbox_id, event_id, and
-- device_id are tied together by one composite foreign key, so a delivery
-- row can never claim an outbox_id while disagreeing with that same outbox
-- row's own event_id/device_id -- giving the full
-- alert_events -> notification_outbox -> notification_deliveries
-- traceability chain a single, structurally enforced source of truth rather
-- than three independently-checkable columns that could silently drift
-- apart. Quiet-hours suppression (Decision 4) is recorded here as
-- outcome = 'canceled' with a distinguishing error_code
-- (for example 'quiet_hours_suppressed'), not a new column or enum value.
-- No provider secret, webhook payload, full notification payload,
-- transcript text, or raw subscription endpoint ever appears in this table,
-- in plaintext or otherwise -- error_code is a fixed, safe, allow-listed
-- code only, mirroring detection_audit.reason exactly.
CREATE TABLE notification_deliveries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    outbox_id bigint NOT NULL,
    event_id text NOT NULL,
    device_id bigint NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('sent', 'failed', 'expired', 'canceled', 'dead_letter', 'unauthorized')),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    error_code text CHECK (error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (outbox_id, event_id, device_id)
        REFERENCES notification_outbox (id, event_id, device_id) ON DELETE RESTRICT
);

CREATE INDEX notification_deliveries_event_idx ON notification_deliveries (event_id, created_at DESC);
CREATE INDEX notification_deliveries_device_idx ON notification_deliveries (device_id, created_at DESC);
CREATE INDEX notification_deliveries_outcome_idx ON notification_deliveries (outcome, created_at DESC);
CREATE INDEX notification_deliveries_outbox_idx ON notification_deliveries (outbox_id);

CREATE TRIGGER notification_deliveries_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON notification_deliveries
    FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_audit_mutation();
ALTER TABLE notification_deliveries ENABLE ALWAYS TRIGGER notification_deliveries_immutable;

COMMENT ON TABLE notification_devices IS 'Step 8D-B: one authenticated user''s bound Push API subscription (docs/notification-relay-amendment-v1.md Section 6). endpoint/p256dh/auth are Web Push subscription values, never a provider credential. At most one active row per endpoint, enforced by a partial unique index.';
COMMENT ON TABLE notification_consents IS 'Step 8D-B: hard immutable, append-only consent/revocation events per user/device (Section 8). Grant and revoke are each a new row; never updated or deleted.';
COMMENT ON TABLE alert_preferences IS 'Step 8D-B: one mutable current-preferences row per user (Section 7). No preference-history versioning; consent history lives separately in notification_consents.';
COMMENT ON TABLE notification_outbox IS 'Step 8D-B: bounded-retry delivery queue (Section 10). Mutable state machine, not an audit log. A row can only ever be created for an alert_events row whose state is matched (Section 11), enforced by trigger.';
COMMENT ON TABLE notification_deliveries IS 'Step 8D-B: hard immutable, append-only, one row per relay delivery attempt (Section 17). No provider secret, webhook payload, full notification payload, transcript text, or raw subscription endpoint ever appears here.';

INSERT INTO schema_migrations (version) VALUES ('000010');

COMMIT;
