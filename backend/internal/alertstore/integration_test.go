package alertstore

// Optional real-database verification, mirroring the existing
// transcriptreview package's convention: skipped unless explicitly opted
// into a local rollback-only database, since this sandbox/CI environment
// cannot assume a running PostgreSQL instance. Everything here runs inside
// one transaction that is always rolled back; no fixture is left behind.
//
// This is the Go-side equivalent of "migration up/down verification" for a
// repository that has no separate down-migration file: it exercises the
// already-applied migration 000007 schema (idempotency, deduplication,
// atomic rollback, and append-only enforcement) through the real
// record_alert_detection function, the same behaviors
// database/tests/000007_schema_test.sql checks directly in psql.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/alerts"
)

func TestLiveAlertPersistenceRollsBack(t *testing.T) {
	if os.Getenv("GFR_ALERTS_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_ALERTS_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local alert-persistence database unavailable")
	}
	defer closeDB()
	pool := db.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tx.Rollback(cleanup)
		}
	}()

	var migrated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = '000007')`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("migration 000007 is not applied to this database")
	}

	sid := strings.Repeat("a", 64)
	fp := "pcm-s16le-16000-mono-v1:sha256:" + strings.Repeat("b", 64)
	var n int64
	if err := tx.QueryRow(ctx, `INSERT INTO radio_transmissions(source_identity, source_path, source_filename,
        source_recorder, tgid, rid, recorded_at, system_site_label, alias_channel_label, channel_label, extension,
        recording_timezone, source_size_bytes, source_modified_at)
        VALUES($1, 'synthetic-alert/live.mp3', 'live.mp3', 'SDRTrunk', 57201, 578060, now(),
        'synthetic', 'synthetic', 'CH1A', '.mp3', 'America/New_York', 100, now()) RETURNING id`, sid).Scan(&n); err != nil {
		t.Fatalf("fixture insert failed: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE radio_transmissions SET processing_status = 'processing' WHERE id = $1`, n); err != nil {
		t.Fatalf("fixture claim failed: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT complete_audio_analysis($1, $2, 1000, 0.1, 0.5, '{}')`, sid, fp); err != nil {
		t.Fatalf("fixture analysis completion failed: %v", err)
	}

	rec := baseRecord(t)
	rec.Evidence = alerts.Evidence{AudioFingerprint: fp}
	rec.Event.EventID = "evt_" + strings.Repeat("c", 64)
	rec.Event.DedupKey = strings.Repeat("d", 64)

	p := &Postgres{DB: tx}
	result, err := p.RecordDetection(ctx, time.Minute, rec)
	if err != nil {
		t.Fatalf("RecordDetection failed against a real database: %v", err)
	}
	if !result.EventCreated || result.Suppressed {
		t.Fatalf("expected a newly created event: %+v", result)
	}

	retry, err := p.RecordDetection(ctx, time.Minute, rec)
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if !retry.Duplicate() {
		t.Fatalf("expected the retry to be an idempotent duplicate: %+v", retry)
	}

	var eventCount, auditCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_events WHERE event_id = $1`, rec.Event.EventID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM detection_audit WHERE event_id = $1`, rec.Event.EventID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected exactly one alert_events row, got %d", eventCount)
	}
	if auditCount != 2 {
		t.Fatalf("expected two detection_audit rows (original + retry), got %d", auditCount)
	}

	if _, err := tx.Exec(ctx, `UPDATE detection_audit SET reason = 'rewritten'`); err == nil {
		t.Fatal("expected the append-only trigger to reject an UPDATE")
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}
