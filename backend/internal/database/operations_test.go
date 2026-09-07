package database

import (
	"context"
	"errors"
	"testing"
)

func TestMetricsDatabaseFailureIsSafe(t *testing.T) {
	for _, db := range []*DB{nil, {}, {pool: &audioPool{row: audioRow{err: errors.New("password transcript path http://private")}}}} {
		_, err := db.TranscriptionMetrics(context.Background(), t.TempDir(), 3)
		if err != ErrUnavailable {
			t.Fatal("unsafe metrics error", err)
		}
	}
	p := &audioPool{}
	db := &DB{pool: p}
	_, err := db.TranscriptionMetrics(context.Background(), "", 3)
	if err != ErrUnavailable || !p.deadline || p.args[0] != "" {
		t.Fatal("malformed metrics or scope not rejected")
	}
}
