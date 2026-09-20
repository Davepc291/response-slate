package alertstore

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/alerts"
)

// Querier is the minimal pgx surface this package needs. A real *pgxpool.Pool
// satisfies it; tests use a fake.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed Store. Construct with Open, or by setting
// DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing transcriptreview package's hardened Open: this pipeline is
// dormant until a future authorized integration explicitly wires it up, and
// must never be pointed at a remote or production database by accident.
func Open(ctx context.Context, connection string) (*Postgres, func(), error) {
	u, err := url.Parse(connection)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return nil, nil, ErrUnavailable
	}
	cfg, err := pgxpool.ParseConfig(connection)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if cfg.ConnConfig.Host != u.Hostname() || len(cfg.ConnConfig.Fallbacks) > 0 {
		if cfg.ConnConfig.Host != u.Hostname() {
			return nil, nil, ErrUnavailable
		}
		cfg.ConnConfig.Fallbacks = nil
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if pool.Ping(ctx) != nil {
		pool.Close()
		return nil, nil, ErrUnavailable
	}
	return &Postgres{pool}, pool.Close, nil
}

// safeDB never returns a raw driver error: a connection string, table name,
// or constraint detail could otherwise leak across the package boundary.
// Only a fixed, closed set of Postgres error codes (all raised by this
// package's own CHECK constraints, foreign keys, unique constraints, or the
// record_alert_detection function's own explicit RAISE) are reclassified as
// caller input errors; every other database error fails closed as
// ErrUnavailable.
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23514", "23503", "23505", "22001", "22023":
			return ErrInput
		}
	}
	return ErrUnavailable
}

// RecordDetection validates rec and cooldown, then calls the single
// record_alert_detection SQL function that atomically writes the audit row
// and, at most, one alert_events row. It never issues more than one
// statement, so there is nothing for this package itself to wrap in an
// application-level transaction: atomicity is enforced by the database.
func (p *Postgres) RecordDetection(ctx context.Context, cooldown time.Duration, rec Record) (Result, error) {
	if p == nil || p.DB == nil {
		return Result{}, ErrUnavailable
	}
	if err := rec.Validate(); err != nil {
		return Result{}, err
	}
	cooldownSecs, err := cooldownSeconds(cooldown)
	if err != nil {
		return Result{}, err
	}

	var toneSetID, keywordListID, dedupKey, eventID, displaySummary *string
	var expiresAt *time.Time
	if rec.Event != nil {
		ev := rec.Event
		dedupKey = &ev.DedupKey
		eventID = &ev.EventID
		displaySummary = &ev.DisplaySummary
		expiresAt = &ev.ExpiresAt
		if rec.DetectorKind == alerts.DetectorTone {
			toneSetID = &ev.ToneSetID
		} else {
			keywordListID = &ev.KeywordListID
		}
	}
	var audioFingerprint, sourceIdentity *string
	if rec.Evidence.AudioFingerprint != "" {
		fp := rec.Evidence.AudioFingerprint
		audioFingerprint = &fp
	} else {
		sid := rec.Evidence.SourceIdentity
		sourceIdentity = &sid
	}
	var measuredHz []float64
	var measuredDuration []int64
	if rec.MeasuredHz != nil {
		measuredHz = rec.MeasuredHz
		measuredDuration = rec.MeasuredDurationMS
	}

	row := p.DB.QueryRow(ctx, `SELECT audit_id, event_created, event_suppressed FROM record_alert_detection(
        $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		rec.CreatedAt, string(rec.SourceKind), string(rec.Channel), rec.TGID, string(rec.DetectorKind),
		rec.ConfigID, string(rec.State), rec.Reason, rec.Confidence,
		measuredHz, measuredDuration, rec.KeywordOccurrences,
		audioFingerprint, sourceIdentity,
		dedupKey, eventID, expiresAt, toneSetID, keywordListID, displaySummary, cooldownSecs)

	var out Result
	if err := row.Scan(&out.AuditID, &out.EventCreated, &out.Suppressed); err != nil {
		return Result{}, safeDB(err)
	}
	return out, nil
}
