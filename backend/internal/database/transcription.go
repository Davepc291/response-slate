package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"greenwich-fire-responder/backend/internal/transcription"
	"path/filepath"
	"time"
)

func (db *DB) NextTranscription(ctx context.Context, directory string, o transcription.Options) (transcription.Job, bool, error) {
	var j transcription.Job
	if db == nil {
		return j, false, ErrUnavailable
	}
	q, ok := db.pool.(audioQuery)
	if !ok {
		return j, false, ErrUnavailable
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return j, false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Exact direct-child path scope; no LIKE pattern or recursive prefix matching.
	err = q.QueryRow(ctx, `SELECT id,source_identity,source_path,source_size_bytes,source_modified_at,transcription_attempts+1
 FROM radio_transmissions WHERE processing_status='completed' AND audio_duplicate_of IS NULL
 AND audio_fingerprint IS NOT NULL AND audio_probe IS NOT NULL AND source_identity IS NOT NULL
 AND source_path=$1 || source_filename AND transcription_status IN ('pending','failed','processing')
 AND transcription_retryable AND (transcription_next_at IS NULL OR transcription_next_at<=clock_timestamp())
 ORDER BY created_at,id LIMIT 1`, directory+string(filepath.Separator)).Scan(&j.ID, &j.Recording.SourceIdentity, &j.Recording.SourcePath, &j.Recording.SizeBytes, &j.Recording.ModifiedAt, &j.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	if err != nil {
		return j, false, ErrUnavailable
	}
	settings, _ := json.Marshal(map[string]any{"language": o.Language, "prompt": o.Prompt, "word_boost": o.WordBoost, "temperature": 0, "beam_size": 5, "best_of": 5, "response_format": "json"})
	j.Claim = transcription.NewClaim()
	var claimed bool
	err = q.QueryRow(ctx, `SELECT claim_transcription($1,$2::uuid,$3,$4,$5,$6,$7::jsonb)`, j.ID, j.Claim, o.MaxAttempts, int(o.Timeout.Seconds())+30, transcription.ProviderID, o.Model, string(settings)).Scan(&claimed)
	if err != nil {
		return j, false, ErrUnavailable
	}
	return j, claimed, nil
}

func (db *DB) FinishTranscription(ctx context.Context, j transcription.Job, r transcription.Result, f *transcription.Failure, delay time.Duration) (bool, error) {
	if db == nil {
		return false, ErrUnavailable
	}
	q, ok := db.pool.(audioQuery)
	if !ok {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var raw, text, code any
	retry := false
	if f == nil {
		raw = r.RawText
		text = r.Text
	} else {
		safe := transcription.SafeFailure(*f)
		code = safe.Code
		retry = safe.Retryable
	}
	var saved bool
	err := q.QueryRow(ctx, `SELECT finish_transcription($1,$2::uuid,$3,$4,$5,$6,$7)`, j.ID, j.Claim, raw, text, code, retry, int(delay.Seconds())).Scan(&saved)
	if err != nil {
		return false, ErrUnavailable
	}
	return saved, nil
}
