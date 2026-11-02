package lan

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestParseSubnet(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.0/24": "192.168.1.0/24", "192.168.1.77/24": "192.168.1.0/24", " 10.1.2.0/22 ": "10.1.0.0/22",
		"172.16.5.0/23": "172.16.4.0/23", "192.168.1.50/32": "192.168.1.50/32",
	} {
		got, err := ParseSubnet(in, false)
		if err != nil || got.String() != want {
			t.Errorf("ParseSubnet(%q) = %v %v, want %s", in, got, err, want)
		}
	}
	for in, why := range map[string]string{
		"192.168.0.0/21": "at most 1024", "10.0.0.0/8": "at most 1024", "8.8.8.0/24": "not a private network",
		"172.32.0.0/24": "not a private network", "169.254.1.0/24": "not a private network", "127.0.0.1/32": "not a private network",
		"100.64.0.0/24": "not a private network", "fd00::/64": "not an IPv4 subnet", "192.168.1.0": "CIDR", "x": "CIDR",
	} {
		if _, err := ParseSubnet(in, false); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("ParseSubnet(%q): %v, want %q", in, err, why)
		}
	}
	if p, err := ParseSubnet("127.0.0.1/32", true); err != nil || p.String() != "127.0.0.1/32" {
		t.Errorf("loopback with the test hook: %v %v", p, err)
	}
	if _, err := ParseSubnet("8.8.8.0/24", true); err == nil {
		t.Error("the loopback hook admitted a public network")
	}
}

func TestHostsAndLimits(t *testing.T) {
	pfx := func(s ...string) []netip.Prefix {
		var out []netip.Prefix
		for _, x := range s {
			out = append(out, netip.MustParsePrefix(x))
		}
		return out
	}
	h, err := Hosts(pfx("192.168.1.0/24"), map[netip.Addr]bool{netip.MustParseAddr("192.168.1.10"): true})
	if err != nil || len(h) != 253 || h[0].String() != "192.168.1.1" || h[len(h)-1].String() != "192.168.1.254" {
		t.Errorf("/24: %d %v", len(h), err)
	}
	for _, a := range h {
		if a.String() == "192.168.1.10" {
			t.Error("this host's own address is probed")
		}
	}
	if h, err := Hosts(pfx("10.0.0.0/22"), nil); err != nil || len(h) != 1022 {
		t.Errorf("/22: %d %v", len(h), err)
	}
	if h, err := Hosts(pfx("192.168.1.0/23", "192.168.4.0/23"), nil); err != nil || len(h) != 1020 {
		t.Errorf("two /23: %d %v", len(h), err)
	}
	if _, err := Hosts(pfx("192.168.1.0/22", "192.168.8.0/24"), nil); err == nil || !strings.Contains(err.Error(), "1280 addresses") {
		t.Errorf("over a /22 in total: %v", err)
	}
	if h, _ := Hosts(pfx("192.168.1.50/32", "192.168.1.60/31"), nil); len(h) != 3 {
		t.Errorf("/32 and /31 keep every address: %v", h)
	}
	if h, _ := Hosts(pfx("192.168.1.0/24", "192.168.1.0/25"), nil); len(h) != 254 {
		t.Errorf("overlap probed twice: %d", len(h))
	}
}

func TestLocalSubnets(t *testing.T) {
	in := []netip.Prefix{
		netip.MustParsePrefix("192.168.1.10/24"),
		netip.MustParsePrefix("192.168.1.11/24"),  // a second interface on the same network
		netip.MustParsePrefix("172.27.80.1/20"),   // a virtual switch: narrowed
		netip.MustParsePrefix("169.254.10.2/16"),  // link-local: never
		netip.MustParsePrefix("203.0.113.5/24"),   // public: never
		netip.MustParsePrefix("100.101.102.3/32"), // CGNAT/VPN: not RFC 1918
		netip.MustParsePrefix("fd00::5/64"),
	}
	got := LocalSubnets(in)
	if len(got) != 2 || got[0].Net.String() != "192.168.1.0/24" || got[0].Self.String() != "192.168.1.10" || got[0].Narrowed.IsValid() ||
		got[1].Net.String() != "172.27.80.0/24" || got[1].Narrowed.String() != "172.27.80.0/20" {
		t.Errorf("%+v", got)
	}
}

func ips(ns []Neighbour) map[string]string {
	out := map[string]string{}
	for _, n := range ns {
		out[n.IP.String()] = n.MAC
	}
	return out
}

// The Linux samples keep the layout captured from a real /proc/net/arp and `ip neigh show` (WSL2,
// 2026-11-03) with invented addresses; the incomplete and failed rows follow proc_pid_net(5) and
// iproute2's ipneigh.c.
const procNetARP = `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.40     0x1         0x2         38:26:56:aa:11:bf     *        eth0
192.168.1.254    0x1         0x2         94:4A:0C:00:9B:00     *        eth0
172.23.0.2       0x1         0x2         02:42:ac:17:00:02     *        br-0347b83f
192.168.1.99     0x1         0x0         00:00:00:00:00:00     *        eth0
192.168.1.98     0x1         0xc         00:00:00:00:00:00     *        eth0
192.168.1.97     0x1         0x6         01:00:5e:00:00:16     *        eth0
`

const ipNeigh = `192.168.1.40 dev eth0 lladdr 38:26:56:aa:11:bf STALE
192.168.1.254 dev eth0 lladdr 94:4a:0c:00:9b:00 DELAY
192.168.1.1 dev eth0 lladdr 04:71:53:00:e6:80 router REACHABLE
192.168.1.2 dev eth0 lladdr 00:26:55:d0:13:2a PROBE
192.168.1.99 dev eth0  INCOMPLETE
192.168.1.98 dev eth0 FAILED
192.168.1.97 dev eth0 lladdr 00:26:55:d0:13:99 FAILED
fe80::1 dev eth0 lladdr 11:22:33:44:55:66 router STALE
`

// macOS: the layout of Apple's arp.c (network_cmds) print routine: no header, the MAC printed with
// %x so leading zeros are dropped, "(incomplete)" without a MAC. Not captured on a Mac (none here).
const bsdArp = `? (192.168.1.1) at 90:72:40:b:dc:a1 on en0 ifscope [ethernet]
? (192.168.1.50) at 0:1b:2:c:d4:5 on en0 ifscope [ethernet]
? (192.168.1.99) at (incomplete) on en0 ifscope [ethernet]
? (192.168.1.255) at ff:ff:ff:ff:ff:ff on en0 ifscope permanent [ethernet]
? (224.0.0.251) at 1:0:5e:0:0:fb on en0 ifscope permanent [ethernet]
cam.lan (192.168.1.51) at aa:bb:cc:dd:ee:1 on en0 ifscope permanent [ethernet]
`

// Windows: the layout documented for arp -a (English, and German from a captured Windows 7 output);
// not captured on the Operator's PC from this sandbox.
const winArp = `
Interface: 192.168.1.100 --- 0x4
  Internet Address      Physical Address      Type
  192.168.1.1           04-71-53-00-e6-80     dynamic
  192.168.1.50          AA-BB-CC-DD-EE-01     dynamic
  192.168.1.255         ff-ff-ff-ff-ff-ff     static
  224.0.0.22            01-00-5e-00-00-16     static
  239.255.255.250       01-00-5e-7f-ff-fa     static

Schnittstelle: 172.27.80.1 --- 0x1a
  Internetadresse       Physische Adresse     Typ
  172.27.80.5           00-15-5d-01-02-03     dynamisch
  172.27.80.6           00-00-00-00-00-00     ungültig
`

func TestNeighbourParsers(t *testing.T) {
	for name, c := range map[string]struct {
		got  []Neighbour
		want map[string]string
	}{
		"proc": {ParseProcNetARP(procNetARP), map[string]string{"192.168.1.40": "38:26:56:aa:11:bf", "192.168.1.254": "94:4a:0c:00:9b:00", "172.23.0.2": "02:42:ac:17:00:02"}},
		"ip":   {ParseIPNeigh(ipNeigh), map[string]string{"192.168.1.40": "38:26:56:aa:11:bf", "192.168.1.254": "94:4a:0c:00:9b:00", "192.168.1.1": "04:71:53:00:e6:80", "192.168.1.2": "00:26:55:d0:13:2a"}},
		"bsd":  {ParseBSDArp(bsdArp), map[string]string{"192.168.1.1": "90:72:40:0b:dc:a1", "192.168.1.50": "00:1b:02:0c:d4:05", "192.168.1.51": "aa:bb:cc:dd:ee:01"}},
		"win":  {ParseWindowsArp(winArp), map[string]string{"192.168.1.1": "04:71:53:00:e6:80", "192.168.1.50": "aa:bb:cc:dd:ee:01", "172.27.80.5": "00:15:5d:01:02:03"}},
	} {
		if got := ips(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, c.want)
		}
	}
	if len(ParseProcNetARP("")) != 0 || len(ParseWindowsArp("No ARP Entries Found.\n")) != 0 {
		t.Error("an empty table is not empty")
	}
}
