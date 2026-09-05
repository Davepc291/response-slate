package database

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"greenwich-fire-responder/backend/internal/recordings"
)

type recordingExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

const insertRecordingSQL = `INSERT INTO radio_transmissions
    (source_identity, source_path, source_filename, source_recorder, tgid, rid,
     recorded_at, system_site_label, alias_channel_label, channel_label,
     extension, recording_timezone, source_size_bytes, source_modified_at)
    VALUES ($1, $2, $3, 'SDRTrunk', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
    ON CONFLICT (source_identity) DO NOTHING`

// InsertRecording writes metadata only. The database unique constraint handles
// retries, concurrent watchers, and uncertain outcomes after a connection loss.
func (db *DB) InsertRecording(ctx context.Context, r recordings.Recording) (bool, error) {
	if db == nil || db.pool == nil {
		return false, ErrUnavailable
	}
	executor, ok := db.pool.(recordingExecutor)
	if !ok {
		return false, ErrUnavailable
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := executor.Exec(writeCtx, insertRecordingSQL,
		r.SourceIdentity, r.SourcePath, r.OriginalFilename, r.TGID, r.RID, r.RecordedAt,
		r.SystemSiteLabel, r.AliasChannelLabel, r.ChannelLabel, r.Extension, r.Timezone,
		r.SizeBytes, r.ModifiedAt)
	if err != nil {
		return false, ErrUnavailable
	}
	return result.RowsAffected() == 1, nil
}
