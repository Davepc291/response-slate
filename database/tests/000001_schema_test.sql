\set ON_ERROR_STOP on
BEGIN;

CREATE FUNCTION pg_temp.assert_true(condition boolean, label text) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    IF condition IS DISTINCT FROM true THEN
        RAISE EXCEPTION 'FAIL: %', label;
    END IF;
    RAISE NOTICE 'PASS: %', label;
END;
$$;

-- Each expected failure runs in a subtransaction; even an unexpected success
-- is rolled back before the helper reports failure.
CREATE FUNCTION pg_temp.expect_error(statement text, expected_state text, label text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    actual_state text;
BEGIN
    BEGIN
        EXECUTE statement;
        RAISE EXCEPTION 'statement unexpectedly succeeded' USING ERRCODE = 'P0001';
    EXCEPTION WHEN OTHERS THEN
        GET STACKED DIAGNOSTICS actual_state = RETURNED_SQLSTATE;
    END;
    PERFORM pg_temp.assert_true(actual_state = expected_state, label);
END;
$$;

DO $$
DECLARE
    transmission bigint;
    unknown_transmission bigint;
    decision bigint;
    invalid_status text;
    valid_status text;
    reason text;
BEGIN
    PERFORM pg_temp.assert_true(
        (SELECT count(*) = 1 FROM schema_migrations WHERE version = '000001'),
        'migration 000001 recorded');
    PERFORM pg_temp.assert_true(
        (SELECT count(*) = 4 AND bool_and(active AND accepts_unit_status)
            AND array_agg(tgid ORDER BY tgid) = ARRAY[57201,57202,57203,57204]::bigint[]
            AND array_agg(channel_label ORDER BY tgid) = ARRAY['CH1A','CH2B','CH3B','CH4C']
            AND array_agg(purpose ORDER BY tgid) = ARRAY['primary_dispatch','status_operations','status_operations','status_operations']
            AND array_agg(can_create_incidents ORDER BY tgid) = ARRAY[true,false,false,false]
         FROM talkgroups), 'four talkgroups, labels, purposes, and permissions');
    PERFORM pg_temp.expect_error(
        'UPDATE talkgroups SET can_create_incidents = true WHERE tgid = 57202',
        '23514', 'only 57201 may create incidents');
    PERFORM pg_temp.assert_true(
        (SELECT array_agg(unit_code ORDER BY display_order) = ARRAY['DC','E2','E3','E4','E5','SQ1','SQ8','T1']
            AND array_agg(station ORDER BY display_order) = ARRAY['HQ','STN2','STN3','STN4','STN5','HQ','STN8','HQ']
            AND array_agg(display_order ORDER BY display_order) = ARRAY[1,2,3,4,5,6,7,8]
            AND bool_and(active)
         FROM units WHERE primary_board), 'primary board order, stations, and active flags');
    PERFORM pg_temp.assert_true(
        (SELECT count(*) = 8 AND bool_and(station IS NULL AND display_order IS NULL AND active)
         FROM units WHERE NOT primary_board), 'non-primary units have no invented stations or board positions');
    PERFORM pg_temp.assert_true(
        (SELECT unit_code = 'E5' AND trusted AND active FROM radio_identities WHERE rid = 578061),
        'RID 578061 maps to trusted E5');
    PERFORM pg_temp.assert_true(
        (SELECT unit_code = 'CAR4' AND trusted AND active FROM radio_identities WHERE rid = 578053),
        'RID 578053 maps to trusted CAR4');
    PERFORM pg_temp.assert_true(
        (SELECT count(*) = 15 AND bool_and(trusted AND active)
         FROM radio_identities), '15 active trusted RID mappings');
    PERFORM pg_temp.assert_true(
        NOT EXISTS (SELECT FROM radio_identities WHERE rid = 577811), 'RID 577811 absent');
    PERFORM pg_temp.expect_error(
        'INSERT INTO radio_identities (rid, unit_code) VALUES (577811, ''E5'')',
        '23514', 'RID 577811 cannot be mapped');

    INSERT INTO radio_transmissions (audio_fingerprint, source_filename, source_recorder,
        tgid, rid, recorded_at, duration_ms, rms, peak, transcript,
        transcription_provider, transcription_model, transcription_status)
    VALUES ('schema-test-' || txid_current(), 'synthetic-test.wav', 'schema-test',
        57202, 578061, now(), 1200, 0.2, 0.8, 'Engine 5 on scene',
        'synthetic', 'text-fixture', 'completed') RETURNING id INTO transmission;

    INSERT INTO radio_transmissions (audio_fingerprint, source_filename, tgid, rid)
    VALUES ('schema-test-unknown-' || txid_current(), 'synthetic-unknown.wav', 999999, 577811)
    RETURNING id INTO unknown_transmission;
    PERFORM pg_temp.assert_true(unknown_transmission IS NOT NULL, 'unknown TGID and unmapped RID recordable');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO radio_transmissions (audio_fingerprint, source_filename) SELECT audio_fingerprint, source_filename FROM radio_transmissions WHERE id = %s', transmission),
        '23505', 'duplicate audio fingerprint rejected');

    INSERT INTO unit_status_decisions (transmission_id, detected_unit, previous_status,
        proposed_status, matched_phrase, confidence, accepted, classifier_version)
    VALUES (transmission, 'E5', 'ENROUTE', 'ONSCENE', 'on scene', 0.95, true, 'schema-test-v1')
    RETURNING id INTO decision;
    PERFORM pg_temp.assert_true(
        (SELECT shadow_mode AND accepted AND detected_unit = 'E5' AND proposed_status = 'ONSCENE'
         FROM unit_status_decisions WHERE id = decision), 'E5 ONSCENE proposal defaults to shadow mode');

    FOREACH valid_status IN ARRAY ARRAY['QUARTERS','ENROUTE','ONSCENE','ONAIR'] LOOP
        INSERT INTO unit_status_decisions (transmission_id, detected_unit, previous_status,
            proposed_status, accepted, classifier_version)
        VALUES (transmission, 'E5', valid_status, valid_status, true, 'schema-test-v1');
    END LOOP;
    PERFORM pg_temp.assert_true(
        (SELECT count(*) = 5 FROM unit_status_decisions WHERE transmission_id = transmission),
        'all normalized statuses allowed; multiple decisions per transmission preserved');

    FOREACH invalid_status IN ARRAY ARRAY['EN_ROUTE','ON_SCENE','AVAILABLE','INVALID',''] LOOP
        PERFORM pg_temp.expect_error(format(
            'INSERT INTO unit_status_decisions (transmission_id, detected_unit, proposed_status, accepted, classifier_version) VALUES (%s, ''E5'', %L, true, ''schema-test-v1'')',
            transmission, invalid_status), '23514', 'invalid proposed status rejected: ' || invalid_status);
        PERFORM pg_temp.expect_error(format(
            'INSERT INTO unit_status_decisions (transmission_id, detected_unit, previous_status, proposed_status, accepted, classifier_version) VALUES (%s, ''E5'', %L, ''ONSCENE'', true, ''schema-test-v1'')',
            transmission, invalid_status), '23514', 'invalid previous status rejected: ' || invalid_status);
    END LOOP;

    FOREACH reason IN ARRAY ARRAY[NULL, '', '   ']::text[] LOOP
        PERFORM pg_temp.expect_error(format(
            'INSERT INTO unit_status_decisions (transmission_id, accepted, rejection_reason, classifier_version) VALUES (%s, false, %L, ''schema-test-v1'')',
            transmission, reason), '23514', 'rejected decision requires nonblank reason: ' || coalesce(quote_literal(reason), 'NULL'));
    END LOOP;
    INSERT INTO unit_status_decisions (transmission_id, accepted, rejection_reason, classifier_version)
    VALUES (unknown_transmission, false, 'unknown RID', 'schema-test-v1');
    PERFORM pg_temp.assert_true(
        EXISTS (SELECT FROM unit_status_decisions WHERE transmission_id = unknown_transmission
            AND NOT accepted AND detected_unit IS NULL AND proposed_status IS NULL),
        'unclassifiable rejection retained without invented unit or status');

    PERFORM pg_temp.expect_error(format(
        'INSERT INTO unit_status_decisions (transmission_id, accepted, classifier_version) VALUES (%s, true, ''schema-test-v1'')', transmission),
        '23514', 'accepted decision requires detected unit and proposal');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO unit_status_decisions (transmission_id, accepted, rejection_reason, shadow_mode, classifier_version) VALUES (%s, false, ''test'', false, ''schema-test-v1'')', transmission),
        '23514', 'non-shadow decision rejected in this milestone');
    PERFORM pg_temp.expect_error(format(
        'INSERT INTO unit_status_decisions (transmission_id, accepted, rejection_reason, confidence, classifier_version) VALUES (%s, false, ''test'', 1.1, ''schema-test-v1'')', transmission),
        '23514', 'out-of-range confidence rejected');
    PERFORM pg_temp.expect_error(format(
        'UPDATE unit_status_decisions SET matched_phrase = ''changed'' WHERE id = %s', decision),
        '55000', 'audit UPDATE blocked');
    PERFORM pg_temp.expect_error(format('DELETE FROM unit_status_decisions WHERE id = %s', decision),
        '55000', 'audit DELETE blocked');
    PERFORM pg_temp.expect_error('TRUNCATE unit_status_decisions', '55000', 'audit TRUNCATE blocked');
    PERFORM pg_temp.expect_error(format('DELETE FROM radio_transmissions WHERE id = %s', transmission),
        '23503', 'transmission with audit decisions cannot be deleted');
    RAISE NOTICE 'ALL SCHEMA TESTS PASSED';
END;
$$;

ROLLBACK;
