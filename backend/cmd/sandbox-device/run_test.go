package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func validP256dh() string {
	b := make([]byte, 65)
	b[0] = 0x04
	for i := 1; i < len(b); i++ {
		b[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validAuth() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(i + 100)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validSubscriptionJSON() string {
	return `{"endpoint":"https://push.example.com/subscription/abc123","keys":{"p256dh":"` + validP256dh() + `","auth":"` + validAuth() + `"}}`
}

func validConfig() Config {
	return Config{DatabaseURL: "postgres://user@127.0.0.1:5432/db", UserID: 1, Confirmed: true}
}

// fakeRow is a minimal pgx.Row fake, mirroring bootstrap-admin's own
// identical helper.
type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("fakeRow: dest/value count mismatch")
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = r.values[i].(bool)
		case *int64:
			*v = r.values[i].(int64)
		default:
			return errors.New("fakeRow: unsupported scan destination type")
		}
	}
	return nil
}

type fakeConn struct {
	userExistsResult bool
	userExistsErr    error // if set, returned as the QueryRow error (e.g. pgx.ErrNoRows)

	insertErr   error
	nextID      int64
	insertCalls []insertCall
}

type insertCall struct {
	userID       int64
	endpoint     string
	testModeTrue bool // true iff the literal SQL text contains "true" for test_mode
}

func (f *fakeConn) QueryRow(_ context.Context, sqlText string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sqlText, "SELECT true FROM users"):
		if f.userExistsErr != nil {
			return fakeRow{err: f.userExistsErr}
		}
		return fakeRow{values: []any{f.userExistsResult}}
	case strings.Contains(sqlText, "INSERT INTO notification_devices"):
		userID, _ := args[0].(int64)
		endpoint, _ := args[1].(string)
		f.insertCalls = append(f.insertCalls, insertCall{
			userID:       userID,
			endpoint:     endpoint,
			testModeTrue: strings.Contains(sqlText, "true"),
		})
		if f.insertErr != nil {
			return fakeRow{err: f.insertErr}
		}
		return fakeRow{values: []any{f.nextID}}
	default:
		return fakeRow{err: errors.New("fakeConn: unexpected query")}
	}
}

func connectorFor(conn dbConn, connErr error) Connector {
	return func(context.Context, string) (dbConn, func(), error) {
		if connErr != nil {
			return nil, nil, connErr
		}
		return conn, func() {}, nil
	}
}

func depsFor(stdin string, conn dbConn, connErr error) (Deps, *bytes.Buffer) {
	var out bytes.Buffer
	return Deps{
		Stdin:    strings.NewReader(stdin),
		Stdout:   &out,
		Connect:  connectorFor(conn, connErr),
		OpenFile: func(string) ([]byte, error) { return nil, errors.New("OpenFile should not be called") },
	}, &out
}

func TestRunImportsFromStdin(t *testing.T) {
	conn := &fakeConn{userExistsResult: true, nextID: 42}
	deps, out := depsFor(validSubscriptionJSON(), conn, nil)

	if err := Run(context.Background(), validConfig(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "DeviceID:  42") {
		t.Fatalf("expected device id 42 in output, got %q", out.String())
	}
	if len(conn.insertCalls) != 1 {
		t.Fatalf("expected exactly one insert, got %d", len(conn.insertCalls))
	}
	if !conn.insertCalls[0].testModeTrue {
		t.Fatal("expected the insert statement to hardcode test_mode = true")
	}
}

func TestRunImportsFromFile(t *testing.T) {
	conn := &fakeConn{userExistsResult: true, nextID: 7}
	var out bytes.Buffer
	deps := Deps{
		Stdin:   strings.NewReader(""),
		Stdout:  &out,
		Connect: connectorFor(conn, nil),
		OpenFile: func(path string) ([]byte, error) {
			if path != "subscription.json" {
				t.Fatalf("unexpected path %q", path)
			}
			return []byte(validSubscriptionJSON()), nil
		},
	}

	cfg := validConfig()
	cfg.File = "subscription.json"

	if err := Run(context.Background(), cfg, deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "DeviceID:  7") {
		t.Fatalf("expected device id 7 in output, got %q", out.String())
	}
	if !strings.Contains(out.String(), "delete the subscription file") {
		t.Fatalf("expected a delete-the-file reminder, got %q", out.String())
	}
}

func TestRunRejectsMalformedJSON(t *testing.T) {
	conn := &fakeConn{}
	deps, _ := depsFor("not json at all", conn, nil)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrMalformedSubscriptionJSON) {
		t.Fatalf("expected %v, got %v", ErrMalformedSubscriptionJSON, err)
	}
	if len(conn.insertCalls) != 0 {
		t.Fatal("expected no insert attempt for malformed JSON")
	}
}

func TestRunRejectsInvalidSubscription(t *testing.T) {
	conn := &fakeConn{}
	deps, _ := depsFor(`{"endpoint":"http://not-https.example.com","keys":{"p256dh":"x","auth":"y"}}`, conn, nil)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrInvalidSubscription) {
		t.Fatalf("expected %v, got %v", ErrInvalidSubscription, err)
	}
	if len(conn.insertCalls) != 0 {
		t.Fatal("expected no insert attempt for an invalid subscription")
	}
}

func TestRunRejectsMissingUser(t *testing.T) {
	conn := &fakeConn{userExistsErr: pgx.ErrNoRows}
	deps, _ := depsFor(validSubscriptionJSON(), conn, nil)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected %v, got %v", ErrUserNotFound, err)
	}
	if len(conn.insertCalls) != 0 {
		t.Fatal("expected no insert attempt for a missing user")
	}
}

func TestRunRejectsDuplicateActiveEndpoint(t *testing.T) {
	conn := &fakeConn{userExistsResult: true, insertErr: &pgconn.PgError{Code: "23505"}}
	deps, _ := depsFor(validSubscriptionJSON(), conn, nil)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrDuplicateActiveEndpoint) {
		t.Fatalf("expected %v, got %v", ErrDuplicateActiveEndpoint, err)
	}
}

func TestRunRequiresConfirmation(t *testing.T) {
	conn := &fakeConn{}
	deps, _ := depsFor(validSubscriptionJSON(), conn, nil)

	cfg := validConfig()
	cfg.Confirmed = false

	err := Run(context.Background(), cfg, deps)
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected %v, got %v", ErrNotConfirmed, err)
	}
	if len(conn.insertCalls) != 0 {
		t.Fatal("expected no insert attempt without confirmation")
	}
}

func TestRunRejectsInvalidUserID(t *testing.T) {
	conn := &fakeConn{}
	deps, _ := depsFor(validSubscriptionJSON(), conn, nil)

	cfg := validConfig()
	cfg.UserID = 0

	err := Run(context.Background(), cfg, deps)
	if !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("expected %v, got %v", ErrInvalidUserID, err)
	}
}

func TestRunRejectsNonLoopbackDatabase(t *testing.T) {
	conn := &fakeConn{}
	deps, _ := depsFor(validSubscriptionJSON(), conn, nil)

	cfg := validConfig()
	cfg.DatabaseURL = "postgres://user@evil.example.com:5432/db"

	err := Run(context.Background(), cfg, deps)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected a loopback-rejection error, got %v", err)
	}
}

// TestRunNeverLeaksEndpointOrKeysInOutputOrErrors proves that neither a
// successful run's stdout nor any failure's error text ever contains the
// subscription endpoint or key material this tool imported.
func TestRunNeverLeaksEndpointOrKeysInOutputOrErrors(t *testing.T) {
	endpoint := "https://push.example.com/subscription/super-secret-endpoint-id"
	p256dh := validP256dh()
	auth := validAuth()
	subscription := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"` + p256dh + `","auth":"` + auth + `"}}`

	conn := &fakeConn{userExistsResult: true, nextID: 1}
	deps, out := depsFor(subscription, conn, nil)

	if err := Run(context.Background(), validConfig(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{endpoint, p256dh, auth} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("output must never contain %q:\n%s", secret, out.String())
		}
	}

	// And on a failure path (duplicate endpoint), the error text must also
	// never contain the secret-shaped values.
	failConn := &fakeConn{userExistsResult: true, insertErr: &pgconn.PgError{Code: "23505"}}
	failDeps, _ := depsFor(subscription, failConn, nil)
	err := Run(context.Background(), validConfig(), failDeps)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range []string{endpoint, p256dh, auth} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error must never contain %q, got %q", secret, err.Error())
		}
	}
}
