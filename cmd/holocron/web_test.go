package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"holocron/internal/app"
	"holocron/internal/osx"
	"holocron/internal/web"
)

// The web role must look for the live-view secret where the supervisor does: in the platform's auth
// folder (internal/web proves the two resolve alike once it is given that folder).
func TestWebBaseLiveSecretDirIsTheSupervisorsAuthFolder(t *testing.T) {
	plat := &osx.Fake{DirsValue: osx.Dirs{Auth: "/state/auth"}}
	e := &env{opts: app.Options{Platform: plat}}
	if got := webBase(e).LiveSecretDir; got != "/state/auth" {
		t.Errorf("LiveSecretDir = %q", got)
	}
}

// web.remoteHost reaches the web role the way web.tlsListen does (holocron.config.json, read by the
// child `holocron web` that `up` starts), and a camui config file cannot clear or replace it.
func TestWebBaseCarriesRemoteHost(t *testing.T) {
	e := &env{opts: app.Options{Platform: &osx.Fake{}}}
	e.cfg.Web.TLSListen, e.cfg.Web.RemoteHost = "192.168.1.50:8443", "remote.example.org"
	file := filepath.Join(t.TempDir(), "camui.json")
	if err := os.WriteFile(file, []byte(`{"remoteHost":"evil.example.net","RemoteHost":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := web.ParseFlagsTo([]string{"-config", file}, io.Discard, webBase(e))
	if err != nil || o.Config.RemoteHost != "remote.example.org" || o.Config.TLSListen != "192.168.1.50:8443" {
		t.Errorf("%v %q %q", err, o.Config.RemoteHost, o.Config.TLSListen)
	}
}
