package web

import (
	"slices"
	"testing"
)

const remoteName = "remote.example.org"

func withRemote(c *Config) { c.RemoteHost = remoteName }

// The HTTPS listener answers to the remote access name (any case, any port) only while RemoteHost
// is set, and the server certificate's names do not change: the CA cannot vouch for a public name.
func TestRemoteHostIsAllowedOnlyWhenSet(t *testing.T) {
	for _, host := range []string{remoteName, remoteName + ":8443", "REMOTE.Example.Org:443"} {
		on := newTLSRig(t, withRemote).do(tlsReq{path: "/login", host: host})
		off := newTLSRig(t, nil).do(tlsReq{path: "/login", host: host})
		if on.Code != 200 || off.Code != 403 {
			t.Errorf("%s: on %d, off %d", host, on.Code, off.Code)
		}
	}
	if rig := newTLSRig(t, withRemote); rig.do(tlsReq{path: "/login", host: "other.example.org"}).Code != 403 {
		t.Error("another public name was allowed")
	}
	rig := newTLSRig(t, withRemote)
	leaf, err := rig.mgr.GetCertificate(nil)
	if err != nil || slices.Contains(leaf.Leaf.DNSNames, remoteName) {
		t.Errorf("certificate names %v %v", leaf.Leaf.DNSNames, err)
	}
}
