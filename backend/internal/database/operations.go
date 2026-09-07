package database

import (
	"context"
	"encoding/json"
	"greenwich-fire-responder/backend/internal/operations"
	"path/filepath"
	"time"
)

func (db *DB) TranscriptionMetrics(ctx context.Context, directory string, _ int) (operations.Metrics, error) {
	var metrics operations.Metrics
	if db == nil {
		return metrics, ErrUnavailable
	}
	q, ok := db.pool.(audioQuery)
	if !ok {
		return metrics, ErrUnavailable
	}
	prefix := ""
	if directory != "" {
		abs, err := filepath.Abs(directory)
		if err != nil {
			return metrics, ErrUnavailable
		}
		prefix = abs + string(filepath.Separator)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT transcription_operations($1)`, prefix).Scan(&raw); err != nil {
		return metrics, ErrUnavailable
	}
	if json.Unmarshal(raw, &metrics) != nil {
		return operations.Metrics{}, ErrUnavailable
	}
	metrics.ObservedAt = metrics.ObservedAt.UTC()
	return metrics, nil
}
