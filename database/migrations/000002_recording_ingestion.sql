BEGIN;

-- Keep audio content fingerprints separate from filename/path identities.
ALTER TABLE radio_transmissions
    ALTER COLUMN audio_fingerprint DROP NOT NULL,
    ADD COLUMN source_identity text UNIQUE CHECK (source_identity ~ '^[0-9a-f]{64}$'),
    ADD COLUMN source_path text CHECK (btrim(source_path) <> ''),
    ADD COLUMN system_site_label text CHECK (btrim(system_site_label) <> ''),
    ADD COLUMN alias_channel_label text CHECK (btrim(alias_channel_label) <> ''),
    ADD COLUMN channel_label text CHECK (channel_label IN ('CH1A', 'CH2B', 'CH3B', 'CH4C')),
    ADD COLUMN extension text CHECK (extension IN ('.mp3', '.wav')),
    ADD COLUMN recording_timezone text CHECK (btrim(recording_timezone) <> ''),
    ADD COLUMN source_size_bytes bigint CHECK (source_size_bytes > 0),
    ADD COLUMN source_modified_at timestamptz,
    ADD CONSTRAINT transmission_identity_required CHECK (
        audio_fingerprint IS NOT NULL OR source_identity IS NOT NULL
    ),
    ADD CONSTRAINT recording_source_metadata_required CHECK (
        source_identity IS NULL OR (
            source_path IS NOT NULL AND system_site_label IS NOT NULL
            AND alias_channel_label IS NOT NULL AND channel_label IS NOT NULL
            AND extension IS NOT NULL AND recording_timezone IS NOT NULL
            AND source_size_bytes IS NOT NULL AND source_modified_at IS NOT NULL
            AND recorded_at IS NOT NULL AND tgid IS NOT NULL AND rid IS NOT NULL
            AND source_recorder IS NOT NULL
        )
    );

COMMENT ON COLUMN radio_transmissions.source_identity IS
    'SHA-256 hex of sdrtrunk-path-v1, NUL separator, and cleaned absolute path; Windows paths case-folded. Not an audio content hash.';
COMMENT ON COLUMN radio_transmissions.audio_fingerprint IS
    'Optional audio content fingerprint; NULL until audio content is inspected by a later milestone.';

INSERT INTO schema_migrations (version) VALUES ('000002');
COMMIT;
