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

// fakeQuerier captures the exact SQL and arguments passed to QueryRow/Query,
// so tests can prove every value travels as a bound parameter, never
// interpolated into the query text (mirroring alertstore/postgres_test.go's
// identical fakeQuerier pattern).
type fakeQuerier struct {
	sql      string
	args     []any
	row      fakeRow
	rows     *fakeRows
	queryErr error
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql = sql
	q.args = args
	return q.row
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.sql = sql
	q.args = args
	if q.queryErr != nil {
		return nil, q.queryErr
	}
	if q.rows == nil {
		return &fakeRows{}, nil
	}
	return q.rows, nil
}

// Begin exists only so *fakeQuerier satisfies Pool for Register/Get/
// ListActive's own tests, none of which ever call it: it fails loudly
// rather than silently misbehaving if a future test path unexpectedly
// reaches it.
func (q *fakeQuerier) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("fakeQuerier: Begin not supported; use fakePool for a transactional test")
}

// fakeRows implements pgx.Rows over a fixed, ordered slice of pgx.Row
// values (fakeRow or an error-only fakeRow), matching ListActive's own
// multi-row query shape. Only Next/Scan/Close/Err have real behavior;
// every other method exists solely to satisfy the interface and panics if
// ever reached.
type fakeRows struct {
	remaining []pgx.Row
	err       error
}

func (r *fakeRows) Next() bool { return len(r.remaining) > 0 }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.remaining[0]
	r.remaining = r.remaining[1:]
	return row.Scan(dest...)
}
func (r *fakeRows) Close()     {}
func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag {
	panic("fakeRows: CommandTag not supported")
}
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	panic("fakeRows: FieldDescriptions not supported")
}
func (r *fakeRows) Values() ([]any, error) { panic("fakeRows: Values not supported") }
func (r *fakeRows) RawValues() [][]byte    { panic("fakeRows: RawValues not supported") }
func (r *fakeRows) Conn() *pgx.Conn        { panic("fakeRows: Conn not supported") }

// fakeIDRow implements pgx.Row over a single int64 column, matching
// Replace's own endpoint-conflict-check query shape (SELECT id FROM ...).
type fakeIDRow struct {
	id  int64
	err error
}

func (r fakeIDRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.id
	return nil
}

// fakeOwnerRow implements pgx.Row over the (user_id, revoked_at) column
// pair Revoke's own lookup query shape produces.
type fakeOwnerRow struct {
	userID    int64
	revokedAt *time.Time
	err       error
}

func (r fakeOwnerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.userID
	*dest[1].(**time.Time) = r.revokedAt
	return nil
}

// fakeTx is a minimal, sequence-driven fake of pgx.Tx. Replace makes its
// QueryRow/Exec calls in a fixed, known order, so this fake just returns
// each pre-programmed response in turn from rows, and reports execErr from
// the (at most one) Exec call. Only the small subset of pgx.Tx that Replace
// actually calls (QueryRow, Exec, Commit, Rollback) has real behavior;
// every other method exists solely to satisfy the interface and panics if
// ever reached, so a test fails loudly -- not silently -- if Replace's
// control flow ever changes to need one of them.
type fakeTx struct {
	rows            []pgx.Row
	queryIndex      int
	execErr         error
	execCalled      bool
	commitErr       error
	commitErrCalled bool
	rollbackCalled  bool
}

func (tx *fakeTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	row := tx.rows[tx.queryIndex]
	tx.queryIndex++
	return row
}
func (tx *fakeTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	tx.execCalled = true
	return pgconn.CommandTag{}, tx.execErr
}
func (tx *fakeTx) Commit(context.Context) error { tx.commitErrCalled = true; return tx.commitErr }
func (tx *fakeTx) Rollback(context.Context) error {
	tx.rollbackCalled = true
	return nil
}
func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) { panic("fakeTx: Begin not supported") }
func (tx *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("fakeTx: CopyFrom not supported")
}
func (tx *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("fakeTx: SendBatch not supported")
}
func (tx *fakeTx) LargeObjects() pgx.LargeObjects { panic("fakeTx: LargeObjects not supported") }
func (tx *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("fakeTx: Prepare not supported")
}
func (tx *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("fakeTx: Query not supported")
}
func (tx *fakeTx) Conn() *pgx.Conn { panic("fakeTx: Conn not supported") }

// fakePool provides Begin for Replace's own tests, returning a
// pre-configured *fakeTx (or beginErr, to test Begin itself failing).
// QueryRow is never expected to be called directly on fakePool by Replace
// (only via the transaction it begins), so it is left unimplemented here
// beyond satisfying Querier trivially through embedding.
type fakePool struct {
	fakeQuerier
	tx       *fakeTx
	beginErr error
}

func (p *fakePool) Begin(context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
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

func TestGetUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, _, err := p.Get(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestGetRejectsInvalidID(t *testing.T) {
	for _, id := range []notifydevices.DeviceID{0, -1} {
		q := &fakeQuerier{}
		p := &Postgres{DB: q}
		if _, _, err := p.Get(context.Background(), id); err != ErrInput {
			t.Fatalf("expected ErrInput for id %v, got %v", id, err)
		}
		if q.sql != "" {
			t.Fatal("an invalid id must never reach the database")
		}
	}
}

func TestGetActiveRegistrationRoundTrips(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{
		id: 7, userID: 3, endpoint: "https://push.example.com/send/abc123",
		p256dh: rawP256dh(), auth: rawAuth(), platform: strPtr("ios-safari"),
		createdAt: now, updatedAt: now,
	}}
	p := &Postgres{DB: q}
	reg, ok, err := p.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for an existing row")
	}
	if !strings.Contains(q.sql, "FROM notification_devices WHERE id = $1") {
		t.Fatalf("expected a plain id lookup, got %q", q.sql)
	}
	if reg.ID != 7 || reg.UserID != 3 || reg.Platform != "ios-safari" {
		t.Fatalf("unexpected fields: %+v", reg)
	}
	if reg.Subscription.Keys.P256dh != validP256dh() || reg.Subscription.Keys.Auth != validAuth() {
		t.Fatalf("expected keys re-encoded to canonical base64url: %+v", reg.Subscription.Keys)
	}
	if !reg.Active() {
		t.Fatal("expected an active registration (revoked_at NULL)")
	}
	if reg.SupersededBy != 0 {
		t.Fatalf("expected no supersession, got %v", reg.SupersededBy)
	}
}

func TestGetDoesNotHideRevokedRegistration(t *testing.T) {
	now := time.Now()
	revokedAt := now.Add(time.Hour)
	supersededBy := int64(42)
	q := &fakeQuerier{row: fakeRow{
		id: 7, userID: 3, endpoint: "https://push.example.com/send/abc123",
		p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: revokedAt,
		revokedAt: &revokedAt, supersededBy: &supersededBy,
	}}
	p := &Postgres{DB: q}
	reg, ok, err := p.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true: Get must return a revoked row, never hide it as not-found")
	}
	if reg.Active() {
		t.Fatal("expected the returned registration to reflect its revoked state")
	}
	if reg.RevokedAt == nil || !reg.RevokedAt.Equal(revokedAt) {
		t.Fatalf("expected RevokedAt to round-trip, got %v", reg.RevokedAt)
	}
	if reg.SupersededBy != 42 {
		t.Fatalf("expected SupersededBy to round-trip, got %v", reg.SupersededBy)
	}
}

func TestGetNonexistentIDReturnsNotFoundWithoutError(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
	p := &Postgres{DB: q}
	reg, ok, err := p.Get(context.Background(), 999)
	if err != nil {
		t.Fatalf("expected a nil error for a not-found lookup, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a nonexistent id")
	}
	if reg != (notifydevices.Registration{}) {
		t.Fatalf("expected a zero-value Registration on not-found, got %+v", reg)
	}
}

func TestGetDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, ok, err := p.Get(context.Background(), 1); err != ErrUnavailable || ok {
		t.Fatalf("expected (false, ErrUnavailable), got (%v, %v)", ok, err)
	}
}

func TestGetRejectsCorruptStoredKeyMaterial(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name         string
		p256dh, auth []byte
	}{
		{"short p256dh", rawP256dh()[:64], rawAuth()},
		{"long p256dh", append(rawP256dh(), 0x00), rawAuth()},
		{"short auth", rawP256dh(), rawAuth()[:15]},
		{"long auth", rawP256dh(), append(rawAuth(), 0x00)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{row: fakeRow{
				id: 1, userID: 1, endpoint: "https://push.example.com/send/abc123",
				p256dh: c.p256dh, auth: c.auth, createdAt: now, updatedAt: now,
			}}
			p := &Postgres{DB: q}
			reg, ok, err := p.Get(context.Background(), 1)
			if err != ErrUnavailable || ok {
				t.Fatalf("expected corrupt stored key material to fail closed as (false, ErrUnavailable), got (%v, %v, %+v)", ok, err, reg)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

// Replace unit tests. Every pre-Begin validation failure uses the plain
// fakeQuerier (proving Begin is never even attempted); every transactional
// scenario uses fakePool/fakeTx, whose QueryRow calls are consumed in the
// exact fixed order Replace's own doc comment describes. The deeper,
// real-schema guarantees these mocks can't prove on their own (the actual
// partial unique index, actual FK/CHECK enforcement, actual physical
// rollback of a failed multi-statement transaction) are covered by
// TestLiveNotificationDeviceReplaceRollsBack instead, exactly mirroring how
// identitystore's own equally-shaped transactional methods (EnrollMFACredential,
// RevokeAllSessionsForUser) have no fake-based unit tests at all, only live
// integration coverage -- this package goes further by unit-testing the
// mockable control-flow layer, but still leans on the live test for the
// database-native guarantees a mock cannot honestly stand in for.

func TestReplaceUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.Replace(context.Background(), time.Now(), 1, 1, validSubscription(), ""); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestReplaceRejectsInvalidInputBeforeBegin(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		userID   identity.UserID
		oldID    notifydevices.DeviceID
		sub      notifydevices.Subscription
		platform string
		now      time.Time
	}{
		{"zero user id", 0, 1, validSubscription(), "", now},
		{"negative user id", -1, 1, validSubscription(), "", now},
		{"zero old id", 1, 0, validSubscription(), "", now},
		{"negative old id", 1, -1, validSubscription(), "", now},
		{"zero time", 1, 1, validSubscription(), "", time.Time{}},
		{"invalid subscription", 1, 1, notifydevices.Subscription{}, "", now},
		{"invalid platform", 1, 1, validSubscription(), "bad<platform>", now},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Replace(context.Background(), c.now, c.userID, c.oldID, c.sub, c.platform); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid input must never reach Begin/the database")
			}
		})
	}
}

func TestReplaceBeginFailureFailsClosed(t *testing.T) {
	pool := &fakePool{beginErr: errors.New("connection refused")}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), time.Now(), 1, 1, validSubscription(), ""); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestReplaceNonexistentOrAlreadyRevokedOldIDReturnsNotFound(t *testing.T) {
	// The lookup query itself filters WHERE revoked_at IS NULL, so a
	// nonexistent id and an already-revoked/superseded id are structurally
	// indistinguishable at the database level -- both simply produce zero
	// rows, exactly matching notifydevices.Store.Replace's own collapsing.
	tx := &fakeTx{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), time.Now(), 1, 999, validSubscription(), ""); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if !tx.rollbackCalled {
		t.Fatal("expected the transaction to be rolled back")
	}
	if tx.commitErrCalled {
		t.Fatal("expected Commit to never be called on a not-found old id")
	}
}

func TestReplaceRejectsWrongOwner(t *testing.T) {
	now := time.Now()
	tx := &fakeTx{rows: []pgx.Row{fakeRow{
		id: 5, userID: 2 /* different from caller */, endpoint: "https://push.example.com/send/other",
		p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now,
	}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), now, 1, 5, validSubscription(), ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if !tx.rollbackCalled || tx.commitErrCalled || tx.execCalled {
		t.Fatal("expected no mutation and a rollback for a wrong-owner rejection")
	}
}

func TestReplaceSameEndpointIsIdempotent(t *testing.T) {
	now := time.Now()
	sub := validSubscription()
	tx := &fakeTx{rows: []pgx.Row{
		fakeRow{id: 5, userID: 1, endpoint: sub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
		fakeRow{id: 5, userID: 1, endpoint: sub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), platform: strPtr("ios-safari"), createdAt: now, updatedAt: now.Add(time.Minute)},
	}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	reg, err := p.Replace(context.Background(), now.Add(time.Minute), 1, 5, sub, "ios-safari")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.ID != 5 {
		t.Fatalf("expected the same DeviceID for a same-endpoint replace, got %v", reg.ID)
	}
	if reg.Platform != "ios-safari" {
		t.Fatalf("expected the platform hint to refresh, got %q", reg.Platform)
	}
	if tx.execCalled {
		t.Fatal("expected no Exec call for the idempotent same-endpoint path (no old row to separately revoke)")
	}
	if !tx.commitErrCalled {
		t.Fatal("expected Commit to be called")
	}
}

func TestReplaceEndpointAlreadyActiveIsForbidden(t *testing.T) {
	// Deliberately does not distinguish "owned by a different user" from
	// "owned by another of the caller's own devices": Replace's own doc
	// comment requires rejecting both identically, and the conflict-check
	// query never even reads the conflicting row's owner, so a single test
	// shape covers both real-world scenarios.
	now := time.Now()
	oldSub := notifydevices.Subscription{Endpoint: "https://push.example.com/send/old", Keys: notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()}}
	newSub := validSubscription() // a different endpoint than oldSub
	tx := &fakeTx{rows: []pgx.Row{
		fakeRow{id: 5, userID: 1, endpoint: oldSub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
		fakeIDRow{id: 9}, // some other active row already holds newSub.Endpoint
	}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), now, 1, 5, newSub, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if !tx.rollbackCalled || tx.commitErrCalled || tx.execCalled {
		t.Fatal("expected no mutation and a rollback for an already-actively-held endpoint")
	}
}

func TestReplaceSuccessfulReplacementCreatesFreshRowAndSupersedesOld(t *testing.T) {
	now := time.Now()
	oldSub := notifydevices.Subscription{Endpoint: "https://push.example.com/send/old", Keys: notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()}}
	newSub := validSubscription()
	tx := &fakeTx{rows: []pgx.Row{
		fakeRow{id: 5, userID: 1, endpoint: oldSub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
		fakeIDRow{err: pgx.ErrNoRows}, // no conflicting active row at the new endpoint
		fakeRow{id: 6, userID: 1, endpoint: newSub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
	}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	fresh, err := p.Replace(context.Background(), now, 1, 5, newSub, "android-chrome")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fresh.ID != 6 {
		t.Fatalf("expected a new DeviceID distinct from the old one, got %v", fresh.ID)
	}
	if fresh.Subscription.Endpoint != newSub.Endpoint {
		t.Fatalf("expected the fresh row to carry the new endpoint, got %q", fresh.Subscription.Endpoint)
	}
	if !tx.execCalled {
		t.Fatal("expected the old row to be revoked/superseded via Exec")
	}
	if !tx.commitErrCalled {
		t.Fatal("expected Commit to be called")
	}
}

func TestReplaceNoPartialMutationWhenFinalExecFails(t *testing.T) {
	now := time.Now()
	oldSub := notifydevices.Subscription{Endpoint: "https://push.example.com/send/old", Keys: notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()}}
	newSub := validSubscription()
	tx := &fakeTx{
		rows: []pgx.Row{
			fakeRow{id: 5, userID: 1, endpoint: oldSub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
			fakeIDRow{err: pgx.ErrNoRows},
			fakeRow{id: 6, userID: 1, endpoint: newSub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
		},
		execErr: errors.New("connection reset while revoking the old row"),
	}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), now, 1, 5, newSub, ""); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if tx.commitErrCalled {
		t.Fatal("expected Commit to never be called when the old-row revoke Exec fails: no partial mutation may be visible")
	}
	if !tx.rollbackCalled {
		t.Fatal("expected the transaction to be rolled back")
	}
}

func TestReplaceCommitFailureFailsClosed(t *testing.T) {
	now := time.Now()
	sub := validSubscription()
	tx := &fakeTx{
		rows: []pgx.Row{
			fakeRow{id: 5, userID: 1, endpoint: sub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
			fakeRow{id: 5, userID: 1, endpoint: sub.Endpoint, p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now},
		},
		commitErr: errors.New("commit failed"),
	}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if _, err := p.Replace(context.Background(), now, 1, 5, sub, ""); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

// Revoke unit tests. Same fakeTx/fakePool infrastructure as Replace's own
// tests: real behavior for QueryRow/Exec/Commit/Rollback, everything else
// panics if ever reached. The deeper, real-schema guarantees these mocks
// can't prove (true row locking, real concurrent-revoke serialization) are
// covered by TestLiveNotificationDeviceRevokeRollsBack and the temporary
// concurrency probe instead.

func TestRevokeUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if err := p.Revoke(context.Background(), time.Now(), 1, 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRevokeRejectsInvalidInputBeforeBegin(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		userID identity.UserID
		id     notifydevices.DeviceID
		now    time.Time
	}{
		{"zero user id", 0, 1, now},
		{"negative user id", -1, 1, now},
		{"zero device id", 1, 0, now},
		{"negative device id", 1, -1, now},
		{"zero time", 1, 1, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if err := p.Revoke(context.Background(), c.now, c.userID, c.id); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid input must never reach Begin/the database")
			}
		})
	}
}

func TestRevokeBeginFailureFailsClosed(t *testing.T) {
	pool := &fakePool{beginErr: errors.New("connection refused")}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRevokeNonexistentIDReturnsNotFound(t *testing.T) {
	tx := &fakeTx{rows: []pgx.Row{fakeOwnerRow{err: pgx.ErrNoRows}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 999); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if !tx.rollbackCalled || tx.commitErrCalled || tx.execCalled {
		t.Fatal("expected no mutation and a rollback for a nonexistent id")
	}
}

func TestRevokeRejectsWrongOwner(t *testing.T) {
	tx := &fakeTx{rows: []pgx.Row{fakeOwnerRow{userID: 2 /* different from caller */}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 5); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if !tx.rollbackCalled || tx.commitErrCalled || tx.execCalled {
		t.Fatal("expected no mutation and a rollback for a wrong-owner revoke attempt")
	}
}

func TestRevokeAlreadyRevokedIsIdempotent(t *testing.T) {
	existingRevokedAt := time.Now().Add(-time.Hour)
	tx := &fakeTx{rows: []pgx.Row{fakeOwnerRow{userID: 1, revokedAt: &existingRevokedAt}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 5); err != nil {
		t.Fatalf("expected nil (idempotent success), got %v", err)
	}
	if tx.execCalled {
		t.Fatal("expected no Exec call for an already-revoked id: revoked_at must never be overwritten")
	}
	if tx.commitErrCalled {
		t.Fatal("expected no Commit call for a no-op idempotent revoke")
	}
	if !tx.rollbackCalled {
		t.Fatal("expected the read-only transaction to be rolled back")
	}
}

func TestRevokeActiveRegistrationSucceeds(t *testing.T) {
	tx := &fakeTx{rows: []pgx.Row{fakeOwnerRow{userID: 1, revokedAt: nil}}}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	now := time.Now()
	if err := p.Revoke(context.Background(), now, 1, 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tx.execCalled {
		t.Fatal("expected Exec to be called to set revoked_at")
	}
	if !tx.commitErrCalled {
		t.Fatal("expected Commit to be called")
	}
}

func TestRevokeExecFailureFailsClosed(t *testing.T) {
	tx := &fakeTx{
		rows:    []pgx.Row{fakeOwnerRow{userID: 1, revokedAt: nil}},
		execErr: errors.New("connection reset"),
	}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 5); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if tx.commitErrCalled {
		t.Fatal("expected Commit to never be called when the revoke Exec fails")
	}
	if !tx.rollbackCalled {
		t.Fatal("expected the transaction to be rolled back")
	}
}

func TestRevokeCommitFailureFailsClosed(t *testing.T) {
	tx := &fakeTx{
		rows:      []pgx.Row{fakeOwnerRow{userID: 1, revokedAt: nil}},
		commitErr: errors.New("commit failed"),
	}
	pool := &fakePool{tx: tx}
	p := &Postgres{DB: pool}
	if err := p.Revoke(context.Background(), time.Now(), 1, 5); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

// ListActive unit tests.

func TestListActiveUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.ListActive(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestListActiveRejectsInvalidUserIDBeforeQuerying(t *testing.T) {
	for _, id := range []identity.UserID{0, -1} {
		q := &fakeQuerier{}
		p := &Postgres{DB: q}
		if _, err := p.ListActive(context.Background(), id); err != ErrInput {
			t.Fatalf("expected ErrInput for user id %v, got %v", id, err)
		}
		if q.sql != "" {
			t.Fatal("an invalid user id must never reach the database")
		}
	}
}

func TestListActiveQueriesOnlyByUserIDAndActiveAndOrdersDeterministically(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}
	p := &Postgres{DB: q}
	if _, err := p.ListActive(context.Background(), 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.sql, "WHERE user_id = $1 AND revoked_at IS NULL") {
		t.Fatalf("expected the query to be scoped to user_id and active rows only, got %q", q.sql)
	}
	if !strings.Contains(q.sql, "ORDER BY created_at DESC, id DESC") {
		t.Fatalf("expected deterministic newest-first ordering, got %q", q.sql)
	}
	if len(q.args) != 1 || q.args[0] != int64(7) {
		t.Fatalf("expected exactly one bound user_id argument (7), got %v", q.args)
	}
}

func TestListActiveEmptyResultReturnsEmptySliceNilError(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}
	p := &Postgres{DB: q}
	regs, err := p.ListActive(context.Background(), 1)
	if err != nil {
		t.Fatalf("expected a nil error, got %v", err)
	}
	if regs == nil {
		t.Fatal("expected a non-nil, empty slice, not a nil slice")
	}
	if len(regs) != 0 {
		t.Fatalf("expected zero results, got %d", len(regs))
	}
}

func TestListActiveReturnsMultipleRegistrationsPreservingFieldsAndOrder(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)
	// fakeRows returns rows in the exact order given; this test does not
	// re-verify the database's own ORDER BY correctness (that belongs to
	// the live integration test), only that ListActive preserves whatever
	// order its Query call is given, rather than reordering results itself.
	newest := fakeRow{id: 2, userID: 1, endpoint: "https://push.example.com/send/newest",
		p256dh: rawP256dh(), auth: rawAuth(), platform: strPtr("ios-safari"), createdAt: later, updatedAt: later}
	oldest := fakeRow{id: 1, userID: 1, endpoint: "https://push.example.com/send/oldest",
		p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now}
	q := &fakeQuerier{rows: &fakeRows{remaining: []pgx.Row{newest, oldest}}}
	p := &Postgres{DB: q}
	regs, err := p.ListActive(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("expected 2 results, got %d", len(regs))
	}
	if regs[0].ID != 2 || regs[1].ID != 1 {
		t.Fatalf("expected the order given by Query to be preserved (newest first), got ids %v, %v", regs[0].ID, regs[1].ID)
	}
	if regs[0].Platform != "ios-safari" || regs[1].Platform != "" {
		t.Fatalf("expected platform hints to round-trip per row, got %+v", regs)
	}
	for _, r := range regs {
		if r.Subscription.Keys.P256dh != validP256dh() || r.Subscription.Keys.Auth != validAuth() {
			t.Fatalf("expected canonical base64url keys to round-trip, got %+v", r.Subscription.Keys)
		}
		if r.UserID != 1 || !r.Active() {
			t.Fatalf("expected every row to belong to user 1 and be active, got %+v", r)
		}
	}
}

func TestListActiveQueryErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{queryErr: errors.New("connection reset")}
	p := &Postgres{DB: q}
	if _, err := p.ListActive(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestListActiveRowsErrFailsClosed(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{err: errors.New("connection reset mid-stream")}}
	p := &Postgres{DB: q}
	if _, err := p.ListActive(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestListActiveRejectsCorruptStoredKeyMaterial(t *testing.T) {
	now := time.Now()
	good := fakeRow{id: 1, userID: 1, endpoint: "https://push.example.com/send/a", p256dh: rawP256dh(), auth: rawAuth(), createdAt: now, updatedAt: now}
	corrupt := fakeRow{id: 2, userID: 1, endpoint: "https://push.example.com/send/b", p256dh: rawP256dh()[:64] /* wrong length */, auth: rawAuth(), createdAt: now, updatedAt: now}
	q := &fakeQuerier{rows: &fakeRows{remaining: []pgx.Row{good, corrupt}}}
	p := &Postgres{DB: q}
	regs, err := p.ListActive(context.Background(), 1)
	if err != ErrUnavailable {
		t.Fatalf("expected corrupt stored key material to fail the whole call closed as ErrUnavailable, got err=%v regs=%+v", err, regs)
	}
	if regs != nil {
		t.Fatalf("expected no partial result on failure, got %+v", regs)
	}
}
