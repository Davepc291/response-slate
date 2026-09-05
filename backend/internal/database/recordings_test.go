package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"greenwich-fire-responder/backend/internal/recordings"
)

type recordingPool struct {
	fakePool
	result  string
	execErr error
}

func (p *recordingPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if _, ok := ctx.Deadline(); !ok {
		return pgconn.CommandTag{}, errors.New("no deadline")
	}
	if !strings.Contains(sql, "ON CONFLICT (source_identity) DO NOTHING") || len(args) != 13 {
		return pgconn.CommandTag{}, errors.New("bad insert")
	}
	return pgconn.NewCommandTag(p.result), p.execErr
}

func TestInsertRecording(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		err          error
		inserted     bool
	}{
		{"accepted", "INSERT 0 1", nil, true}, {"duplicate", "INSERT 0 0", nil, false},
		{"safe failure", "", errors.New("postgres://user:secret@host/db"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &DB{pool: &recordingPool{result: tc.result, execErr: tc.err}}
			inserted, err := db.InsertRecording(context.Background(), recordings.Recording{})
			if inserted != tc.inserted || (tc.err == nil && err != nil) || (tc.err != nil && err != ErrUnavailable) {
				t.Fatal("incorrect or unsafe insertion result")
			}
		})
	}
	if inserted, err := (&DB{}).InsertRecording(context.Background(), recordings.Recording{}); inserted || err != ErrUnavailable {
		t.Fatal("unconfigured database accepted recording")
	}
}
