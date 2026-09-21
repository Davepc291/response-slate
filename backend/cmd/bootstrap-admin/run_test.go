package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRow is a minimal pgx.Row fake: it satisfies pgx.Row's single-method
// interface (Scan(dest ...any) error) without any network or database
// dependency.
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
		case *int:
			*v = r.values[i].(int)
		case *int64:
			*v = r.values[i].(int64)
		case *string:
			*v = r.values[i].(string)
		default:
			return errors.New("fakeRow: unsupported scan destination type")
		}
	}
	return nil
}

// fakeConn is an in-memory dbConn fake standing in for a real PostgreSQL
// connection, so Run's validation and control-flow logic can be exercised
// without starting Docker or PostgreSQL (forbidden for this task).
type fakeConn struct {
	userCount int
	countErr  error
	insertErr error
	nextID    int64

	insertCalled       bool
	insertedEmail      string
	insertedDisplay    string
	insertedHash       string
	insertedPassedArgN int
}

func (f *fakeConn) QueryRow(_ context.Context, sqlText string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sqlText, "count(*)"):
		if f.countErr != nil {
			return fakeRow{err: f.countErr}
		}
		return fakeRow{values: []any{f.userCount}}
	case strings.Contains(sqlText, "INSERT INTO users"):
		f.insertCalled = true
		f.insertedPassedArgN = len(args)
		if len(args) >= 3 {
			f.insertedEmail, _ = args[0].(string)
			f.insertedDisplay, _ = args[1].(string)
			f.insertedHash, _ = args[2].(string)
		}
		if f.insertErr != nil {
			return fakeRow{err: f.insertErr}
		}
		return fakeRow{values: []any{f.nextID}}
	default:
		return fakeRow{err: errors.New("fakeConn: unexpected query")}
	}
}

// spyConnect wraps a Connector and records whether it was ever invoked, so
// tests can assert that a precondition failure short-circuits before any
// database connection is attempted.
type spyConnect struct {
	called bool
	conn   dbConn
	err    error
}

func (s *spyConnect) connect(_ context.Context, _ string) (dbConn, func(), error) {
	s.called = true
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.conn, func() {}, nil
}

// scriptedPasswords returns a PasswordReader that yields each entry of
// values in order, one per call, and records how many times it was called.
type scriptedPasswords struct {
	values []string
	calls  int
}

func (s *scriptedPasswords) read(string) (string, error) {
	if s.calls >= len(s.values) {
		return "", errors.New("scriptedPasswords: no more scripted values")
	}
	v := s.values[s.calls]
	s.calls++
	return v, nil
}

const validPassword = "correct horse battery staple local test"

func baseConfig() Config {
	return Config{
		DatabaseURL: "postgres://gfr_dev:localpw@127.0.0.1:5432/greenwich_fire_responder_dev?sslmode=disable",
		Email:       "Bootstrap.Admin@Example.TEST",
		DisplayName: "Bootstrap Admin",
		Confirmed:   true,
	}
}

func TestRun_RejectsWithoutConfirmation(t *testing.T) {
	cfg := baseConfig()
	cfg.Confirmed = false
	spy := &spyConnect{}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected ErrNotConfirmed, got %v", err)
	}
	if spy.called {
		t.Error("Connect must not be called when -confirm is missing")
	}
	if pw.calls != 0 {
		t.Error("password must never be prompted for when -confirm is missing")
	}
}

func TestRequireLoopbackDatabase(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"empty", "", true},
		{"malformed", "://not-a-url", true},
		{"wrong scheme", "mysql://user:pw@127.0.0.1:3306/db", true},
		{"public host", "postgres://user:pw@example.com:5432/db", true},
		{"public ip", "postgres://user:pw@10.0.0.5:5432/db", true},
		{"attacker subdomain trick", "postgres://user:pw@localhost.evil.example.com:5432/db", true},
		{"loopback ipv4", "postgres://user:pw@127.0.0.1:5432/db", false},
		{"loopback hostname", "postgres://user:pw@localhost:5432/db", false},
		{"loopback ipv6", "postgres://user:pw@[::1]:5432/db", false},
		{"postgresql scheme loopback", "postgresql://user:pw@127.0.0.1:5432/db", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireLoopbackDatabase(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("requireLoopbackDatabase(%q): expected error, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("requireLoopbackDatabase(%q): unexpected error: %v", tc.url, err)
			}
		})
	}
}

func TestRun_RejectsNonLoopbackHost(t *testing.T) {
	cfg := baseConfig()
	cfg.DatabaseURL = "postgres://gfr_dev:localpw@db.example.com:5432/greenwich_fire_responder_dev"
	spy := &spyConnect{}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("expected a non-loopback rejection error, got %v", err)
	}
	if spy.called {
		t.Error("Connect must not be called for a non-loopback database host")
	}
	if pw.calls != 0 {
		t.Error("password must never be prompted for when the database host is rejected")
	}
}

func TestRun_RejectsWhenUsersTableNotEmpty(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 3}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if !errors.Is(err, ErrUsersNotEmpty) {
		t.Fatalf("expected ErrUsersNotEmpty, got %v", err)
	}
	if !spy.called {
		t.Error("Connect should be called before the users-table check can run")
	}
	if pw.calls != 0 {
		t.Error("password must never be prompted for when the users table is not empty")
	}
	if fc.insertCalled {
		t.Error("no insert may happen when the users table is not empty")
	}
}

func TestRun_RejectsPasswordMismatch(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 0, nextID: 1}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword + "x"}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("expected ErrPasswordMismatch, got %v", err)
	}
	if fc.insertCalled {
		t.Error("no insert may happen when password confirmation does not match")
	}
}

func TestRun_RejectsPasswordBelowPolicyMinimum(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 0, nextID: 1}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{"short1234", "short1234"}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if err == nil {
		t.Fatal("expected an error for a below-minimum-length password")
	}
	if fc.insertCalled {
		t.Error("no insert may happen when the password fails policy validation")
	}
}

func TestRun_RejectsInvalidEmail(t *testing.T) {
	cfg := baseConfig()
	cfg.Email = "not-an-email"
	spy := &spyConnect{}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if err == nil {
		t.Fatal("expected an error for an invalid email address")
	}
	if spy.called {
		t.Error("Connect must not be called for an invalid email address")
	}
}

func TestRun_RejectsBlankDisplayName(t *testing.T) {
	cfg := baseConfig()
	cfg.DisplayName = "   "
	spy := &spyConnect{}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if err == nil {
		t.Fatal("expected an error for a blank display name")
	}
	if spy.called {
		t.Error("Connect must not be called for a blank display name")
	}
}

func TestRun_Success(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 0, nextID: 42}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fc.insertCalled {
		t.Fatal("expected the administrator row to be inserted")
	}
	if fc.insertedEmail != "bootstrap.admin@example.test" {
		t.Errorf("email not normalized: got %q", fc.insertedEmail)
	}
	if fc.insertedDisplay != cfg.DisplayName {
		t.Errorf("display name mismatch: got %q", fc.insertedDisplay)
	}
	if fc.insertedHash == "" || fc.insertedHash == validPassword {
		t.Error("inserted value must be a hash, never the plaintext password")
	}
	if !strings.HasPrefix(fc.insertedHash, "$argon2id$") {
		t.Errorf("expected a PHC-encoded argon2id hash, got %q", fc.insertedHash)
	}
	if pw.calls != 2 {
		t.Errorf("expected the password to be prompted for exactly twice, got %d", pw.calls)
	}

	successMsg := out.String()
	if !strings.Contains(successMsg, "Bootstrap complete") {
		t.Errorf("expected a success message, got %q", successMsg)
	}
	if !strings.Contains(successMsg, "system_administrator") {
		t.Errorf("expected the success message to name the role, got %q", successMsg)
	}
}

func TestRun_NeverLeaksSecretsToOutput(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 0, nextID: 7}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	if err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	combined := out.String() + errOut.String()
	if strings.Contains(combined, validPassword) {
		t.Error("password must never appear in any command output")
	}
	if strings.Contains(combined, "$argon2id$") {
		t.Error("the password hash must never appear in any command output")
	}
	if strings.Contains(combined, "localpw") {
		t.Error("the database credential embedded in GFR_DATABASE_URL must never appear in output")
	}
}

func TestRun_NeverLeaksSecretsOnFailure(t *testing.T) {
	cfg := baseConfig()
	fc := &fakeConn{userCount: 0, insertErr: errors.New("insert failed")}
	spy := &spyConnect{conn: fc}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})
	if err == nil {
		t.Fatal("expected an error from a failed insert")
	}
	if strings.Contains(err.Error(), validPassword) {
		t.Error("an insert-failure error must never contain the plaintext password")
	}
	if strings.Contains(err.Error(), "localpw") {
		t.Error("an insert-failure error must never contain the database credential")
	}
}

func TestRun_ConnectFailureNeverLeaksCredential(t *testing.T) {
	cfg := baseConfig()
	spy := &spyConnect{err: errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")}
	pw := &scriptedPasswords{values: []string{validPassword, validPassword}}
	var out, errOut bytes.Buffer

	err := Run(context.Background(), cfg, Deps{
		Stdout: &out, Stderr: &errOut,
		ReadPassword: pw.read, Connect: spy.connect,
	})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "localpw") {
		t.Error("a connection-failure error must never contain the database credential")
	}
	if pw.calls != 0 {
		t.Error("password must never be prompted for when the database connection fails")
	}
}
