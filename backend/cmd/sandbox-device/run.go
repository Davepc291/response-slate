// Command sandbox-device is a standalone, local-development-only tool
// whose only job is importing exactly one already-created browser
// PushSubscription as a single test_mode=true notification_devices row, for
// Step 8D-B Part 13's controlled sandbox push path. It never sends
// anything (it does not import notifyrelay or notifywebpush at all), never
// creates a user, and has no option capable of creating a non-test
// registration: test_mode is hardcoded true in its one INSERT statement,
// never a flag.
//
// This tool is never imported by, registered with, or reachable from
// backend/cmd/api. It is a one-time, out-of-band local provisioning step
// for the sandbox transport test, not a production registration mechanism
// -- that remains the future, separately authorized HTTP route this
// amendment has never built.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/notifydevices"
)

// maxSubscriptionJSONBytes bounds how much this tool will ever read from
// stdin or a file: a resource-safety ceiling against a pathological input,
// not a claim about any real PushSubscription JSON's actual size (which is
// always a few hundred bytes).
const maxSubscriptionJSONBytes = 64 << 10

// Config is the parsed, validated input for one sandbox-device run.
// Endpoint/p256dh/auth are deliberately never fields here: the subscription
// itself is always read from Deps.Stdin or an explicit file (see Run), never
// from a command-line flag, so it never lands in shell history.
type Config struct {
	DatabaseURL string
	UserID      int64
	File        string // empty means read from Deps.Stdin
	Confirmed   bool
}

// dbConn is the minimal database surface Run needs: every operation here
// (checking the user exists, inserting the device row) is one atomic
// statement.
type dbConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connector opens (and returns a cleanup func for) the database connection
// Run operates against, mirroring bootstrap-admin's own identical Connector
// shape exactly.
type Connector func(ctx context.Context, databaseURL string) (dbConn, func(), error)

// Deps carries every side-effecting dependency Run needs.
type Deps struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Connect Connector
	// OpenFile reads an explicitly supplied subscription file. Injected
	// (rather than calling os.ReadFile directly) purely so tests never
	// touch the real filesystem.
	OpenFile func(path string) ([]byte, error)
}

var (
	// ErrNotConfirmed marks a run that omitted the required -confirm flag.
	ErrNotConfirmed = errors.New("sandbox-device: refusing to run without -confirm")
	// ErrInvalidUserID marks a non-positive -user-id.
	ErrInvalidUserID = errors.New("sandbox-device: user id must be a positive integer")
	// ErrDatabaseURLEmpty marks a run with no GFR_DATABASE_URL set.
	ErrDatabaseURLEmpty = errors.New("sandbox-device: GFR_DATABASE_URL is not set")
	// ErrMalformedSubscriptionJSON marks input that is not valid JSON, or
	// does not match the standard browser PushSubscription shape
	// ({"endpoint","keys":{"p256dh","auth"}}). Never echoes the offending
	// input.
	ErrMalformedSubscriptionJSON = errors.New("sandbox-device: subscription input is not valid PushSubscription JSON")
	// ErrInvalidSubscription marks a structurally parseable subscription
	// whose values fail notifydevices.Subscription.Validate (wrong key
	// sizes, non-https endpoint, and so on).
	ErrInvalidSubscription = errors.New("sandbox-device: subscription failed validation")
	// ErrUserNotFound marks a -user-id with no matching row in users: this
	// tool never creates a user.
	ErrUserNotFound = errors.New("sandbox-device: user id does not exist")
	// ErrDuplicateActiveEndpoint marks an endpoint already actively held by
	// any registration (Step 8D-B Section 6's own active-endpoint
	// uniqueness rule, enforced at the database level by
	// notification_devices_endpoint_active_idx). This tool never upserts
	// or replaces an existing registration -- it only ever creates a fresh
	// one.
	ErrDuplicateActiveEndpoint = errors.New("sandbox-device: this endpoint is already actively registered")
	// ErrDatabaseUnavailable marks a database failure. Never wraps a raw
	// driver error: a connection string or constraint detail could
	// otherwise leak across this tool's own boundary.
	ErrDatabaseUnavailable = errors.New("sandbox-device: database unavailable")
)

// requireLoopbackDatabase duplicates bootstrap-admin's own identical check
// (deliberately, rather than importing a sibling cmd package, which this
// repository's convention never does): a fast, network-free,
// defense-in-depth rejection of every host except 127.0.0.1, localhost, and
// ::1, performed before Deps.Connect is ever called.
func requireLoopbackDatabase(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return ErrDatabaseURLEmpty
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("sandbox-device: GFR_DATABASE_URL is not a valid connection URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return errors.New("sandbox-device: GFR_DATABASE_URL must use the postgres:// or postgresql:// scheme")
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("sandbox-device: refusing non-loopback database host %q: this tool only operates against 127.0.0.1, localhost, or ::1", u.Hostname())
	}
}

// subscriptionJSON is the standard browser PushSubscription.toJSON() shape.
type subscriptionJSON struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// parseSubscription reads up to maxSubscriptionJSONBytes from r, decodes it
// as a subscriptionJSON, and validates it via notifydevices.Subscription's
// own exported rules. It never echoes the raw input or the parsed values in
// any returned error.
func parseSubscription(r io.Reader) (notifydevices.Subscription, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxSubscriptionJSONBytes+1))
	if err != nil {
		return notifydevices.Subscription{}, fmt.Errorf("sandbox-device: read subscription input: %w", err)
	}
	if len(raw) > maxSubscriptionJSONBytes {
		return notifydevices.Subscription{}, ErrMalformedSubscriptionJSON
	}

	var parsed subscriptionJSON
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return notifydevices.Subscription{}, ErrMalformedSubscriptionJSON
	}

	sub := notifydevices.Subscription{
		Endpoint: parsed.Endpoint,
		Keys:     notifydevices.Keys{P256dh: parsed.Keys.P256dh, Auth: parsed.Keys.Auth},
	}
	if err := sub.Validate(); err != nil {
		return notifydevices.Subscription{}, ErrInvalidSubscription
	}
	return sub, nil
}

// decodeSubscriptionKey duplicates notifydevicestore's own identical helper
// (itself already a deliberate, documented duplication of
// notifydevices.decodeB64URL): this tool must validate/encode before ever
// reaching SQL, and neither sibling package exports this for reuse.
func decodeSubscriptionKey(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// safeDB never returns a raw driver error, mirroring notifydevicestore's
// own identical safeDB exactly: a connection string, table name, or
// constraint detail could otherwise leak across this tool's own boundary.
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			return ErrDuplicateActiveEndpoint
		}
	}
	return ErrDatabaseUnavailable
}

// userExists reports whether id names an existing users row. It never
// returns or logs any column value, only a boolean.
func userExists(ctx context.Context, conn dbConn, id int64) (bool, error) {
	var exists bool
	err := conn.QueryRow(ctx, `SELECT true FROM users WHERE id = $1`, id).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, safeDB(err)
	}
	return exists, nil
}

// insertTestDevice performs the one atomic INSERT ... RETURNING statement
// that creates this tool's one device row. test_mode is a literal `true` in
// the statement text itself -- there is no parameter, column, or flag
// anywhere in this tool capable of setting it to false, so a non-test
// registration is structurally impossible to create through this path.
// Active-endpoint uniqueness (notification_devices_endpoint_active_idx) and
// the users foreign key are both left to the database's own constraints;
// this function only classifies the resulting error (see safeDB).
func insertTestDevice(ctx context.Context, conn dbConn, userID int64, sub notifydevices.Subscription) (int64, error) {
	p256dh, err := decodeSubscriptionKey(sub.Keys.P256dh)
	if err != nil {
		return 0, ErrInvalidSubscription
	}
	auth, err := decodeSubscriptionKey(sub.Keys.Auth)
	if err != nil {
		return 0, ErrInvalidSubscription
	}

	var id int64
	err = conn.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, test_mode, created_at, updated_at)
        VALUES ($1, $2, $3, $4, true, now(), now())
        RETURNING id`,
		userID, sub.Endpoint, p256dh, auth).Scan(&id)
	if err != nil {
		return 0, safeDB(err)
	}
	return id, nil
}

// Run performs one full import: validate confirmation and user id, enforce
// a loopback-only database, read and validate exactly one subscription from
// Deps.Stdin or an explicit file, confirm the user exists, insert exactly
// one test_mode=true device row, and print only the new device's numeric
// id and status -- never the endpoint, keys, database URL, or full
// subscription JSON.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	if !cfg.Confirmed {
		return ErrNotConfirmed
	}
	if cfg.UserID <= 0 {
		return ErrInvalidUserID
	}
	if err := requireLoopbackDatabase(cfg.DatabaseURL); err != nil {
		return err
	}

	var raw io.Reader = deps.Stdin
	if cfg.File != "" {
		content, err := deps.OpenFile(cfg.File)
		if err != nil {
			return fmt.Errorf("sandbox-device: read subscription file: %w", err)
		}
		raw = strings.NewReader(string(content))
	}

	sub, err := parseSubscription(raw)
	if err != nil {
		return err
	}

	conn, cleanup, err := deps.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("sandbox-device: connect to database: %w", err)
	}
	defer cleanup()

	exists, err := userExists(ctx, conn, cfg.UserID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}

	id, err := insertTestDevice(ctx, conn, cfg.UserID, sub)
	if err != nil {
		return err
	}

	fmt.Fprintln(deps.Stdout, "sandbox-device: import complete.")
	fmt.Fprintf(deps.Stdout, "  DeviceID:  %d\n", id)
	fmt.Fprintln(deps.Stdout, "  Status:    created")
	fmt.Fprintln(deps.Stdout, "  TestMode:  true")
	if cfg.File != "" {
		fmt.Fprintln(deps.Stdout, "Reminder: delete the subscription file you supplied now; it is not needed again and is not deleted automatically.")
	}
	return nil
}
