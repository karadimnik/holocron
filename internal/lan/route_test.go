package lan

import (
	"fmt"
	"testing"
)

func routeText(rs []DefaultRoute) string {
	s := ""
	for _, r := range rs {
		name, gw, addr := "-", "-", "-"
		if r.Interface != "" {
			name = r.Interface
		}
		if r.Gateway.IsValid() {
			gw = r.Gateway.String()
		}
		if r.Addr.IsValid() {
			addr = r.Addr.String()
		}
		s += fmt.Sprintf("[%s %s via %s metric %d]", name, addr, gw, r.Metric)
	}
	return s
}

const procNetRouteDocker = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"eth0\t00000000\t0101A8C0\t0003\t0\t0\t1002\t00000000\t0\t0\t0\n" +
	"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
	"br-bce0347b83fb\t000017AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
	"eth0\t0001A8C0\t00000000\t0001\t0\t0\t1002\t00FFFFFF\t0\t0\t0\n"

const procNetRouteTwo = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"wlan0\t00000000\t0102A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
	"eth1\t00000000\t0100000A\t0002\t0\t0\t50\t00000000\t0\t0\t0\n" + // not up: ignored
	"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
	"tun0\t00000000\t00000000\t0001\t0\t0\t700\t00000000\t0\t0\t0\n" // on-link default of a VPN

func TestParseProcNetRoute(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"docker host":         {procNetRouteDocker, "[eth0 - via 192.168.1.1 metric 1002]"},
		"two defaults":        {procNetRouteTwo, "[eth0 - via 192.168.1.1 metric 100][wlan0 - via 192.168.2.1 metric 600][tun0 - via - metric 700]"},
		"no default":          {"Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\nlo\t0000007F\t00000000\t0001\t0\t0\t0\t000000FF\t0\t0\t0\n", ""},
		"empty and truncated": {"", ""},
	} {
		if got := routeText(ParseProcNetRoute(c.in)); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

const netstatMac = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            link#25            UCSIg               utun4
default            192.168.1.1        UGScg                 en0
default            link#18            UCSIg           bridge100
127                127.0.0.1          UCS                   lo0
192.168.1/24       link#6             UCS                   en0      !
192.168.64         link#18            UC              bridge100      !
`

const netstatMacOld = `Routing tables

Internet:
Destination        Gateway            Flags    Refs      Use   Netif Expire
default            192.168.1.1        UGSc       41        0     en0
127                127.0.0.1          UCS         0        0     lo0
`

const routeGetMac = `   route to: default
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`

func TestParseMacRoutes(t *testing.T) {
	for name, c := range map[string]struct {
		got  []DefaultRoute
		want string
	}{
		"netstat, scoped last":  {ParseNetstatInet(netstatMac), "[en0 - via 192.168.1.1 metric 0][utun4 - via - metric 0][bridge100 - via - metric 0]"},
		"netstat, old columns":  {ParseNetstatInet(netstatMacOld), "[en0 - via 192.168.1.1 metric 0]"},
		"netstat, none":         {ParseNetstatInet("Routing tables\n\nInternet:\nDestination Gateway Flags Netif Expire\n127 127.0.0.1 UCS lo0\n"), ""},
		"route get default":     {ParseRouteGet(routeGetMac), "[en0 - via 192.168.1.1 metric 0]"},
		"route get, no route":   {ParseRouteGet("route: writing to routing socket: not in table\n"), ""},
		"route get, no gateway": {ParseRouteGet("   route to: default\n  interface: utun4\n"), "[utun4 - via - metric 0]"},
	} {
		if got := routeText(c.got); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

const routePrintEnglish = `===========================================================================
Interface List
 17...00 15 5d 8a 3b 01 ......Hyper-V Virtual Ethernet Adapter
  8...a4 bb 6d 11 22 33 ......Intel(R) Ethernet Connection I219-V
  1...........................Software Loopback Interface 1
===========================================================================

IPv4 Route Table
===========================================================================
Active Routes:
Network Destination        Netmask          Gateway       Interface  Metric
          0.0.0.0          0.0.0.0      192.168.1.1    192.168.1.33     25
          0.0.0.0          0.0.0.0          On-link       10.8.0.2     281
        127.0.0.0        255.0.0.0         On-link         127.0.0.1    331
      172.27.80.0    255.255.240.0         On-link       172.27.80.1    271
===========================================================================
Persistent Routes:
  Network Address          Netmask  Gateway Address  Metric
          0.0.0.0          0.0.0.0      192.168.9.1       5
===========================================================================
`

const routePrintGerman = `===========================================================================
Schnittstellenliste
  8...a4 bb 6d 11 22 33 ......Intel(R) Ethernet Connection I219-V
  1...........................Software Loopback Interface 1
===========================================================================

IPv4-Routentabelle
===========================================================================
Aktive Routen:
     Netzwerkziel    Netzwerkmaske          Gateway   Schnittstelle Metrik
          0.0.0.0          0.0.0.0      192.168.1.1    192.168.1.33     25
          0.0.0.0          0.0.0.0   Auf Verbindung       10.8.0.2    281
===========================================================================
St` + "ä" + `ndige Routen:
  Netzwerkadresse    Netzmaske  Gatewayadresse  Metrik
          0.0.0.0          0.0.0.0      192.168.9.1  Standard
===========================================================================
`

func TestParseWindowsRoutePrint(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"english, persistent default ignored": {routePrintEnglish, "[- 192.168.1.33 via 192.168.1.1 metric 25][- 10.8.0.2 via - metric 281]"},
		"german, on-link in two words":        {routePrintGerman, "[- 192.168.1.33 via 192.168.1.1 metric 25][- 10.8.0.2 via - metric 281]"},
		"no default":                          {"=====\nIPv4 Route Table\n=====\nActive Routes:\n127.0.0.0 255.0.0.0 On-link 127.0.0.1 331\n=====\n", ""},
		"empty":                               {"", ""},
	} {
		if got := routeText(ParseWindowsRoutePrint(c.in)); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}
