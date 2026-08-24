package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies names the proxy addresses whose X-Forwarded-For header may be
// believed.
//
// The zero value trusts nothing, and that is deliberate: it is exactly the
// behaviour this gateway had before Step 26, so every existing test passes
// against it unmodified and a deployment has to opt in.
//
// WHY this type exists at all. Step 26 puts Caddy in front of the gateway so
// the frontend and the API share one origin. With a proxy in front,
// r.RemoteAddr is the proxy's address on every single request, so the per-IP
// limiter keeps working perfectly -- on one key, for the entire internet. 100
// requests per 15 minutes shared by everybody is a self-inflicted outage that
// arrives looking like "login is broken", not like a rate limit.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies reads a comma-separated list of CIDRs or bare addresses.
// An empty string yields the zero value, which trusts nothing.
//
// A bare address is accepted and treated as a single-host prefix, because
// naming one container by address is the common case and requiring "/32" on it
// is the kind of papercut that ends in somebody trusting 0.0.0.0/0 to make the
// error go away.
func ParseTrustedProxies(raw string) (TrustedProxies, error) {
	var t TrustedProxies
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(field); err == nil {
			t.prefixes = append(t.prefixes, prefix)
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return TrustedProxies{}, &net.AddrError{Err: "not an address or CIDR", Addr: field}
		}
		t.prefixes = append(t.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return t, nil
}

// Empty reports whether anything is trusted. Used at boot to log which mode
// the gateway is in, because "the rate limiter is counting the proxy" is not
// something anyone should have to discover from a graph.
func (t TrustedProxies) Empty() bool { return len(t.prefixes) == 0 }

func (t TrustedProxies) trusts(host string) bool {
	if len(t.prefixes) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	// An IPv4-mapped IPv6 address and its IPv4 form are the same host, and
	// which one shows up depends on how the listener was opened. Compare in
	// one form so a trusted 172.18.0.5 is still trusted as ::ffff:172.18.0.5;
	// netip.Prefix.Contains says false for the mapped form against a v4
	// prefix, so without this a proxy on a dual-stack listener is silently
	// untrusted and every client behind it shares one budget again.
	//
	// No address-family guard here on purpose. Contains already refuses to
	// match across families -- ::/0 does not contain 172.18.0.5 -- so a guard
	// is a branch that cannot change an answer. One was written and removed
	// after a mutation that deleted it changed nothing.
	addr = addr.Unmap()
	for _, prefix := range t.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerIP is the address the TCP connection actually came from, with the port
// stripped.
//
// The port is stripped because it differs on every connection; leaving it in a
// limiter key would hand a single client an unbounded number of budgets.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port to split (some test servers, some proxies). The whole value
		// is then the address -- better a coarse key than no limiting.
		return r.RemoteAddr
	}
	return host
}

// clientIP returns the address to hold responsible for this request.
//
// With nothing trusted it is r.RemoteAddr and nothing else. Not
// X-Forwarded-For, not X-Real-IP -- both are client-authored at that hop, and
// a limiter keying on either can be handed a fresh budget per request by
// anyone who sets the header. docs/security-backlog.md asserted the opposite
// once and was wrong; the gateway's own SetXForwarded() call runs on r.Out
// inside proxy.New's Rewrite, after every middleware, and builds only the
// upstream request. Nothing sanitises the inbound header.
//
// When the peer IS a trusted proxy, the client is the RIGHTMOST entry of
// X-Forwarded-For, and rightmost is the entire security argument. A client
// sending "X-Forwarded-For: 1.2.3.4" has its real address appended by the
// proxy, producing "1.2.3.4, <real>". The rightmost entry is the one the
// trusted hop wrote and the only one that is not client-authored. Taking the
// leftmost -- the reflex, and what "the original client IP" sounds like it
// means -- reintroduces precisely the bypass described above.
//
// This is correct for ONE trusted hop and no more. With two proxies the
// rightmost entry is the inner proxy, and the limiter would key on it. SPEC.md
// §2.4 is what guarantees one, which is why that decision and this code have
// to be read together.
func clientIP(r *http.Request, trusted TrustedProxies) string {
	peer := peerIP(r)
	if !trusted.trusts(peer) {
		return peer
	}
	if forwarded := rightmostForwarded(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		return forwarded
	}
	// A trusted proxy that sent no usable header. Fall back to the peer rather
	// than to an empty key, which would merge every such request into one
	// bucket -- the bug this whole type exists to prevent, reintroduced by its
	// own error path.
	return peer
}

// rightmostForwarded returns the last syntactically valid address in an
// X-Forwarded-For header, or "" if there is none.
//
// Validity is checked rather than assumed. An unparseable value must not
// become a limiter key: keys are compared as strings, so "not-an-ip" would be
// a perfectly good shared bucket for everyone who sent it.
func rightmostForwarded(header string) string {
	fields := strings.Split(header, ",")
	for i := len(fields) - 1; i >= 0; i-- {
		field := strings.TrimSpace(fields[i])
		if field == "" {
			continue
		}
		// Some proxies write "ip:port" here even though the spec does not ask
		// for it. Take the address if so, and give up on the field otherwise.
		if addr, err := netip.ParseAddr(field); err == nil {
			return addr.Unmap().String()
		}
		if host, _, err := net.SplitHostPort(field); err == nil {
			if addr, err := netip.ParseAddr(host); err == nil {
				return addr.Unmap().String()
			}
		}
		return ""
	}
	return ""
}
