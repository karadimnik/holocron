package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remoteRigConfig turns the rig's web object into an HTTPS listener with remote access (or without,
// for remoteHost "") and gives it one admin account; it returns the auth folder.
func remoteRigConfig(t *testing.T, r *rig, remoteHost string) string {
	t.Helper()
	cfg, _ := os.ReadFile(r.cfg)
	authDir := filepath.Join(filepath.Dir(filepath.Dir(r.cfg)), "auth")
	os.MkdirAll(authDir, 0o700)
	os.WriteFile(filepath.Join(authDir, "users.json"), []byte(`{"version":1,"users":[{"id":"u1","username":"anna","hash":"x","role":"admin","createdAt":"2026-01-01T00:00:00Z"}]}`), 0o600)
	extra := `"tlsListen": "192.168.1.50:8443", `
	if remoteHost != "" {
		extra += `"remoteHost": "` + remoteHost + `", `
	}
	os.WriteFile(r.cfg, []byte(strings.Replace(string(cfg), `"listen"`, extra+`"listen"`, 1)), 0o644)
	return authDir
}

func TestDoctorWebRemote(t *testing.T) {
	r := newRig(t)
	r.running()
	authDir := remoteRigConfig(t, r, "Remote.Example.org")
	var log strings.Builder
	line := func(at time.Time, ip, result string) {
		fmt.Fprintf(&log, "%s ip=%s event=login result=%s user=%q\n", at.UTC().Format(time.RFC3339), ip, result, "anna")
	}
	now := time.Now()
	for i := 0; i < 12; i++ {
		line(now.Add(-time.Hour), "203.0.113.7", "fail")
	}
	for i := 0; i < 5; i++ {
		line(now.Add(-time.Hour), "198.51.100.9", "locked")
	}
	for i := 0; i < 2; i++ {
		line(now.Add(-time.Hour), "2001:db8::1", "throttled")
	}
	line(now.Add(-time.Hour), "203.0.113.50", "fail")
	line(now.Add(-time.Hour), "203.0.113.7", "ok")
	line(now.Add(-30*time.Hour), "203.0.113.99", "fail") // older than 24 h
	line(now.Add(-time.Hour), "192.168.1.20", "fail")
	line(now.Add(-time.Hour), "100.64.0.9", "fail")
	os.WriteFile(filepath.Join(authDir, "auth.log"), []byte(log.String()), 0o600)
	r.doctorTools(nil)
	out := mustRun(r, "--json", "doctor", "--group", "web")
	text := textOf(t, out)
	for _, want := range []string{
		"web.remote info remote access",
		"on for remote.example.org: requests from public addresses are viewer-only; 1 account(s) (1 admin, 0 viewer); policy: viewer-only whatever the account's role; sign-in 2 a minute per peer (3 banked",
		"sessions end after 12 h idle or 7 days",
		"web.remote warn remote access",
		"the server certificate does not name remote.example.org",
		"no viewer account: only admins could sign in remotely, and they would be capped to viewer",
		"20 failed or locked remote sign-in attempt(s) in 24 h (LAN: 2); top peers: 203.0.113.7 (12), 198.51.100.9 (5), 2001:db8::1 (2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	for _, want := range []string{"holocron user add NAME --role viewer", `"remoteHost": "remote.example.org"`, `"viewerOnly": true`} {
		if !strings.Contains(strings.ReplaceAll(out, `":"`, `": "`), want) && !strings.Contains(out, want) {
			t.Errorf("JSON lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "password") && strings.Contains(out, "hash") {
		t.Error("doctor printed something about hashes")
	}
}

func TestDoctorWebRemoteOffPrintsNothing(t *testing.T) {
	r := newRig(t)
	r.running()
	remoteRigConfig(t, r, "")
	r.doctorTools(nil)
	if out := mustRun(r, "--json", "doctor", "--group", "web"); strings.Contains(out, "web.remote") || strings.Contains(out, "remote access") {
		t.Errorf("remote access off, yet:\n%s", out)
	}
}

func TestStatusRemoteLine(t *testing.T) {
	var on, off bytes.Buffer
	printWeb(&on, webReport{Enabled: true, Listen: "127.0.0.1:8787", TLS: true, TLSListen: "192.168.1.50:8443", Login: true, RemoteHost: "remote.example.org"})
	printWeb(&off, webReport{Enabled: true, Listen: "127.0.0.1:8787", TLS: true, TLSListen: "192.168.1.50:8443", Login: true})
	if !strings.HasSuffix(on.String(), "\nremote access: on (viewer-only) for remote.example.org\n") || strings.Contains(off.String(), "remote") {
		t.Errorf("on %q\noff %q", on.String(), off.String())
	}
	e := &env{}
	e.cfg.Web.RemoteHost = "remote.example.org"
	if w := webStatus(e); w.RemoteHost != "" {
		t.Errorf("remote access reported without the HTTPS listener: %+v", w)
	}
	e.cfg.Web.TLSListen = "192.168.1.50:8443"
	if w := webStatus(e); w.RemoteHost != "remote.example.org" {
		t.Errorf("%+v", w)
	}
}
