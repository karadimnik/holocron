package config

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// DefaultWebListen is the web app's loopback address (camui's own default).
const DefaultWebListen = "127.0.0.1:8787"

// Web is the "web" object: the web role that `holocron up` runs beside the supervisor.
type Web struct {
	Enabled   bool   `json:"enabled"`   // `up` starts the web role
	Listen    string `json:"listen"`    // loopback listener; a non-loopback address needs CAMUI_TOKEN (the web app refuses otherwise)
	TLSListen string `json:"tlsListen"` // private IPv4:port of the LAN HTTPS listener; "" = off
	TLSDir    string `json:"tlsDir"`    // local CA and server certificate; "" = the web app's default folder
	AuthDir   string `json:"authDir"`   // users.json, sessions.json, auth.log; "" = the web app's default folder
	// RemoteHost is the public DNS name the HTTPS listener also answers to (remote access,
	// viewer-only); "" = off. Normalised by NormalizeRemoteHost; needs TLSListen.
	RemoteHost string `json:"remoteHost,omitempty"`
}

func defaultWeb() Web { return Web{Enabled: true, Listen: DefaultWebListen} }

func validHostPort(s string) bool {
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strings.TrimLeft(port, "0123456789") == ""
}

func (d *decoder) web(c *Config) {
	raw, ok := d.raw["web"]
	if !ok {
		return
	}
	keys, fields, ok := d.object("web", raw)
	if !ok {
		return
	}
	w := &c.Web
	for _, k := range keys {
		v, p := fields[k], "web."+k
		switch strings.ToLower(k) {
		case "enabled":
			d.subBool(p, v, &w.Enabled)
		case "listen", "tlslisten":
			var s string
			d.subStr(p, v, &s)
			if d.lastProblemOn(p) {
				continue
			}
			s = strings.TrimSpace(s)
			switch {
			case strings.ToLower(k) == "listen" && !validHostPort(s):
				d.problem(p, "must be host:port with a port from 1 to 65535, got %q", s)
			case strings.ToLower(k) == "tlslisten" && s != "" && !validHostPort(s):
				d.problem(p, "must be empty (off) or IPv4:port with a port from 1 to 65535, got %q", s)
			case strings.ToLower(k) == "listen":
				w.Listen = s
			default:
				w.TLSListen = s
			}
		case "tlsdir":
			d.subStr(p, v, &w.TLSDir)
		case "authdir":
			d.subStr(p, v, &w.AuthDir)
		case "remotehost":
			var s string
			d.subStr(p, v, &s)
			if d.lastProblemOn(p) {
				continue
			}
			host, err := NormalizeRemoteHost(s)
			if err != nil {
				d.problem(p, "%v", err)
				continue
			}
			w.RemoteHost = host
		default:
			d.warn(p, "unknown key, ignored")
		}
	}
	if w.RemoteHost != "" && w.TLSListen == "" && !d.hasProblemUnder("web.tlsListen") {
		d.problem("web.remoteHost", "needs web.tlsListen: remote access is served only by the HTTPS login listener")
	}
}

// NormalizeRemoteHost validates web.remoteHost: empty (off) or one DNS host name of LDH labels,
// at most 253 characters, at least two labels and a top-level label that is not all digits. It
// returns the name in lower case without a trailing dot. IP literals, ports, schemes, paths,
// wildcards and spaces are refused: the name goes into the server certificate and the Host
// allowlist, so it must be exactly one name.
func NormalizeRemoteHost(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	bad := func(why string) error {
		return errors.New("must be empty (off) or one DNS name such as remote.example.org: " + why)
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return "", bad("it contains a space")
	}
	if strings.Contains(s, "://") {
		return "", bad("no scheme")
	}
	if strings.ContainsAny(s, "/?#") {
		return "", bad("no path")
	}
	if strings.Contains(s, "*") {
		return "", bad("no wildcard")
	}
	if strings.ContainsAny(s, ":[]") {
		return "", bad("no port and no IPv6 address")
	}
	h := strings.TrimSuffix(strings.ToLower(s), ".")
	if net.ParseIP(h) != nil {
		return "", bad("an IP address is not a name")
	}
	if h == "" || len(h) > 253 {
		return "", bad("a name is 1 to 253 characters")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", bad("a public name has at least two labels")
	}
	for _, l := range labels {
		if !ldhLabel(l) {
			return "", bad("each label is 1 to 63 letters, digits or inner hyphens")
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", bad("the last label must not be all digits")
	}
	return h, nil
}

func ldhLabel(l string) bool {
	if len(l) < 1 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
