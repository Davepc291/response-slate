\set ON_ERROR_STOP on
BEGIN;

CREATE FUNCTION pg_temp.check_audio(condition boolean, label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF condition IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %', label; END IF;
    RAISE NOTICE 'PASS: %', label;
END;
$$;

DO $$
DECLARE
    first_source text := md5('audio-first-' || txid_current()) || md5('audio-first-' || txid_current());
    second_source text := md5('audio-second-' || txid_current()) || md5('audio-second-' || txid_current());
    fingerprint text := 'pcm-s16le-16000-mono-v1:sha256:' || md5('pcm-' || txid_current()) || md5('pcm-' || txid_current());
    first_id bigint;
    second_id bigint;
    outcome text;
    rows_changed bigint;
BEGIN
    PERFORM pg_temp.check_audio(EXISTS(SELECT FROM schema_migrations WHERE version='000003'), 'migration 000003 recorded');
    INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,
        recorded_at,system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at)
    SELECT s, 'synthetic/' || s || '.mp3', 'synthetic.mp3', 'SDRTrunk',57201,578060,
        now(),'Greenwich_Fairfield','T-NEW_GFD1','CH1A','.mp3','America/New_York',100,now()
    FROM unnest(ARRAY[first_source,second_source]) AS s;
    SELECT id INTO first_id FROM radio_transmissions WHERE source_identity=first_source;
    SELECT id INTO second_id FROM radio_transmissions WHERE source_identity=second_source;

    UPDATE radio_transmissions SET processing_status='processing',analysis_attempts=analysis_attempts+1
        WHERE source_identity=first_source AND processing_status IN ('pending','failed')
        AND analysis_retryable AND analysis_attempts<3;
    GET DIAGNOSTICS rows_changed=ROW_COUNT;
    PERFORM pg_temp.check_audio(rows_changed=1, 'pending analysis claimed once');
    UPDATE radio_transmissions SET processing_status='processing',analysis_attempts=analysis_attempts+1
        WHERE source_identity=first_source AND processing_status IN ('pending','failed') AND analysis_retryable AND analysis_attempts<3;
    GET DIAGNOSTICS rows_changed=ROW_COUNT;
    PERFORM pg_temp.check_audio(rows_changed=0, 'concurrent claim cannot acquire processing row');

    outcome:=complete_audio_analysis(first_source,fingerprint,1000,0.25,0.5,'{"format":"mp3","codec":"mp3","sample_rate":8000,"channels":1}');
    PERFORM pg_temp.check_audio(outcome='analyzed' AND EXISTS(SELECT FROM radio_transmissions WHERE id=first_id
        AND duration_ms=1000 AND rms=0.25 AND peak=0.5 AND audio_fingerprint=fingerprint
        AND processing_status='completed' AND NOT analysis_retryable AND audio_probe->>'format'='mp3'),
        'duration, RMS, peak, probe, fingerprint, and completion stored together');
    PERFORM pg_temp.check_audio(complete_audio_analysis(first_source,fingerprint,1000,0.25,0.5,'{}')='already-processed',
        'completion retry is idempotent');

    UPDATE radio_transmissions SET processing_status='processing',analysis_attempts=1 WHERE id=second_id;
    outcome:=complete_audio_analysis(second_source,fingerprint,1000,0.25,0.5,'{"format":"mp3"}');
    PERFORM pg_temp.check_audio(outcome='duplicate-content' AND EXISTS(SELECT FROM radio_transmissions WHERE id=second_id
        AND audio_duplicate_of=first_id AND audio_fingerprint IS NULL AND processing_status='skipped'
        AND duration_ms=1000 AND NOT analysis_retryable), 'second path preserved as skipped canonical alias');
    PERFORM pg_temp.check_audio((SELECT count(*)=1 FROM radio_transmissions WHERE audio_fingerprint=fingerprint), 'one logical fingerprint owner');
    PERFORM pg_temp.check_audio(complete_audio_analysis(second_source,fingerprint,1000,0.25,0.5,'{}')='already-processed', 'duplicate completion retry is idempotent');
    UPDATE radio_transmissions SET processing_status='failed',error_message='audio_analysis_failed'
        WHERE id=first_id AND processing_status='processing';
    GET DIAGNOSTICS rows_changed=ROW_COUNT;
    PERFORM pg_temp.check_audio(rows_changed=0, 'late failure cannot overwrite committed success');

    BEGIN
        UPDATE radio_transmissions SET audio_duplicate_of=id WHERE id=second_id;
        RAISE EXCEPTION 'FAIL: self duplicate accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: self duplicate rejected'; END;
    BEGIN
        DELETE FROM radio_transmissions WHERE id=first_id;
        RAISE EXCEPTION 'FAIL: referenced canonical row deleted';
    EXCEPTION WHEN foreign_key_violation THEN RAISE NOTICE 'PASS: canonical row protected while aliases exist'; END;
    BEGIN
        PERFORM complete_audio_analysis(first_source,'invalid',1000,0.25,0.5,'{}');
        RAISE EXCEPTION 'FAIL: malformed fingerprint accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: canonical fingerprint format validated'; END;
    BEGIN
        PERFORM complete_audio_analysis(first_source,fingerprint,1000,1.1,0.5,'{}');
        RAISE EXCEPTION 'FAIL: invalid RMS accepted';
    EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: invalid measurements rejected'; END;

    -- Isolated retry-state assertions use a third, legacy-compatible test row.
    INSERT INTO radio_transmissions(audio_fingerprint,source_filename,processing_status,analysis_attempts,analysis_retryable)
        VALUES ('retry-'||txid_current(),'synthetic.wav','failed',3,true);
    UPDATE radio_transmissions SET processing_status='processing',analysis_attempts=analysis_attempts+1
        WHERE audio_fingerprint='retry-'||txid_current() AND processing_status IN ('pending','failed')
        AND analysis_retryable AND analysis_attempts<3;
    GET DIAGNOSTICS rows_changed=ROW_COUNT;
    PERFORM pg_temp.check_audio(rows_changed=0,'persisted attempt cap prevents infinite retry');
    UPDATE radio_transmissions SET analysis_attempts=1,analysis_retryable=false WHERE audio_fingerprint='retry-'||txid_current();
    UPDATE radio_transmissions SET processing_status='processing' WHERE audio_fingerprint='retry-'||txid_current()
        AND processing_status IN ('pending','failed') AND analysis_retryable AND analysis_attempts<3;
    GET DIAGNOSTICS rows_changed=ROW_COUNT;
    PERFORM pg_temp.check_audio(rows_changed=0,'permanent invalid audio is not retryable');
    RAISE NOTICE 'ALL AUDIO ANALYSIS SCHEMA TESTS PASSED';
END;
$$;
ROLLBACK;
