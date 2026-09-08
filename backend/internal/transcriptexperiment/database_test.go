package transcriptexperiment

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

type fakeRow struct{ err error }

func (r fakeRow) Scan(...any) error { return r.err }

type queryRecorder struct {
	sql  string
	args []any
}

func (q *queryRecorder) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql = sql
	q.args = args
	return fakeRow{errors.New("SYNTHETIC SECRET")}
}
func TestReadOnlyDatabaseBoundary(t *testing.T) {
	if readOnlyOptions.AccessMode != pgx.ReadOnly || readOnlyOptions.IsoLevel != pgx.RepeatableRead {
		t.Fatal("transaction settings")
	}
	q := &queryRecorder{}
	p := postgres{query: q}
	if _, e := p.Resolve(context.Background(), 42); e != ErrEvidence {
		t.Fatal("unsafe error")
	}
	if !strings.HasPrefix(q.sql, "SELECT ") || len(q.args) != 1 || q.args[0] != int64(42) {
		t.Fatal("not parameterized SELECT")
	}
	for _, guard := range []string{"r.id=a.transmission_id", "a.id=$1", "a.outcome='completed'", "r.transcription_status='completed'", "r.audio_duplicate_of IS NULL", "a.finished_at IS NOT NULL", "r.transcription_model=a.model"} {
		if !strings.Contains(q.sql, guard) {
			t.Fatal("missing eligibility guard", guard)
		}
	}
	for _, url := range []string{"", "SYNTHETIC SECRET", "postgres://example.invalid/db", "postgres://127.0.0.1/db?host=example.invalid", "postgres://127.0.0.1/db?options=bad"} {
		if _, e := databaseConfig(url); e != ErrDatabase {
			t.Fatal("unsafe database URL")
		}
	}
	cfg, e := databaseConfig("postgres://synthetic:placeholder@127.0.0.1:5432/local?sslmode=disable")
	if e != nil || cfg.RuntimeParams["default_transaction_read_only"] != "on" || len(cfg.Fallbacks) != 0 {
		t.Fatal("config", e)
	}
}
