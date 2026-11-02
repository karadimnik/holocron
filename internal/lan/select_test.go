package lan

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

const (
	lanFlags = net.FlagUp | net.FlagBroadcast | net.FlagMulticast
	p2pFlags = net.FlagUp | net.FlagPointToPoint | net.FlagMulticast
	loFlags  = net.FlagUp | net.FlagLoopback
)

func ia(name, cidr string, flags net.Flags) IfaceAddr {
	return IfaceAddr{Name: name, Addr: netip.MustParsePrefix(cidr), Flags: flags}
}

func byName(name, gateway string) DefaultRoute {
	return DefaultRoute{Interface: name, Gateway: netip.MustParseAddr(gateway)}
}

func byAddr(addr, gateway string) DefaultRoute {
	return DefaultRoute{Addr: netip.MustParseAddr(addr), Gateway: netip.MustParseAddr(gateway)}
}

// verdicts is "name:+" for a kept and "name:-" for a skipped interface, in order.
func verdicts(s Selection) string {
	var out []string
	for _, c := range s.Considered {
		v := "-"
		if c.Kept {
			v = "+"
		}
		out = append(out, c.Name+":"+v)
	}
	return strings.Join(out, " ")
}

func nets(s Selection) string {
	var out []string
	for _, l := range s.Nets {
		out = append(out, l.Net.String())
	}
	return strings.Join(out, ",")
}

func TestSelect(t *testing.T) {
	five := []IfaceAddr{
		ia("eth0", "192.168.1.9/24", lanFlags), ia("eth1", "192.168.2.9/24", lanFlags), ia("eth2", "192.168.3.9/24", lanFlags),
		ia("eth3", "192.168.4.9/24", lanFlags), ia("eth4", "192.168.5.9/24", lanFlags),
	}
	for name, c := range map[string]struct {
		ifs      []IfaceAddr
		routes   []DefaultRoute
		routeErr error
		nets     string // the networks scanned
		verdicts string
		notes    []string // each must appear in some note
		refusal  string   // a substring; "" when the selection must succeed
		reasons  map[string]string
	}{
		"docker host (WSL): the Docker bridges are dropped": {
			ifs: []IfaceAddr{
				ia("lo", "127.0.0.1/8", loFlags), ia("eth0", "192.168.1.9/24", lanFlags), ia("br-bce0347b83fb", "172.23.0.1/16", lanFlags),
				ia("br-edc1ab7a8d59", "172.25.0.1/16", lanFlags), ia("docker0", "172.17.0.1/16", lanFlags),
			},
			routes:   []DefaultRoute{byName("eth0", "192.168.1.1")},
			nets:     "192.168.1.0/24",
			verdicts: "lo:- eth0:+ br-bce0347b83fb:- br-edc1ab7a8d59:- docker0:-",
			reasons:  map[string]string{"eth0": "carries the default route", "br-bce0347b83fb": "virtual interface: Docker or Linux bridge", "docker0": "virtual interface: Docker bridge", "lo": "loopback"},
		},
		"macOS: bridge100, utun, awdl, llw, vmenet are dropped": {
			ifs: []IfaceAddr{
				ia("lo0", "127.0.0.1/8", loFlags), ia("en0", "192.168.1.33/24", lanFlags), ia("bridge100", "192.168.64.1/24", lanFlags),
				ia("utun3", "10.8.0.2/32", p2pFlags), ia("awdl0", "169.254.7.7/16", lanFlags), ia("llw0", "169.254.8.8/16", lanFlags),
				ia("vmenet0", "192.168.65.1/24", lanFlags), ia("bridge0", "192.168.70.1/24", lanFlags),
			},
			routes:   []DefaultRoute{byName("en0", "192.168.1.1"), {Interface: "utun3"}},
			nets:     "192.168.1.0/24,192.168.70.0/24",
			verdicts: "lo0:- en0:+ bridge100:- utun3:- awdl0:- llw0:- vmenet0:- bridge0:+",
			reasons:  map[string]string{"bridge100": "virtual interface: VM or Internet Sharing bridge", "utun3": "virtual interface: VPN or tunnel", "awdl0": "link-local address (169.254.x.x), not a LAN", "bridge0": "a LAN, not on the default route"},
		},
		"Windows: Hyper-V switches, VPN adapter and loopback are dropped, default route found by address": {
			ifs: []IfaceAddr{
				ia("Loopback Pseudo-Interface 1", "127.0.0.1/8", loFlags), ia("Ethernet", "192.168.1.33/24", lanFlags),
				ia("vEthernet (WSL (Hyper-V firewall))", "172.27.80.1/20", lanFlags), ia("vEthernet (Default Switch)", "172.28.16.1/20", lanFlags),
				ia("Contoso VPN", "10.8.0.2/24", lanFlags), ia("Local Area Connection* 3", "192.168.137.1/24", lanFlags),
			},
			routes:   []DefaultRoute{byAddr("192.168.1.33", "192.168.1.1")},
			nets:     "192.168.1.0/24",
			verdicts: "Loopback Pseudo-Interface 1:- Ethernet:+ vEthernet (WSL (Hyper-V firewall)):- vEthernet (Default Switch):- Contoso VPN:- Local Area Connection* 3:-",
			reasons:  map[string]string{"vEthernet (Default Switch)": "virtual interface: Hyper-V virtual switch", "Contoso VPN": "virtual interface: VPN", "Local Area Connection* 3": "virtual interface: Wi-Fi direct or hotspot"},
		},
		"two real LANs under the limit are both kept": {
			ifs:      []IfaceAddr{ia("Ethernet", "192.168.1.33/24", lanFlags), ia("Wi-Fi", "192.168.2.40/24", lanFlags)},
			routes:   []DefaultRoute{byAddr("192.168.2.40", "192.168.2.1")},
			nets:     "192.168.1.0/24,192.168.2.0/24",
			verdicts: "Ethernet:+ Wi-Fi:+",
			reasons:  map[string]string{"Ethernet": "a LAN, not on the default route", "Wi-Fi": "carries the default route"},
		},
		"five LANs over the limit narrow to the default-route one": {
			ifs:      five,
			routes:   []DefaultRoute{byName("eth2", "192.168.3.1")},
			nets:     "192.168.3.0/24",
			verdicts: "eth0:- eth1:- eth2:+ eth3:- eth4:-",
			notes:    []string{"only the default route's network 192.168.3.0/24 (eth2)", "192.168.1.0/24 (eth0)", "192.168.5.0/24 (eth4)", "--subnet"},
			reasons:  map[string]string{"eth0": "left out, over 1024 addresses in all: add it with --subnet 192.168.1.0/24"},
		},
		"over the limit with two default routes: the preferred one": {
			ifs:      five,
			routes:   []DefaultRoute{byName("eth4", "192.168.5.1"), byName("eth1", "192.168.2.1")},
			nets:     "192.168.5.0/24",
			verdicts: "eth0:- eth1:- eth2:- eth3:- eth4:+",
		},
		"default route unreadable: every physical-looking LAN": {
			ifs:      []IfaceAddr{ia("eth0", "192.168.1.9/24", lanFlags), ia("docker0", "172.17.0.1/16", lanFlags), ia("eth1", "192.168.2.9/24", lanFlags)},
			routeErr: errors.New("open /proc/net/route: denied"),
			nets:     "192.168.1.0/24,192.168.2.0/24",
			verdicts: "eth0:+ docker0:- eth1:+",
			notes:    []string{"default route cannot be read (open /proc/net/route: denied)"},
			reasons:  map[string]string{"eth0": "a physical-looking LAN (default route unknown)"},
		},
		"no default route at all is the same as unreadable": {
			ifs:      []IfaceAddr{ia("eth0", "192.168.1.9/24", lanFlags)},
			nets:     "192.168.1.0/24",
			verdicts: "eth0:+",
			notes:    []string{"this host has no default route"},
		},
		"over the limit and no default route: refuse": {
			ifs:      five,
			routeErr: errors.New("no reader"),
			verdicts: "eth0:- eth1:- eth2:- eth3:- eth4:-",
			refusal:  "hold 1280 addresses, more than one run probes (1024), and none of them is on the default route to choose it (the default route cannot be read: no reader)",
		},
		"over the limit and the default route is on a dropped interface: refuse": {
			ifs:      append(append([]IfaceAddr{}, five...), ia("utun2", "10.8.0.2/32", p2pFlags)),
			routes:   []DefaultRoute{{Interface: "utun2"}},
			verdicts: "eth0:- eth1:- eth2:- eth3:- eth4:- utun2:-",
			refusal:  "(the default route leaves by an interface that is not one of them)",
		},
		"nothing usable: refuse and name what was looked at": {
			ifs:      []IfaceAddr{ia("lo", "127.0.0.1/8", loFlags), ia("docker0", "172.17.0.1/16", lanFlags), ia("eth0", "203.0.113.5/24", lanFlags), ia("wlan0", "169.254.3.4/16", lanFlags)},
			routes:   []DefaultRoute{byName("eth0", "203.0.113.1")},
			verdicts: "lo:- docker0:- eth0:- wlan0:-",
			refusal:  "no interface is on a private IPv4 LAN",
			reasons:  map[string]string{"eth0": "not a private network (10.x, 172.16-31.x, 192.168.x)"},
		},
		"no interfaces at all": {
			refusal: "no interface is on a private IPv4 LAN",
		},
		"full-tunnel VPN owns the default route: the physical LAN is still scanned": {
			ifs:      []IfaceAddr{ia("wlan0", "192.168.1.9/24", lanFlags), ia("tun0", "10.8.0.2/24", lanFlags)},
			routes:   []DefaultRoute{{Interface: "tun0"}, byName("wlan0", "192.168.1.1")},
			nets:     "192.168.1.0/24",
			verdicts: "wlan0:+ tun0:-",
		},
		"Ethernet and Wi-Fi on one network are scanned once": {
			ifs:      []IfaceAddr{ia("eth0", "192.168.1.9/24", lanFlags), ia("wlan0", "192.168.1.10/24", lanFlags)},
			routes:   []DefaultRoute{byName("wlan0", "192.168.1.1")},
			nets:     "192.168.1.0/24",
			verdicts: "eth0:+ wlan0:+",
			reasons:  map[string]string{"eth0": "carries the default route", "wlan0": "same network as eth0, scanned once"},
		},
		"a LAN wider than a /24 is narrowed": {
			ifs:      []IfaceAddr{ia("eth0", "10.1.5.20/16", lanFlags)},
			routes:   []DefaultRoute{byName("eth0", "10.1.0.1")},
			nets:     "10.1.5.0/24",
			verdicts: "eth0:+",
			notes:    []string{"10.1.0.0/16 is wider than a /24: only 10.1.5.0/24 (around this host's 10.1.5.20) is scanned"},
		},
		"flags: point-to-point and no broadcast are dropped whatever the name": {
			ifs:      []IfaceAddr{ia("uplink0", "192.168.9.2/24", p2pFlags), ia("eth7", "192.168.8.2/24", net.FlagUp|net.FlagMulticast), ia("eth0", "192.168.1.9/24", lanFlags)},
			routes:   []DefaultRoute{byName("eth0", "192.168.1.1")},
			nets:     "192.168.1.0/24",
			verdicts: "uplink0:- eth7:- eth0:+",
			reasons:  map[string]string{"uplink0": "point-to-point link (VPN or tunnel)", "eth7": "no broadcast, so not a LAN"},
		},
	} {
		s := Select(c.ifs, c.routes, c.routeErr)
		if c.refusal == "" {
			if s.Refusal != "" {
				t.Errorf("%s: refused: %s", name, s.Refusal)
				continue
			}
		} else if !strings.Contains(s.Refusal, c.refusal) || len(s.Nets) != 0 {
			t.Errorf("%s: refusal %q (nets %v), want it to contain %q", name, s.Refusal, s.Nets, c.refusal)
		}
		if got := nets(s); got != c.nets {
			t.Errorf("%s: scans %q, want %q", name, got, c.nets)
		}
		if got := verdicts(s); got != c.verdicts {
			t.Errorf("%s: verdicts %q, want %q", name, got, c.verdicts)
		}
		for _, want := range c.notes {
			if !strings.Contains(strings.Join(s.Notes, "\n"), want) {
				t.Errorf("%s: no note contains %q: %q", name, want, s.Notes)
			}
		}
		for iface, want := range c.reasons {
			found := false
			for _, k := range s.Considered {
				if k.Name == iface {
					found = true
					if k.Reason != want {
						t.Errorf("%s: %s reason %q, want %q", name, iface, k.Reason, want)
					}
				}
			}
			if !found {
				t.Errorf("%s: %s not considered", name, iface)
			}
		}
		for _, k := range s.Considered {
			if k.Reason == "" || !k.CIDR.IsValid() {
				t.Errorf("%s: %+v has no reason or network", name, k)
			}
		}
	}
}

// The selection never exceeds what Hosts accepts, so an automatic run cannot hit the limit.
func TestSelectFitsTheLimit(t *testing.T) {
	var ifs []IfaceAddr
	for i := 1; i <= 9; i++ {
		ifs = append(ifs, ia("eth"+string(rune('0'+i)), "10.0."+string(rune('0'+i))+".5/24", lanFlags))
	}
	s := Select(ifs, []DefaultRoute{byName("eth3", "10.0.3.1")}, nil)
	var ps []netip.Prefix
	for _, l := range s.Nets {
		ps = append(ps, l.Net)
	}
	if _, err := Hosts(ps, nil); err != nil || len(ps) != 1 {
		t.Errorf("%v %v", ps, err)
	}
	four := Select(ifs[:4], nil, errors.New("x"))
	ps = nil
	for _, l := range four.Nets {
		ps = append(ps, l.Net)
	}
	if _, err := Hosts(ps, nil); err != nil || len(ps) != 4 {
		t.Errorf("four /24 are exactly the limit: %v %v", ps, err)
	}
}
