package clientip

import (
	"net/http"
	"testing"
)

func newReq(remoteAddr, xff string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return req
}

func TestResolveIgnoresXFFWhenNoProxyTrusted(t *testing.T) {
	r := NewResolver(nil)
	req := newReq("203.0.113.9:54321", "1.2.3.4")
	if got := r.Resolve(req); got != "203.0.113.9" {
		t.Fatalf("expected the immediate peer, got %q", got)
	}
}

func TestResolveIgnoresXFFWhenPeerNotTrusted(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(trusted)
	req := newReq("203.0.113.9:54321", "1.2.3.4")
	if got := r.Resolve(req); got != "203.0.113.9" {
		t.Fatalf("expected the immediate peer since it is not a trusted proxy, got %q", got)
	}
}

func TestResolveUsesXFFWhenPeerTrusted(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(trusted)
	req := newReq("10.0.0.5:12345", "198.51.100.7")
	if got := r.Resolve(req); got != "198.51.100.7" {
		t.Fatalf("expected the forwarded client address, got %q", got)
	}
}

func TestResolveWalksPastTrustedHops(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(trusted)
	// Rightmost hop (10.0.0.9) is our trusted proxy; the next one left
	// (198.51.100.7) is the real, untrusted client.
	req := newReq("10.0.0.9:1", "198.51.100.7, 10.0.0.9")
	if got := r.Resolve(req); got != "198.51.100.7" {
		t.Fatalf("expected to walk past the trusted hop, got %q", got)
	}
}

func TestResolveFallsBackOnMalformedXFF(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(trusted)
	req := newReq("10.0.0.5:12345", "not-an-ip")
	if got := r.Resolve(req); got != "10.0.0.5" {
		t.Fatalf("expected fallback to the immediate peer, got %q", got)
	}
}

func TestParseTrustedProxiesRejectsInvalidEntries(t *testing.T) {
	if _, err := ParseTrustedProxies("not-an-ip-or-cidr"); err == nil {
		t.Fatal("expected an error for an invalid trusted proxy entry")
	}
}

func TestParseTrustedProxiesEmptyIsSafeDefault(t *testing.T) {
	trusted, err := ParseTrustedProxies("")
	if err != nil || trusted != nil {
		t.Fatalf("expected empty input to yield no trusted proxies, got %v %v", trusted, err)
	}
}
