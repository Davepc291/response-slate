// Package authratelimit implements the Step 9C bounded, testable
// rate-limiting seam approved for login attempts, invitation-redemption
// attempts, password-reset requests, and password-reset completion
// (docs/authentication-authorization-v1.md Section 4: "Failed sign-in
// attempts are rate-limited per account and per source IP/network, with
// bounded, configurable thresholds and a backoff window, never an unbounded
// lockout"). It never reveals whether a specific key (account or IP)
// exists: Allow reports only a boolean, and callers must render the exact
// same generic "too many attempts" message regardless of which key, if any,
// triggered it. The in-memory implementation is a single-process bound: a
// distributed limiter would require a provider decision (for example, a
// shared cache) not approved by any existing contract, so this package
// exposes only the interface plus a safe, capacity-bounded single-process
// foundation, documented as such rather than silently presented as
// distributed-safe.
package authratelimit

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

// Options is the fixed-window policy for one limiter instance: at most
// MaxAttempts within Window, per key.
type Options struct {
	MaxAttempts int
	Window      time.Duration
}

// ErrInvalidOptions marks a non-positive attempt count or window: picking an
// implicit default here would silently choose an operational value the
// contract leaves open (Section 15, item 6 analog).
var ErrInvalidOptions = errors.New("authratelimit: max attempts and window must be positive, explicitly chosen values")

func (o Options) Validate() error {
	if o.MaxAttempts <= 0 || o.Window <= 0 {
		return ErrInvalidOptions
	}
	return nil
}

// maxTrackedKeys bounds total memory use regardless of how many distinct
// keys (accounts, IP addresses) are ever presented, so this package can
// never become its own unbounded-memory denial-of-service vector.
const maxTrackedKeys = 50_000

// Limiter is the seam every call site depends on, so a future distributed
// implementation can be substituted without changing any caller.
type Limiter interface {
	// Allow reports whether one more attempt for key is currently
	// permitted, and records the attempt if so. It never returns an error:
	// a rate limiter that could fail open or closed on an error is exactly
	// the ambiguity Section 4 forbids ("never an unbounded lockout that
	// becomes its own denial-of-service vector").
	Allow(key string, now time.Time) bool
}

type entry struct {
	key         string
	windowStart time.Time
	count       int
	elem        *list.Element
}

// MemoryLimiter is a bounded, single-process fixed-window limiter. It is
// safe for concurrent use. It is explicitly NOT a distributed rate limiter:
// each server process tracks its own counters, so a deployment running more
// than one API process has a correspondingly higher effective bound. This
// is documented here, not hidden, per Step 9C's requirement to implement
// only "a safe bounded single-process foundation" absent an approved
// distributed-provider decision.
type MemoryLimiter struct {
	opts Options

	mu      sync.Mutex
	entries map[string]*entry
	order   *list.List // front = least recently touched, for bounded eviction
}

// NewMemoryLimiter constructs a bounded in-memory Limiter. It panics on
// invalid Options because Options is always caller/config-supplied and
// constructed once at startup — matching this repository's existing
// fail-fast-at-construction convention for explicitly-chosen policy values.
func NewMemoryLimiter(opts Options) *MemoryLimiter {
	if err := opts.Validate(); err != nil {
		panic(err)
	}
	return &MemoryLimiter{
		opts:    opts,
		entries: make(map[string]*entry),
		order:   list.New(),
	}
}

// Allow implements Limiter using a fixed-window counter per key. A window
// resets when now has advanced past windowStart+Window since the key's
// first attempt in the current window, matching the "bounded, configurable
// thresholds and a backoff window" requirement without an unbounded
// lockout: the key becomes allowed again once its window has elapsed, never
// requiring an administrator action to clear it.
func (l *MemoryLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowStart) >= l.opts.Window {
		if !ok {
			l.evictIfFullLocked()
			e = &entry{key: key}
			l.entries[key] = e
			e.elem = l.order.PushBack(key)
		} else {
			l.order.MoveToBack(e.elem)
		}
		e.windowStart = now
		e.count = 1
		return true
	}
	l.order.MoveToBack(e.elem)
	if e.count >= l.opts.MaxAttempts {
		return false
	}
	e.count++
	return true
}

// evictIfFullLocked drops the least-recently-touched key once the tracked
// set reaches capacity, so a flood of distinct keys (for example, many
// distinct attempted email addresses) cannot grow memory without bound.
// Evicting the oldest entry is a safe fail-open for that one key (it simply
// starts a fresh window), never a fail-open for the system as a whole.
func (l *MemoryLimiter) evictIfFullLocked() {
	if len(l.entries) < maxTrackedKeys {
		return
	}
	front := l.order.Front()
	if front == nil {
		return
	}
	l.order.Remove(front)
	delete(l.entries, front.Value.(string))
}

// Len reports the number of currently tracked keys, for tests only.
func (l *MemoryLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
