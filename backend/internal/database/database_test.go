package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fakePool struct {
	err    error
	closed bool
	ping   func(context.Context) error
}

func (p *fakePool) Ping(ctx context.Context) error {
	if p.ping != nil {
		return p.ping(ctx)
	}
	return p.err
}

func (p *fakePool) Close() { p.closed = true }

func TestOpen(t *testing.T) {
	secretErr := errors.New("postgres://user:secret@example.invalid/db: internal error")
	for _, tc := range []struct {
		name       string
		url        string
		required   bool
		createErr  error
		pingErr    error
		wantErr    error
		wantPool   bool
		wantClosed bool
	}{
		{name: "unconfigured optional"},
		{name: "unconfigured required", required: true, wantErr: ErrRequired},
		{name: "ready optional", url: "configured", wantPool: true},
		{name: "ready required", url: "configured", required: true, wantPool: true},
		{name: "unavailable optional retains pool", url: "configured", pingErr: secretErr, wantPool: true},
		{name: "unavailable required closes pool", url: "configured", required: true, pingErr: secretErr, wantErr: ErrUnavailable, wantClosed: true},
		{name: "invalid optional", url: "configured", createErr: secretErr},
		{name: "invalid required is safe", url: "configured", required: true, createErr: secretErr, wantErr: ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePool{err: tc.pingErr}
			db, err := open(context.Background(), tc.url, tc.required, func(ctx context.Context, _ string) (pool, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > ConnectTimeout {
					t.Fatal("pool initialization needs a bounded deadline")
				}
				if tc.createErr != nil {
					return nil, tc.createErr
				}
				return p, nil
			})
			if err != tc.wantErr {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if p.closed != tc.wantClosed {
				t.Fatalf("pool closed = %v, want %v", p.closed, tc.wantClosed)
			}
			if err != nil {
				if db != nil {
					t.Fatal("startup failure returned a database")
				}
				return
			}
			defer db.Close()
			if (db.pool != nil) != tc.wantPool {
				t.Fatal("unexpected pool retention")
			}
			if !tc.wantPool && db.Ping(context.Background()) != ErrUnavailable {
				t.Fatal("unconfigured database must be unavailable")
			}
		})
	}
}

func TestOptionalDatabaseRecovers(t *testing.T) {
	p := &fakePool{err: errors.New("offline")}
	db, err := open(context.Background(), "configured", false, func(context.Context, string) (pool, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p.err = nil
	if err := db.Ping(context.Background()); err != nil {
		t.Fatal("readiness did not recover")
	}
}

func TestPingDeadlineAndCancellation(t *testing.T) {
	p := &fakePool{ping: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > PingTimeout {
			t.Fatal("ping needs a bounded deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	db := &DB{pool: p}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := db.Ping(ctx); err != ErrUnavailable {
		t.Fatalf("error = %v, want safe unavailable error", err)
	}
	db.Close()
	if !p.closed {
		t.Fatal("Close did not close the pool")
	}
}

func TestPGXConfiguration(t *testing.T) {
	_, err := newPool(context.Background(), "postgres://user:secret@localhost:invalid/db")
	if err != ErrUnavailable {
		t.Fatal("invalid driver configuration must return only a safe error")
	}
	p, err := newPool(context.Background(), "postgres://example:placeholder@127.0.0.1:1/example?sslmode=disable&connect_timeout=60&pool_min_conns=0&pool_min_idle_conns=0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.(*pgxpool.Pool).Config().ConnConfig.ConnectTimeout != ConnectTimeout {
		t.Fatal("driver connection timeout was not capped")
	}
}
