package web

import (
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Remote access (web.remoteHost): the HTTPS listener also answers to one public DNS name, for a
// phone outside the home network reached through a router port-forward. A request is REMOTE when
// RemoteHost is set and its TCP peer address is public. The class comes from the peer address
// only, never from a header: X-Forwarded-For and the like are not read, and the Host header is
// the client's to write (a remote caller can send Host: <the LAN address>). A port-forward keeps
// the caller's public source address; a LAN device that uses the public name through the
// router's hairpin NAT arrives from a private address and stays a LAN request.
//
// A remote request is capped to the viewer experience in authService.gate, the one choke point:
// it must name RemoteHost as its Host, its user's effective role is viewer whatever the account's
// role, the view-only allow-list applies even with tlsReadOnly false, its session obeys the
// shorter remote limits, and its sign-in attempts take a stricter per-peer bucket as well. The
// loopback listener (no login, every caller an admin) is never involved: it binds 127.0.0.1 and
// nothing here proxies to it.
// ---------------------------------------------------------------------------

// cgnat is RFC 6598 shared address space (carrier-grade NAT): not routable on the internet.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PeerIsRemote reports whether a peer address (an IP literal, as r.RemoteAddr's host or an
// auth.log ip= field holds it) is public: not loopback, RFC 1918 or IPv6 ULA, link-local, CGNAT
// or unspecified. Anything that does not parse is remote: unknown fails closed.
func PeerIsRemote(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return true
	}
	addr = addr.Unmap()
	local := addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnat.Contains(addr) || addr.IsUnspecified()
	return !local
}

// remote reports whether r is a remote request (RemoteHost set and a public peer).
func (a *authService) remote(r *http.Request) bool {
	return a.remoteHost != "" && PeerIsRemote(sourceIP(r))
}

// A remote session ends after 12 hours without use or 7 days after sign-in, whichever is first
// (LAN: 7 days idle, 30 days). A phone that checks the cameras daily signs in about once a week;
// a forgotten or stolen session stops working within half a day of disuse.
var remoteSession = sessionLimits{idle: 12 * time.Hour, lifetime: 7 * 24 * time.Hour}

// Remote sign-in attempts take a second, stricter token bucket per peer beside the LAN one
// (10 a minute, 5 banked): 2 a minute, 3 banked. Three quick tries cover a mistyped password;
// after that one address gets one guess every 30 s, and the per-name lockout (5 free failures,
// then 30 s doubling to 15 min) still bounds the guesses on any one name from all addresses.
const (
	remoteLoginRatePerMinute = 2
	remoteLoginBurst         = 3
)

// remotePeerKey is the remote bucket's key: an IPv4 address, or the /64 of an IPv6 one (one
// subscriber holds a whole /64, so per-address buckets would not limit anything).
func remotePeerKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	p, _ := addr.WithZone("").Prefix(64)
	return p.String()
}

// remoteHeaders are the response headers of every remote answer: HSTS for a day (not
// includeSubDomains, not preload: the name is the Operator's dynamic-DNS name and only it), no
// MIME sniffing, and no caching of API answers (the guard sets the last two for the routes it
// passes; a refusal by the gate never reaches it). Referrer-Policy is set for the whole listener.
func remoteHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Strict-Transport-Security", "max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store")
	}
}
