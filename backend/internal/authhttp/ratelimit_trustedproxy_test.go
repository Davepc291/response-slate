package authhttp

import (
	"net/http"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/clientip"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityservice"
)

// TestRateLimitIgnoresXFFWithoutTrustedProxy confirms the per-IP login
// limiter keys on the real TCP peer, not a client-supplied
// X-Forwarded-For, when no trusted proxy is configured — the safe default
// (Section 6's "must not blindly trust X-Forwarded-For").
func TestRateLimitIgnoresXFFWithoutTrustedProxy(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("xffvictim@example.test", identity.RoleResponder)

	// Same real peer, spoofing a different X-Forwarded-For on every
	// request: since no proxy is trusted, every request must still count
	// against the same per-IP bucket (the real peer), so the limiter still
	// trips.
	var lastCode int
	for i := 0; i < 25; i++ {
		opts := reqOpts{headers: map[string]string{"X-Forwarded-For": randomLookingIP(i)}}
		rec := hn.request(http.MethodPost, "/api/auth/login",
			[]byte(`{"email":"xffvictim@example.test","password":"wrong-password-value"}`), opts)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected the per-IP limiter to trip regardless of spoofed XFF, got %d", lastCode)
	}
}

// TestRateLimitUsesXFFWhenProxyTrusted confirms that once a trusted proxy
// is configured, distinct X-Forwarded-For client addresses get independent
// rate-limit buckets, while requests sharing one forwarded address still
// share a bucket.
func TestRateLimitUsesXFFWhenProxyTrusted(t *testing.T) {
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := identityservice.New(store, audit, testServiceConfig(), nil)
	opts := testAuthOptions()
	trusted, err := clientip.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	opts.TrustedProxies = trusted
	// A high per-account ceiling isolates this test to per-IP bucketing
	// behavior only; the per-account limiter has its own dedicated
	// coverage in TestLoginRateLimited.
	opts.LoginRateLimitPerAccount.MaxAttempts = 1000
	handlers, err := New(svc, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	hn := &harness{t: t, h: handlers, store: store, audit: audit, now: fixedNow, remote: "10.0.0.5:5555"}
	handlers.clock = func() time.Time { return hn.now }
	hn.mux = handlers.Mux()
	hn.seedActiveUser("xfftrusted@example.test", identity.RoleResponder)

	// Exhaust the limit for forwarded client A.
	for i := 0; i < 20; i++ {
		hn.request(http.MethodPost, "/api/auth/login",
			[]byte(`{"email":"xfftrusted@example.test","password":"wrong-password-value"}`),
			reqOpts{headers: map[string]string{"X-Forwarded-For": "198.51.100.1"}})
	}
	blockedA := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"xfftrusted@example.test","password":"wrong-password-value"}`),
		reqOpts{headers: map[string]string{"X-Forwarded-For": "198.51.100.1"}})
	if blockedA.Code != http.StatusTooManyRequests {
		t.Fatalf("expected client A to be rate-limited, got %d", blockedA.Code)
	}

	// A different forwarded client through the same trusted proxy must have
	// its own, unaffected bucket.
	stillAllowedB := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"xfftrusted@example.test","password":"wrong-password-value"}`),
		reqOpts{headers: map[string]string{"X-Forwarded-For": "198.51.100.2"}})
	if stillAllowedB.Code == http.StatusTooManyRequests {
		t.Fatal("expected a different forwarded client to have its own rate-limit bucket")
	}
}

func randomLookingIP(n int) string {
	return "198.51.100." + itoa(n%254+1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
