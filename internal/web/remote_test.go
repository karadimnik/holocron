package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const remoteName = "remote.example.org"

func withRemote(c *Config) { c.RemoteHost = remoteName }

// The HTTPS listener answers to the remote access name (any case, any port) only while RemoteHost
// is set, and the LAN certificate's names do not change: the remote name has its own CA and leaf
// (tlsremote_test.go).
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

func TestPeerIsRemote(t *testing.T) {
	for ip, want := range map[string]bool{
		"203.0.113.7": true, "8.8.8.8": true, "172.32.0.1": true, "100.128.0.1": true, "2001:db8::1": true,
		"::ffff:203.0.113.7": true, "224.0.0.1": true, "": true, "garbage": true, "203.0.113.7:5000": true,
		"192.168.1.20": false, "10.1.2.3": false, "172.16.0.1": false, "127.0.0.1": false, "::1": false,
		"100.64.1.2": false, "100.127.255.254": false, "169.254.1.1": false, "fd00::1": false, "fc00::1": false,
		"fe80::1": false, "fe80::1%eth0": false, "::ffff:192.168.1.20": false, "0.0.0.0": false, "::": false,
	} {
		if got := PeerIsRemote(ip); got != want {
			t.Errorf("PeerIsRemote(%q) = %v, want %v", ip, got, want)
		}
	}
}

const (
	publicPeer = "203.0.113.7:50000"
	lanPeer    = "192.168.1.20:50000"
	viewerName = "vera"
)

func remoteRig(t *testing.T, mutate func(*Config)) *tlsRig {
	t.Helper()
	rig := newTLSRig(t, func(c *Config) {
		c.RemoteHost = remoteName
		if mutate != nil {
			mutate(c)
		}
	})
	hash, _ := fastHasher().Hash(testPassword)
	if err := rig.users.Put(User{ID: "u-vera", Username: viewerName, Hash: hash, Role: roleViewer, CreatedAt: rig.clock.now()}); err != nil {
		t.Fatal(err)
	}
	return rig
}

func readWrite(c *Config) { f := false; c.TLSReadOnly = &f }

// signInFrom signs name in from peer with Host host and returns the cookie and the role the answer names.
func (rig *tlsRig) signInFrom(t *testing.T, name, peer, host string) (string, string) {
	t.Helper()
	rec := rig.do(tlsReq{method: "POST", path: "/api/auth/login", host: host, remote: peer, headers: jsonHdr, body: loginBody(name, testPassword)})
	var body struct{ Role string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("login %s from %s: %d %s", name, peer, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value, body.Role
		}
	}
	t.Fatal("no session cookie")
	return "", ""
}

func meRole(t *testing.T, rig *tlsRig, cookie, peer, host string) string {
	t.Helper()
	rec := rig.do(tlsReq{path: "/api/auth/me", host: host, remote: peer, cookie: cookie})
	var body struct{ Role string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Role
}

// An admin account signing in from a public peer gets only the viewer routes; from a private peer
// it keeps today's behaviour; with RemoteHost off a public peer is treated as today too.
func TestRemoteAdminIsCappedToViewer(t *testing.T) {
	rig := remoteRig(t, readWrite)
	cookie, role := rig.signInFrom(t, testUser, publicPeer, remoteName)
	if role != roleViewer || meRole(t, rig, cookie, publicPeer, remoteName) != roleViewer {
		t.Errorf("remote admin's role %q / me %q", role, meRole(t, rig, cookie, publicPeer, remoteName))
	}
	admin := []tlsReq{
		{method: "GET", path: "/api/admin/users"}, {method: "GET", path: "/api/admin/config"}, {method: "GET", path: "/admin.js"},
		{method: "GET", path: "/api/config"}, {method: "GET", path: "/api/cameras"},
		{method: "POST", path: "/api/control", headers: jsonHdr, body: "{}"},
		{method: "PUT", path: "/api/config", headers: jsonHdr, body: "{}"},
		{method: "POST", path: "/api/acknowledge", headers: jsonHdr, body: "{}"},
		{method: "POST", path: "/api/admin/users/add", headers: jsonHdr, body: "{}"},
		{method: "POST", path: "/api/admin/tls/rotate", headers: jsonHdr, body: "{}"},
	}
	for _, q := range admin {
		q.host, q.remote, q.cookie = remoteName, publicPeer, cookie
		if rec := rig.do(q); !isAdminRefusal(rec) {
			t.Errorf("remote admin %s %s: %d %s", q.method, q.path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/", "/api/status", "/api/live", "/api/footage", "/api/tls/info"} {
		if rec := rig.do(tlsReq{path: path, host: remoteName, remote: publicPeer, cookie: cookie}); rec.Code == 401 || rec.Code == 403 {
			t.Errorf("remote viewer route %s: %d", path, rec.Code)
		}
	}
	if !strings.Contains(rig.logs.String(), "remote access is viewer-only") || !strings.Contains(rig.logs.String(), "from=203.0.113.7") {
		t.Error("the remote cap left no audit record with the peer address")
	}

	lan, lanRole := rig.signInFrom(t, testUser, lanPeer, lanHost)
	if lanRole != roleAdmin || meRole(t, rig, lan, lanPeer, lanHost) != roleAdmin {
		t.Errorf("LAN admin's role %q", lanRole)
	}
	if rec := rig.do(tlsReq{path: "/api/admin/users", host: lanHost, remote: lanPeer, cookie: lan}); rec.Code != 200 {
		t.Errorf("LAN admin: %d", rec.Code)
	}
	// Hairpin NAT: a LAN device using the public name is a LAN request.
	if rec := rig.do(tlsReq{path: "/api/admin/users", host: remoteName, remote: lanPeer, cookie: lan}); rec.Code != 200 {
		t.Errorf("LAN admin via the public name: %d", rec.Code)
	}
	// The same cookie from a public peer is capped: the class follows the peer, not the session.
	if rec := rig.do(tlsReq{path: "/api/admin/users", host: remoteName, remote: publicPeer, cookie: lan}); !isAdminRefusal(rec) {
		t.Errorf("LAN session from a public peer: %d", rec.Code)
	}
	for _, peer := range []string{"127.0.0.1:1", "100.64.1.2:1", "[fd00::1]:1", "[fe80::1%eth0]:1", "169.254.1.1:1", "[::ffff:192.168.1.20]:1"} {
		if rec := rig.do(tlsReq{path: "/api/admin/users", host: lanHost, remote: peer, cookie: lan}); rec.Code != 200 {
			t.Errorf("private peer %s: %d", peer, rec.Code)
		}
	}
	for _, peer := range []string{"[2001:db8::1]:1", "[::ffff:203.0.113.7]:1", "garbage", "8.8.8.8:1"} {
		if rec := rig.do(tlsReq{path: "/api/admin/users", host: remoteName, remote: peer, cookie: lan}); !isAdminRefusal(rec) {
			t.Errorf("public or malformed peer %s: %d", peer, rec.Code)
		}
	}

	off := newTLSRig(t, readWrite)
	offCookie := off.login(t)
	if rec := off.do(tlsReq{path: "/api/admin/users", remote: publicPeer, cookie: offCookie}); rec.Code != 200 {
		t.Errorf("RemoteHost off, public peer: %d (today's behaviour is 200)", rec.Code)
	}
}

// Forwarding headers and the Host header never make a public peer local.
func TestRemoteClassIgnoresHeaders(t *testing.T) {
	rig := remoteRig(t, readWrite)
	cookie, _ := rig.signInFrom(t, testUser, publicPeer, remoteName)
	spoof := map[string]string{"X-Forwarded-For": "192.168.1.20", "X-Real-IP": "127.0.0.1", "Forwarded": "for=192.168.1.20", "X-Forwarded-Host": lanHost, "True-Client-IP": "10.0.0.1"}
	if rec := rig.do(tlsReq{path: "/api/admin/users", host: remoteName, remote: publicPeer, cookie: cookie, headers: spoof}); !isAdminRefusal(rec) {
		t.Errorf("spoofed headers: %d", rec.Code)
	}
	for _, host := range []string{lanHost, lanIP, "localhost", "127.0.0.1:8443", "other.example.org"} {
		if rec := rig.do(tlsReq{path: "/api/admin/users", host: host, remote: publicPeer, cookie: cookie, headers: spoof}); rec.Code != 403 {
			t.Errorf("public peer with Host %s: %d", host, rec.Code)
		}
		if rec := rig.do(tlsReq{path: "/login", host: host, remote: publicPeer}); rec.Code != 403 {
			t.Errorf("public peer, login page with Host %s: %d", host, rec.Code)
		}
	}
}

// The view-only allow-list applies to remote requests even with tlsReadOnly false.
func TestRemoteIsViewOnlyEvenWhenReadWrite(t *testing.T) {
	rig := remoteRig(t, readWrite)
	cookie, _ := rig.signInFrom(t, viewerName, publicPeer, remoteName)
	q := tlsReq{method: "POST", path: "/nothing", host: remoteName, remote: publicPeer, cookie: cookie, headers: jsonHdr, body: "{}"}
	if rec := rig.do(q); rec.Code != 403 || !strings.Contains(rec.Body.String(), readOnlyMessage) {
		t.Errorf("remote POST to an unlisted path: %d %s", rec.Code, rec.Body.String())
	}
	lan, _ := rig.signInFrom(t, viewerName, lanPeer, lanHost)
	q.host, q.remote, q.cookie = lanHost, lanPeer, lan
	if rec := rig.do(q); rec.Code == 403 {
		t.Errorf("LAN POST with tlsReadOnly false was refused: %s", rec.Body.String())
	}
	rq := tlsReq{method: "POST", path: "/api/auth/logout", host: remoteName, remote: publicPeer, cookie: cookie, headers: jsonHdr}
	if rec := rig.do(rq); rec.Code != 200 {
		t.Errorf("remote sign-out: %d", rec.Code)
	}
}

// A public peer gets 3 quick sign-in attempts, then one every 30 s; an IPv6 /64 is one peer; a LAN
// peer keeps the LAN bucket (5 banked). Each attempt is audited with the peer address.
func TestRemoteSignInBucket(t *testing.T) {
	rig := remoteRig(t, nil)
	try := func(peer, host string, i int) int {
		return rig.do(tlsReq{method: "POST", path: "/api/auth/login", host: host, remote: peer, headers: jsonHdr, body: loginBody(fmt.Sprintf("nobody%d", i), "wrong password here")}).Code
	}
	for i := 0; i < 3; i++ {
		if code := try(publicPeer, remoteName, i); code != 401 {
			t.Fatalf("remote attempt %d: %d", i+1, code)
		}
	}
	if code := try(publicPeer, remoteName, 3); code != 429 {
		t.Errorf("4th quick remote attempt: %d, want 429", code)
	}
	rig.clock.advance(30 * time.Second)
	if code := try(publicPeer, remoteName, 4); code != 401 {
		t.Errorf("remote attempt after 30 s: %d", code)
	}
	for i := 0; i < 5; i++ {
		if code := try(lanPeer, lanHost, 10+i); code != 401 {
			t.Errorf("LAN attempt %d: %d", i+1, code)
		}
	}
	for i, peer := range []string{"[2001:db8:0:5::1]:1", "[2001:db8:0:5::2]:1", "[2001:db8:0:5:ffff::9]:1"} {
		if code := try(peer, remoteName, 20+i); code != 401 {
			t.Errorf("IPv6 attempt %d: %d", i+1, code)
		}
	}
	if code := try("[2001:db8:0:5::77]:1", remoteName, 30); code != 429 {
		t.Errorf("4th attempt from the same /64: %d", code)
	}
	if code := try("[2001:db8:0:6::1]:1", remoteName, 31); code != 401 {
		t.Errorf("another /64: %d", code)
	}
	logs := rig.logs.String()
	if !strings.Contains(logs, "from=203.0.113.7 peer=remote") || !strings.Contains(logs, "result=throttled") || strings.Contains(logs, "wrong password here") {
		t.Error("sign-in audit records are missing the peer, the result, or leak the password")
	}
}

// A remote session ends after 12 h idle or 7 days; the cookie says 7 days; LAN sessions keep 7 and 30 days.
func TestRemoteSessionLimits(t *testing.T) {
	rig := remoteRig(t, nil)
	rec := rig.do(tlsReq{method: "POST", path: "/api/auth/login", host: remoteName, remote: publicPeer, headers: jsonHdr, body: loginBody(viewerName, testPassword)})
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.MaxAge != 7*24*3600 || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("remote cookie %+v", cookie)
	}
	lan, _ := rig.signInFrom(t, viewerName, lanPeer, lanHost)
	status := func(c, peer, host string) int {
		return rig.do(tlsReq{path: "/api/status", host: host, remote: peer, cookie: c}).Code
	}
	rig.clock.advance(11 * time.Hour)
	if status(cookie.Value, publicPeer, remoteName) == 401 {
		t.Error("remote session ended before 12 h idle")
	}
	rig.clock.advance(12*time.Hour + time.Minute)
	if status(cookie.Value, publicPeer, remoteName) != 401 {
		t.Error("remote session outlived 12 h idle")
	}
	if status(lan, lanPeer, lanHost) == 401 {
		t.Error("LAN session ended after 23 h")
	}
	// A LAN session presented by a public peer is held to the remote limits (and ended).
	rig.clock.advance(13 * time.Hour)
	if status(lan, publicPeer, remoteName) != 401 {
		t.Error("a LAN session idle 13 h was accepted from a public peer")
	}

	s := newSessionStore(t.TempDir(), rig.clock.now)
	now := rig.clock.now()
	fresh := Session{CreatedAt: now.Add(-8 * 24 * time.Hour), LastSeen: now, ExpiresAt: now.Add(22 * 24 * time.Hour)}
	if !s.expired(fresh, now, remoteSession) || s.expired(fresh, now, lanSession) {
		t.Error("the 7-day remote lifetime is not applied, or the LAN one changed")
	}
}

// Remote answers carry HSTS (a day, nothing more), nosniff and no-store on API answers, refusals
// included; LAN answers carry no HSTS (unchanged).
func TestRemoteResponseHeaders(t *testing.T) {
	rig := remoteRig(t, nil)
	cookie, _ := rig.signInFrom(t, viewerName, publicPeer, remoteName)
	for _, q := range []tlsReq{{path: "/api/status", cookie: cookie}, {path: "/api/status"}, {path: "/api/admin/users", cookie: cookie}, {path: "/login"}} {
		q.host, q.remote = remoteName, publicPeer
		h := rig.do(q).Header()
		if h.Get("Strict-Transport-Security") != "max-age=86400" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: %v", q.path, h)
		}
		if strings.HasPrefix(q.path, "/api/") && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", q.path, h.Get("Cache-Control"))
		}
	}
	if h := rig.do(tlsReq{path: "/login", remote: lanPeer}).Header(); h.Get("Strict-Transport-Security") != "" {
		t.Errorf("LAN answer carries HSTS: %v", h)
	}
}
