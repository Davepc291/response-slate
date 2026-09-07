package transcription

import (
	"context"
	"time"
)

// RequestMeasurement excludes file checks and multipart setup. Duration uses
// Go's monotonic clock, from immediately before Client.Do through body reading.
// RequestStartedAt is UTC wall time for correlation with PostgreSQL claims.
type RequestMeasurement struct {
	StartedAt time.Time
	Duration  time.Duration
}
type measurementKey struct{}

func withMeasurement(ctx context.Context, m *RequestMeasurement) context.Context {
	return context.WithValue(ctx, measurementKey{}, m)
}
