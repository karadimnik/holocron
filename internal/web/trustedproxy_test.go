package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// web.trustedProxy: one proxy address whose X-Forwarded-For names the real client (clientaddr.go).
// Everything else - off by default, another peer, a malformed header - keeps the TCP peer.

const (
	proxyAddr = "127.0.0.2"
	proxyPeer = "127.0.0.2:40000"
	tailPeer  = "100.101.102.103"
)

func withProxy(c *Config) { c.TrustedProxy = proxyAddr }

func proxyAuth(t *testing.T, proxy string, remoteHost string) *authService {
	t.Helper()
	a := &authService{remoteHost: remoteHost}
	if proxy != "" {
		a.trustedProxy = netip.MustParseAddr(proxy)
	}
	return a
}

func reqFrom(peer string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://"+lanHost+"/", nil)
	r.RemoteAddr = peer
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestAddrClass(t *testing.T) {
	for ip, want := range map[string]string{
		"100.64.0.1": netTailnet, "100.101.102.103": netTailnet, "100.127.255.254": netTailnet, "::ffff:100.100.1.2": netTailnet,
		"fd7a:115c:a1e0::1": netTailnet, "fd7a:115c:a1e0:ab12::5": netTailnet,
		"100.63.255.255": netRemote, "100.128.0.1": netRemote, "203.0.113.7": netRemote, "2001:db8::1": netRemote, "garbage": netRemote, "": netRemote,
		"192.168.1.20": netLAN, "10.0.0.5": netLAN, "127.0.0.2": netLAN, "::1": netLAN, "fd00::1": netLAN, "fd7a:115c:a1e1::1": netLAN, "169.254.1.1": netLAN,
	} {
		if got := addrClass(ip); got != want {
			t.Errorf("addrClass(%q) = %q, want %q", ip, got, want)
		}
	}
}

// The header is ignored unless the setting names the peer; the class comes from the peer then.
func TestForwardedForIsIgnoredWhenOffOrFromAnotherPeer(t *testing.T) {
	off := proxyAuth(t, "", remoteName)
	on := proxyAuth(t, proxyAddr, remoteName)
	for _, c := range []struct {
		name     string
		a        *authService
		peer     string
		xff      string
		wantIP   string
		wantNet  string
		wantProx bool
	}{
		{"off, peer is the would-be proxy", off, proxyPeer, tailPeer, proxyAddr, netLAN, false},
		{"off, a LAN device claims to be public", off, lanPeer, "203.0.113.7", "192.168.1.20", netLAN, false},
		{"off, a public peer claims to be LAN", off, publicPeer, "192.168.1.9", "203.0.113.7", netRemote, false},
		{"on, a LAN device claims to be tailnet", on, lanPeer, tailPeer, "192.168.1.20", netLAN, false},
		{"on, a public peer claims to be LAN", on, publicPeer, "192.168.1.9", "203.0.113.7", netRemote, false},
		{"on, a loopback peer that is not the proxy", on, "127.0.0.3:5000", tailPeer, "127.0.0.3", netLAN, false},
		{"on, the proxy's address with another port is the proxy", on, "127.0.0.2:1", tailPeer, tailPeer, netTailnet, true},
		{"on, a direct tailnet peer with no header", on, tailPeer + ":5000", "", tailPeer, netTailnet, false},
	} {
		r := reqFrom(c.peer)
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		ip, via := c.a.clientIP(r)
		if ip != c.wantIP || via != c.wantProx || c.a.netClass(r) != c.wantNet {
			t.Errorf("%s: ip %q via %v class %q, want %q %v %q", c.name, ip, via, c.a.netClass(r), c.wantIP, c.wantProx, c.wantNet)
		}
	}
	// The proxy's address in its IPv4-mapped form is still the proxy.
	if ip, via := on.clientIP(reqFrom("[::ffff:127.0.0.2]:5000", tailPeer)); ip != tailPeer || !via {
		t.Errorf("mapped form of the proxy: %q %v", ip, via)
	}
}

// From the trusted proxy: the right-most entry decides, and anything that is not an address falls back
// to the proxy's own (loopback, so LAN) address - never to a more trusted class.
func TestForwardedForFromTheTrustedProxy(t *testing.T) {
	a := proxyAuth(t, proxyAddr, remoteName)
	huge := strings.Repeat("1.1.1.1, ", maxForwardedFor/9+1) + tailPeer
	for _, c := range []struct {
		name    string
		xff     []string
		wantIP  string
		wantNet string
	}{
		{"tailnet", []string{tailPeer}, tailPeer, netTailnet},
		{"tailnet IPv6", []string{"fd7a:115c:a1e0::7"}, "fd7a:115c:a1e0::7", netTailnet},
		{"tailnet IPv4-mapped", []string{"::ffff:100.100.1.2"}, "100.100.1.2", netTailnet},
		{"private", []string{"192.168.1.20"}, "192.168.1.20", netLAN},
		{"public", []string{"203.0.113.7"}, "203.0.113.7", netRemote},
		{"spaces around", []string{"  " + tailPeer + "  "}, tailPeer, netTailnet},
		{"the caller pre-filled a public address on the left", []string{"203.0.113.7, " + tailPeer}, tailPeer, netTailnet},
		{"the caller pre-filled a tailnet address on the left", []string{tailPeer + ", 203.0.113.7"}, "203.0.113.7", netRemote},
		{"two header lines are one list", []string{"203.0.113.7", tailPeer}, tailPeer, netTailnet},
		{"two header lines, the last is public", []string{tailPeer, "203.0.113.7"}, "203.0.113.7", netRemote},
		{"no header", nil, proxyAddr, netLAN},
		{"empty header", []string{""}, proxyAddr, netLAN},
		{"trailing comma", []string{tailPeer + ","}, proxyAddr, netLAN},
		{"only commas", []string{",,"}, proxyAddr, netLAN},
		{"garbage", []string{"garbage"}, proxyAddr, netLAN},
		{"unknown", []string{"unknown"}, proxyAddr, netLAN},
		{"a host name", []string{"tailscale.example.org"}, proxyAddr, netLAN},
		{"with a port", []string{tailPeer + ":443"}, proxyAddr, netLAN},
		{"a CIDR", []string{"100.64.0.0/10"}, proxyAddr, netLAN},
		{"with a zone", []string{"fe80::1%eth0"}, proxyAddr, netLAN},
		{"garbage on the right of a good entry", []string{tailPeer + ", nope"}, proxyAddr, netLAN},
		{"a header longer than any proxy writes", []string{huge}, proxyAddr, netLAN},
	} {
		r := reqFrom(proxyPeer, c.xff...)
		ip, _ := a.clientIP(r)
		if ip != c.wantIP || a.netClass(r) != c.wantNet {
			t.Errorf("%s: ip %q class %q, want %q %q", c.name, ip, a.netClass(r), c.wantIP, c.wantNet)
		}
	}
}

// A public client behind the proxy is remote even with remote access off, so it never gets LAN
// rights; a public peer that connects directly with remote access off is classified as it always was.
func TestPublicClientBehindTheProxyIsNeverLAN(t *testing.T) {
	r := reqFrom(proxyPeer, "203.0.113.7")
	if got := proxyAuth(t, proxyAddr, "").netClass(r); got != netRemote {
		t.Errorf("behind the proxy, remote access off: %q, want remote", got)
	}
	if got := proxyAuth(t, "", "").netClass(reqFrom(publicPeer)); got != netLAN {
		t.Errorf("direct public peer, remote access off: %q, want lan (unchanged behaviour)", got)
	}
}

func TestTrustedProxyIsValidatedAgain(t *testing.T) {
	for _, bad := range []string{"0.0.0.0", "203.0.113.7", "*", "localhost", "127.0.0.0/8", "127.0.0.2:80"} {
		if _, err := trustedProxyOf(bad); err == nil {
			t.Errorf("trustedProxyOf(%q) accepted", bad)
		}
	}
	dir := t.TempDir()
	cfg := Config{Listen: "127.0.0.1:8787", ArchiveRoot: dir, StatusFile: dir + "/_state/status.json", ScriptDir: dir, TLSListen: lanListen, TLSDir: dir + "/tls", AuthDir: dir + "/auth", TrustedProxy: "203.0.113.7"}
	rig := newTLSRig(t, nil)
	if _, err := newTLSHandler(cfg, rig.auth.log, rig.certs, certNames{}, rig.auth); err == nil || !strings.Contains(err.Error(), "web.trustedProxy") {
		t.Errorf("a public trusted proxy built a handler: %v", err)
	}
}

func authLogOf(t *testing.T, rig *tlsRig) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rig.cfg.AuthDir, "auth.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fromProxy(client string, q tlsReq) tlsReq {
	q.remote = proxyPeer
	q.headers = merge(q.headers, "X-Forwarded-For", client)
	return q
}

// Behind the proxy the throttle is per real client: one tailnet device that hammers the sign-in
// does not throttle another, nor the home network. With the setting off, the same traffic shares the
// proxy's one bucket (the situation the setting exists to fix).
func TestSignInThrottleIsPerEffectiveAddress(t *testing.T) {
	try := func(rig *tlsRig, client, name, password string) int {
		return rig.do(fromProxy(client, tlsReq{method: "POST", path: "/api/auth/login", headers: jsonHdr, body: loginBody(name, password)})).Code
	}
	rig := newTLSRig(t, withProxy)
	for i := 0; i < 5; i++ {
		if code := try(rig, "100.64.0.10", "name"+string(rune('a'+i)), "wrong password here"); code != 401 {
			t.Fatalf("burst attempt %d: %d", i+1, code)
		}
	}
	if code := try(rig, "100.64.0.10", "namez", "wrong password here"); code != 429 {
		t.Errorf("6th attempt of one tailnet device: %d, want 429", code)
	}
	if code := try(rig, "100.64.0.11", testUser, testPassword); code != 200 {
		t.Errorf("another tailnet device: %d, want 200", code)
	}
	if rec := rig.loginFrom(lanPeer, testUser, testPassword); rec.Code != 200 {
		t.Errorf("a LAN device: %d, want 200", rec.Code)
	}
	if log := authLogOf(t, rig); !strings.Contains(log, "ip=100.64.0.10 event=login result=throttled") || strings.Contains(log, "ip="+proxyAddr) {
		t.Errorf("auth.log does not name the real client:\n%s", log)
	}

	shared := newTLSRig(t, nil) // setting off: the header is ignored, all share the proxy's bucket
	for i := 0; i < 5; i++ {
		try(shared, "100.64.0.10", "name"+string(rune('a'+i)), "wrong password here")
	}
	if code := try(shared, "100.64.0.11", testUser, testPassword); code != 429 {
		t.Errorf("setting off, another device behind the proxy: %d, want 429 (one shared bucket)", code)
	}
	if log := authLogOf(t, shared); !strings.Contains(log, "ip="+proxyAddr+" ") || strings.Contains(log, "ip=100.64.0.10") {
		t.Errorf("setting off, auth.log must keep the peer:\n%s", log)
	}
}

// A remote client behind the proxy shares the stricter remote bucket per real address too.
func TestRemoteSignInBucketIsPerEffectiveAddress(t *testing.T) {
	rig := remoteRig(t, withProxy)
	login := func(client string) int {
		return rig.do(fromProxy(client, tlsReq{method: "POST", path: "/api/auth/login", host: remoteName, headers: jsonHdr, body: loginBody(viewerName, "wrong password here")})).Code
	}
	for i := 0; i < 3; i++ {
		if code := login("203.0.113.7"); code != 401 {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	if code := login("203.0.113.7"); code != 429 {
		t.Errorf("4th attempt of one remote client: %d, want 429 (remote bucket)", code)
	}
	if code := login("198.51.100.9"); code != 401 {
		t.Errorf("another remote client: %d, want 401 (not throttled)", code)
	}
}

// A tailnet client signs in and is granted what a LAN client is: its own role, and the view-only
// setting as on the LAN. Only the label differs.
func TestTailnetClientIsGrantedWhatLANIs(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		rig := newTLSRig(t, func(c *Config) { withProxy(c); c.TLSReadOnly = boolPtr(readOnly) })
		sign := func(q tlsReq) string {
			q.method, q.path, q.headers, q.body = "POST", "/api/auth/login", jsonHdr, loginBody(testUser, testPassword)
			rec := rig.do(q)
			if rec.Code != 200 {
				t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
			}
			return sessionCookieOf(rec).Value
		}
		tail := sign(fromProxy(tailPeer, tlsReq{}))
		lan := sign(tlsReq{remote: lanPeer})
		for _, q := range []tlsReq{
			{path: "/api/admin/users"}, {path: "/api/status"}, {path: "/api/auth/me"},
			{method: "POST", path: "/api/control", headers: jsonHdr, body: "{}"},
			{method: "PUT", path: "/api/config", headers: jsonHdr, body: "{}"},
		} {
			a, b := q, q
			a.cookie, b.cookie = tail, lan
			got, want := rig.do(fromProxy(tailPeer, a)), rig.do(withPeer(b, lanPeer))
			if got.Code != want.Code {
				t.Errorf("tlsReadOnly=%v %s %s: tailnet %d, LAN %d: not the same grants", readOnly, q.method, q.path, got.Code, want.Code)
			}
		}
	}
}

func withPeer(q tlsReq, peer string) tlsReq { q.remote = peer; return q }

// A public client behind the proxy gets the remote caps: viewer-only whatever its role, the remote
// host must be named, and with remote access off it is refused outright.
func TestPublicClientBehindTheProxyGetsTheRemoteCaps(t *testing.T) {
	rig := remoteRig(t, func(c *Config) { withProxy(c); readWrite(c) })
	login := func(host string) *httptest.ResponseRecorder {
		return rig.do(fromProxy("203.0.113.7", tlsReq{method: "POST", path: "/api/auth/login", host: host, headers: jsonHdr, body: loginBody(testUser, testPassword)}))
	}
	rec := login(remoteName)
	cookie := sessionCookieOf(rec)
	if rec.Code != 200 || cookie == nil || !strings.Contains(rec.Body.String(), `"role":"viewer"`) {
		t.Fatalf("remote admin sign-in: %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []tlsReq{{path: "/api/admin/users"}, {method: "POST", path: "/api/control", headers: jsonHdr, body: "{}"}} {
		q.host, q.cookie = remoteName, cookie.Value
		if rec := rig.do(fromProxy("203.0.113.7", q)); rec.Code != 403 {
			t.Errorf("remote client behind the proxy, %s %s: %d, want 403", q.method, q.path, rec.Code)
		}
	}
	if rec := login(lanHost); rec.Code != 403 || !strings.Contains(rec.Body.String(), "host not allowed") {
		t.Errorf("a public client naming the LAN host: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(authLogOf(t, rig), "ip=203.0.113.7 ") {
		t.Error("auth.log does not name the public client")
	}

	off := newTLSRig(t, withProxy) // remote access off
	for _, host := range []string{lanHost, remoteName} {
		if rec := off.do(fromProxy("203.0.113.7", tlsReq{method: "POST", path: "/api/auth/login", host: host, headers: jsonHdr, body: loginBody(testUser, testPassword)})); rec.Code != 403 {
			t.Errorf("remote access off, a public client behind the proxy naming %s: %d, want 403", host, rec.Code)
		}
	}
}

// Who's online, the live table and the bandwidth split follow the real client behind the proxy.
func TestPresenceAndBandwidthFollowTheRealClient(t *testing.T) {
	w := newWhoRigWith(t, withProxy)
	admin := w.login(t)
	signIn := func(client string) string {
		rec := w.do(fromProxy(client, tlsReq{method: "POST", path: "/api/auth/login", headers: jsonHdr, body: loginBody(viewerUser, viewerPassword)}))
		if rec.Code != 200 {
			t.Fatalf("sign-in from %s: %d %s", client, rec.Code, rec.Body.String())
		}
		return sessionCookieOf(rec).Value
	}
	tail := signIn(tailPeer)

	poll := func(cookie, client, query string) {
		t.Helper()
		if rec := w.do(fromProxy(client, tlsReq{path: "/api/status?" + query, cookie: cookie})); rec.Code != 200 {
			t.Fatalf("poll: %d", rec.Code)
		}
	}
	poll(tail, tailPeer, "pg="+pageA+"&bw=500000&bwb=62500")
	poll(admin, "192.168.1.20", "pg="+pageB+"&bw=1500000&bwb=187500") // a LAN device that also goes through the proxy
	got := w.sessions(t, admin)

	row := rowOf(t, got, viewerUser)
	if row.Network != netTailnet || row.Peer != tailPeer || !row.Online {
		t.Errorf("tailnet viewer's row: %+v", row)
	}
	if r := rowOf(t, got, testUser); r.Network != netLAN || r.Peer != "192.168.1.20" {
		t.Errorf("LAN admin's row: %+v", r)
	}
	b := got.Bandwidth
	if b.TailnetBps != 500_000 || b.HomeBps != 1_500_000 || b.InternetBps != 0 || b.CurrentBps != 2_000_000 || b.TailnetBytes != 62_500 || b.HomeBytes != 187_500 || b.TotalBytes != 250_000 {
		t.Errorf("bandwidth %+v, want three buckets: tailnet 0.5, home 1.5, internet 0", b)
	}
	// A viewer who opens a live session: the table records the real client too.
	rec := w.do(fromProxy(tailPeer, tlsReq{method: "POST", path: "/api/live/Cam%20One/whep?stream=main", host: lanHost, cookie: tail,
		headers: map[string]string{"Content-Type": "application/sdp", "Origin": "https://" + lanHost}, body: testOffer}))
	if rec.Code != 201 {
		t.Fatalf("open live: %d %s", rec.Code, rec.Body.String())
	}
	var watching *WatchRow
	for _, v := range w.sessions(t, admin).Watching {
		if v.User == viewerUser {
			v := v
			watching = &v
		}
	}
	if watching == nil || watching.Network != netTailnet || watching.Peer != tailPeer {
		t.Errorf("watching row: %+v, want tailnet %s", watching, tailPeer)
	}
}

// A device that reaches the HTTPS listener directly from a tailnet address (a subnet route, say) is
// the tailnet class with no setting at all.
func TestDirectTailnetPeerIsLabelledWithoutTheSetting(t *testing.T) {
	w := newWhoRig(t)
	rec := w.do(tlsReq{method: "POST", path: "/api/auth/login", remote: tailPeer + ":5000", headers: jsonHdr, body: loginBody(viewerUser, viewerPassword)})
	if rec.Code != 200 {
		t.Fatalf("sign-in: %d", rec.Code)
	}
	w.poll(t, "/api/status?pg="+pageA+"&bw=100000&bwb=12500", sessionCookieOf(rec).Value, lanHost, tailPeer+":5000")
	got := w.sessions(t, w.login(t))
	if row := rowOf(t, got, viewerUser); row.Network != netTailnet || row.Peer != tailPeer {
		t.Errorf("row %+v, want tailnet", row)
	}
	if got.Bandwidth.TailnetBps != 100_000 || got.Bandwidth.HomeBps != 0 {
		t.Errorf("bandwidth %+v, want it in the tailnet bucket", got.Bandwidth)
	}
}

func TestBandwidthHasThreeBuckets(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var m bandwidthMeter
	m.report(checkin(pageA, "1000000", "125000"), "l1", netLAN, now)
	m.report(checkin(pageB, "2000000", "250000"), "l2", netTailnet, now)
	m.report(checkin("0a1b2c3d4e5f6073", "3000000", "375000"), "l3", netRemote, now)
	m.report(checkin("0a1b2c3d4e5f6074", "4000000", "500000"), "", "", now) // an unset class is home
	got := m.snapshot(now, now)
	if got.HomeBps != 5_000_000 || got.TailnetBps != 2_000_000 || got.InternetBps != 3_000_000 || got.CurrentBps != 10_000_000 {
		t.Errorf("rates %+v", got)
	}
	if got.HomeBytes != 625_000 || got.TailnetBytes != 250_000 || got.InternetBytes != 375_000 || got.TotalBytes != 1_250_000 {
		t.Errorf("bytes %+v", got)
	}
}

// ReadSignInFailures counts what auth.log holds: the client the proxy named, by class.
func TestSignInFailuresAreCountedByClass(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var b strings.Builder
	for _, ip := range []string{"100.64.0.9", "100.64.0.9", "fd7a:115c:a1e0::2", "192.168.1.20", "127.0.0.2", "203.0.113.7"} {
		b.WriteString(now.Format(time.RFC3339) + " ip=" + ip + " event=login result=fail user=\"anna\"\n")
	}
	if err := os.WriteFile(filepath.Join(dir, authLogName), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSignInFailures(dir, now.Add(-time.Hour))
	if err != nil || got.Tailnet != 3 || got.LAN != 2 || got.Remote != 1 {
		t.Errorf("%+v %v, want tailnet 3, LAN 2, remote 1", got, err)
	}
}
