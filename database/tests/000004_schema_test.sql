\set ON_ERROR_STOP on
BEGIN;
CREATE FUNCTION pg_temp.check_stt(condition boolean,label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN IF condition IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %',label; END IF;
RAISE NOTICE 'PASS: %',label; END; $$;
DO $$
DECLARE
 a bigint; b bigint; c uuid:='00000000-0000-0000-0000-000000000001';
 d uuid:='00000000-0000-0000-0000-000000000002';
 src text:=md5('stt-'||txid_current())||md5('stt-'||txid_current());
 settings jsonb:='{"language":"en","prompt":"","word_boost":""}';
BEGIN
 PERFORM pg_temp.check_stt(EXISTS(SELECT FROM schema_migrations WHERE version='000004'),'migration 000004 recorded');
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at)
 VALUES(src,'synthetic/fixture.mp3','fixture.mp3','SDRTrunk',57201,578060,now(),'Greenwich_Fairfield','T-NEW_GFD1','CH1A','.mp3','America/New_York',100,now()) RETURNING id INTO a;
 PERFORM pg_temp.check_stt(NOT claim_transcription(a,c,3,90,'openai-compatible-http-v1','small.en',settings),'unanalyzed recording not claimed');
 UPDATE radio_transmissions SET processing_status='processing' WHERE id=a;
 PERFORM complete_audio_analysis(src,'pcm-s16le-16000-mono-v1:sha256:'||src,1000,0.1,0.5,'{}');
 PERFORM pg_temp.check_stt(claim_transcription(a,c,3,90,'openai-compatible-http-v1','small.en',settings),'canonical claimed');
 PERFORM pg_temp.check_stt(NOT claim_transcription(a,d,3,90,'openai-compatible-http-v1','small.en',settings),'second worker cannot claim active lease');
 PERFORM pg_temp.check_stt((SELECT count(*)=1 FROM transcription_attempts WHERE transmission_id=a),'one attempt recorded');
 PERFORM pg_temp.check_stt(NOT finish_transcription(a,d,'wrong','wrong',NULL,false,5),'foreign token cannot finish');
 BEGIN
 PERFORM finish_transcription(a,c,'','',NULL,false,5);
 RAISE EXCEPTION 'FAIL: empty result accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: empty result rejected'; END;
 PERFORM pg_temp.check_stt(finish_transcription(a,c,NULL,NULL,'provider_timeout',true,5),'retryable failure persisted');
 PERFORM pg_temp.check_stt(NOT claim_transcription(a,d,3,90,'openai-compatible-http-v1','small.en',settings),'retry delay enforced');
 UPDATE radio_transmissions SET transcription_next_at=clock_timestamp()-interval '1 second' WHERE id=a;
 PERFORM pg_temp.check_stt(claim_transcription(a,d,3,90,'openai-compatible-http-v1','small.en',settings),'due retry claimed');
 PERFORM pg_temp.check_stt(finish_transcription(a,d,'Thank you for reporting on the distraction.','Thank you for reporting on the distraction.',NULL,false,5),'raw baseline stored');
 PERFORM pg_temp.check_stt((SELECT transcription_status='completed' AND transcription_attempts=2 AND transcription_raw_text=transcript
 AND transcription_model='small.en' AND transcription_provider='openai-compatible-http-v1'
 AND transcription_finished_at IS NOT NULL AND transcription_claim IS NULL AND NOT transcription_retryable FROM radio_transmissions WHERE id=a),'completion fields atomic');
 PERFORM pg_temp.check_stt(NOT claim_transcription(a,c,3,90,'openai-compatible-http-v1','small.en',settings),'completed transcript not reclaimed');
 PERFORM pg_temp.check_stt(NOT finish_transcription(a,d,NULL,NULL,'provider_timeout',true,5),'late failure preserves success');
 BEGIN UPDATE radio_transmissions SET transcript='rewritten' WHERE id=a;
 RAISE EXCEPTION 'FAIL: completed evidence overwritten';
 EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: direct rewrite rejected'; END;
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at)
 VALUES(md5(src)||md5(src),'synthetic/alias.mp3','alias.mp3','SDRTrunk',57201,578060,now(),'Greenwich_Fairfield','T-NEW_GFD1','CH1A','.mp3','America/New_York',100,now()) RETURNING id INTO b;
 UPDATE radio_transmissions SET processing_status='processing' WHERE id=b;
 PERFORM complete_audio_analysis(md5(src)||md5(src),'pcm-s16le-16000-mono-v1:sha256:'||src,1000,0.1,0.5,'{}');
 PERFORM pg_temp.check_stt((SELECT transcription_status='skipped' FROM radio_transmissions WHERE id=b)
 AND NOT claim_transcription(b,c,3,90,'openai-compatible-http-v1','small.en',settings),'duplicate alias skipped without request');
 -- A separate synthetic canonical exercises crash recovery and exhaustion.
 UPDATE radio_transmissions SET audio_duplicate_of=NULL,processing_status='completed',audio_fingerprint='recovery-'||src,
 transcription_status='pending',transcription_retryable=true WHERE id=b;
 PERFORM pg_temp.check_stt(claim_transcription(b,'00000000-0000-0000-0000-000000000003',3,90,'openai-compatible-http-v1','small.en',settings),'recovery fixture claimed');
 UPDATE radio_transmissions SET transcription_next_at=clock_timestamp()-interval '1 second' WHERE id=b;
 PERFORM pg_temp.check_stt(claim_transcription(b,'00000000-0000-0000-0000-000000000004',3,90,'openai-compatible-http-v1','small.en',settings),'expired lease recovered with new token');
 PERFORM pg_temp.check_stt(NOT finish_transcription(b,'00000000-0000-0000-0000-000000000003','late','late',NULL,false,5),'expired owner cannot complete recovered work');
 UPDATE radio_transmissions SET transcription_next_at=clock_timestamp()-interval '1 second' WHERE id=b;
 PERFORM pg_temp.check_stt(NOT claim_transcription(b,c,2,90,'openai-compatible-http-v1','small.en',settings),'expired final attempt not reclaimed');
 PERFORM pg_temp.check_stt((SELECT transcription_status='failed' AND NOT transcription_retryable AND transcription_error='attempts_exhausted' FROM radio_transmissions WHERE id=b),'expired final attempt terminates safely');
 PERFORM pg_temp.check_stt((SELECT count(*)=2 AND bool_and(outcome='interrupted') FROM transcription_attempts WHERE transmission_id=b),'crash attempts retained');
 BEGIN UPDATE radio_transmissions SET transcription_attempts=6 WHERE id=b;
 RAISE EXCEPTION 'FAIL: attempt bound accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: attempt bound enforced'; END;
 BEGIN UPDATE radio_transmissions SET transcription_error='unsafe detail' WHERE id=b;
 RAISE EXCEPTION 'FAIL: arbitrary error accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: error allowlist enforced'; END;
END; $$;
ROLLBACK;
