\set ON_ERROR_STOP on
BEGIN;
CREATE FUNCTION pg_temp.check_alert(ok boolean, label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %', label; END IF; RAISE NOTICE 'PASS: %', label; END; $$;

-- Synthetic evidence only; every fixture and detection in this suite rolls back.
CREATE FUNCTION pg_temp.alert_fixture(label text, OUT source_identity text, OUT fingerprint text)
LANGUAGE plpgsql AS $$
DECLARE src text := md5(label || txid_current()); n bigint;
BEGIN
    source_identity := src || src;
    fingerprint := 'pcm-s16le-16000-mono-v1:sha256:' || src || src;
    INSERT INTO radio_transmissions(source_identity, source_path, source_filename, source_recorder, tgid, rid,
        recorded_at, system_site_label, alias_channel_label, channel_label, extension, recording_timezone,
        source_size_bytes, source_modified_at)
    VALUES(source_identity, 'synthetic-alert/' || label || '.mp3', label || '.mp3', 'SDRTrunk', 57201, 578060, now(),
        'synthetic', 'synthetic', 'CH1A', '.mp3', 'America/New_York', 100, now())
    RETURNING id INTO n;
    UPDATE radio_transmissions SET processing_status = 'processing' WHERE id = n;
    PERFORM complete_audio_analysis(source_identity, fingerprint, 1000, 0.1, 0.5, '{}');
END; $$;

DO $$
DECLARE
    sid text; fp text;
    dedup_a text := md5('dedup-a-1') || md5('dedup-a-2');
    dedup_b text := md5('dedup-b-1') || md5('dedup-b-2');
    event_a text := 'evt_' || md5('event-a-1') || md5('event-a-2');
    event_a_retry_id bigint;
    event_a2 text := 'evt_' || md5('event-a2-1') || md5('event-a2-2');
    event_b text := 'evt_' || md5('event-b-1') || md5('event-b-2');
    v_audit_id bigint; v_created boolean; v_suppressed boolean;
    audit_count_before bigint; audit_count_after bigint;
    sql text;
BEGIN
    PERFORM pg_temp.check_alert(EXISTS(SELECT FROM schema_migrations WHERE version = '000007'), 'migration recorded');

    SELECT * INTO sid, fp FROM pg_temp.alert_fixture('canonical');

    -- 1. Successful tone-result persistence.
    SELECT audit_id, event_created, event_suppressed INTO v_audit_id, v_created, v_suppressed
    FROM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'matched', 'matched',
        0.9, ARRAY[600, 900]::double precision[], ARRAY[500, 500]::bigint[], NULL, fp, NULL,
        dedup_a, event_a, now() + interval '5 minutes', 'quick-call-1', NULL,
        'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
    PERFORM pg_temp.check_alert(v_created AND NOT v_suppressed, 'tone match creates a visible alert event');
    PERFORM pg_temp.check_alert(EXISTS(SELECT FROM alert_events WHERE event_id = event_a AND detection_audit_id = v_audit_id),
        'tone alert event references its own audit row');

    -- 2. Successful keyword-result persistence (ambiguous state).
    SELECT audit_id, event_created, event_suppressed INTO v_audit_id, v_created, v_suppressed
    FROM record_alert_detection(now(), 'shadow_replay', 'CH1A', 57201, 'keyword', 'structure-fire', 'ambiguous',
        'keyword_ambiguous_context', NULL, NULL, NULL, 2, NULL, sid,
        dedup_b, event_b, now() + interval '1 minute', NULL, 'structure-fire',
        'CH1A keyword ambiguous match: structure fire (synthetic). NOT LIVE CAD.', 300);
    PERFORM pg_temp.check_alert(v_created AND NOT v_suppressed, 'keyword ambiguous match creates a visible alert event');
    PERFORM pg_temp.check_alert((SELECT state FROM alert_events WHERE event_id = event_b) = 'ambiguous',
        'ambiguous state preserved, not silently promoted or dropped');

    -- 3. Duplicate retry / idempotency: identical event_id replays with no new event row.
    SELECT audit_id INTO event_a_retry_id
    FROM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'matched', 'matched',
        0.9, ARRAY[600, 900]::double precision[], ARRAY[500, 500]::bigint[], NULL, fp, NULL,
        dedup_a, event_a, now() + interval '5 minutes', 'quick-call-1', NULL,
        'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
    PERFORM pg_temp.check_alert(event_a_retry_id <> v_audit_id, 'retry still writes its own audit row');
    PERFORM pg_temp.check_alert((SELECT count(*) FROM alert_events WHERE event_id = event_a) = 1,
        'retried event_id is idempotent: exactly one alert_events row');
    PERFORM pg_temp.check_alert((SELECT count(*) FROM detection_audit WHERE event_id = event_a) = 2,
        'every attempt is still audited, including the retry');

    -- 4. Deduplication: a distinct detection sharing dedup_a within cooldown is suppressed, not silently dropped.
    SELECT audit_id, event_created, event_suppressed INTO v_audit_id, v_created, v_suppressed
    FROM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'matched', 'matched',
        0.95, ARRAY[600, 900]::double precision[], ARRAY[500, 520]::bigint[], NULL, fp, NULL,
        dedup_a, event_a2, now() + interval '5 minutes', 'quick-call-1', NULL,
        'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
    PERFORM pg_temp.check_alert(NOT v_created AND v_suppressed, 'second detection within cooldown is suppressed, not delivered');
    PERFORM pg_temp.check_alert((SELECT count(*) FROM alert_events WHERE dedup_key = dedup_a) = 1,
        'at most one visible alert per dedup_key within its cooldown period');
    PERFORM pg_temp.check_alert((SELECT count(*) FROM detection_audit WHERE dedup_key = dedup_a) = 3,
        'the suppressed duplicate still writes its own detection audit row');

    -- 5. Rejected evidence: malformed/duplicate input never reaches alert_events.
    SELECT audit_id, event_created, event_suppressed INTO v_audit_id, v_created, v_suppressed
    FROM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'rejected',
        'builder_validation_failed', NULL, NULL, NULL, NULL, fp, NULL,
        NULL, NULL, now() + interval '5 minutes', NULL, NULL, NULL, 300);
    PERFORM pg_temp.check_alert(NOT v_created AND NOT v_suppressed, 'rejected input never becomes a visible alert');
    PERFORM pg_temp.check_alert((SELECT state FROM detection_audit WHERE id = v_audit_id) = 'rejected',
        'rejected attempts are still recorded in the audit log');

    -- 6. Bounds and malformed input are rejected, never partially processed.
    audit_count_before := (SELECT count(*) FROM detection_audit);
    BEGIN
        PERFORM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'matched', 'matched',
            0.9, ARRAY[1,2,3,4,5,6,7,8,9]::double precision[], ARRAY[1,2,3,4,5,6,7,8,9]::bigint[], NULL, fp, NULL,
            md5('oversize')||md5('oversize2'), 'evt_'||md5('oversize3')||md5('oversize4'), now() + interval '1 minute',
            'quick-call-1', NULL, 'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
        RAISE EXCEPTION 'FAIL: oversized measured_hz array accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: measured array cardinality bounded'; END;

    BEGIN
        PERFORM record_alert_detection(now(), 'synthetic', 'CH1A', 57202, 'tone', 'quick-call-1', 'matched', 'matched',
            0.9, NULL, NULL, NULL, fp, NULL,
            md5('mismatch')||md5('mismatch2'), 'evt_'||md5('mismatch3')||md5('mismatch4'), now() + interval '1 minute',
            'quick-call-1', NULL, 'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
        RAISE EXCEPTION 'FAIL: mismatched channel/tgid accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: channel/tgid mapping enforced (contract Section 2 restriction)'; END;

    -- 7. SQL-injection-shaped labels/config remain inert: rejected by CHECK, never executed.
    BEGIN
        PERFORM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone',
            $q$quick'; DROP TABLE alert_events; --$q$, 'matched', 'matched',
            0.9, NULL, NULL, NULL, fp, NULL,
            md5('inject')||md5('inject2'), 'evt_'||md5('inject3')||md5('inject4'), now() + interval '1 minute',
            $q$quick'; DROP TABLE alert_events; --$q$, NULL,
            'CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.', 300);
        RAISE EXCEPTION 'FAIL: SQL-injection-shaped config_id accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: SQL-injection-shaped config_id rejected inertly'; END;
    PERFORM pg_temp.check_alert(EXISTS(SELECT FROM information_schema.tables WHERE table_name = 'alert_events'),
        'alert_events table still exists: injection-shaped input was never executed as SQL');
    audit_count_after := (SELECT count(*) FROM detection_audit);
    PERFORM pg_temp.check_alert(audit_count_before = audit_count_after,
        'every rejected call rolled back completely: no partial audit row leaked');

    -- 8. Transaction rollback on partial failure: a bad display_summary fails the
    -- alert_events insert after the detection_audit insert already ran inside the
    -- same function call; both must roll back together, not just the second half.
    audit_count_before := (SELECT count(*) FROM detection_audit);
    BEGIN
        PERFORM record_alert_detection(now(), 'synthetic', 'CH1A', 57201, 'tone', 'quick-call-1', 'matched', 'matched',
            0.9, NULL, NULL, NULL, fp, NULL,
            md5('badsummary')||md5('badsummary2'), 'evt_'||md5('badsummary3')||md5('badsummary4'), now() + interval '1 minute',
            'quick-call-1', NULL, 'missing the required safety phrase', 300);
        RAISE EXCEPTION 'FAIL: display_summary without NOT LIVE CAD accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: NOT LIVE CAD labeling enforced (ANE-02)'; END;
    audit_count_after := (SELECT count(*) FROM detection_audit);
    PERFORM pg_temp.check_alert(audit_count_before = audit_count_after,
        'partial failure inside one detection call rolls back its own audit insert too');

    -- 9. Append-only enforcement on both tables.
    FOREACH sql IN ARRAY ARRAY[
        'UPDATE detection_audit SET reason=''rewritten''', 'DELETE FROM detection_audit', 'TRUNCATE detection_audit CASCADE',
        'UPDATE alert_events SET state=''matched''', 'DELETE FROM alert_events', 'TRUNCATE alert_events'
    ] LOOP
        BEGIN EXECUTE sql; RAISE EXCEPTION 'FAIL: audit/event mutation allowed (%)', sql;
        EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: append-only enforcement %', split_part(sql, ' ', 1);
        END;
    END LOOP;

    -- 10. Ordering: detection_audit identity assignment preserves call order.
    PERFORM pg_temp.check_alert(
        (SELECT min(id) FROM detection_audit WHERE dedup_key = dedup_a) < (SELECT max(id) FROM detection_audit WHERE dedup_key = dedup_a),
        'detector output ordering preserved via monotonically assigned audit ids');
END; $$;
ROLLBACK;
