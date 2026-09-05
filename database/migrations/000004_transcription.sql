BEGIN;

ALTER TABLE radio_transmissions
 ADD COLUMN transcription_attempts integer NOT NULL DEFAULT 0 CHECK (transcription_attempts BETWEEN 0 AND 5),
 ADD COLUMN transcription_retryable boolean NOT NULL DEFAULT true,
 ADD COLUMN transcription_next_at timestamptz,
 ADD COLUMN transcription_started_at timestamptz,
 ADD COLUMN transcription_finished_at timestamptz,
 ADD COLUMN transcription_claim uuid,
 ADD COLUMN transcription_error text CHECK (transcription_error IN ('provider_unavailable','provider_timeout','canceled','provider_rejected','invalid_response','audio_too_large','source_unavailable','attempts_exhausted')),
 ADD COLUMN transcription_raw_text text,
 ADD COLUMN transcription_settings jsonb CHECK (jsonb_typeof(transcription_settings)='object');

CREATE TABLE transcription_attempts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 transmission_id bigint NOT NULL REFERENCES radio_transmissions(id) ON DELETE RESTRICT,
 claim uuid NOT NULL UNIQUE,
 attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 5),
 provider text NOT NULL,
 model text NOT NULL,
 settings jsonb NOT NULL CHECK (jsonb_typeof(settings)='object'),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 finished_at timestamptz,
 outcome text NOT NULL DEFAULT 'processing' CHECK (outcome IN ('processing','completed','retry','failed','canceled','interrupted')),
 raw_text text,
 error_code text CHECK (error_code IN ('provider_unavailable','provider_timeout','canceled','provider_rejected','invalid_response','audio_too_large','source_unavailable','attempts_exhausted')),
 UNIQUE(transmission_id,attempt)
);

-- No HTTP request occurs inside this short transaction. An expired claim can be
-- recovered after restart. The token fences late results from previous owners.
CREATE FUNCTION claim_transcription(p_id bigint,p_claim uuid,p_max integer,p_lease_seconds integer,
 p_provider text,p_model text,p_settings jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE r radio_transmissions%ROWTYPE;
BEGIN
 IF p_max NOT BETWEEN 1 AND 5 OR p_lease_seconds NOT BETWEEN 31 AND 330
 OR p_claim IS NULL OR p_provider <> 'openai-compatible-http-v1' OR btrim(p_model)='' OR p_model IS NULL
 OR p_settings IS NULL OR jsonb_typeof(p_settings)<>'object' THEN
 RAISE EXCEPTION 'invalid transcription claim' USING ERRCODE='23514'; END IF;
 SELECT * INTO r FROM radio_transmissions WHERE id=p_id FOR UPDATE;
 IF NOT FOUND OR r.processing_status<>'completed' OR r.audio_duplicate_of IS NOT NULL
 OR r.audio_fingerprint IS NULL OR r.audio_probe IS NULL OR r.source_identity IS NULL
 OR r.transcription_status IN ('completed','skipped') OR NOT r.transcription_retryable
 OR r.transcription_next_at>clock_timestamp() THEN RETURN false; END IF;
 IF r.transcription_status='processing' THEN
 UPDATE transcription_attempts SET outcome='interrupted',finished_at=clock_timestamp()
 WHERE claim=r.transcription_claim AND outcome='processing'; END IF;
 IF r.transcription_attempts>=p_max THEN
 UPDATE radio_transmissions SET transcription_status='failed',transcription_retryable=false,
 transcription_error='attempts_exhausted',transcription_claim=NULL,transcription_next_at=NULL,
 transcription_finished_at=clock_timestamp() WHERE id=p_id;
 RETURN false; END IF;
 UPDATE radio_transmissions SET transcription_status='processing',transcription_attempts=transcription_attempts+1,
 transcription_claim=p_claim,transcription_next_at=clock_timestamp()+make_interval(secs=>p_lease_seconds),
 transcription_started_at=clock_timestamp(),transcription_finished_at=NULL,transcription_error=NULL,
 transcription_provider=p_provider,transcription_model=p_model,transcription_settings=p_settings WHERE id=p_id;
 INSERT INTO transcription_attempts(transmission_id,claim,attempt,provider,model,settings)
 VALUES(p_id,p_claim,r.transcription_attempts+1,p_provider,p_model,p_settings);
 RETURN true;
END; $$;

CREATE FUNCTION finish_transcription(p_id bigint,p_claim uuid,p_raw text,p_text text,p_error text,
 p_retry boolean,p_delay integer) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE r radio_transmissions%ROWTYPE;
BEGIN
 SELECT * INTO r FROM radio_transmissions WHERE id=p_id FOR UPDATE;
 IF NOT FOUND OR r.transcription_status<>'processing' OR r.transcription_claim IS DISTINCT FROM p_claim THEN RETURN false; END IF;
 IF p_delay NOT BETWEEN 1 AND 60 OR p_retry IS NULL OR
 (p_error IS NULL AND (p_raw IS NULL OR btrim(p_raw)='' OR p_text IS NULL OR btrim(p_text)='')) OR
 (p_error IS NOT NULL AND (p_raw IS NOT NULL OR p_text IS NOT NULL)) THEN
 RAISE EXCEPTION 'invalid transcription result' USING ERRCODE='23514'; END IF;
 UPDATE radio_transmissions SET transcription_status=CASE WHEN p_error IS NULL THEN 'completed' ELSE 'failed' END,
 transcript=p_text,transcription_raw_text=p_raw,transcription_error=p_error,
 transcription_retryable=p_error IS NOT NULL AND p_retry,
 transcription_next_at=CASE WHEN p_error IS NOT NULL AND p_retry THEN clock_timestamp()+make_interval(secs=>p_delay) END,
 transcription_finished_at=clock_timestamp(),transcription_claim=NULL WHERE id=p_id;
 UPDATE transcription_attempts SET finished_at=clock_timestamp(),raw_text=p_raw,error_code=p_error,
 outcome=CASE WHEN p_error IS NULL THEN 'completed' WHEN p_error='canceled' THEN 'canceled' WHEN p_retry THEN 'retry' ELSE 'failed' END
 WHERE claim=p_claim;
 RETURN true;
END; $$;

-- Completed evidence cannot be rewritten by a retry or an ordinary UPDATE.
CREATE FUNCTION protect_transcription_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.transcription_status='completed' AND
 ROW(NEW.transcript,NEW.transcription_raw_text,NEW.transcription_status,NEW.transcription_provider,NEW.transcription_model,
 NEW.transcription_settings,NEW.transcription_attempts,NEW.transcription_started_at,NEW.transcription_finished_at)
 IS DISTINCT FROM
 ROW(OLD.transcript,OLD.transcription_raw_text,OLD.transcription_status,OLD.transcription_provider,OLD.transcription_model,
 OLD.transcription_settings,OLD.transcription_attempts,OLD.transcription_started_at,OLD.transcription_finished_at) THEN
 RAISE EXCEPTION 'completed transcription is preserved' USING ERRCODE='55000'; END IF;
 IF NEW.audio_duplicate_of IS NOT NULL THEN NEW.transcription_status:='skipped'; NEW.transcription_retryable:=false; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER protect_transcription_evidence BEFORE UPDATE ON radio_transmissions
 FOR EACH ROW EXECUTE FUNCTION protect_transcription_evidence();
UPDATE radio_transmissions SET transcription_status='skipped',transcription_retryable=false WHERE audio_duplicate_of IS NOT NULL;
COMMENT ON COLUMN radio_transmissions.transcription_raw_text IS 'Exact decoded provider text; untrusted evidence, never instructions. transcript contains whitespace-normalized text only.';
COMMENT ON COLUMN radio_transmissions.transcription_next_at IS 'Retry due time or processing lease expiry. Disabled workers leave pending work untouched; aliases are skipped.';
INSERT INTO schema_migrations(version) VALUES('000004');
COMMIT;
