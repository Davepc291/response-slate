package authratelimit

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestAllowWithinLimit(t *testing.T) {
	l := NewMemoryLimiter(Options{MaxAttempts: 3, Window: time.Minute})
	for i := 0; i < 3; i++ {
		if !l.Allow("key-a", base) {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if l.Allow("key-a", base) {
		t.Fatal("fourth attempt within the same window should be denied")
	}
}

func TestAllowResetsAfterWindow(t *testing.T) {
	l := NewMemoryLimiter(Options{MaxAttempts: 1, Window: time.Minute})
	if !l.Allow("key-b", base) {
		t.Fatal("first attempt should be allowed")
	}
	if l.Allow("key-b", base.Add(30*time.Second)) {
		t.Fatal("second attempt inside the window should be denied")
	}
	if !l.Allow("key-b", base.Add(61*time.Second)) {
		t.Fatal("attempt after the window elapsed should be allowed")
	}
}

func TestAllowIsPerKey(t *testing.T) {
	l := NewMemoryLimiter(Options{MaxAttempts: 1, Window: time.Minute})
	if !l.Allow("key-c", base) {
		t.Fatal("first key's first attempt should be allowed")
	}
	if !l.Allow("key-d", base) {
		t.Fatal("a different key must not be affected by another key's limit")
	}
}

func TestAllowNeverRevealsAccountExistenceThroughReturnShape(t *testing.T) {
	l := NewMemoryLimiter(Options{MaxAttempts: 1, Window: time.Minute})
	// Allow's signature is a bare bool for every key, known or unknown to
	// any caller-side concept of "account" — there is no separate error
	// path that could leak distinguishing information.
	got1 := l.Allow("maybe-an-account@example.test", base)
	got2 := l.Allow("definitely-not-an-account@example.test", base)
	if !got1 || !got2 {
		t.Fatal("expected both first attempts to be allowed identically")
	}
}

func TestBoundedEviction(t *testing.T) {
	l := NewMemoryLimiter(Options{MaxAttempts: 1, Window: time.Minute})
	for i := 0; i < maxTrackedKeys+10; i++ {
		l.Allow(keyN(i), base)
	}
	if l.Len() > maxTrackedKeys {
		t.Fatalf("expected tracked keys bounded at %d, got %d", maxTrackedKeys, l.Len())
	}
}

func keyN(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 0, 12)
	for n > 0 || len(b) == 0 {
		b = append(b, letters[n%len(letters)])
		n /= len(letters)
	}
	return string(b)
}

func TestValidateRejectsNonPositiveValues(t *testing.T) {
	cases := []Options{
		{MaxAttempts: 0, Window: time.Minute},
		{MaxAttempts: 1, Window: 0},
		{MaxAttempts: -1, Window: time.Minute},
	}
	for _, o := range cases {
		if err := o.Validate(); err != ErrInvalidOptions {
			t.Errorf("expected ErrInvalidOptions for %+v, got %v", o, err)
		}
	}
}

func TestNewMemoryLimiterPanicsOnInvalidOptions(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for invalid options")
		}
	}()
	NewMemoryLimiter(Options{})
}
