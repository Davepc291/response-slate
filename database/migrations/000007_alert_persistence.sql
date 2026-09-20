BEGIN;

-- Step 8C: append-only persistence for the Step 8B synthetic/shadow-only
-- alert-event-v1 model (docs/alert-notification-engine-v1.md Section 5, 9).
-- No tone_configurations, keyword_configurations, notification_deliveries, or
-- alert_preferences table is created here: those remain gated on a future
-- administration or authentication contract (Section 8, 10) and are not
-- authorized by this migration. This migration adds no incident, unit-status,
-- or CAD-write behavior.

CREATE TABLE detection_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at timestamptz NOT NULL,
    source_kind text NOT NULL CHECK (source_kind IN ('synthetic', 'shadow_replay')),
    channel text NOT NULL CHECK (channel IN ('CH1A', 'CH2B', 'CH3B', 'CH4C')),
    tgid bigint NOT NULL,
    detector_kind text NOT NULL CHECK (detector_kind IN ('tone', 'keyword')),
    -- tone_set_id or keyword_list_id; opaque caller-assigned configuration label.
    config_id text NOT NULL CHECK (config_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    state text NOT NULL CHECK (state IN ('matched', 'ambiguous', 'partial', 'low_confidence', 'rejected')),
    reason text NOT NULL CHECK (reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    confidence numeric CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    -- Section 3: "every candidate match records its measured frequency,
    -- duration, and confidence...regardless of outcome." Bounded arrays, never
    -- a free-form blob; length matches tonedetection.maxSegments (8).
    measured_hz double precision[] CHECK (measured_hz IS NULL OR cardinality(measured_hz) BETWEEN 1 AND 8),
    measured_duration_ms bigint[] CHECK (measured_duration_ms IS NULL OR cardinality(measured_duration_ms) BETWEEN 1 AND 8),
    keyword_occurrences integer CHECK (keyword_occurrences IS NULL OR keyword_occurrences BETWEEN 0 AND 100000),
    -- Evidence-only identity (Section 2): exactly one of the two existing
    -- radio_transmissions identity primitives, never a filesystem path.
    audio_fingerprint text CHECK (audio_fingerprint IS NULL OR audio_fingerprint ~ '^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$'),
    source_identity text CHECK (source_identity IS NULL OR source_identity ~ '^[0-9a-f]{64}$'),
    dedup_key text CHECK (dedup_key IS NULL OR dedup_key ~ '^[0-9a-f]{64}$'),
    event_id text CHECK (event_id IS NULL OR event_id ~ '^evt_[0-9a-f]{64}$'),
    CONSTRAINT detection_audit_channel_tgid CHECK (
        (channel = 'CH1A' AND tgid = 57201) OR (channel = 'CH2B' AND tgid = 57202) OR
        (channel = 'CH3B' AND tgid = 57203) OR (channel = 'CH4C' AND tgid = 57204)
    ),
    CONSTRAINT detection_audit_evidence_xor CHECK (
        (audio_fingerprint IS NOT NULL AND source_identity IS NULL) OR
        (audio_fingerprint IS NULL AND source_identity IS NOT NULL)
    ),
    CONSTRAINT detection_audit_measured_pair CHECK (
        (measured_hz IS NULL AND measured_duration_ms IS NULL) OR
        (measured_hz IS NOT NULL AND measured_duration_ms IS NOT NULL AND cardinality(measured_hz) = cardinality(measured_duration_ms))
    ),
    CONSTRAINT detection_audit_tone_fields CHECK (detector_kind = 'tone' OR (measured_hz IS NULL AND measured_duration_ms IS NULL)),
    CONSTRAINT detection_audit_keyword_fields CHECK (detector_kind = 'keyword' OR keyword_occurrences IS NULL),
    -- A rejected evaluation (Section 11: duplicate/malformed input) never
    -- reaches alerts.Builder successfully, so it has no dedup_key/event_id;
    -- every other state always does, because Section 5's Event schema covers
    -- matched, ambiguous, partial, and low_confidence alike.
    CONSTRAINT detection_audit_rejected_shape CHECK (
        (state = 'rejected' AND dedup_key IS NULL AND event_id IS NULL) OR
        (state <> 'rejected' AND dedup_key IS NOT NULL AND event_id IS NOT NULL)
    ),
    FOREIGN KEY (audio_fingerprint) REFERENCES radio_transmissions (audio_fingerprint) ON DELETE RESTRICT,
    FOREIGN KEY (source_identity) REFERENCES radio_transmissions (source_identity) ON DELETE RESTRICT
);

CREATE INDEX detection_audit_created_idx ON detection_audit (created_at DESC, id DESC);
CREATE INDEX detection_audit_dedup_idx ON detection_audit (dedup_key, created_at DESC) WHERE dedup_key IS NOT NULL;
CREATE INDEX detection_audit_event_idx ON detection_audit (event_id) WHERE event_id IS NOT NULL;

CREATE FUNCTION reject_alert_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'alert audit history is append-only; insert a new row instead of mutating history'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER detection_audit_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON detection_audit
    FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_audit_mutation();
ALTER TABLE detection_audit ENABLE ALWAYS TRIGGER detection_audit_immutable;

-- alert-event-v1 (Section 5). Field set matches the contract's table exactly;
-- no transcript text, file path, credential, raw audio, or dataset identifier
-- column exists, and none can be added without a new migration.
CREATE TABLE alert_events (
    event_id text PRIMARY KEY CHECK (event_id ~ '^evt_[0-9a-f]{64}$'),
    schema_version text NOT NULL CHECK (schema_version = 'alert-event-v1'),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    source_kind text NOT NULL CHECK (source_kind IN ('synthetic', 'shadow_replay')),
    channel text NOT NULL CHECK (channel IN ('CH1A', 'CH2B', 'CH3B', 'CH4C')),
    tgid bigint NOT NULL,
    detector_kind text NOT NULL CHECK (detector_kind IN ('tone', 'keyword')),
    tone_set_id text CHECK (tone_set_id IS NULL OR tone_set_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    keyword_list_id text CHECK (keyword_list_id IS NULL OR keyword_list_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    confidence numeric CHECK (confidence IS NULL OR confidence BETWEEN 0 AND 1),
    state text NOT NULL CHECK (state IN ('matched', 'ambiguous', 'partial', 'low_confidence')),
    synthetic boolean NOT NULL CHECK (synthetic),
    shadow_only boolean NOT NULL CHECK (shadow_only),
    dedup_key text NOT NULL CHECK (dedup_key ~ '^[0-9a-f]{64}$'),
    display_summary text NOT NULL CHECK (
        octet_length(display_summary) BETWEEN 1 AND 256
        AND display_summary LIKE '%NOT LIVE CAD%'
        AND translate(display_summary, E'\n\r\t', '') !~ '[[:cntrl:]]'
    ),
    -- Traceability back to the audit row that produced this event; not part
    -- of the Section 5 field table, but never exposes anything the audit row
    -- itself does not already carry.
    detection_audit_id bigint NOT NULL REFERENCES detection_audit (id) ON DELETE RESTRICT,
    CONSTRAINT alert_events_channel_tgid CHECK (
        (channel = 'CH1A' AND tgid = 57201) OR (channel = 'CH2B' AND tgid = 57202) OR
        (channel = 'CH3B' AND tgid = 57203) OR (channel = 'CH4C' AND tgid = 57204)
    ),
    CONSTRAINT alert_events_detector_config CHECK (
        (detector_kind = 'tone' AND tone_set_id IS NOT NULL AND keyword_list_id IS NULL) OR
        (detector_kind = 'keyword' AND keyword_list_id IS NOT NULL AND tone_set_id IS NULL)
    )
);

CREATE INDEX alert_events_dedup_idx ON alert_events (dedup_key, created_at DESC);
CREATE INDEX alert_events_created_idx ON alert_events (created_at DESC, event_id);
CREATE INDEX alert_events_channel_created_idx ON alert_events (channel, created_at DESC);
CREATE INDEX alert_events_audit_idx ON alert_events (detection_audit_id);

CREATE TRIGGER alert_events_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON alert_events
    FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_audit_mutation();
ALTER TABLE alert_events ENABLE ALWAYS TRIGGER alert_events_immutable;

-- Single-statement atomic write path: one detection_audit row always, and at
-- most one alert_events row, guarded by a per-dedup_key advisory lock so
-- concurrent detections of the same evidence/detector/config serialize
-- instead of racing the cooldown check. Cooldown is a required, explicit,
-- caller-supplied argument (Section 14 leaves the actual duration
-- unresolved); this function assumes no default cooldown of its own. The
-- upper bound of 2592000 seconds (30 days) is a resource-safety ceiling
-- against pathological input, not a chosen operational policy value.
CREATE FUNCTION record_alert_detection(
    p_created_at timestamptz, p_source_kind text, p_channel text, p_tgid bigint,
    p_detector_kind text, p_config_id text, p_state text, p_reason text, p_confidence numeric,
    p_measured_hz double precision[], p_measured_duration_ms bigint[], p_keyword_occurrences integer,
    p_audio_fingerprint text, p_source_identity text,
    p_dedup_key text, p_event_id text, p_expires_at timestamptz,
    p_tone_set_id text, p_keyword_list_id text, p_display_summary text, p_cooldown_seconds integer
) RETURNS TABLE(audit_id bigint, event_created boolean, event_suppressed boolean)
LANGUAGE plpgsql AS $$
DECLARE
    v_audit_id bigint;
    v_existing timestamptz;
    v_created boolean := false;
    v_suppressed boolean := false;
    v_rowcount integer;
BEGIN
    IF p_cooldown_seconds IS NULL OR p_cooldown_seconds < 0 OR p_cooldown_seconds > 2592000 THEN
        RAISE EXCEPTION 'cooldown_seconds must be an explicit value between 0 and 2592000' USING ERRCODE = '22023';
    END IF;

    INSERT INTO detection_audit (created_at, source_kind, channel, tgid, detector_kind, config_id,
        state, reason, confidence, measured_hz, measured_duration_ms, keyword_occurrences,
        audio_fingerprint, source_identity, dedup_key, event_id)
    VALUES (p_created_at, p_source_kind, p_channel, p_tgid, p_detector_kind, p_config_id,
        p_state, p_reason, p_confidence, p_measured_hz, p_measured_duration_ms, p_keyword_occurrences,
        p_audio_fingerprint, p_source_identity, p_dedup_key, p_event_id)
    RETURNING id INTO v_audit_id;

    IF p_state <> 'rejected' THEN
        PERFORM pg_advisory_xact_lock(hashtextextended(p_dedup_key, 0));
        IF EXISTS (SELECT 1 FROM alert_events WHERE event_id = p_event_id) THEN
            -- Idempotent retry of an already-recorded event: no new row, and
            -- this is not a cooldown suppression of a distinct detection.
            v_created := false;
            v_suppressed := false;
        ELSE
            SELECT max(created_at) INTO v_existing FROM alert_events WHERE dedup_key = p_dedup_key;
            IF v_existing IS NOT NULL AND p_created_at < v_existing + make_interval(secs => p_cooldown_seconds) THEN
                v_suppressed := true;
            ELSE
                INSERT INTO alert_events (event_id, schema_version, created_at, expires_at, source_kind,
                    channel, tgid, detector_kind, tone_set_id, keyword_list_id, confidence, state,
                    synthetic, shadow_only, dedup_key, display_summary, detection_audit_id)
                VALUES (p_event_id, 'alert-event-v1', p_created_at, p_expires_at, p_source_kind,
                    p_channel, p_tgid, p_detector_kind, p_tone_set_id, p_keyword_list_id, p_confidence, p_state,
                    true, true, p_dedup_key, p_display_summary, v_audit_id)
                ON CONFLICT (event_id) DO NOTHING;
                GET DIAGNOSTICS v_rowcount = ROW_COUNT;
                v_created := v_rowcount > 0;
            END IF;
        END IF;
    END IF;

    RETURN QUERY SELECT v_audit_id, v_created, v_suppressed;
END;
$$;

COMMENT ON TABLE detection_audit IS 'Append-only Step 8B detector evaluation attempts (matched/ambiguous/partial/low_confidence/rejected), synthetic and shadow-only. No transcript text, file path, credential, or raw audio.';
COMMENT ON TABLE alert_events IS 'Append-only alert-event-v1 records (docs/alert-notification-engine-v1.md Section 5). synthetic and shadow_only are always true through Step 8F. No relay or preference system reads this table yet.';
COMMENT ON FUNCTION record_alert_detection IS 'Sole write path for Step 8C alert persistence: one audit row always, at most one event row per dedup_key per caller-supplied cooldown, atomic per call.';
INSERT INTO schema_migrations(version) VALUES ('000007');
COMMIT;
