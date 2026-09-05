package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"greenwich-fire-responder/backend/internal/audioanalysis"
)

type audioRow struct {
	value string
	err   error
}

func (r audioRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch v := dest[0].(type) {
	case *int64:
		*v = 1
	case *string:
		*v = r.value
	}
	return nil
}

type audioPool struct {
	fakePool
	row      audioRow
	sql      string
	args     []any
	deadline bool
}

func (p *audioPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	p.sql = sql
	p.args = args
	_, p.deadline = ctx.Deadline()
	return p.row
}
func (p *audioPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	p.sql = sql
	p.args = args
	_, p.deadline = ctx.Deadline()
	return pgconn.NewCommandTag("UPDATE 1"), p.row.err
}

func TestAudioDatabase(t *testing.T) {
	p := &audioPool{}
	db := &DB{pool: p}
	if ok, err := db.ClaimAudio(context.Background(), "source", 3); err != nil || !ok || !p.deadline || !strings.Contains(p.sql, "analysis_attempts<$2") {
		t.Fatal("claim not guarded/bounded")
	}
	p.row.err = pgx.ErrNoRows
	if ok, err := db.ClaimAudio(context.Background(), "source", 3); err != nil || ok {
		t.Fatal("ineligible claim not respected")
	}
	p.row = audioRow{value: "duplicate-content"}
	result := audioanalysis.Result{Fingerprint: audioanalysis.FingerprintPrefix + strings.Repeat("a", 64), DurationMS: 1000, RMS: .25, Peak: .5}
	if outcome, err := db.CompleteAudio(context.Background(), "source", result); err != nil || outcome != "duplicate-content" || !strings.Contains(p.sql, "complete_audio_analysis") || len(p.args) != 6 {
		t.Fatal("transactional result update failed")
	}
	if err := db.FailAudio(context.Background(), "source", "postgres://secret", false); err != nil || p.args[1] != "audio_analysis_failed" || !strings.Contains(p.sql, "processing_status='processing'") {
		t.Fatal("failure could overwrite completion or leak errors")
	}
	p.row.err = errors.New("postgres://user:secret@private/db")
	if _, err := db.CompleteAudio(context.Background(), "source", result); err != ErrUnavailable {
		t.Fatal("unsafe database error")
	}
	if _, err := db.ClaimAudio(context.Background(), "source", 3); err != ErrUnavailable {
		t.Fatal("unsafe claim error")
	}
	if err := db.FailAudio(context.Background(), "source", "audio_timeout", true); err != ErrUnavailable {
		t.Fatal("unsafe fail error")
	}
	if _, err := (&DB{}).CompleteAudio(context.Background(), "source", result); err != ErrUnavailable {
		t.Fatal("unconfigured database accepted result")
	}
}
