package lan

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// DefaultRoute is one default route (0.0.0.0/0) of the OS routing table: the interface it leaves by.
// Linux and macOS name the interface; Windows gives this host's address on it instead.
type DefaultRoute struct {
	Interface string     // interface name (Linux, macOS); empty on Windows
	Addr      netip.Addr // this host's address on the interface (Windows); invalid elsewhere
	Gateway   netip.Addr // the router; invalid when the route is on-link
	Metric    int
}

// Routes reads the default routes of the OS routing table, the preferred one first. SystemRoutes is the
// real reader; FakeRoutes stands in for it in tests.
type Routes interface {
	Default(ctx context.Context) ([]DefaultRoute, error)
}

// FakeRoutes is a fixed answer.
type FakeRoutes struct {
	Routes []DefaultRoute
	Err    error
}

func (f FakeRoutes) Default(context.Context) ([]DefaultRoute, error) { return f.Routes, f.Err }

func byMetric(rs []DefaultRoute) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Metric < rs[j].Metric })
}

// ParseProcNetRoute reads Linux /proc/net/route: a header line, then tab-separated Iface, Destination,
// Gateway, Flags, RefCnt, Use, Metric, Mask, ... with the addresses as little-endian hex. A default
// route has destination and mask 0 and the RTF_UP bit (0x1) of Flags; the lowest metric comes first.
func ParseProcNetRoute(text string) []DefaultRoute {
	var out []DefaultRoute
	for _, l := range lines(text) {
		f := strings.Fields(l)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x1 == 0 {
			continue
		}
		metric, err := strconv.Atoi(f[6])
		if err != nil {
			continue
		}
		r := DefaultRoute{Interface: f[0], Metric: metric}
		if gw, err := strconv.ParseUint(f[2], 16, 32); err == nil && gw != 0 {
			r.Gateway = netip.AddrFrom4([4]byte{byte(gw), byte(gw >> 8), byte(gw >> 16), byte(gw >> 24)})
		}
		out = append(out, r)
	}
	byMetric(out)
	return out
}

// ParseNetstatInet reads macOS `netstat -rn -f inet`: a header with Destination, Gateway, Flags, Netif
// columns, then rows. The rows whose destination is "default" and whose flags carry U (up) are the
// default routes; the interface-scoped ones (flag I, the secondary default of an interface that is not
// the primary service) come after the others. The Netif column is found from the header, since older
// systems print Refs and Use before it.
func ParseNetstatInet(text string) []DefaultRoute {
	netif := 3
	var primary, scoped []DefaultRoute
	for _, l := range lines(text) {
		f := strings.Fields(l)
		if len(f) >= 4 && f[0] == "Destination" {
			for i, h := range f {
				if h == "Netif" {
					netif = i
				}
			}
			continue
		}
		if len(f) <= netif || f[0] != "default" || !strings.Contains(f[2], "U") {
			continue
		}
		r := DefaultRoute{Interface: f[netif]}
		if gw, err := netip.ParseAddr(f[1]); err == nil && gw.Is4() {
			r.Gateway = gw
		}
		if strings.Contains(f[2], "I") {
			scoped = append(scoped, r)
		} else {
			primary = append(primary, r)
		}
	}
	return append(primary, scoped...)
}

// ParseRouteGet reads macOS `route -n get default`: "key: value" lines, of which "interface" and
// "gateway" matter. It names the one interface the system routes through.
func ParseRouteGet(text string) []DefaultRoute {
	var r DefaultRoute
	for _, l := range lines(text) {
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "interface":
			r.Interface = val
		case "gateway":
			if gw, err := netip.ParseAddr(val); err == nil && gw.Is4() {
				r.Gateway = gw
			}
		}
	}
	if r.Interface == "" {
		return nil
	}
	return []DefaultRoute{r}
}

// ParseWindowsRoutePrint reads Windows `route print -4`. The active routes are the block after the
// third line of "=" signs (interface list, table title, then the routes; the persistent routes follow
// the next one), found by structure so no localised heading is needed. A default route is a row
// "0.0.0.0 0.0.0.0 <gateway or a localised On-link> <interface address> <metric>": only the numbers
// are used. The lowest metric comes first.
func ParseWindowsRoutePrint(text string) []DefaultRoute {
	var out []DefaultRoute
	rules := 0
	for _, l := range lines(text) {
		f := strings.Fields(l)
		if len(f) > 0 && strings.Trim(f[0], "=") == "" {
			rules++
			continue
		}
		if rules != 3 || len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue
		}
		addr, err := netip.ParseAddr(f[len(f)-2])
		if err != nil || !addr.Is4() {
			continue
		}
		metric, err := strconv.Atoi(f[len(f)-1])
		if err != nil {
			continue
		}
		r := DefaultRoute{Addr: addr, Metric: metric}
		if gw, err := netip.ParseAddr(f[2]); err == nil && gw.Is4() {
			r.Gateway = gw
		}
		out = append(out, r)
	}
	byMetric(out)
	return out
}
