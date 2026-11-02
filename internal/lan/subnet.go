// Package lan is the network side of `holocron cam discover`: which private IPv4 networks this host
// is on, which addresses a scan may probe, and what the OS neighbour (ARP) table says about them.
// It sends nothing itself; the probing is the caller's.
package lan

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// MaxAddresses is the most addresses one run may probe: a /22.
const MaxAddresses = 1024

var (
	private = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	loopback = netip.MustParsePrefix("127.0.0.0/8")
)

func within(p, outer netip.Prefix) bool { return outer.Bits() <= p.Bits() && outer.Contains(p.Addr()) }

// IsPrivate reports whether the whole network lies in RFC 1918 space (link-local is not).
func IsPrivate(p netip.Prefix) bool {
	for _, o := range private {
		if within(p, o) {
			return true
		}
	}
	return false
}

// ParseSubnet reads a --subnet value: an IPv4 network in CIDR form, at most a /22, inside RFC 1918
// space. allowLoopback admits 127.0.0.0/8 too (tests only; no flag sets it).
func ParseSubnet(s string, allowLoopback bool) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q is not an IPv4 subnet in CIDR form such as 192.168.1.0/24", s)
	}
	p = p.Masked()
	if p.Bits() < 22 {
		return netip.Prefix{}, fmt.Errorf("%s holds %d addresses; one run probes at most %d (a /22): pass a smaller subnet", p, 1<<(32-p.Bits()), MaxAddresses)
	}
	if IsPrivate(p) || (allowLoopback && within(p, loopback)) {
		return p, nil
	}
	return netip.Prefix{}, fmt.Errorf("%s is not a private network (10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16): cam discover scans only the local private network", p)
}

// Local is one private network this host is on.
type Local struct {
	Net      netip.Prefix // what is scanned
	Self     netip.Addr   // this host's address on it
	Narrowed netip.Prefix // the interface's own network when it was wider than a /24 (else invalid)
}

// LocalSubnets keeps the private IPv4 networks among the interface addresses (each an address with
// its prefix length, as the interface carries it). Loopback, link-local and public networks are left
// out; a network wider than a /24 (a VPN, a virtual switch) is narrowed to the /24 around this host's
// address, so an automatic run stays small. Duplicates are dropped.
func LocalSubnets(addrs []netip.Prefix) []Local {
	var out []Local
	seen := map[netip.Prefix]bool{}
	for _, a := range addrs {
		if !a.Addr().Is4() || !IsPrivate(a.Masked()) {
			continue
		}
		l := Local{Net: a.Masked(), Self: a.Addr()}
		if a.Bits() < 24 {
			l.Narrowed = a.Masked()
			l.Net = netip.PrefixFrom(a.Addr(), 24).Masked()
		}
		if seen[l.Net] {
			continue
		}
		seen[l.Net] = true
		out = append(out, l)
	}
	return out
}

// Hosts lists the addresses of nets in order, without the network and broadcast address of a /30 or
// wider and without skip (this host's own addresses). More than MaxAddresses in all is refused.
func Hosts(nets []netip.Prefix, skip map[netip.Addr]bool) ([]netip.Addr, error) {
	total := 0
	for _, n := range nets {
		total += 1 << (32 - n.Bits())
	}
	if total > MaxAddresses {
		return nil, fmt.Errorf("the subnets hold %d addresses; one run probes at most %d (a /22): pass fewer or smaller --subnet values", total, MaxAddresses)
	}
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, n := range nets {
		n = n.Masked()
		first, size := n.Addr(), 1<<(32-n.Bits())
		a := first
		for i := 0; i < size; i, a = i+1, a.Next() {
			if size >= 4 && (i == 0 || i == size-1) {
				continue
			}
			if skip[a] || seen[a] {
				continue
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out, nil
}

// InterfaceAddrs is the IPv4 addresses, with their prefix length, of this host's interfaces that are
// up and not loopback.
func InterfaceAddrs() ([]netip.Prefix, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := in.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ones, bits := n.Mask.Size()
			ip, _ := netip.AddrFromSlice(n.IP.To4())
			if bits == 32 {
				out = append(out, netip.PrefixFrom(ip, ones))
			}
		}
	}
	return out, nil
}
