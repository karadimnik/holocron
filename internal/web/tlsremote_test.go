package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
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
