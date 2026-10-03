package notifyoutcome

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

func fixedNow() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }

func validClaimToken() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }

func validEventID() string { return "evt_" + strings.Repeat("a", 64) }

func sampleDelivery() notifydelivery.Delivery {
	return notifydelivery.Delivery{
		OutboxID:      1,
		EventID:       validEventID(),
		DeviceID:      1,
		Outcome:       notifydelivery.OutcomeSent,
		AttemptNumber: 1,
	}
}

// fakePoolAlwaysErrors fails every Begin call, so Store's own input-shaped
// fail-closed path is exercised without ever needing a real pgx.Tx -- this
// package's actual transactional behavior (commit/rollback across the three
// composed stores) can only be meaningfully proven against a real database,
// exactly mirroring notifydevicestore.Replace/Revoke's own established
// testing convention; see integration_test.go for that proof.
type fakePoolAlwaysErrors struct{ calls int }

func (f *fakePoolAlwaysErrors) Begin(context.Context) (pgx.Tx, error) {
	f.calls++
	return nil, errors.New("fakePoolAlwaysErrors: begin refused")
}

func TestRecordSentUnconfiguredStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, _, err := s.RecordSent(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordCanceledUnconfiguredStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, _, err := s.RecordCanceled(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordFailedAttemptUnconfiguredStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, _, err := s.RecordFailedAttempt(context.Background(), fixedNow(), 1, fixedNow().Add(time.Minute), validClaimToken(), sampleDelivery())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordDeadLetterUnconfiguredStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, _, err := s.RecordDeadLetter(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery())
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordUnauthorizedUnconfiguredStoreFailsClosed(t *testing.T) {
	s := &Store{}
	_, _, err := s.RecordUnauthorized(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery(), identity.UserID(1), notifydevices.DeviceID(1))
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestEveryMethodFailsClosedWhenBeginFails(t *testing.T) {
	pool := &fakePoolAlwaysErrors{}
	s := &Store{DB: pool}

	if _, _, err := s.RecordSent(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery()); err != ErrUnavailable {
		t.Fatalf("RecordSent: expected ErrUnavailable, got %v", err)
	}
	if _, _, err := s.RecordCanceled(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery()); err != ErrUnavailable {
		t.Fatalf("RecordCanceled: expected ErrUnavailable, got %v", err)
	}
	if _, _, err := s.RecordFailedAttempt(context.Background(), fixedNow(), 1, fixedNow().Add(time.Minute), validClaimToken(), sampleDelivery()); err != ErrUnavailable {
		t.Fatalf("RecordFailedAttempt: expected ErrUnavailable, got %v", err)
	}
	if _, _, err := s.RecordDeadLetter(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery()); err != ErrUnavailable {
		t.Fatalf("RecordDeadLetter: expected ErrUnavailable, got %v", err)
	}
	if _, _, err := s.RecordUnauthorized(context.Background(), fixedNow(), 1, validClaimToken(), sampleDelivery(), identity.UserID(1), notifydevices.DeviceID(1)); err != ErrUnavailable {
		t.Fatalf("RecordUnauthorized: expected ErrUnavailable, got %v", err)
	}
	if pool.calls != 5 {
		t.Fatalf("expected exactly 5 Begin attempts (one per method), got %d", pool.calls)
	}
}

func TestDeliveryOutcomeForStateMapping(t *testing.T) {
	cases := []struct {
		state notifyoutbox.State
		want  notifydelivery.Outcome
		ok    bool
	}{
		{notifyoutbox.StatePending, notifydelivery.OutcomeFailed, true},
		{notifyoutbox.StateDeadLetter, notifydelivery.OutcomeDeadLetter, true},
		{notifyoutbox.StateExpired, notifydelivery.OutcomeExpired, true},
		{notifyoutbox.StateSent, "", false},
		{notifyoutbox.StateCanceled, "", false},
	}
	for _, c := range cases {
		got, ok := deliveryOutcomeForState(c.state)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("state %q: got (%q, %v), want (%q, %v)", c.state, got, ok, c.want, c.ok)
		}
	}
}
