package web

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"holocron/internal/config"
)

// ---------------------------------------------------------------------------
// Whose request is it? (web.trustedProxy)
//
// By default the answer is the TCP peer address and nothing else: no header decides the class of a
// request, because the Host and Forwarded headers are the caller's to write.
//
// web.trustedProxy opts in to one exception. A reverse proxy on this PC (for example nginx behind
// `tailscale serve`) connects to the HTTPS listener from a single, dedicated local address, so every
// visitor, whatever network they came from, shows up as that address. When the setting names the
// address, a request whose TCP peer is exactly that address has its client read from the
// X-Forwarded-For header the proxy passes on; a request from any other peer has the header
// ignored, as before. This file is the only place that header is read.
//
// Which entry: the RIGHT-MOST one. X-Forwarded-For is a list that each proxy appends the address it
// received the connection from to; the entries to the left of the last were written by whoever
// came before, and a client can pre-fill the header with anything. Only the right-most entry was
// written (or passed through unchanged) by the proxy this setting trusts, so it is the only one
// that is trustworthy. With `tailscale serve` in front the header holds exactly one entry, the
// tailnet address of the caller (tailscale strips any copy the caller sent). If a proxy in between
// appends its own peer (nginx's $proxy_add_x_forwarded_for), the right-most entry is that proxy's
// peer address instead, which is loopback or private and so lands in the LAN class: the wrong
// label, never a more trusted one (docs/ui.md, "Behind a reverse proxy or a tunnel").
//
// A header that is missing, empty, has an empty or non-address right-most entry, or holds a zone,
// a port or a host name, is treated as absent: the client is the proxy's own address, which is a
// loopback or private one and therefore the LAN class. The fallback is never a more trusted class
// than the proxy itself.
// ---------------------------------------------------------------------------

// The address spaces that name a Tailscale tailnet: the CGNAT block it hands out for IPv4
// (RFC 6598, also what a carrier uses) and the ULA prefix of its IPv6 addresses.
var (
	tailnetV4 = cgnat
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// maxForwardedFor bounds what is looked at: a longer header is not a proxy's, and the right-most
// entry of a huge one is not worth splitting it for.
const maxForwardedFor = 4 << 10

// trustedProxyOf parses web.trustedProxy for the HTTPS listener: "" is off (the zero Addr). The
// value was already checked by config.NormalizeTrustedProxy; it is checked again here, so a web app
// started with a hand-built Config cannot trust a wildcard or a public address.
func trustedProxyOf(s string) (netip.Addr, error) {
	norm, err := config.NormalizeTrustedProxy(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("web.trustedProxy %q: %v", s, err)
	}
	if norm == "" {
		return netip.Addr{}, nil
	}
	return netip.MustParseAddr(norm), nil
}

// peerAddr is the TCP peer address of r, without port. It is what the loopback listener (which has
// no proxy in front of it) and the rest of the code that needs the connection itself use; the
// HTTPS listener's decisions go through authService.clientIP.
func peerAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// addrClass is the class of an address literal: netTailnet for a tailnet address, netRemote for a
// public (or unparseable) one, netLAN for loopback, private, link-local and the rest. The
// caller decides whether the remote class applies (authService.netClass).
func addrClass(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err == nil {
		addr = addr.Unmap()
		if tailnetV4.Contains(addr) || tailnetV6.Contains(addr) {
			return netTailnet
		}
	}
	if PeerIsRemote(ip) {
		return netRemote
	}
	return netLAN
}

// clientIP is the effective client address of r and whether it was read from a trusted proxy's
// X-Forwarded-For: the TCP peer, unless web.trustedProxy is set and the peer is exactly that
// address and the header's right-most entry is an IP address.
func (a *authService) clientIP(r *http.Request) (ip string, viaProxy bool) {
	peer := peerAddr(r)
	if !a.trustedProxy.IsValid() {
		return peer, false
	}
	p, err := netip.ParseAddr(peer)
	if err != nil || p.Unmap() != a.trustedProxy {
		return peer, false
	}
	if client, ok := lastForwarded(r.Header.Values("X-Forwarded-For")); ok {
		return client, true
	}
	return peer, false
}

// lastForwarded is the right-most entry of the X-Forwarded-For header lines (several lines count as
// one list, in order), as a canonical IP literal. ok is false for anything else.
func lastForwarded(lines []string) (string, bool) {
	if len(lines) == 0 {
		return "", false
	}
	list := strings.Join(lines, ",")
	if len(list) > maxForwardedFor {
		return "", false
	}
	entry := strings.TrimSpace(list[strings.LastIndexByte(list, ',')+1:])
	addr, err := netip.ParseAddr(entry)
	if err != nil || addr.Zone() != "" {
		return "", false
	}
	return addr.Unmap().String(), true
}

// netClass is the class of the client of r: netTailnet, netRemote or netLAN.
//
// A tailnet address is its own class from any source. A public address is remote only when remote
// access is on (web.remoteHost) or when it came from a trusted proxy: a public peer that connects
// directly with remote access off is classified as it always was (nothing routes one there), but a
// public client behind the proxy is never granted LAN rights: with remote access off, the gate
// refuses it outright (authService.gate).
func (a *authService) netClass(r *http.Request) string {
	ip, viaProxy := a.clientIP(r)
	class := addrClass(ip)
	if class == netRemote && a.remoteHost == "" && !viaProxy {
		return netLAN
	}
	return class
}
