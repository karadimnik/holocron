package web

import (
	"net/http"
	"net/netip"
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
