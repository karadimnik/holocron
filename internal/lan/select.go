package lan

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

// IfaceAddr is one IPv4 address of an interface that is up, with the interface's name and flags.
type IfaceAddr struct {
	Name  string
	Addr  netip.Prefix // the address with its prefix length, as the interface carries it
	Flags net.Flags
}

// Considered is one interface address looked at by Select and what became of it. CIDR is the network
// the interface is on; Reason says why it is scanned (Kept) or not.
type Considered struct {
	Name   string       `json:"name"`
	CIDR   netip.Prefix `json:"cidr"`
	Kept   bool         `json:"kept"`
	Reason string       `json:"reason"`
}

// Selection is the automatic choice of networks to scan. Refusal is set (and Nets empty) when no sound
// choice exists; Considered lists every address looked at, either way.
type Selection struct {
	Nets       []Local
	Considered []Considered
	Notes      []string
	Refusal    string
}

var (
	// Interface names that are not a LAN the host is on: container, VM, VPN, tunnel and Wi-Fi direct
	// style interfaces of Linux, macOS and Windows. Matched in lower case.
	virtualPrefixes = []struct{ prefix, kind string }{
		{"docker", "Docker bridge"}, {"br-", "Docker or Linux bridge"}, {"veth", "container link"},
		{"virbr", "libvirt VM bridge"}, {"vmnet", "VM network"}, {"vmenet", "VM network"}, {"vboxnet", "VM network"},
		{"utun", "VPN or tunnel"}, {"tun", "VPN or tunnel"}, {"tap", "VPN or tunnel"}, {"ppp", "VPN or dial-up link"},
		{"wg", "WireGuard"}, {"tailscale", "Tailscale"}, {"awdl", "Apple Wi-Fi direct"}, {"llw", "Apple Wi-Fi direct"},
	}
	virtualContains = []struct{ part, kind string }{
		{"vethernet", "Hyper-V virtual switch"}, {"hyper-v", "Hyper-V virtual switch"}, {"vmware", "VM network"},
		{"virtualbox", "VM network"}, {"loopback", "loopback"}, {"vpn", "VPN"}, {"wireguard", "WireGuard"},
		{"tailscale", "Tailscale"}, {"local area connection*", "Wi-Fi direct or hotspot"}, {"bluetooth", "Bluetooth"},
		{"isatap", "tunnel"}, {"teredo", "tunnel"},
	}
	macVMBridge = regexp.MustCompile(`^bridge\d{3,}$`) // bridge100 and up: Internet Sharing or a VM, not the Thunderbolt bridge0
)

// virtualKind names what kind of virtual interface an interface name is, or "" for a physical-looking one.
func virtualKind(name string) string {
	n := strings.ToLower(name)
	for _, v := range virtualContains { // first: "vethernet" would otherwise read as the Linux "veth"
		if strings.Contains(n, v.part) {
			return v.kind
		}
	}
	for _, v := range virtualPrefixes {
		if strings.HasPrefix(n, v.prefix) {
			return v.kind
		}
	}
	if macVMBridge.MatchString(n) {
		return "VM or Internet Sharing bridge"
	}
	return ""
}

// skipReason is why an interface address cannot be a LAN to scan, or "" when it can.
func skipReason(in IfaceAddr) string {
	a := in.Addr.Addr()
	switch {
	case in.Flags&net.FlagLoopback != 0 || a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast():
		return "link-local address (169.254.x.x), not a LAN"
	case !IsPrivate(in.Addr.Masked()):
		return "not a private network (10.x, 172.16-31.x, 192.168.x)"
	}
	if kind := virtualKind(in.Name); kind != "" {
		return "virtual interface: " + kind
	}
	switch {
	case in.Flags&net.FlagPointToPoint != 0:
		return "point-to-point link (VPN or tunnel)"
	case in.Flags&net.FlagBroadcast == 0:
		return "no broadcast, so not a LAN"
	}
	return ""
}

func (r DefaultRoute) on(in IfaceAddr) bool {
	return (r.Interface != "" && strings.EqualFold(r.Interface, in.Name)) || (r.Addr.IsValid() && r.Addr == in.Addr.Addr())
}

func addresses(n netip.Prefix) int { return 1 << (32 - n.Bits()) }

// Select picks the networks an automatic scan covers: the LANs this host is on, not the virtual
// interfaces (containers, VMs, VPNs, tunnels, Wi-Fi direct) and not what is not a private LAN. A
// network wider than a /24 is narrowed to the /24 around this host's address. When the networks left
// together exceed MaxAddresses, only the one on the default route is scanned (the others are listed in
// a note); when the default route cannot be read, every physical-looking network is scanned.
// routes is the default routes, the preferred first; routeErr is why they could not be read.
func Select(ifs []IfaceAddr, routes []DefaultRoute, routeErr error) Selection {
	if routeErr == nil && len(routes) == 0 {
		routeErr = errors.New("this host has no default route")
	}
	type cand struct {
		con     int // index in Considered
		name    string
		local   Local
		rank    int // position of its default route in routes; -1 when it carries none
		sameAs  int // index of the candidate that already scans the same network; -1 for none
		keepNet bool
	}
	var s Selection
	var cands []cand
	byNet := map[netip.Prefix]int{}
	for _, in := range ifs {
		c := Considered{Name: in.Name, CIDR: in.Addr.Masked()}
		if why := skipReason(in); why != "" {
			c.Reason = why
			s.Considered = append(s.Considered, c)
			continue
		}
		l := Local{Net: in.Addr.Masked(), Self: in.Addr.Addr()}
		if in.Addr.Bits() < 24 {
			l.Narrowed = in.Addr.Masked()
			l.Net = netip.PrefixFrom(in.Addr.Addr(), 24).Masked()
		}
		rank := -1
		for i, r := range routes {
			if r.on(in) {
				rank = i
				break
			}
		}
		s.Considered = append(s.Considered, c)
		k := cand{con: len(s.Considered) - 1, name: in.Name, local: l, rank: rank, sameAs: -1}
		if j, dup := byNet[l.Net]; dup {
			k.sameAs = j
			if rank >= 0 && (cands[j].rank < 0 || rank < cands[j].rank) {
				cands[j].rank = rank
			}
		} else {
			byNet[l.Net] = len(cands)
		}
		cands = append(cands, k)
	}
	if len(cands) == 0 {
		s.Refusal = "no interface is on a private IPv4 LAN (10.x, 172.16-31.x, 192.168.x)"
		return s
	}

	total, first := 0, -1 // addresses of the distinct networks; the candidate on the preferred default route
	for i, k := range cands {
		if k.sameAs >= 0 {
			continue
		}
		total += addresses(k.local.Net)
		if k.rank >= 0 && (first < 0 || k.rank < cands[first].rank) {
			first = i
		}
	}
	routeKnown := routeErr == nil
	for i := range cands {
		cands[i].keepNet = cands[i].sameAs < 0
	}
	over := total > MaxAddresses
	if over {
		for i := range cands {
			cands[i].keepNet = false
		}
		if first >= 0 {
			cands[first].keepNet = true
		}
	}
	if over && first < 0 {
		s.Refusal = fmt.Sprintf("the LANs found hold %d addresses, more than one run probes (%d), and none of them is on the default route to choose it", total, MaxAddresses)
		if routeKnown {
			s.Refusal += " (the default route leaves by an interface that is not one of them)"
		} else {
			s.Refusal += fmt.Sprintf(" (the default route cannot be read: %v)", routeErr)
		}
		for _, k := range cands {
			s.Considered[k.con].Reason = "a LAN, but too many to scan together and no default route to choose by"
		}
		return s
	}

	var left []string
	for _, k := range cands {
		c := &s.Considered[k.con]
		switch {
		case k.sameAs >= 0:
			c.Kept, c.Reason = cands[k.sameAs].keepNet, "same network as "+cands[k.sameAs].name+", scanned once"
			if !c.Kept {
				c.Reason = "same network as " + cands[k.sameAs].name + ", left out with it"
			}
		case k.keepNet:
			c.Kept = true
			switch {
			case k.rank >= 0:
				c.Reason = "carries the default route"
			case routeKnown:
				c.Reason = "a LAN, not on the default route"
			default:
				c.Reason = "a physical-looking LAN (default route unknown)"
			}
			s.Nets = append(s.Nets, k.local)
			if k.local.Narrowed.IsValid() {
				s.Notes = append(s.Notes, fmt.Sprintf("%s is wider than a /24: only %s (around this host's %s) is scanned; pass --subnet for more", k.local.Narrowed, k.local.Net, k.local.Self))
			}
		default:
			c.Reason = fmt.Sprintf("left out, over %d addresses in all: add it with --subnet %s", MaxAddresses, k.local.Net)
			left = append(left, fmt.Sprintf("%s (%s)", k.local.Net, k.name))
		}
	}
	if !routeKnown {
		s.Notes = append(s.Notes, fmt.Sprintf("the default route cannot be read (%v): every physical-looking LAN is scanned", routeErr))
	}
	if len(left) > 0 {
		s.Notes = append(s.Notes, fmt.Sprintf("the LANs found together exceed %d addresses: only the default route's network %s (%s) is scanned; not scanned: %s; add one with --subnet", MaxAddresses, cands[first].local.Net, cands[first].name, strings.Join(left, ", ")))
	}
	return s
}
