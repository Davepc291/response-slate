package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"greenwich-fire-responder/backend/internal/audioanalysis"
)

type audioQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (db *DB) ClaimAudio(ctx context.Context, identity string, maxAttempts int) (bool, error) {
	if db == nil {
		return false, ErrUnavailable
	}
	q, ok := db.pool.(audioQuery)
	if !ok {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var id int64
	err := q.QueryRow(ctx, `UPDATE radio_transmissions SET processing_status='processing',
        analysis_attempts=analysis_attempts+1, error_message=NULL
        WHERE source_identity=$1 AND processing_status IN ('pending','failed')
        AND analysis_retryable AND analysis_attempts<$2 AND audio_duplicate_of IS NULL
        RETURNING id`, identity, maxAttempts).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrUnavailable
	}
	return true, nil
}

func (db *DB) CompleteAudio(ctx context.Context, identity string, result audioanalysis.Result) (string, error) {
	if db == nil {
		return "", ErrUnavailable
	}
	q, ok := db.pool.(audioQuery)
	if !ok {
		return "", ErrUnavailable
	}
	probe, err := json.Marshal(result.Probe)
	if err != nil {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var outcome string
	err = q.QueryRow(ctx, `SELECT complete_audio_analysis($1,$2,$3,$4,$5,$6::jsonb)`, identity,
		result.Fingerprint, result.DurationMS, result.RMS, result.Peak, string(probe)).Scan(&outcome)
	if err != nil {
		return "", ErrUnavailable
	}
	return outcome, nil
}

func (db *DB) FailAudio(ctx context.Context, identity, reason string, retryable bool) error {
	if db == nil {
		return ErrUnavailable
	}
	q, ok := db.pool.(recordingExecutor)
	if !ok {
		return ErrUnavailable
	}
	// Keep even accidental caller errors from persisting credentials.
	switch reason {
	case "invalid_audio", "unreasonable_duration", "audio_tool_unavailable", "audio_timeout",
		"audio_canceled", "source_changed_or_unavailable", "audio_analysis_failed":
	default:
		reason = "audio_analysis_failed"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := q.Exec(ctx, `UPDATE radio_transmissions SET processing_status='failed',
        error_message=$2, analysis_retryable=$3 WHERE source_identity=$1 AND processing_status='processing'`, identity, reason, retryable)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
