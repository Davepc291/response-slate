BEGIN;

-- Versioned policy: bucket = first 32 bits of MD5(canonical fingerprint) mod 100.
-- MD5 is only a deterministic distributor here, not an integrity/security hash.
CREATE FUNCTION transcript_dataset_split(fingerprint text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT CASE WHEN bucket<80 THEN 'train' WHEN bucket<90 THEN 'validation' ELSE 'test' END
 FROM (SELECT ('x'||substr(md5(fingerprint),1,8))::bit(32)::bigint % 100 AS bucket) b;
$$;

ALTER TABLE radio_transmissions ADD CONSTRAINT transmission_audio_review_key UNIQUE(id,audio_fingerprint);
ALTER TABLE transcription_attempts ADD CONSTRAINT attempt_transmission_review_key UNIQUE(id,transmission_id);

CREATE TABLE transcript_dataset_items (
 audio_fingerprint text PRIMARY KEY CHECK (audio_fingerprint ~ '^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$'),
 transmission_id bigint NOT NULL UNIQUE,
 split text GENERATED ALWAYS AS (transcript_dataset_split(audio_fingerprint)) STORED,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(transmission_id,audio_fingerprint),
 FOREIGN KEY(transmission_id,audio_fingerprint) REFERENCES radio_transmissions(id,audio_fingerprint) ON DELETE RESTRICT
);

CREATE TABLE transcript_reviews (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 transmission_id bigint NOT NULL,
 transcription_attempt_id bigint NOT NULL,
 audio_fingerprint text NOT NULL,
 raw_model_transcript text NOT NULL CHECK (btrim(raw_model_transcript)<>'' AND octet_length(raw_model_transcript)<=4194304),
 model text NOT NULL CHECK (model ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
 verdict text NOT NULL CHECK (verdict IN ('accepted','excluded','needs_followup')),
 reviewer_label text NOT NULL CHECK (btrim(reviewer_label)<>'' AND octet_length(reviewer_label)<=80 AND reviewer_label !~ '[[:cntrl:]]'),
 reference_transcript text CHECK (octet_length(reference_transcript)<=65536 AND translate(reference_transcript,E'\n\r\t','') !~ '[[:cntrl:]]'),
 notes text CHECK (octet_length(notes)<=4096 AND translate(notes,E'\n\r\t','') !~ '[[:cntrl:]]'),
 exclusion_reason text CHECK (octet_length(exclusion_reason)<=1024 AND translate(exclusion_reason,E'\n\r\t','') !~ '[[:cntrl:]]'),
 reviewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (verdict<>'accepted' OR (reference_transcript IS NOT NULL AND reference_transcript ~ '[^[:space:]]')),
 CHECK (verdict<>'excluded' OR (exclusion_reason IS NOT NULL AND exclusion_reason ~ '[^[:space:]]')),
 FOREIGN KEY(transmission_id,audio_fingerprint) REFERENCES transcript_dataset_items(transmission_id,audio_fingerprint) ON DELETE RESTRICT,
 FOREIGN KEY(transcription_attempt_id,transmission_id) REFERENCES transcription_attempts(id,transmission_id) ON DELETE RESTRICT
);
CREATE INDEX transcript_reviews_history_idx ON transcript_reviews(transmission_id,id DESC);
CREATE INDEX transcript_reviews_attempt_idx ON transcript_reviews(transcription_attempt_id);
CREATE INDEX transcript_reviews_verdict_idx ON transcript_reviews(verdict,id DESC);
CREATE INDEX transcript_dataset_export_idx ON transcript_dataset_items(split,audio_fingerprint);
CREATE INDEX transcript_review_queue_idx ON radio_transmissions(id)
 WHERE processing_status='completed' AND transcription_status='completed' AND audio_duplicate_of IS NULL;

CREATE VIEW transcript_review_candidates AS
 SELECT r.id AS transmission_id,a.id AS transcription_attempt_id,r.audio_fingerprint,
 r.channel_label,r.tgid,r.duration_ms,r.source_path,a.raw_text AS raw_model_transcript,a.model
 FROM radio_transmissions r
 CROSS JOIN LATERAL (
  SELECT * FROM transcription_attempts a WHERE a.transmission_id=r.id
  AND a.outcome='completed' AND a.finished_at IS NOT NULL AND btrim(a.raw_text)<>''
  AND a.model ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
  ORDER BY a.attempt DESC,a.id DESC LIMIT 1
 ) a
 WHERE r.processing_status='completed' AND r.transcription_status='completed'
 AND r.audio_duplicate_of IS NULL AND r.audio_probe IS NOT NULL AND r.duration_ms IS NOT NULL
 AND r.audio_fingerprint ~ '^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$';

CREATE FUNCTION validate_transcript_dataset_item() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM radio_transmissions WHERE id=NEW.transmission_id AND audio_fingerprint=NEW.audio_fingerprint
 AND processing_status='completed' AND transcription_status='completed' AND audio_duplicate_of IS NULL
 AND audio_probe IS NOT NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'recording is not review eligible' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER validate_transcript_dataset_item BEFORE INSERT ON transcript_dataset_items
 FOR EACH ROW EXECUTE FUNCTION validate_transcript_dataset_item();

CREATE FUNCTION prepare_transcript_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c transcript_review_candidates%ROWTYPE;
BEGIN
 -- Serialize submissions per transmission and prevent evidence changes while
 -- copying the exact selected attempt. No model text is inferred or corrected.
 PERFORM 1 FROM radio_transmissions WHERE id=NEW.transmission_id FOR UPDATE;
 PERFORM 1 FROM transcription_attempts WHERE id=NEW.transcription_attempt_id FOR SHARE;
 SELECT * INTO c FROM transcript_review_candidates
 WHERE transmission_id=NEW.transmission_id AND transcription_attempt_id=NEW.transcription_attempt_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'recording or attempt is not review eligible' USING ERRCODE='23514'; END IF;
 INSERT INTO transcript_dataset_items(audio_fingerprint,transmission_id)
 VALUES(c.audio_fingerprint,c.transmission_id) ON CONFLICT(audio_fingerprint) DO NOTHING;
 NEW.audio_fingerprint:=c.audio_fingerprint;
 NEW.raw_model_transcript:=c.raw_model_transcript;
 NEW.model:=c.model;
 NEW.reviewed_at:=clock_timestamp();
 RETURN NEW;
END; $$;
CREATE TRIGGER prepare_transcript_review BEFORE INSERT ON transcript_reviews
 FOR EACH ROW EXECUTE FUNCTION prepare_transcript_review();

CREATE FUNCTION reject_transcript_review_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'review audit is append-only; submit a superseding review' USING ERRCODE='55000'; END; $$;
CREATE TRIGGER transcript_reviews_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON transcript_reviews
 FOR EACH STATEMENT EXECUTE FUNCTION reject_transcript_review_mutation();
CREATE TRIGGER transcript_dataset_items_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON transcript_dataset_items
 FOR EACH STATEMENT EXECUTE FUNCTION reject_transcript_review_mutation();
ALTER TABLE transcript_reviews ENABLE ALWAYS TRIGGER transcript_reviews_immutable;
ALTER TABLE transcript_dataset_items ENABLE ALWAYS TRIGGER transcript_dataset_items_immutable;
ALTER TABLE transcript_reviews ENABLE ALWAYS TRIGGER prepare_transcript_review;
ALTER TABLE transcript_dataset_items ENABLE ALWAYS TRIGGER validate_transcript_dataset_item;

-- Foreign keys protect identity/deletion; these guards also protect eligibility
-- and the parent attempt evidence, which previously permitted direct UPDATE.
CREATE FUNCTION protect_reviewed_transmission() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT FROM transcript_dataset_items WHERE transmission_id=OLD.id) AND
 (NEW.processing_status<>'completed' OR NEW.transcription_status<>'completed' OR NEW.audio_duplicate_of IS NOT NULL OR
 NEW.audio_probe IS NULL OR ROW(NEW.audio_fingerprint,NEW.duration_ms,NEW.channel_label,NEW.tgid)
 IS DISTINCT FROM ROW(OLD.audio_fingerprint,OLD.duration_ms,OLD.channel_label,OLD.tgid)) THEN
 RAISE EXCEPTION 'reviewed recording evidence is preserved' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER protect_reviewed_transmission BEFORE UPDATE ON radio_transmissions
 FOR EACH ROW EXECUTE FUNCTION protect_reviewed_transmission();
CREATE FUNCTION protect_reviewed_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT FROM transcript_reviews WHERE transcription_attempt_id=OLD.id) AND
 ROW(NEW.transmission_id,NEW.raw_text,NEW.model,NEW.provider,NEW.outcome,NEW.finished_at,NEW.attempt,NEW.settings)
 IS DISTINCT FROM ROW(OLD.transmission_id,OLD.raw_text,OLD.model,OLD.provider,OLD.outcome,OLD.finished_at,OLD.attempt,OLD.settings) THEN
 RAISE EXCEPTION 'reviewed attempt evidence is preserved' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER protect_reviewed_attempt BEFORE UPDATE ON transcription_attempts
 FOR EACH ROW EXECUTE FUNCTION protect_reviewed_attempt();
ALTER TABLE radio_transmissions ENABLE ALWAYS TRIGGER protect_reviewed_transmission;
ALTER TABLE transcription_attempts ENABLE ALWAYS TRIGGER protect_reviewed_attempt;

-- Select latest OVER ALL verdicts before filtering accepted exports. An excluded
-- or needs_followup review must supersede an earlier accepted reference.
CREATE VIEW transcript_review_latest AS
 SELECT DISTINCT ON (transmission_id) * FROM transcript_reviews ORDER BY transmission_id,id DESC;

COMMENT ON TABLE transcript_reviews IS 'Explicit human submissions only in production; synthetic verification must roll back. Database cannot attest a human actually listened. Never execute transcript text.';
COMMENT ON COLUMN transcript_dataset_items.split IS 'Immutable 80/10/10 hash-bucket split v1; approximate proportions, never reassigned after a review.';
INSERT INTO schema_migrations(version) VALUES('000006');
COMMIT;
