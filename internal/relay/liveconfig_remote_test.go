package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LiveAdvertiseRemoteHost adds web.remoteHost, as a quoted name, after 127.0.0.1 and the LAN
// addresses; nothing else in the live config changes. These goldens are Holocron's own (the
// PowerShell system never had the setting): live/remote-host*.yml are the inputs of live/lan.yml
// and of a single bound address, with the setting on.
func TestLiveConfigAdvertisesTheRemoteHost(t *testing.T) {
	g := loadGolden(t)
	lan := map[string]json.RawMessage{
		"LiveAdvertiseHosts": json.RawMessage(`["192.0.2.50","192.0.2.51"]`), "LiveUdpAddress": json.RawMessage(`"0.0.0.0:18189"`),
		"LiveViewEnabled": json.RawMessage("true"), "LiveWhepAddress": json.RawMessage(`"127.0.0.1:18889"`),
	}
	bound := map[string]json.RawMessage{
		"LiveAdvertiseHosts": json.RawMessage(`["192.0.2.50"]`), "LiveUdpAddress": json.RawMessage(`"192.0.2.50:18189"`),
		"LiveViewEnabled": json.RawMessage("true"), "LiveWhepAddress": json.RawMessage(`"127.0.0.1:18889"`),
	}
	for _, c := range []struct {
		file     string
		settings map[string]json.RawMessage
		hosts    string
	}{
		{"live/remote-host.yml", lan, `webrtcAdditionalHosts: ["127.0.0.1", "192.0.2.50", "192.0.2.51", "remote.example.org"]`},
		{"live/remote-host-bound.yml", bound, `webrtcAdditionalHosts: ["127.0.0.1", "192.0.2.50", "remote.example.org"]`},
	} {
		t.Run(c.file, func(t *testing.T) {
			cfg := configFor(t, c.settings)
			off, err := LiveConfig(LiveInputs{Config: cfg, Cameras: g.cameras(), Login: Login{User: g.LiveLogin.User, Secret: g.LiveLogin.Secret}})
			if err != nil {
				t.Fatal(err)
			}
			cfg.Web.RemoteHost = "remote.example.org"
			if again, _ := LiveConfig(LiveInputs{Config: cfg, Cameras: g.cameras(), Login: Login{User: g.LiveLogin.User, Secret: g.LiveLogin.Secret}}); !bytes.Equal(again, off) {
				t.Error("web.remoteHost alone changed the live config")
			}
			cfg.LiveAdvertiseRemoteHost = true
			on, err := LiveConfig(LiveInputs{Config: cfg, Cameras: g.cameras(), Login: Login{User: g.LiveLogin.User, Secret: g.LiveLogin.Secret}})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(goldenRoot, filepath.FromSlash(c.file)))
			if err != nil {
				t.Fatal(err)
			}
			if d := firstDifference(on, want); d != "" {
				t.Errorf("differs from %s: %s", c.file, d)
			}
			// exactly one line differs from the same config with the setting off, and it is the hosts line
			a, b := strings.Split(string(off), "\r\n"), strings.Split(string(on), "\r\n")
			if len(a) != len(b) {
				t.Fatalf("%d lines with the setting off, %d with it on", len(a), len(b))
			}
			var changed []string
			for i := range a {
				if a[i] != b[i] {
					changed = append(changed, b[i])
				}
			}
			if len(changed) != 1 || changed[0] != c.hosts {
				t.Errorf("changed lines %q, want only %q", changed, c.hosts)
			}
		})
	}
	// the lan variant with the setting off is the PowerShell golden itself
	want, _ := os.ReadFile(filepath.Join(goldenRoot, "live", "lan.yml"))
	if d := firstDifference(g.generate(t, "live", lan), want); d != "" {
		t.Errorf("setting off: differs from live/lan.yml: %s", d)
	}
}
