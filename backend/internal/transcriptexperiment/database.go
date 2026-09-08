package transcriptexperiment

import (
	"context"
	"github.com/jackc/pgx/v5"
	"net/url"
	"time"
)

type Evidence struct {
	AttemptID                              int64
	Path, Fingerprint, Channel, Model, Raw string
	TGID, DurationMS                       int64
}
type Resolver interface {
	Resolve(context.Context, int64) (Evidence, error)
	Close()
}
type rowQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type postgres struct {
	query rowQuery
	close func()
}

func (p *postgres) Close() { p.close() }

const evidenceSQL = `SELECT a.id,r.source_path,r.audio_fingerprint,r.channel_label,a.model,a.raw_text,r.tgid,r.duration_ms
 FROM transcription_attempts a JOIN radio_transmissions r ON r.id=a.transmission_id
 WHERE a.id=$1 AND a.outcome='completed' AND a.finished_at IS NOT NULL
 AND r.processing_status='completed' AND r.transcription_status='completed'
 AND r.audio_duplicate_of IS NULL AND r.audio_probe IS NOT NULL
 AND r.audio_fingerprint IS NOT NULL AND r.source_path IS NOT NULL
 AND r.transcription_model=a.model`

func (p *postgres) Resolve(ctx context.Context, id int64) (Evidence, error) {
	var v Evidence
	if id < 1 {
		return v, ErrEvidence
	}
	e := p.query.QueryRow(ctx, evidenceSQL, id).Scan(&v.AttemptID, &v.Path, &v.Fingerprint, &v.Channel, &v.Model, &v.Raw, &v.TGID, &v.DurationMS)
	if e != nil {
		return Evidence{}, ErrEvidence
	}
	return v, nil
}
func databaseConfig(connection string) (*pgx.ConnConfig, error) {
	u, e := url.Parse(connection)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
		return nil, ErrDatabase
	}
	// Do not inherit missing user/database/password from PG* variables or pgpass.
	if u.User == nil || u.User.Username() == "" || u.Path == "" || u.Path == "/" {
		return nil, ErrDatabase
	}
	password, present := u.User.Password()
	if !present || password == "" {
		return nil, ErrDatabase
	}
	// Disallow alternate hosts, service files, and arbitrary startup options.
	for key := range u.Query() {
		if key != "sslmode" {
			return nil, ErrDatabase
		}
	}
	cfg, e := pgx.ParseConfig(connection)
	if e != nil || cfg.Host != u.Hostname() {
		return nil, ErrDatabase
	}
	cfg.Fallbacks = nil
	cfg.User = u.User.Username()
	cfg.Password = password
	cfg.ConnectTimeout = 3 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "gfr-offline-experiment", "default_transaction_read_only": "on"}
	return cfg, nil
}

var readOnlyOptions = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// OpenDatabase receives only the CLI's GFR_DATABASE_URL value. There is no .env
// loader and no query accepting user-supplied SQL. SELECTs share one snapshot.
func OpenDatabase(ctx context.Context, connection string) (Resolver, error) {
	cfg, e := databaseConfig(connection)
	if e != nil {
		return nil, e
	}
	conn, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		return nil, ErrDatabase
	}
	tx, e := conn.BeginTx(ctx, readOnlyOptions)
	if e != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn.Close(closeCtx)
		return nil, ErrDatabase
	}
	return &postgres{query: tx, close: func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
		_ = conn.Close(c)
	}}, nil
}
