BEGIN;

ALTER TABLE transcription_attempts
 ADD COLUMN provider_request_started_at timestamptz,
 ADD COLUMN provider_request_duration_ms double precision,
 ADD CONSTRAINT provider_timing_pair CHECK (
  (provider_request_started_at IS NULL AND provider_request_duration_ms IS NULL) OR
  (provider_request_started_at IS NOT NULL AND provider_request_duration_ms IS NOT NULL
   AND provider_request_duration_ms >= 0 AND provider_request_duration_ms < 'Infinity'::double precision)
 );

-- Keep evidence and measurements in the same transaction and under the existing
-- claim fence. Old callers remain compatible; their timing fields stay NULL.
CREATE FUNCTION finish_transcription_observed(p_id bigint,p_claim uuid,p_raw text,p_text text,p_error text,
 p_retry boolean,p_delay integer,p_request_start timestamptz,p_request_ms double precision)
 RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE saved boolean;
BEGIN
 saved := finish_transcription(p_id,p_claim,p_raw,p_text,p_error,p_retry,p_delay);
 IF saved THEN
  UPDATE transcription_attempts SET provider_request_started_at=p_request_start,
   provider_request_duration_ms=p_request_ms WHERE claim=p_claim;
 END IF;
 RETURN saved;
END; $$;

CREATE INDEX radio_transmissions_source_directory_idx
 ON radio_transmissions ((left(source_path,length(source_path)-length(source_filename))))
 WHERE source_identity IS NOT NULL;

-- Fixed-shape aggregates only: no transcripts, names, paths, RIDs or URLs.
-- Prefix includes the OS directory separator, supplied privately by the API.
CREATE FUNCTION transcription_operations(p_prefix text) RETURNS jsonb
LANGUAGE sql STABLE AS $$
WITH scoped AS MATERIALIZED (
 SELECT * FROM radio_transmissions WHERE source_identity IS NOT NULL
 AND left(source_path,length(source_path)-length(source_filename))=p_prefix
 AND source_path=p_prefix||source_filename
), eligible AS (
 SELECT * FROM scoped WHERE processing_status='completed' AND audio_duplicate_of IS NULL
 AND audio_fingerprint IS NOT NULL AND audio_probe IS NOT NULL AND transcription_retryable
 AND transcription_status IN ('pending','failed','processing')
 AND (transcription_next_at IS NULL OR transcription_next_at<=statement_timestamp())
), attempts AS MATERIALIZED (
 SELECT a.*,s.created_at AS ingested_at FROM transcription_attempts a JOIN scoped s ON s.id=a.transmission_id
), recent AS (
 SELECT * FROM attempts WHERE finished_at IS NOT NULL ORDER BY finished_at DESC,id DESC LIMIT 100
), measurements AS (
 SELECT metric,value FROM recent CROSS JOIN LATERAL (VALUES
 ('ingestion_to_claim',greatest(0,extract(epoch FROM started_at-ingested_at)*1000)::double precision),
 ('claim_to_request',CASE WHEN provider_request_started_at IS NOT NULL THEN greatest(0,extract(epoch FROM provider_request_started_at-started_at)*1000)::double precision END),
 ('provider_request',provider_request_duration_ms),
 ('claim_to_completion',CASE WHEN outcome='completed' THEN greatest(0,extract(epoch FROM finished_at-started_at)*1000)::double precision END),
 ('ingestion_to_completion',CASE WHEN outcome='completed' THEN greatest(0,extract(epoch FROM finished_at-ingested_at)*1000)::double precision END)
 ) AS v(metric,value)
), summaries AS (
 SELECT metric,jsonb_build_object('samples',count(value),'mean_ms',avg(value),
 'p95_ms',percentile_cont(0.95) WITHIN GROUP (ORDER BY value),'max_ms',max(value)) AS summary
 FROM measurements GROUP BY metric
)
SELECT jsonb_build_object(
 'observed_at',statement_timestamp(),
 'waiting_jobs',(SELECT count(*) FROM eligible),
 'oldest_waiting_age_ms',(SELECT greatest(0,extract(epoch FROM statement_timestamp()-min(created_at))*1000) FROM eligible HAVING count(*)>0),
 'processing_jobs',(SELECT count(*) FROM scoped WHERE transcription_status='processing' AND transcription_next_at>statement_timestamp()),
 'retry_waiting_jobs',(SELECT count(*) FROM scoped WHERE transcription_status='failed' AND transcription_retryable AND transcription_next_at>statement_timestamp()),
 'completed_jobs',(SELECT count(*) FROM scoped WHERE transcription_status='completed'),
 'failed_jobs',(SELECT count(*) FROM scoped WHERE transcription_status='failed'),
 'skipped_jobs',(SELECT count(*) FROM scoped WHERE transcription_status='skipped'),
 'attempt_count',(SELECT count(*) FROM attempts),
 'retry_count',(SELECT count(*) FROM attempts WHERE attempt>1),
 'timeout_count',(SELECT count(*) FROM attempts WHERE error_code='provider_timeout'),
 'last_success_at',(SELECT max(finished_at) FROM attempts WHERE outcome='completed'),
 'last_provider_failure_at',(SELECT max(finished_at) FROM attempts WHERE error_code IN ('provider_unavailable','provider_timeout','provider_rejected','invalid_response')),
 'recent_timings',coalesce((SELECT jsonb_object_agg(metric,summary) FROM summaries),'{}'::jsonb)
);
$$;

COMMENT ON COLUMN transcription_attempts.provider_request_duration_ms IS
 'Monotonic client milliseconds from Client.Do through response body read and validation; includes upload/network/server time. NULL for legacy, unissued or interrupted requests.';
INSERT INTO schema_migrations(version) VALUES('000005');
COMMIT;
