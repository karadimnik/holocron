package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const testRemoteHost = "remote.example.org"

func testRemoteManager(t *testing.T, dir string) *certManager {
	t.Helper()
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "tls")
	}
	return newRemoteCertManager(dir, testRemoteHost, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func remoteNames() certNames { return certNames{DNS: []string{testRemoteHost}} }

func readCertFile(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s holds no PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// The remote CA, parsed back from the file a phone installs: a path-length-zero CA that signs
// certificates only, with a CRITICAL name constraint naming exactly the one DNS name.
func TestRemoteCAProperties(t *testing.T) {
	m := testRemoteManager(t, "")
	if _, _, err := m.EnsureCA(); err != nil {
		t.Fatal(err)
	}
	ca := readCertFile(t, filepath.Join(m.dir, remoteCACertFile))
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.MaxPathLen != 0 || !ca.MaxPathLenZero {
		t.Errorf("basic constraints: IsCA %v valid %v pathlen %d zero %v", ca.IsCA, ca.BasicConstraintsValid, ca.MaxPathLen, ca.MaxPathLenZero)
	}
	if ca.KeyUsage != x509.KeyUsageCertSign {
		t.Errorf("key usage %v, want certificate signing only", ca.KeyUsage)
	}
	if !slices.Equal(ca.PermittedDNSDomains, []string{testRemoteHost}) || !ca.PermittedDNSDomainsCritical {
		t.Errorf("DNS constraint %v critical %v", ca.PermittedDNSDomains, ca.PermittedDNSDomainsCritical)
	}
	var excluded []string
	for _, n := range ca.ExcludedIPRanges {
		excluded = append(excluded, n.String())
	}
	if !slices.Equal(excluded, []string{"0.0.0.0/0", "::/0"}) || len(ca.PermittedIPRanges) != 0 {
		t.Errorf("IP constraint: excluded %v permitted %v", excluded, ca.PermittedIPRanges)
	}
	if !slices.Equal(ca.PermittedEmailAddresses, []string{"invalid"}) || !slices.Equal(ca.PermittedURIDomains, []string{"invalid"}) {
		t.Errorf("e-mail %v and URI %v constraints", ca.PermittedEmailAddresses, ca.PermittedURIDomains)
	}
	nameConstraints := asn1.ObjectIdentifier{2, 5, 29, 30}
	found := false
	for _, ext := range ca.Extensions {
		if ext.Id.Equal(nameConstraints) {
			found = true
			if !ext.Critical {
				t.Error("the name constraints extension is not critical")
			}
		}
	}
	if !found {
		t.Error("no name constraints extension")
	}
	if life := ca.NotAfter.Sub(ca.NotBefore); life > 10*366*24*time.Hour {
		t.Errorf("CA validity %v exceeds 10 years", life)
	}
	if !strings.Contains(ca.Subject.CommonName, "Holocron remote CA for "+testRemoteHost) {
		t.Errorf("subject %q", ca.Subject.CommonName)
	}
	if p := remoteCAProblem(ca, testRemoteHost); p != "" {
		t.Errorf("remoteCAProblem: %s", p)
	}
	for _, lan := range []string{caCertFile, caKeyFile, leafCertFile, leafKeyFile} {
		if _, err := os.Stat(filepath.Join(m.dir, lan)); !os.IsNotExist(err) {
			t.Errorf("the remote manager touched the LAN file %s: %v", lan, err)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(m.dir, remoteCAKeyFile))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("remote CA key mode %v (%v), want 0600", fi.Mode().Perm(), err)
		}
	}
}

func TestRemoteCAProblemRefusesOtherCAs(t *testing.T) {
	lan, _, err := testManager(t).EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	if remoteCAProblem(lan, testRemoteHost) == "" {
		t.Error("the LAN CA passes as a remote CA")
	}
	remote, _, err := testRemoteManager(t, "").EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	if p := remoteCAProblem(remote, "other.example.org"); !strings.Contains(p, "it is for "+testRemoteHost) {
		t.Errorf("remote CA checked for another name: %q", p)
	}
}

// The served remote leaf names exactly the remote host, is a server certificate within the
// lifetime limit, and verifies against the remote CA alone for that name and no other.
func TestRemoteLeafVerifiesOnlyForTheRemoteHost(t *testing.T) {
	m := testRemoteManager(t, "")
	cert, note, err := m.EnsureLeaf(remoteNames())
	if err != nil || note != "created" {
		t.Fatalf("EnsureLeaf: %q %v", note, err)
	}
	leaf := cert.Leaf
	if !slices.Equal(leaf.DNSNames, []string{testRemoteHost}) || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.URIs) != 0 {
		t.Errorf("leaf names: DNS %v IP %v email %v URI %v", leaf.DNSNames, leaf.IPAddresses, leaf.EmailAddresses, leaf.URIs)
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) || leaf.IsCA {
		t.Errorf("leaf usage %v IsCA %v", leaf.ExtKeyUsage, leaf.IsCA)
	}
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > 200*24*time.Hour {
		t.Errorf("leaf validity %v exceeds 200 days", life)
	}
	ca := readCertFile(t, filepath.Join(m.dir, remoteCACertFile))
	now := time.Now()
	if err := verifyFor(t, leaf, ca, testRemoteHost, now); err != nil {
		t.Errorf("the remote leaf does not verify for %s: %v", testRemoteHost, err)
	}
	for _, host := range []string{"other.example.org", "example.org", "remote", "192.168.1.50"} {
		if verifyFor(t, leaf, ca, host, now) == nil {
			t.Errorf("the remote leaf verifies for %s", host)
		}
	}
	lanCA, _, err := newCertManager(m.dir, m.log).EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	if verifyFor(t, leaf, lanCA, testRemoteHost, now) == nil {
		t.Error("the remote leaf verifies against the LAN CA")
	}
}

// Even with its key in hand, the remote CA cannot make a verifiable certificate for any name
// but the remote host: a forged leaf is refused by the constraint, whatever name is checked.
func TestRemoteCARefusesForgedLeaves(t *testing.T) {
	m := testRemoteManager(t, "")
	ca, caKey, err := m.EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	forge := func(tmpl x509.Certificate) *x509.Certificate {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serial, _ := randomSerial()
		tmpl.SerialNumber, tmpl.Subject = serial, pkix.Name{CommonName: "forged"}
		tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Minute), time.Now().Add(24*time.Hour)
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	uri, _ := url.Parse("https://" + testRemoteHost + "/")
	cases := []struct {
		name  string
		tmpl  x509.Certificate
		host  string
		allow bool
	}{
		{"the remote host", x509.Certificate{DNSNames: []string{testRemoteHost}}, testRemoteHost, true},
		{"a name under it (RFC 5280: a dNSName constraint permits subdomains)", x509.Certificate{DNSNames: []string{"cam." + testRemoteHost}}, "cam." + testRemoteHost, true},
		{"another name", x509.Certificate{DNSNames: []string{"other.example.org"}}, "other.example.org", false},
		{"the parent name", x509.Certificate{DNSNames: []string{"example.org"}}, "example.org", false},
		{"a single-label name", x509.Certificate{DNSNames: []string{"remote"}}, "remote", false},
		{"a name ending in the host without a dot", x509.Certificate{DNSNames: []string{"x" + testRemoteHost}}, "x" + testRemoteHost, false},
		{"localhost", x509.Certificate{DNSNames: []string{"localhost"}}, "localhost", false},
		{"a private IP", x509.Certificate{IPAddresses: []net.IP{net.ParseIP("192.168.1.50").To4()}}, "192.168.1.50", false},
		{"a public IP", x509.Certificate{IPAddresses: []net.IP{net.ParseIP("8.8.8.8").To4()}}, "8.8.8.8", false},
		{"an IPv6 address", x509.Certificate{IPAddresses: []net.IP{net.ParseIP("2001:db8::1")}}, "2001:db8::1", false},
		{"the host plus an IP", x509.Certificate{DNSNames: []string{testRemoteHost}, IPAddresses: []net.IP{net.ParseIP("192.168.1.50").To4()}}, testRemoteHost, false},
		{"the host plus an e-mail name", x509.Certificate{DNSNames: []string{testRemoteHost}, EmailAddresses: []string{"someone@" + testRemoteHost}}, testRemoteHost, false},
		{"the host plus a URI name", x509.Certificate{DNSNames: []string{testRemoteHost}, URIs: []*url.URL{uri}}, testRemoteHost, false},
	}
	for _, c := range cases {
		err := verifyFor(t, forge(c.tmpl), ca, c.host, time.Now())
		if c.allow && err != nil {
			t.Errorf("%s: refused: %v", c.name, err)
		}
		if !c.allow && err == nil {
			t.Errorf("%s: verified, want refused", c.name)
		}
	}
}

func remoteCfg(t *testing.T, host string) Config {
	t.Helper()
	return Config{TLSListen: lanListen, TLSDir: filepath.Join(t.TempDir(), "tls"), RemoteHost: host}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The handshake's SNI picks the leaf: the remote one for the remote host in any case, the
// LAN one for everything else.
func TestSNISelectsTheRemoteLeafOnlyForTheRemoteHost(t *testing.T) {
	certs, _, _, _, err := prepareCerts(remoteCfg(t, testRemoteHost), quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if certs.remote == nil {
		t.Fatal("no remote manager with RemoteHost set")
	}
	for _, c := range []struct {
		sni        string
		wantRemote bool
	}{
		{testRemoteHost, true},
		{"REMOTE.Example.ORG", true},
		{"192.168.1.50", false},
		{"127.0.0.1", false},
		{"localhost", false},
		{"", false},
		{"other.example.org", false},
		{"cam." + testRemoteHost, false},
	} {
		got, err := certs.GetCertificate(&tls.ClientHelloInfo{ServerName: c.sni})
		if err != nil {
			t.Fatalf("%q: %v", c.sni, err)
		}
		isRemote := slices.Equal(got.Leaf.DNSNames, []string{testRemoteHost})
		if isRemote != c.wantRemote {
			t.Errorf("SNI %q: remote leaf %v, want %v (names %v %v)", c.sni, isRemote, c.wantRemote, got.Leaf.DNSNames, got.Leaf.IPAddresses)
		}
	}
	// A real handshake each way, each verified against its own CA only.
	remoteCA := readCertFile(t, filepath.Join(certs.remote.dir, remoteCACertFile))
	lanCA := readCertFile(t, filepath.Join(certs.lan.dir, caCertFile))
	for _, c := range []struct {
		sni  string
		ca   *x509.Certificate
		fail bool
	}{
		{testRemoteHost, remoteCA, false},
		{"192.168.1.50", lanCA, false},
		{testRemoteHost, lanCA, true},
		{"192.168.1.50", remoteCA, true},
	} {
		roots := x509.NewCertPool()
		roots.AddCert(c.ca)
		client, server := net.Pipe()
		go func() {
			s := tls.Server(server, serverTLSConfig(certs))
			_ = s.Handshake()
			s.Close()
		}()
		err := tls.Client(client, &tls.Config{RootCAs: roots, ServerName: c.sni}).Handshake()
		client.Close()
		if (err != nil) != c.fail {
			t.Errorf("handshake for %s against %s: %v (want failure %v)", c.sni, c.ca.Subject.CommonName, err, c.fail)
		}
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// With remoteHost empty nothing about the remote CA exists; turning it on leaves the LAN CA and
// LAN leaf byte for byte as they were.
func TestRemoteHostLeavesTheLANCertificatesUnchanged(t *testing.T) {
	cfg := remoteCfg(t, "")
	certs, _, _, _, err := prepareCerts(cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if certs.remote != nil {
		t.Error("a remote manager with RemoteHost empty")
	}
	if got := dirNames(t, cfg.TLSDir); !slices.Equal(got, []string{caCertFile, caKeyFile, leafCertFile, leafKeyFile}) {
		t.Errorf("files with remote access off: %v", got)
	}
	got, _ := certs.GetCertificate(&tls.ClientHelloInfo{ServerName: testRemoteHost})
	if len(got.Leaf.DNSNames) != 1 || got.Leaf.DNSNames[0] != "localhost" {
		t.Errorf("remote access off served %v for the remote name", got.Leaf.DNSNames)
	}
	before := map[string][]byte{}
	for _, f := range []string{caCertFile, leafCertFile} {
		before[f], _ = os.ReadFile(filepath.Join(cfg.TLSDir, f))
	}
	lanSerial := got.Leaf.SerialNumber

	cfg.RemoteHost = testRemoteHost
	certs, _, lanNote, remoteNote, err := prepareCerts(cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if lanNote != "reused" || remoteNote != "created" {
		t.Errorf("notes: LAN %q remote %q", lanNote, remoteNote)
	}
	for f, data := range before {
		now, _ := os.ReadFile(filepath.Join(cfg.TLSDir, f))
		if string(now) != string(data) {
			t.Errorf("%s changed when remote access was turned on", f)
		}
	}
	lan, _ := certs.GetCertificate(&tls.ClientHelloInfo{ServerName: "192.168.1.50"})
	if lan.Leaf.SerialNumber.Cmp(lanSerial) != 0 || !slices.Equal(lan.Leaf.DNSNames, []string{"localhost"}) || len(lan.Leaf.IPAddresses) != 2 {
		t.Errorf("LAN leaf changed: serial %v names %v %v", lan.Leaf.SerialNumber, lan.Leaf.DNSNames, lan.Leaf.IPAddresses)
	}

	// Cleared again: the files stay, nothing serves them.
	cfg.RemoteHost = ""
	certs, _, _, _, err = prepareCerts(cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.TLSDir, remoteCACertFile)); err != nil {
		t.Errorf("clearing remoteHost removed the remote CA: %v", err)
	}
	if got, _ := certs.GetCertificate(&tls.ClientHelloInfo{ServerName: testRemoteHost}); slices.Contains(got.Leaf.DNSNames, testRemoteHost) {
		t.Error("the remote leaf is still served with remoteHost cleared")
	}
}

// The remote leaf is reused, renewed near expiry and reissued when its names differ.
func TestRemoteLeafRenewal(t *testing.T) {
	m := testRemoteManager(t, "")
	clock := time.Now()
	m.now = func() time.Time { return clock }
	first, note, err := m.EnsureLeaf(remoteNames())
	if err != nil || note != "created" {
		t.Fatalf("%q %v", note, err)
	}
	if _, note, _ := m.EnsureLeaf(remoteNames()); note != "reused" {
		t.Errorf("second start: %q", note)
	}
	clock = first.Leaf.NotAfter.Add(-29 * 24 * time.Hour)
	renewed, note, err := m.EnsureLeaf(remoteNames())
	if err != nil || note != "renewed (near expiry)" {
		t.Errorf("29 days before expiry: %q %v", note, err)
	}
	if !slices.Equal(renewed.Leaf.DNSNames, []string{testRemoteHost}) {
		t.Errorf("renewed names %v", renewed.Leaf.DNSNames)
	}
	if _, err := m.issueLeaf(readCertFile(t, filepath.Join(m.dir, remoteCACertFile)), mustRemoteKey(t, m), certNames{DNS: []string{"cam." + testRemoteHost}}); err != nil {
		t.Fatal(err)
	}
	if got, note, _ := m.EnsureLeaf(remoteNames()); note != "renewed (the names changed)" || !slices.Equal(got.Leaf.DNSNames, []string{testRemoteHost}) {
		t.Errorf("a leaf for another name: %q %v", note, got.Leaf.DNSNames)
	}
}

func mustRemoteKey(t *testing.T, m *certManager) *ecdsa.PrivateKey {
	t.Helper()
	_, key, err := m.EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// A new remoteHost gets a new remote CA: the old CA, its key and its leaf are deleted (the old
// key can sign nothing more) and the replacement is logged as a warning.
func TestRemoteHostChangeReplacesTheRemoteCA(t *testing.T) {
	cfg := remoteCfg(t, testRemoteHost)
	certs, _, _, _, err := prepareCerts(cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	old, _ := certs.remote.CAInfo()

	cfg.RemoteHost = "other.example.org"
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))
	certs, _, _, note, err := prepareCerts(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	if note != "created" {
		t.Errorf("remote note %q", note)
	}
	ca := readCertFile(t, filepath.Join(cfg.TLSDir, remoteCACertFile))
	if fingerprint(ca) == old.Fingerprint || !slices.Equal(ca.PermittedDNSDomains, []string{"other.example.org"}) {
		t.Errorf("the remote CA was not replaced: %v", ca.PermittedDNSDomains)
	}
	leaf, _ := certs.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example.org"})
	if !slices.Equal(leaf.Leaf.DNSNames, []string{"other.example.org"}) {
		t.Errorf("leaf for the new name: %v", leaf.Leaf.DNSNames)
	}
	if got := dirNames(t, cfg.TLSDir); !slices.Equal(got, []string{remoteCACertFile, remoteCAKeyFile, caCertFile, caKeyFile, remoteLeafCertFile, remoteLeafKeyFile, leafCertFile, leafKeyFile}) {
		t.Errorf("files after the change: %v", got)
	}
	if !strings.Contains(logs.String(), "remote CA replaced") || !strings.Contains(logs.String(), "it is for "+testRemoteHost) {
		t.Errorf("no replacement warning: %s", logs.String())
	}
}

// holocron tls rotate starts over with both CAs: the remote files are certificate files too.
func TestRotateCARemovesTheRemoteCA(t *testing.T) {
	cfg := remoteCfg(t, testRemoteHost)
	if _, _, _, _, err := prepareCerts(cfg, quietLog()); err != nil {
		t.Fatal(err)
	}
	removed, left, err := RotateCA(cfg.TLSDir)
	if err != nil || len(removed) != 8 || len(left) != 0 {
		t.Errorf("removed %v left %v err %v", removed, left, err)
	}
}

// Trust bootstrap: the CA files and fingerprints are served to LAN peers only. A remote peer,
// signed in or not, gets the 404 of a route that does not exist for both CAs and the info.
func TestCADownloadIsLANOnly(t *testing.T) {
	rig := remoteRig(t, nil)
	lan, _ := rig.signInFrom(t, viewerName, lanPeer, lanHost)
	for _, c := range []struct{ query, file, name string }{{"", caCertFile, "ipcctv-local-ca.crt"}, {"?ca=remote", remoteCACertFile, "holocron-remote-ca.crt"}} {
		rec := rig.do(tlsReq{path: "/api/tls/ca.crt" + c.query, host: lanHost, remote: lanPeer, cookie: lan})
		want, _ := os.ReadFile(filepath.Join(rig.cfg.TLSDir, c.file))
		if rec.Code != 200 || rec.Body.String() != string(want) || containsKeyMaterial(rec.Body.Bytes()) {
			t.Errorf("LAN download %q: %d, same as %s: %v", c.query, rec.Code, c.file, rec.Body.String() == string(want))
		}
		if !strings.Contains(rec.Header().Get("Content-Disposition"), c.name) {
			t.Errorf("LAN download %q: %q", c.query, rec.Header().Get("Content-Disposition"))
		}
	}
	if rec := rig.do(tlsReq{path: "/api/tls/ca.crt?ca=other", host: lanHost, remote: lanPeer, cookie: lan}); rec.Code != 404 {
		t.Errorf("unknown CA: %d", rec.Code)
	}
	var info map[string]any
	rec := rig.do(tlsReq{path: "/api/tls/info", host: lanHost, remote: lanPeer, cookie: lan})
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	remoteCA := readCertFile(t, filepath.Join(rig.cfg.TLSDir, remoteCACertFile))
	if info["remoteHost"] != remoteName || info["remoteCaFingerprintSha256"] != fingerprint(remoteCA) || info["remoteDownload"] != "api/tls/ca.crt?ca=remote" || info["caFingerprintSha256"] == info["remoteCaFingerprintSha256"] {
		t.Errorf("LAN info: %v", info)
	}

	remote, _ := rig.signInFrom(t, viewerName, publicPeer, remoteName)
	for _, path := range []string{"/api/tls/ca.crt", "/api/tls/ca.crt?ca=remote", "/api/tls/info"} {
		for _, cookie := range []string{remote, ""} {
			rec := rig.do(tlsReq{path: path, host: remoteName, remote: publicPeer, cookie: cookie})
			if cookie != "" && rec.Code != 404 || cookie == "" && rec.Code != 401 || strings.Contains(rec.Body.String(), "BEGIN") || strings.Contains(rec.Body.String(), "Fingerprint") {
				t.Errorf("remote peer (signed in %v) %s: %d %q", cookie != "", path, rec.Code, rec.Body.String())
			}
		}
	}
	if !strings.Contains(rig.logs.String(), "certificate authority refused to a remote peer") {
		t.Error("no log line for the refused remote download")
	}
}

func TestRemoteCADownloadAbsentWhileRemoteAccessIsOff(t *testing.T) {
	rig := newTLSRig(t, nil)
	cookie := rig.loginAs(t, testUser, testPassword)
	if rec := rig.do(tlsReq{path: "/api/tls/ca.crt?ca=remote", cookie: cookie}); rec.Code != 404 {
		t.Errorf("remote CA with remote access off: %d", rec.Code)
	}
	rec := rig.do(tlsReq{path: "/api/tls/info", cookie: cookie})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "remote") {
		t.Errorf("info with remote access off: %d %s", rec.Code, rec.Body.String())
	}
}

// The Phone tab offers the remote CA beside the LAN one, with the home-network warning, filled
// from api/tls/info and hidden when it names no remote host.
func TestPhoneTabOffersTheRemoteCA(t *testing.T) {
	page, _ := staticFiles.ReadFile("static/index.html")
	app, _ := staticFiles.ReadFile("static/app.js")
	for _, want := range []string{`id="remotePhonePanel"`, `href="api/tls/ca.crt?ca=remote"`,
		`Install this ONLY while on your home network; it lets this phone trust <span id="remoteHostName"></span> and nothing else.`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	for _, want := range []string{`el("remoteHostName").textContent = remote ? info.remoteHost`, `el("remoteFingerprint").textContent = remote ? info.remoteCaFingerprintSha256`, `el("remotePhonePanel").classList.toggle("hide", !remote)`} {
		if !strings.Contains(string(app), want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

// /api/auth/me says whether the session is remote, and the page shows the remote banner instead
// of the viewer one for it.
func TestMeSaysRemoteAndThePageShowsTheRemoteBanner(t *testing.T) {
	rig := remoteRig(t, nil)
	for _, c := range []struct {
		peer, host string
		want       bool
	}{{publicPeer, remoteName, true}, {lanPeer, lanHost, false}, {lanPeer, remoteName, false}} {
		cookie, _ := rig.signInFrom(t, testUser, c.peer, c.host)
		var me map[string]any
		if err := json.Unmarshal(rig.do(tlsReq{path: "/api/auth/me", host: c.host, remote: c.peer, cookie: cookie}).Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if me["remote"] != c.want {
			t.Errorf("me from %s as %s: remote %v, want %v", c.peer, c.host, me["remote"], c.want)
		}
		rig.clock.advance(time.Minute)
	}
	page, _ := staticFiles.ReadFile("static/index.html")
	app, _ := staticFiles.ReadFile("static/app.js")
	if !strings.Contains(string(page), `<div id="remoteNote" class="viewonly-note hide"><strong>Signed in from outside your home network: viewing only.</strong></div>`) {
		t.Error("index.html lacks the remote banner")
	}
	for _, want := range []string{`remote = me.remote === true`, `el("viewerNote").classList.toggle("hide", !viewer || remote)`, `el("remoteNote").classList.toggle("hide", !remote)`} {
		if !strings.Contains(string(app), want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

func TestRemoteCertState(t *testing.T) {
	cfg := remoteCfg(t, testRemoteHost)
	if st, err := RemoteCertState(cfg.TLSDir, testRemoteHost); err != nil || st.CAPresent {
		t.Errorf("empty folder: %+v %v", st, err)
	}
	certs, _, _, _, err := prepareCerts(cfg, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	st, err := RemoteCertState(cfg.TLSDir, testRemoteHost)
	info, _ := certs.remote.CAInfo()
	if err != nil || !st.CAPresent || st.CAProblem != "" || st.Fingerprint != info.Fingerprint || !st.LeafPresent || !st.LeafSigned ||
		!slices.Equal(st.LeafNames, []string{testRemoteHost}) || len(st.UnprotectedKeys) != 0 || st.LeafNotAfter.Before(time.Now().Add(190*24*time.Hour)) {
		t.Errorf("after start: %+v %v", st, err)
	}
	if st, _ := RemoteCertState(cfg.TLSDir, "other.example.org"); !strings.Contains(st.CAProblem, "it is for "+testRemoteHost) {
		t.Errorf("for another host: %q", st.CAProblem)
	}
	if runtime.GOOS != "windows" {
		key := filepath.Join(cfg.TLSDir, remoteLeafKeyFile)
		os.Chmod(key, 0o640)
		if st, _ := RemoteCertState(cfg.TLSDir, testRemoteHost); !slices.Equal(st.UnprotectedKeys, []string{key}) || st.KeyDetail != "leaf-remote.key: mode 0640; the LAN CA key: mode 0600" {
			t.Errorf("readable key: %v %q", st.UnprotectedKeys, st.KeyDetail)
		}
	}
}
