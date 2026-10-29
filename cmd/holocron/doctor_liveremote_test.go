package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

// live.remote: whether a viewer outside the home network is offered a public address for video.
// Silent without remote access or live view; the DNS lookup happens only with --deep.
func TestDoctorLiveRemote(t *testing.T) {
	const lanUDP = `"LiveUdpAddress": "0.0.0.0:8189", "LiveAdvertiseHosts": ["192.168.1.50"],`
	for _, c := range []struct {
		name, remote, live string
		deep               bool
		ips                []net.IP
		lookupErr          error
		want               []string // "level detail" fragments, in order, of the live.remote lines
		fix                []string
	}{
		{name: "no remote access", live: `"LiveViewEnabled": true, ` + lanUDP + ` "LiveAdvertiseRemoteHost": false,`},
		{name: "live view off", remote: "remote.example.org", live: lanUDP},
		{name: "not advertised, loopback media", remote: "remote.example.org", live: `"LiveViewEnabled": true,`,
			want: []string{"warn remote access is on for remote.example.org, but a viewer outside the home network cannot receive video: no public address is offered (LiveAdvertiseRemoteHost is false)"},
			fix:  []string{`set "LiveUdpAddress": "0.0.0.0:8189", "LiveAdvertiseHosts": ["<this PC's LAN IPv4>"] and "LiveAdvertiseRemoteHost": true (loopback 127.0.0.1:8189 cannot be forwarded), then holocron restart --detach`}},
		{name: "not advertised, LAN media", remote: "remote.example.org", live: `"LiveViewEnabled": true, ` + lanUDP,
			want: []string{"warn remote access is on for remote.example.org, but a viewer outside"},
			fix:  []string{"holocron config set LiveAdvertiseRemoteHost true, then holocron restart --detach ; see docs/ui.md"}},
		{name: "advertised", remote: "remote.example.org", live: `"LiveViewEnabled": true, "LiveAdvertiseRemoteHost": true, ` + lanUDP,
			want: []string{"info offered to viewers outside the home network as remote.example.org, media on UDP 8189: the router must forward UDP external port 8189 (the same number) to this PC's LAN address port 8189, and Windows Firewall must allow it inbound from any address; the doctor cannot see the router"}},
		{name: "advertised, bound address, deep", remote: "remote.example.org", deep: true, ips: []net.IP{net.ParseIP("203.0.113.7")},
			live: `"LiveViewEnabled": true, "LiveAdvertiseRemoteHost": true, "LiveUdpAddress": "192.168.1.50:8189", "LiveAdvertiseHosts": ["192.168.1.50"],`,
			want: []string{"info offered to viewers outside the home network as remote.example.org, media on UDP 8189: the router must forward UDP external port 8189 (the same number) to 192.168.1.50 port 8189",
				"ok   remote.example.org resolves on this PC to 203.0.113.7"}},
		{name: "deep, does not resolve", remote: "remote.example.org", deep: true, lookupErr: errors.New("no such host"),
			live: `"LiveViewEnabled": true, "LiveAdvertiseRemoteHost": true, ` + lanUDP,
			want: []string{"info offered", "warn remote.example.org does not resolve on this PC (no such host): the live instance skips it and offers no public address"},
			fix:  []string{"nslookup remote.example.org"}},
		{name: "deep, private only", remote: "remote.example.org", deep: true, ips: []net.IP{net.ParseIP("192.168.1.1"), net.ParseIP("100.64.0.9")},
			live: `"LiveViewEnabled": true, "LiveAdvertiseRemoteHost": true, ` + lanUDP,
			want: []string{"info offered", "warn remote.example.org resolves on this PC to 192.168.1.1, 100.64.0.9 only, none of them public"},
			fix:  []string{"make remote.example.org resolve to this home's public address on this PC too"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			remoteRigConfig(t, r, c.remote)
			r.setConfig(c.live)
			r.liveRunning()
			lookups := 0
			r.doctorTools(func(d *doctorDeps) {
				d.http = &http.Client{Transport: refuseRT{}}
				d.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
					lookups++
					if _, ok := ctx.Deadline(); !ok || host != c.remote {
						t.Errorf("lookup of %q, deadline %v", host, ok)
					}
					return c.ips, c.lookupErr
				}
			})
			args := []string{"--json", "doctor", "--group", "live"}
			if c.deep {
				args = append(args, "--deep")
			}
			out, _, _ := r.run(args...)
			var rep doctorReport
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var got, fixes []string
			for _, ch := range rep.Checks {
				if ch.ID == "live.remote" {
					got = append(got, padRight(ch.Level, 4)+" "+ch.Detail)
					fixes = append(fixes, ch.Fix)
				}
			}
			if len(got) != len(c.want) {
				t.Fatalf("live.remote lines %q, want %d", got, len(c.want))
			}
			for i, w := range c.want {
				if !strings.HasPrefix(got[i], w) {
					t.Errorf("line %d %q, want prefix %q", i, got[i], w)
				}
			}
			for _, f := range c.fix {
				if !strings.Contains(strings.Join(fixes, "\n"), f) {
					t.Errorf("fixes %q lack %q", fixes, f)
				}
			}
			if want := map[bool]int{true: 1, false: 0}[c.deep]; lookups != want {
				t.Errorf("%d lookups, want %d", lookups, want)
			}
		})
	}
}
