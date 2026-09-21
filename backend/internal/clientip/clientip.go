// Package clientip resolves the client network address for an incoming HTTP
// request, approved as part of Step 9C (docs/authentication-authorization-v1.md
// Section 4: "per source IP/network" rate limiting). It never blindly trusts
// the X-Forwarded-For header: that header is only consulted when the
// immediate TCP peer (r.RemoteAddr) is itself an explicitly configured
// trusted proxy, and only a bounded number of hops are ever inspected. When
// no trusted proxy is configured (the safe default), the resolved address is
// always r.RemoteAddr, exactly as if this package did not exist.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// maxForwardedHops bounds how many comma-separated X-Forwarded-For entries
// are ever inspected, so a request with an absurdly long header cannot cause
// unbounded work.
const maxForwardedHops = 10

// Resolver resolves the trust-aware client address for a request.
type Resolver struct {
	// trusted is the set of proxy addresses (single IPs or CIDR ranges)
	// permitted to supply a trustworthy X-Forwarded-For header. An empty
	// set (the default) means no proxy is trusted and X-Forwarded-For is
	// always ignored.
	trusted []*net.IPNet
}

// NewResolver builds a Resolver from a list of trusted proxy CIDRs or bare
// IP addresses (a bare IP is treated as a /32 or /128). An invalid entry
// fails closed: it is rejected by the caller (see ParseTrustedProxies)
// rather than silently ignored, so a configuration typo cannot silently
// widen trust.
func NewResolver(trusted []*net.IPNet) Resolver {
	out := make([]*net.IPNet, len(trusted))
	copy(out, trusted)
	return Resolver{trusted: out}
}

// ParseTrustedProxies parses a comma-separated list of CIDRs or bare IP
// addresses into the form NewResolver expects. An empty or blank input
// yields an empty, safe-default trust set.
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, errInvalidTrustedProxy
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			part = part + "/" + itoa(bits)
		}
		_, network, err := net.ParseCIDR(part)
		if err != nil {
			return nil, errInvalidTrustedProxy
		}
		out = append(out, network)
	}
	return out, nil
}

func itoa(n int) string {
	if n == 32 {
		return "32"
	}
	return "128"
}

var errInvalidTrustedProxy = trustedProxyError{}

type trustedProxyError struct{}

func (trustedProxyError) Error() string { return "clientip: invalid trusted proxy address or CIDR" }

func (r Resolver) isTrusted(ip net.IP) bool {
	for _, n := range r.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve returns the best-effort client IP for req. If the immediate peer
// is not a trusted proxy, or no proxy is configured at all, the immediate
// peer's address is returned unchanged and X-Forwarded-For is never
// consulted. If the immediate peer is trusted, the nearest (rightmost)
// untrusted hop in X-Forwarded-For is used, falling back to the immediate
// peer on any parse failure — never panicking, never returning empty.
func (r Resolver) Resolve(req *http.Request) string {
	peerHost, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		peerHost = req.RemoteAddr
	}
	peerIP := net.ParseIP(peerHost)
	if peerIP == nil || len(r.trusted) == 0 || !r.isTrusted(peerIP) {
		return peerHost
	}

	header := req.Header.Get("X-Forwarded-For")
	if header == "" {
		return peerHost
	}
	hops := strings.Split(header, ",")
	if len(hops) > maxForwardedHops {
		hops = hops[len(hops)-maxForwardedHops:]
	}
	// Walk from the rightmost (nearest) hop outward, skipping any hop that
	// is itself a trusted proxy, and return the first untrusted address
	// found. This resists a client-supplied XFF value spoofing an earlier
	// hop, since only entries appended by our own trusted proxies are ever
	// consulted at all.
	for i := len(hops) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(hops[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if !r.isTrusted(ip) {
			return candidate
		}
	}
	return peerHost
}
