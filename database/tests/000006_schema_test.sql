\set ON_ERROR_STOP on
BEGIN;
CREATE FUNCTION pg_temp.check_review(ok boolean,label text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN IF ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'FAIL: %',label; END IF; RAISE NOTICE 'PASS: %',label; END; $$;
-- Synthetic evidence only. Every fixture, including submitted references, rolls back.
CREATE FUNCTION pg_temp.review_fixture(label text,status text DEFAULT 'completed') RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE src text:=md5(label||txid_current()); n bigint;c uuid:=md5('claim-'||label||txid_current())::uuid;
BEGIN
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at)
 VALUES(src||src,'synthetic-review/'||label||'.mp3',label||'.mp3','SDRTrunk',57201,578060,now(),
 'synthetic','synthetic','CH1A','.mp3','America/New_York',100,now()) RETURNING id INTO n;
 UPDATE radio_transmissions SET processing_status='processing' WHERE id=n;
 PERFORM complete_audio_analysis(src||src,'pcm-s16le-16000-mono-v1:sha256:'||src||src,1000,0.1,0.5,'{}');
 PERFORM claim_transcription(n,c,3,90,'openai-compatible-http-v1','small.en','{}');
 IF status='completed' THEN PERFORM finish_transcription(n,c,E'  SYNTHETIC raw model evidence\n','SYNTHETIC raw model evidence',NULL,false,5);
 ELSE UPDATE radio_transmissions SET transcription_status=status WHERE id=n; END IF;
 RETURN n;
END; $$;
DO $$
DECLARE a bigint;b bigint;attempt_a bigint;attempt_b bigint; alias_id bigint;
 fp text;split_before text; state text;sql text;review_id bigint;
BEGIN
 PERFORM pg_temp.check_review(EXISTS(SELECT FROM schema_migrations WHERE version='000006'),'migration recorded');
 a:=pg_temp.review_fixture('canonical');b:=pg_temp.review_fixture('other');
 SELECT transcription_attempt_id,audio_fingerprint INTO attempt_a,fp FROM transcript_review_candidates WHERE transmission_id=a;
 SELECT transcription_attempt_id INTO attempt_b FROM transcript_review_candidates WHERE transmission_id=b;
 PERFORM pg_temp.check_review(attempt_a IS NOT NULL,'canonical completed attempt eligible');
 FOREACH state IN ARRAY ARRAY['pending','processing','failed','skipped'] LOOP
  alias_id:=pg_temp.review_fixture('state-'||state,state);
  PERFORM pg_temp.check_review(NOT EXISTS(SELECT FROM transcript_review_candidates WHERE transmission_id=alias_id),'ineligible state '||state);
 END LOOP;
 alias_id:=pg_temp.review_fixture('missing-fingerprint');
 UPDATE radio_transmissions SET audio_fingerprint=NULL WHERE id=alias_id;
 PERFORM pg_temp.check_review(NOT EXISTS(SELECT FROM transcript_review_candidates WHERE transmission_id=alias_id),'missing fingerprint excluded');
 INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,
 system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at,
 processing_status,transcription_status,audio_duplicate_of)
 VALUES(md5('alias'||txid_current())||md5('alias'||txid_current()),'synthetic-review/alias.mp3','alias.mp3','SDRTrunk',57201,578060,now(),
 'synthetic','synthetic','CH1A','.mp3','America/New_York',100,now(),'skipped','skipped',a) RETURNING id INTO alias_id;
 PERFORM pg_temp.check_review(NOT EXISTS(SELECT FROM transcript_review_candidates WHERE transmission_id=alias_id),'duplicate alias excluded');
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(alias_id,attempt_a,'accepted','SYNTHETIC TEST ONLY','SYNTHETIC reference');RAISE EXCEPTION 'FAIL: alias review accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: alias review rejected'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(-1,attempt_a,'accepted','SYNTHETIC TEST ONLY','SYNTHETIC reference');RAISE EXCEPTION 'FAIL: unknown ID accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: unknown ID rejected'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(a,attempt_b,'accepted','SYNTHETIC TEST ONLY','SYNTHETIC reference');RAISE EXCEPTION 'FAIL: mismatched attempt accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: mismatched attempt rejected'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(a,attempt_a,'accepted','SYNTHETIC TEST ONLY',E' \n\t');RAISE EXCEPTION 'FAIL: blank reference accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: blank accepted reference rejected'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label)
 VALUES(a,attempt_a,'excluded','SYNTHETIC TEST ONLY');RAISE EXCEPTION 'FAIL: missing exclusion reason accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: exclusion requires reason'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(a,attempt_a,'accepted','SYNTHETIC TEST ONLY',chr(1));RAISE EXCEPTION 'FAIL: control accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: reference controls rejected'; END;
 BEGIN INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(a,attempt_a,'accepted','SYNTHETIC TEST ONLY',repeat('x',65537));RAISE EXCEPTION 'FAIL: oversized reference accepted';
 EXCEPTION WHEN check_violation THEN RAISE NOTICE 'PASS: reference size bounded'; END;
 INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript,raw_model_transcript,model)
 VALUES(a,attempt_a,'accepted','SYNTHETIC TEST ONLY',E'SYNTHETIC reference one\n','attempted override','attempted-override') RETURNING id INTO review_id;
 SELECT split INTO split_before FROM transcript_dataset_items WHERE transmission_id=a;
 PERFORM pg_temp.check_review(split_before=transcript_dataset_split(fp),'split deterministic from fingerprint');
 PERFORM pg_temp.check_review((SELECT raw_model_transcript=E'  SYNTHETIC raw model evidence\n' AND model='small.en' FROM transcript_reviews WHERE id=review_id),'snapshot copies exact original attempt, ignoring overrides');
 PERFORM pg_temp.check_review((SELECT raw_text=E'  SYNTHETIC raw model evidence\n' FROM transcription_attempts WHERE id=attempt_a) AND (SELECT transcription_raw_text=E'  SYNTHETIC raw model evidence\n' FROM radio_transmissions WHERE id=a),'raw source transcripts unchanged');
 INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript)
 VALUES(a,attempt_a,'accepted','SYNTHETIC TEST ONLY','SYNTHETIC reference two') RETURNING id INTO review_id;
 PERFORM pg_temp.check_review((SELECT count(*)=2 FROM transcript_reviews WHERE transmission_id=a) AND (SELECT id=review_id FROM transcript_review_latest WHERE transmission_id=a),'re-review appends and largest review ID wins');
 INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label)
 VALUES(a,attempt_a,'needs_followup','SYNTHETIC TEST ONLY');
 PERFORM pg_temp.check_review(NOT EXISTS(SELECT FROM transcript_review_latest WHERE transmission_id=a AND verdict='accepted'),'follow-up supersedes accepted truth');
 INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,exclusion_reason)
 VALUES(a,attempt_a,'excluded','SYNTHETIC TEST ONLY','SYNTHETIC exclusion');
 PERFORM pg_temp.check_review((SELECT verdict='excluded' FROM transcript_review_latest WHERE transmission_id=a) AND (SELECT split=split_before FROM transcript_dataset_items WHERE transmission_id=a),'exclusion supersedes without changing split');
 BEGIN INSERT INTO transcript_dataset_items(audio_fingerprint,transmission_id,split) VALUES(fp,a,'test');RAISE EXCEPTION 'FAIL: split supplied';
 EXCEPTION WHEN SQLSTATE '428C9' THEN RAISE NOTICE 'PASS: split cannot be supplied'; END;
 BEGIN INSERT INTO transcript_dataset_items(audio_fingerprint,transmission_id) VALUES(fp,a);RAISE EXCEPTION 'FAIL: duplicate content assigned twice';
 EXCEPTION WHEN unique_violation THEN RAISE NOTICE 'PASS: duplicate content has one split'; END;
 FOREACH sql IN ARRAY ARRAY['UPDATE transcript_reviews SET notes=''rewritten''','DELETE FROM transcript_reviews',
 'TRUNCATE transcript_reviews','UPDATE transcript_dataset_items SET created_at=now()',
 'DELETE FROM transcript_dataset_items','TRUNCATE transcript_dataset_items CASCADE'] LOOP
  BEGIN EXECUTE sql;RAISE EXCEPTION 'FAIL: audit mutation allowed';
  EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: append-only enforcement %',split_part(sql,' ',1); END;
 END LOOP;
 BEGIN UPDATE transcription_attempts SET raw_text='rewritten' WHERE id=attempt_a;RAISE EXCEPTION 'FAIL: reviewed attempt changed';
 EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: reviewed attempt protected'; END;
 BEGIN UPDATE radio_transmissions SET processing_status='failed' WHERE id=a;RAISE EXCEPTION 'FAIL: reviewed canonical changed';
 EXCEPTION WHEN SQLSTATE '55000' THEN RAISE NOTICE 'PASS: reviewed canonical eligibility protected'; END;
END; $$;
ROLLBACK;
