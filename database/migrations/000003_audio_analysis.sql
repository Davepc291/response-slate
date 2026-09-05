BEGIN;

ALTER TABLE radio_transmissions
    ADD COLUMN audio_probe jsonb CHECK (jsonb_typeof(audio_probe) = 'object'),
    ADD COLUMN audio_duplicate_of bigint REFERENCES radio_transmissions(id) ON DELETE RESTRICT,
    ADD COLUMN analysis_attempts integer NOT NULL DEFAULT 0 CHECK (analysis_attempts >= 0),
    ADD COLUMN analysis_retryable boolean NOT NULL DEFAULT true,
    ADD CONSTRAINT audio_duplicate_reference CHECK (
        audio_duplicate_of IS NULL OR (audio_duplicate_of <> id AND source_identity IS NOT NULL
            AND audio_fingerprint IS NULL AND processing_status = 'skipped')
    );
CREATE INDEX radio_transmissions_audio_duplicate_idx ON radio_transmissions(audio_duplicate_of)
    WHERE audio_duplicate_of IS NOT NULL;

-- A single SELECT invokes this function within one transaction. Serialize by
-- fingerprint before locking the source row, so different paths cannot both win.
CREATE FUNCTION complete_audio_analysis(p_source text, p_fingerprint text, p_duration bigint,
    p_rms numeric, p_peak numeric, p_probe jsonb) RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
    source_row radio_transmissions%ROWTYPE;
    canonical_id bigint;
BEGIN
    IF p_fingerprint IS NULL OR p_fingerprint !~ '^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$'
        OR p_duration IS NULL OR p_duration < 0 OR p_duration > 3600000
        OR p_rms IS NULL OR NOT (p_rms BETWEEN 0 AND 1)
        OR p_peak IS NULL OR NOT (p_peak BETWEEN 0 AND 1) OR p_rms > p_peak
        OR p_probe IS NULL OR jsonb_typeof(p_probe) <> 'object' THEN
        RAISE EXCEPTION 'invalid audio analysis result' USING ERRCODE = '23514';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_fingerprint, 0));
    SELECT * INTO STRICT source_row FROM radio_transmissions WHERE source_identity = p_source FOR UPDATE;
    IF source_row.processing_status IN ('completed', 'skipped') THEN
        RETURN 'already-processed';
    END IF;
    IF source_row.processing_status <> 'processing' THEN
        RAISE EXCEPTION 'audio analysis has not been claimed' USING ERRCODE = '55000';
    END IF;
    SELECT id INTO canonical_id FROM radio_transmissions WHERE audio_fingerprint = p_fingerprint;
    IF canonical_id IS NOT NULL AND canonical_id <> source_row.id THEN
        UPDATE radio_transmissions SET duration_ms = p_duration, rms = p_rms, peak = p_peak,
            audio_probe = p_probe, audio_duplicate_of = canonical_id, audio_fingerprint = NULL,
            processing_status = 'skipped', analysis_retryable = false, error_message = NULL
            WHERE id = source_row.id;
        RETURN 'duplicate-content';
    END IF;
    UPDATE radio_transmissions SET duration_ms = p_duration, rms = p_rms, peak = p_peak,
        audio_probe = p_probe, audio_fingerprint = p_fingerprint,
        processing_status = 'completed', analysis_retryable = false, error_message = NULL
        WHERE id = source_row.id;
    RETURN 'analyzed';
END;
$$;

COMMENT ON COLUMN radio_transmissions.audio_fingerprint IS
    'Canonical PCM fingerprint: pcm-s16le-16000-mono-v1:sha256: followed by lowercase SHA-256 of decoded mono 16kHz signed 16-bit LE PCM. Legacy fingerprints remain valid.';
COMMENT ON COLUMN radio_transmissions.audio_duplicate_of IS
    'Metadata-only source alias of a canonical transmission; do not count as another logical transmission.';
INSERT INTO schema_migrations(version) VALUES ('000003');
COMMIT;
