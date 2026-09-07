\set ON_ERROR_STOP on
BEGIN;
CREATE FUNCTION pg_temp.check_monitor(condition boolean,label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN IF condition IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %',label; END IF;
RAISE NOTICE 'PASS: %',label; END; $$;
DO $$
DECLARE
 prefix text:='monitor-test-'||txid_current()||'/';
 src text:=md5('monitor-'||txid_current())||md5('monitor-'||txid_current());
 a bigint;b bigint;
 c uuid:='00000000-0000-0000-0000-000000000005';
 d uuid:='00000000-0000-0000-0000-000000000006';
 m jsonb;
BEGIN
 PERFORM pg_temp.check_monitor(EXISTS(SELECT FROM schema_migrations WHERE version='000005'),'migration recorded');
 m:=transcription_operations(prefix);
 PERFORM pg_temp.check_monitor((m->>'waiting_jobs')::int=0 AND m->'oldest_waiting_age_ms'='null'::jsonb,'empty queue has null oldest age');
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at,created_at)
 VALUES(src,prefix||'fixture.mp3','fixture.mp3','SDRTrunk',57201,578060,now(),
 'Greenwich_Fairfield','T-NEW_GFD1','CH1A','.mp3','America/New_York',100,now(),statement_timestamp()-interval '60 seconds') RETURNING id INTO a;
 UPDATE radio_transmissions SET processing_status='processing' WHERE id=a;
 PERFORM complete_audio_analysis(src,'pcm-s16le-16000-mono-v1:sha256:'||src,1000,0.1,0.5,'{}');
 m:=transcription_operations(prefix);
 PERFORM pg_temp.check_monitor((m->>'waiting_jobs')::int=1 AND (m->>'oldest_waiting_age_ms')::numeric BETWEEN 60000 AND 61000,'eligible queue and age from ingestion');
 PERFORM pg_temp.check_monitor(claim_transcription(a,c,3,90,'openai-compatible-http-v1','small.en','{}'),'claim accepted');
 m:=transcription_operations(prefix);
 PERFORM pg_temp.check_monitor((m->>'waiting_jobs')::int=0 AND (m->>'processing_jobs')::int=1,'one active claim');
 -- Invalid measurement rolls back the legacy completion as well.
 BEGIN
  PERFORM finish_transcription_observed(a,c,NULL,NULL,'provider_timeout',true,5,clock_timestamp(),-1);
  RAISE EXCEPTION 'FAIL: negative duration accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: invalid duration rolls back completion'; END;
 PERFORM pg_temp.check_monitor((SELECT transcription_status='processing' FROM radio_transmissions WHERE id=a),'atomic measurement and outcome');
 PERFORM pg_temp.check_monitor(finish_transcription_observed(a,c,NULL,NULL,'provider_timeout',true,5,clock_timestamp(),2500),'timeout evidence recorded');
 m:=transcription_operations(prefix);
 PERFORM pg_temp.check_monitor((m->>'failed_jobs')::int=1 AND (m->>'retry_waiting_jobs')::int=1 AND (m->>'timeout_count')::int=1 AND m->'last_provider_failure_at'<>'null'::jsonb,'failed retry and timeout counts');
 UPDATE radio_transmissions SET transcription_next_at=clock_timestamp()-interval '1 second' WHERE id=a;
 PERFORM pg_temp.check_monitor((transcription_operations(prefix)->>'waiting_jobs')::int=1,'due retry returns to eligible queue');
 PERFORM claim_transcription(a,d,3,90,'openai-compatible-http-v1','small.en','{}');
 PERFORM finish_transcription_observed(a,d,'synthetic words','synthetic words',NULL,false,5,clock_timestamp(),1000);
 PERFORM pg_temp.check_monitor(NOT finish_transcription_observed(a,d,'late','late',NULL,false,5,clock_timestamp(),999),'repeat completion is no-op');
 m:=transcription_operations(prefix);
 PERFORM pg_temp.check_monitor((m->>'completed_jobs')::int=1 AND (m->>'attempt_count')::int=2 AND (m->>'retry_count')::int=1 AND m->'last_success_at'<>'null'::jsonb,'completion and attempt evidence');
 PERFORM pg_temp.check_monitor((m#>>'{recent_timings,provider_request,samples}')::int=2 AND (m#>>'{recent_timings,provider_request,mean_ms}')::numeric=1750,'provider duration summary');
 PERFORM pg_temp.check_monitor((m#>>'{recent_timings,claim_to_completion,samples}')::int=1 AND (m#>>'{recent_timings,ingestion_to_completion,samples}')::int=1,'completion timings exclude failed attempts');
 INSERT INTO radio_transmissions(audio_fingerprint,source_filename,transcription_status) VALUES('monitor-skip-'||src,'ignored','skipped') RETURNING id INTO b;
 PERFORM pg_temp.check_monitor((transcription_operations(prefix)->>'skipped_jobs')::int=0,'other scope excluded');
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at,
 processing_status,transcription_status,audio_duplicate_of)
 VALUES(md5(src)||md5(src),prefix||'alias.mp3','alias.mp3','SDRTrunk',57201,578060,now(),
 'Greenwich_Fairfield','T-NEW_GFD1','CH1A','.mp3','America/New_York',100,now(),'skipped','skipped',a);
 PERFORM pg_temp.check_monitor((transcription_operations(prefix)->>'skipped_jobs')::int=1,'scoped aliases counted as skipped');
 PERFORM pg_temp.check_monitor(m::text !~ 'fixture.mp3|synthetic words|578060|source_path|authorization|https?://','bounded aggregate has no sensitive labels');
END; $$;
ROLLBACK;
