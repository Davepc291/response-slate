\set ON_ERROR_STOP on
BEGIN;

DO $$
DECLARE
    identity text := lpad(txid_current()::text, 64, '0');
    transmission bigint;
    affected bigint;
BEGIN
    IF NOT EXISTS (SELECT FROM schema_migrations WHERE version = '000002') THEN
        RAISE EXCEPTION 'FAIL: migration 000002 not recorded';
    END IF;
    RAISE NOTICE 'PASS: migration 000002 recorded';

    INSERT INTO radio_transmissions
        (source_identity, source_path, source_filename, source_recorder, tgid, rid,
         recorded_at, system_site_label, alias_channel_label, channel_label,
         extension, recording_timezone, source_size_bytes, source_modified_at)
    VALUES (identity, 'synthetic/recording.mp3', '20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3',
        'SDRTrunk', 57201, 578060, '2026-09-05 08:16:09-04', 'Greenwich_Fairfield',
        'T-NEW_GFD1', 'CH1A', '.mp3', 'America/New_York', 100, now())
    RETURNING id INTO transmission;
    IF NOT EXISTS (SELECT FROM radio_transmissions WHERE id = transmission
        AND audio_fingerprint IS NULL AND transcript IS NULL AND duration_ms IS NULL
        AND rms IS NULL AND peak IS NULL AND transcription_status = 'pending'
        AND processing_status = 'pending' AND rid = 578060
        AND recorded_at = '2026-09-05 12:16:09+00'::timestamptz) THEN
        RAISE EXCEPTION 'FAIL: metadata-only row';
    END IF;
    RAISE NOTICE 'PASS: native metadata stored without audio or inferred unit';

    INSERT INTO radio_transmissions
        (source_identity, source_path, source_filename, source_recorder, tgid, rid,
         recorded_at, system_site_label, alias_channel_label, channel_label,
         extension, recording_timezone, source_size_bytes, source_modified_at)
    SELECT source_identity, source_path, source_filename, source_recorder, tgid, rid,
        recorded_at, system_site_label, alias_channel_label, channel_label,
        extension, recording_timezone, source_size_bytes, source_modified_at
    FROM radio_transmissions WHERE id = transmission
    ON CONFLICT (source_identity) DO NOTHING;
    GET DIAGNOSTICS affected = ROW_COUNT;
    IF affected <> 0 THEN RAISE EXCEPTION 'FAIL: duplicate insert'; END IF;
    RAISE NOTICE 'PASS: ON CONFLICT makes repeated ingestion idempotent';

    BEGIN
        UPDATE radio_transmissions SET source_identity = 'invalid' WHERE id = transmission;
        RAISE EXCEPTION 'FAIL: invalid identity accepted';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'PASS: malformed source identity rejected';
    END;
    BEGIN
        INSERT INTO radio_transmissions (source_filename) VALUES ('synthetic.mp3');
        RAISE EXCEPTION 'FAIL: missing identity accepted';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'PASS: transmission requires a source or audio identity';
    END;
    BEGIN
        UPDATE radio_transmissions SET source_path = NULL WHERE id = transmission;
        RAISE EXCEPTION 'FAIL: incomplete source metadata accepted';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'PASS: source metadata required';
    END;
    BEGIN
        UPDATE radio_transmissions SET source_size_bytes = 0 WHERE id = transmission;
        RAISE EXCEPTION 'FAIL: empty recording accepted';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'PASS: empty recordings rejected';
    END;
    BEGIN
        UPDATE radio_transmissions SET extension = '.exe' WHERE id = transmission;
        RAISE EXCEPTION 'FAIL: unsupported extension accepted';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'PASS: unsupported extensions rejected';
    END;
    INSERT INTO radio_transmissions (audio_fingerprint, source_filename)
    VALUES ('legacy-synthetic-' || txid_current(), 'legacy-synthetic.wav');
    RAISE NOTICE 'PASS: legacy audio-fingerprint inserts still work';
    RAISE NOTICE 'ALL INGESTION SCHEMA TESTS PASSED';
END;
$$;

ROLLBACK;
