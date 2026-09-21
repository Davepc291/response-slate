package identityauditstore

// Optional real-database verification, mirroring the existing
// alertstore/identitystore convention: skipped unless explicitly opted into
// a local database (set GFR_IDENTITY_LIVE_TEST=true and GFR_DATABASE_URL).
// Everything here runs inside one transaction that is always rolled back.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

func TestLiveRecordAndAppendOnly(t *testing.T) {
	if os.Getenv("GFR_IDENTITY_LIVE_TEST") != "true" {
		t.Skip("explicit local database opt-in required (set GFR_IDENTITY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, _, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatalf("local identity audit database unavailable: %v", err)
	}
	pool := db.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(c)
		}
	}()
	p := &Postgres{DB: tx}

	var migrated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = '000008')`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("migration 000008 is not applied to this database")
	}

	var accountID int64
	email := fmt.Sprintf("synthetic-audit-%d@example.test", os.Getpid())
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role)
        VALUES ($1, 'Synthetic Audit Target', 'responder') RETURNING id`, email).Scan(&accountID); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev, err := identityaudit.New(identityaudit.InvitationCreated, identity.UserID(accountID), 0, "", identityaudit.Metadata{"role": "responder"}, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Record(ctx, ev)
	if err != nil || id == 0 {
		t.Fatalf("Record: id=%d err=%v", id, err)
	}

	var storedType, storedMetadata string
	if err := tx.QueryRow(ctx, `SELECT event_type, metadata::text FROM identity_audit_log WHERE id = $1`, id).
		Scan(&storedType, &storedMetadata); err != nil {
		t.Fatal(err)
	}
	if storedType != string(identityaudit.InvitationCreated) {
		t.Fatalf("expected stored event_type %q, got %q", identityaudit.InvitationCreated, storedType)
	}

	// Attempting to persist a forbidden-shaped event never reaches the
	// database at all: Record validates before building any statement.
	badEv := identityaudit.Event{
		Type: identityaudit.InvitationCreated, AccountID: identity.UserID(accountID),
		Metadata: identityaudit.Metadata{"password": "should-never-be-accepted"}, CreatedAt: now,
	}
	if _, err := p.Record(ctx, badEv); err != ErrInput {
		t.Fatalf("expected ErrInput for forbidden metadata key, got %v", err)
	}

	// Append-only enforcement at the database level. Each attempt runs in
	// its own savepoint (via nested Begin/Rollback) so one rejected
	// statement does not abort the whole outer transaction before the next
	// assertion runs.
	for _, stmt := range []string{
		`UPDATE identity_audit_log SET reason = 'rewritten' WHERE id = $1`,
		`DELETE FROM identity_audit_log WHERE id = $1`,
	} {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, execErr := savepoint.Exec(ctx, stmt, id)
		if execErr == nil {
			t.Fatalf("expected the append-only trigger to reject: %s", stmt)
		}
		_ = savepoint.Rollback(ctx)
	}

	c, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := tx.Rollback(c); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}
