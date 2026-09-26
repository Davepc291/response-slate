package notifydevicestore

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

func validP256dh() string {
	b := make([]byte, 65)
	b[0] = 0x04
	return base64.RawURLEncoding.EncodeToString(b)
}

func validAuth() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 16))
}

func validSubscription() notifydevices.Subscription {
	return notifydevices.Subscription{
		Endpoint: "https://push.example.com/send/abc123",
		Keys:     notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()},
	}
}

// fakeRow implements pgx.Row over a fixed row of the exact shape Register's
// RETURNING clause produces, or a fixed error.
type fakeRow struct {
	id, userID           int64
	endpoint             string
	p256dh, auth         []byte
	platform             *string
	createdAt, updatedAt time.Time
	revokedAt            *time.Time
	supersededBy         *int64
	err                  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.id
	*dest[1].(*int64) = r.userID
	*dest[2].(*string) = r.endpoint
	*dest[3].(*[]byte) = r.p256dh
	*dest[4].(*[]byte) = r.auth
	*dest[5].(**string) = r.platform
	*dest[6].(*time.Time) = r.createdAt
	*dest[7].(*time.Time) = r.updatedAt
	*dest[8].(**time.Time) = r.revokedAt
	*dest[9].(**int64) = r.supersededBy
	return nil
}

// fakeQuerier captures the exact SQL and arguments passed to QueryRow, so
// tests can prove every value travels as a bound parameter, never
// interpolated into the query text (mirroring alertstore/postgres_test.go's
// identical fakeQuerier pattern).
type fakeQuerier struct {
	sql  string
	args []any
	row  fakeRow
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql = sql
	q.args = args
	return q.row
}

func rawP256dh() []byte {
	b := make([]byte, 65)
	b[0] = 0x04
	return b
}

func rawAuth() []byte { return make([]byte, 16) }

func TestOpenRejectsNonLocalOrInvalid(t *testing.T) {
	for _, url := range []string{"", "not-a-url", "postgres://user:secret@remote.invalid/db", "postgres://user:secret@127.0.0.1/db?host=remote.invalid"} {
		_, closeDB, err := Open(context.Background(), url)
		if closeDB != nil {
			closeDB()
		}
		if err != ErrUnavailable {
			t.Fatalf("expected ErrUnavailable for %q, got %v", url, err)
		}
	}
}

func TestOpenRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, closeDB, err := Open(ctx, "postgres://fixture:secret@127.0.0.1:1/fixture?sslmode=disable")
	if closeDB != nil {
		closeDB()
	}
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for a canceled dial, got %v", err)
	}
}

func TestRegisterUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.Register(context.Background(), time.Now(), 1, validSubscription(), ""); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRegisterRejectsInvalidInputBeforeQuerying(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		userID   identity.UserID
		sub      notifydevices.Subscription
		platform string
	}{
		{"zero user id", 0, validSubscription(), ""},
		{"negative user id", -1, validSubscription(), ""},
		{"empty endpoint", 1, notifydevices.Subscription{Keys: notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()}}, ""},
		{"invalid p256dh", 1, notifydevices.Subscription{Endpoint: "https://push.example.com/x", Keys: notifydevices.Keys{P256dh: "not-valid", Auth: validAuth()}}, ""},
		{"invalid auth", 1, notifydevices.Subscription{Endpoint: "https://push.example.com/x", Keys: notifydevices.Keys{P256dh: validP256dh(), Auth: "not-valid"}}, ""},
		{"invalid platform", 1, validSubscription(), "bad<platform>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Register(context.Background(), now, c.userID, c.sub, c.platform); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid input must never reach the database")
			}
		})
	}
}

func TestRegisterRejectsZeroTime(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.Register(context.Background(), time.Time{}, 1, validSubscription(), ""); err != ErrInput {
		t.Fatalf("expected ErrInput for a zero-value now, got %v", err)
	}
}

func TestRegisterSuccessDecodesKeysToBytea(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{
		id: 1, userID: 1, endpoint: "https://push.example.com/send/abc123",
		p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now,
	}}
	p := &Postgres{DB: q}
	reg, err := p.Register(context.Background(), now, 1, validSubscription(), "android-chrome")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(q.sql, "ON CONFLICT (endpoint) WHERE revoked_at IS NULL") {
		t.Fatalf("expected the endpoint-partial-index conflict target to be used: %q", q.sql)
	}
	if strings.Contains(q.sql, "'") {
		t.Fatal("query text must contain only placeholders, never an inlined value")
	}
	if len(q.args) != 6 {
		t.Fatalf("expected 6 bound parameters, got %d", len(q.args))
	}
	// args[2]/args[3] are the decoded p256dh/auth bytea values, never the
	// original base64 text.
	p256dhArg, ok := q.args[2].([]byte)
	if !ok || len(p256dhArg) != 65 {
		t.Fatalf("expected a 65-byte decoded p256dh argument, got %#v", q.args[2])
	}
	authArg, ok := q.args[3].([]byte)
	if !ok || len(authArg) != 16 {
		t.Fatalf("expected a 16-byte decoded auth argument, got %#v", q.args[3])
	}

	if reg.ID != 1 || reg.UserID != 1 {
		t.Fatalf("unexpected identity fields: %+v", reg)
	}
	if reg.Subscription.Keys.P256dh != validP256dh() || reg.Subscription.Keys.Auth != validAuth() {
		t.Fatalf("expected the returned Registration's keys to be re-encoded back to the original base64url text: %+v", reg.Subscription.Keys)
	}
	if !reg.Active() {
		t.Fatal("expected a freshly registered device to be active")
	}
}

func TestRegisterEmptyPlatformSendsNilArgument(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{id: 1, userID: 1, endpoint: "https://push.example.com/x", p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now}}
	p := &Postgres{DB: q}
	if _, err := p.Register(context.Background(), now, 1, validSubscription(), ""); err != nil {
		t.Fatal(err)
	}
	platformArg, ok := q.args[4].(*string)
	if !ok || platformArg != nil {
		t.Fatalf("expected a nil *string platform argument for an empty platform hint, got %#v", q.args[4])
	}
}

func TestRegisterEndpointConflictWithDifferentOwnerIsForbidden(t *testing.T) {
	// The only way Register's own INSERT ... ON CONFLICT ... RETURNING can
	// return zero rows is the ownership WHERE guard blocking the update:
	// this is what pgx surfaces as ErrNoRows in that situation.
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
	p := &Postgres{DB: q}
	if _, err := p.Register(context.Background(), time.Now(), 1, validSubscription(), ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestSafeDatabaseErrorClassificationNeverMapsUniqueViolationToForbidden(t *testing.T) {
	secret := "postgres://user:VERY-SECRET-PASSWORD@10.0.0.5/private-db: connection refused"
	cases := []struct {
		err  error
		want error
	}{
		{errors.New(secret), ErrUnavailable},
		{&pgconn.PgError{Code: "23514", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "23503", Message: secret}, ErrInput},
		// Deliberately NOT ErrForbidden: safeDB stays generic. A raw
		// unique_violation reaching safeDB (rather than being resolved by
		// Register's own ON CONFLICT clause) is anomalous, and this proves
		// safeDB never globally reinterprets 23505 as an ownership meaning.
		{&pgconn.PgError{Code: "23505", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "22001", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "22023", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "40001", Message: secret}, ErrUnavailable},
	}
	for _, tc := range cases {
		q := &fakeQuerier{row: fakeRow{err: tc.err}}
		p := &Postgres{DB: q}
		_, err := p.Register(context.Background(), time.Now(), 1, validSubscription(), "")
		if err != tc.want {
			t.Fatalf("classify(%v) = %v, want %v", tc.err, err, tc.want)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "10.0.0.5") {
			t.Fatalf("database error leaked into a safe error: %v", err)
		}
	}
}

func TestRegisterSQLInjectionShapedEndpointNeverReachesQueryText(t *testing.T) {
	sub := validSubscription()
	sub.Endpoint = "https://push.example.com/'; DROP TABLE notification_devices; --"
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	// This exact endpoint shape still parses as a valid https URL (the
	// injected text lives in the path, which validateEndpoint does not
	// otherwise constrain), so it is expected to reach the database -- as a
	// bound parameter, never concatenated into the query text.
	q.row = fakeRow{id: 1, userID: 1, endpoint: sub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: time.Now(), updatedAt: time.Now()}
	if _, err := p.Register(context.Background(), time.Now(), 1, sub, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.sql, "DROP TABLE") {
		t.Fatal("endpoint value leaked into query text instead of traveling as a bound parameter")
	}
}
