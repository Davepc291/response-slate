// Package database owns the PostgreSQL pool and exposes safe connectivity checks.
package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ConnectTimeout = 5 * time.Second
	PingTimeout    = 2 * time.Second
)

var (
	ErrUnavailable = errors.New("database unavailable")
	ErrRequired    = errors.New("required database is not configured")
)

type pool interface {
	Ping(context.Context) error
	Close()
}

// DB can represent an unconfigured database without a nil interface.
type DB struct {
	pool pool
}

// Open checks connectivity at startup. An optional, unreachable pool is kept so
// later readiness checks can recover without restarting the API.
func Open(ctx context.Context, url string, required bool) (*DB, error) {
	return open(ctx, url, required, newPool)
}

func open(ctx context.Context, url string, required bool, create func(context.Context, string) (pool, error)) (*DB, error) {
	db := &DB{}
	if strings.TrimSpace(url) == "" {
		if required {
			return nil, ErrRequired
		}
		return db, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	p, err := create(connectCtx, url)
	if err != nil {
		// Never wrap driver errors: they can contain credentials or the URL.
		if required {
			return nil, ErrUnavailable
		}
		return db, nil
	}
	db.pool = p
	if err := db.Ping(ctx); err != nil && required {
		db.Close()
		return nil, ErrUnavailable
	}
	return db, nil
}

func newPool(ctx context.Context, url string) (pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, ErrUnavailable
	}
	// Cap connection attempts even when the URL requests a longer timeout.
	if cfg.ConnConfig.ConnectTimeout <= 0 || cfg.ConnConfig.ConnectTimeout > ConnectTimeout {
		cfg.ConnConfig.ConnectTimeout = ConnectTimeout
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, ErrUnavailable
	}
	return p, nil
}

// Ping includes pool acquisition in its deadline and returns only safe errors.
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.pool == nil {
		return ErrUnavailable
	}
	pingCtx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	if err := db.pool.Ping(pingCtx); err != nil {
		return ErrUnavailable
	}
	return nil
}

// Close is called after HTTP shutdown, when handlers have released connections.
func (db *DB) Close() {
	if db != nil && db.pool != nil {
		db.pool.Close()
	}
}
