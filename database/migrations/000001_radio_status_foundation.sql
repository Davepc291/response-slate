BEGIN;

CREATE TABLE schema_migrations (
    version text PRIMARY KEY CHECK (version ~ '^[0-9]{6}$'),
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE talkgroups (
    tgid bigint PRIMARY KEY CHECK (tgid > 0),
    channel_label text NOT NULL CHECK (btrim(channel_label) <> ''),
    purpose text NOT NULL CHECK (btrim(purpose) <> ''),
    can_create_incidents boolean NOT NULL DEFAULT false,
    accepts_unit_status boolean NOT NULL DEFAULT true,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dispatch_talkgroup_only CHECK (NOT can_create_incidents OR tgid = 57201)
);

CREATE TABLE units (
    unit_code text PRIMARY KEY CHECK (btrim(unit_code) <> ''),
    station text CHECK (btrim(station) <> ''),
    display_order integer CHECK (display_order > 0),
    primary_board boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT primary_board_order_required CHECK (NOT primary_board OR display_order IS NOT NULL)
);

CREATE UNIQUE INDEX units_primary_board_order_idx
    ON units (display_order) WHERE primary_board;

CREATE TABLE radio_identities (
    rid bigint PRIMARY KEY CHECK (rid > 0 AND rid <> 577811),
    unit_code text NOT NULL REFERENCES units (unit_code) ON DELETE RESTRICT,
    trusted boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX radio_identities_unit_idx ON radio_identities (unit_code);

CREATE TABLE radio_transmissions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    audio_fingerprint text NOT NULL UNIQUE CHECK (btrim(audio_fingerprint) <> ''),
    source_filename text NOT NULL CHECK (btrim(source_filename) <> ''),
    source_recorder text CHECK (btrim(source_recorder) <> ''),
    -- No reference-table FKs: unknown identifiers must remain recordable.
    tgid bigint CHECK (tgid > 0),
    rid bigint CHECK (rid > 0),
    recorded_at timestamptz,
    duration_ms bigint CHECK (duration_ms >= 0),
    -- Linear absolute amplitude relative to full scale, not decibels.
    rms numeric CHECK (rms BETWEEN 0 AND 1),
    peak numeric CHECK (peak BETWEEN 0 AND 1),
    transcript text,
    transcription_provider text,
    transcription_model text,
    transcription_status text NOT NULL DEFAULT 'pending'
        CHECK (transcription_status IN ('pending', 'processing', 'completed', 'failed', 'skipped')),
    processing_status text NOT NULL DEFAULT 'pending'
        CHECK (processing_status IN ('pending', 'processing', 'completed', 'failed', 'skipped')),
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rms_not_above_peak CHECK (rms <= peak)
);

CREATE INDEX radio_transmissions_processing_idx
    ON radio_transmissions (processing_status, created_at, id);
CREATE INDEX radio_transmissions_transcription_idx
    ON radio_transmissions (transcription_status, created_at, id);
CREATE INDEX radio_transmissions_recorded_idx
    ON radio_transmissions (recorded_at DESC NULLS LAST, id DESC);
CREATE INDEX radio_transmissions_tgid_recorded_idx
    ON radio_transmissions (tgid, recorded_at DESC NULLS LAST, id DESC);
CREATE INDEX radio_transmissions_rid_recorded_idx
    ON radio_transmissions (rid, recorded_at DESC NULLS LAST, id DESC);

CREATE TABLE unit_status_decisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transmission_id bigint NOT NULL REFERENCES radio_transmissions (id) ON DELETE RESTRICT,
    -- A detected value is evidence, not necessarily an existing trusted unit.
    detected_unit text CHECK (btrim(detected_unit) <> ''),
    previous_status text CHECK (previous_status IN ('QUARTERS', 'ENROUTE', 'ONSCENE', 'ONAIR')),
    proposed_status text CHECK (proposed_status IN ('QUARTERS', 'ENROUTE', 'ONSCENE', 'ONAIR')),
    matched_phrase text,
    confidence numeric CHECK (confidence BETWEEN 0 AND 1),
    accepted boolean NOT NULL,
    rejection_reason text,
    shadow_mode boolean NOT NULL DEFAULT true CHECK (shadow_mode),
    classifier_version text NOT NULL CHECK (btrim(classifier_version) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rejected_decision_reason_required CHECK (
        accepted OR (rejection_reason IS NOT NULL AND btrim(rejection_reason) <> '')
    ),
    CONSTRAINT accepted_decision_proposal_required CHECK (
        NOT accepted OR (detected_unit IS NOT NULL AND proposed_status IS NOT NULL)
    )
);

CREATE INDEX unit_status_decisions_transmission_idx
    ON unit_status_decisions (transmission_id, created_at, id);
CREATE INDEX unit_status_decisions_unit_created_idx
    ON unit_status_decisions (detected_unit, created_at DESC, id DESC);
CREATE INDEX unit_status_decisions_created_idx
    ON unit_status_decisions (created_at DESC, id DESC);

CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;

CREATE TRIGGER talkgroups_updated_at BEFORE UPDATE ON talkgroups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER units_updated_at BEFORE UPDATE ON units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER radio_identities_updated_at BEFORE UPDATE ON radio_identities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER radio_transmissions_updated_at BEFORE UPDATE ON radio_transmissions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE FUNCTION reject_unit_status_decision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'unit_status_decisions is an immutable audit log; append a new decision'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER unit_status_decisions_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON unit_status_decisions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_unit_status_decision_mutation();
ALTER TABLE unit_status_decisions ENABLE ALWAYS TRIGGER unit_status_decisions_immutable;

INSERT INTO talkgroups (tgid, channel_label, purpose, can_create_incidents, accepts_unit_status) VALUES
    (57201, 'CH1A', 'primary_dispatch', true, true),
    (57202, 'CH2B', 'status_operations', false, true),
    (57203, 'CH3B', 'status_operations', false, true),
    (57204, 'CH4C', 'status_operations', false, true);

INSERT INTO units (unit_code, station, display_order, primary_board) VALUES
    ('DC', 'HQ', 1, true),
    ('E2', 'STN2', 2, true),
    ('E3', 'STN3', 3, true),
    ('E4', 'STN4', 4, true),
    ('E5', 'STN5', 5, true),
    ('SQ1', 'HQ', 6, true),
    ('SQ8', 'STN8', 7, true),
    ('T1', 'HQ', 8, true),
    ('E12', NULL, NULL, false),
    ('E62', NULL, NULL, false),
    ('E71', NULL, NULL, false),
    ('E11', NULL, NULL, false),
    ('TANKER2', NULL, NULL, false),
    ('CAR5', NULL, NULL, false),
    ('CAR1', NULL, NULL, false),
    ('CAR4', NULL, NULL, false);

INSERT INTO radio_identities (rid, unit_code, trusted) VALUES
    (578054, 'SQ8', true),
    (578055, 'T1', true),
    (578056, 'E2', true),
    (578057, 'E12', true),
    (578059, 'SQ1', true),
    (578061, 'E5', true),
    (578064, 'E62', true),
    (578065, 'E71', true),
    (578066, 'E11', true),
    (578068, 'TANKER2', true),
    (578073, 'E4', true),
    (578074, 'E3', true),
    (578105, 'CAR5', true),
    (578000, 'CAR1', true),
    (578053, 'CAR4', true);

INSERT INTO schema_migrations (version) VALUES ('000001');

COMMIT;
