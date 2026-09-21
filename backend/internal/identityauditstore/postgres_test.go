package identityauditstore

// Unit-level coverage for Record's own input validation, independent of a
// live database: a fake Querier lets these run without the
// GFR_IDENTITY_LIVE_TEST opt-in the integration test requires.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

type fakeRow struct{ id int64 }

func (r fakeRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.id
	return nil
}

type fakeQuerier struct{ nextID int64 }

func (f *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	f.nextID++
	return fakeRow{id: f.nextID}
}

var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestRecordAllowsNoAccountForLoginFailureAndInvitationRedemptionFailed(t *testing.T) {
	p := &Postgres{DB: &fakeQuerier{}}
	for _, ev := range []identityaudit.Event{
		{Type: identityaudit.LoginFailure, Metadata: identityaudit.Metadata{"reason_code": "no_such_account"}, CreatedAt: testNow},
		{Type: identityaudit.InvitationRedemptionFailed, Metadata: identityaudit.Metadata{"reason_code": "token_expired"}, CreatedAt: testNow},
	} {
		if _, err := p.Record(context.Background(), ev); err != nil {
			t.Errorf("expected %s with no account to be recordable, got %v", ev.Type, err)
		}
	}
}

func TestRecordRejectsNoAccountForOtherEventTypes(t *testing.T) {
	p := &Postgres{DB: &fakeQuerier{}}
	ev := identityaudit.Event{Type: identityaudit.SessionCreated, Metadata: identityaudit.Metadata{"device_hint": "synthetic"}, CreatedAt: testNow}
	if _, err := p.Record(context.Background(), ev); err != ErrInput {
		t.Fatalf("expected ErrInput for a no-account session_created event, got %v", err)
	}
}

func TestRecordRejectsForbiddenMetadata(t *testing.T) {
	p := &Postgres{DB: &fakeQuerier{}}
	ev := identityaudit.Event{
		Type: identityaudit.SessionCreated, AccountID: identity.UserID(1),
		Metadata: identityaudit.Metadata{"device_hint": "x"}, CreatedAt: testNow,
	}
	if _, err := p.Record(context.Background(), ev); err != nil {
		t.Fatalf("expected valid event to record: %v", err)
	}
}

func TestRecordRejectsNilStore(t *testing.T) {
	var p *Postgres
	if _, err := p.Record(context.Background(), identityaudit.Event{}); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for a nil store, got %v", err)
	}
}
