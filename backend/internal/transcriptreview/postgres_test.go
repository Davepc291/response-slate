package transcriptreview

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSafeDatabaseErrorsAndLocalOnly(t *testing.T) {
	for _, e := range []error{errors.New("password token private-url"), &pgconn.PgError{Code: "23514", Message: "password token private-url"}, pgx.ErrNoRows} {
		safe := safeDB(e)
		if safe == nil || strings.Contains(safe.Error(), "password") {
			t.Fatal("unsafe database error")
		}
	}
	for _, url := range []string{"", "not-a-url-secret", "postgres://user:secret@remote.invalid/db", "postgres://user:secret@127.0.0.1/db?host=remote.invalid"} {
		_, closeDB, e := Open(context.Background(), url)
		if closeDB != nil {
			closeDB()
		}
		if e != ErrUnavailable {
			t.Fatal("nonlocal or invalid connection accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, closeDB, e := Open(ctx, "postgres://fixture:secret@127.0.0.1:1/fixture?sslmode=disable")
	if closeDB != nil {
		closeDB()
	}
	if e != ErrUnavailable {
		t.Fatal("canceled database dial accepted")
	}
}

type blockingStore struct {
	fakeStore
	started chan struct{}
}

func (s *blockingStore) Stats(ctx context.Context) (Stats, error) {
	s.seen(ctx)
	close(s.started)
	<-ctx.Done()
	return Stats{}, ErrUnavailable
}
func TestCLIQueryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &blockingStore{started: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, []string{"stats"}, &bytes.Buffer{}, s, time.Second) }()
	<-s.started
	cancel()
	select {
	case e := <-done:
		if e != ErrUnavailable || !s.deadline {
			t.Fatal("cancellation lost", e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
}
