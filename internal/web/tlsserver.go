package web

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// The HTTPS listener for the LAN. It is a second listener with its own handler
// chain; the loopback listener is untouched. Here a login is mandatory and
// outermost, so no request without a session reaches any handler, static file
// included, except the login page and what it needs.
// ---------------------------------------------------------------------------

// serverCerts are the listener's certificates: the LAN leaf, and the remote leaf while
// web.remoteHost is set (remote is nil otherwise).
type serverCerts struct {
	lan, remote *certManager
}

// GetCertificate serves the remote leaf exactly when the client asks for the remote host by SNI
// (case-insensitive) and the LAN leaf for everything else: an address, localhost, another name,
// no SNI at all.
func (c serverCerts) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c.remote != nil && strings.EqualFold(hello.ServerName, c.remote.remoteHost) {
		return c.remote.GetCertificate(hello)
	}
	return c.lan.GetCertificate(hello)
}

// prepareCerts loads or creates the LAN leaf for the TLSListen address and TLSHosts and, with
// RemoteHost set, the remote CA and its leaf for that one name. The notes say what happened.
func prepareCerts(cfg Config, log *slog.Logger) (certs serverCerts, names certNames, lanNote, remoteNote string, err error) {
	ip, _, err := parseTLSListen(cfg.TLSListen)
	if err != nil {
		return serverCerts{}, certNames{}, "", "", err
	}
	if names, err = buildCertNames(ip, cfg.TLSHosts); err != nil {
		return serverCerts{}, certNames{}, "", "", err
	}
	certs.lan = newCertManager(cfg.TLSDir, log)
	if lanNote, err = certs.lan.Prepare(names); err != nil {
		return serverCerts{}, certNames{}, "", "", fmt.Errorf("TLS certificate: %w", err)
	}
	if cfg.RemoteHost != "" {
		certs.remote = newRemoteCertManager(cfg.TLSDir, cfg.RemoteHost, log)
		if remoteNote, err = certs.remote.Prepare(certNames{DNS: []string{cfg.RemoteHost}}); err != nil {
			return serverCerts{}, certNames{}, "", "", fmt.Errorf("remote TLS certificate: %w", err)
		}
	}
	return certs, names, lanNote, remoteNote, nil
}

// serverTLSConfig is the TLS side: TLS 1.2 at the very least (Go negotiates 1.3
// with any current client), the certificate chosen by SNI, HTTP/2 and HTTP/1.1.
func serverTLSConfig(certs serverCerts) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: certs.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}

// tlsGuardConfig is the Config the guard sees for the LAN listener: its own
// address, its certificate names and the remote access name (RemoteHost, when
// set) are the allowed Hosts, and the loopback listener's AllowedHosts are NOT
// carried over. RemoteHost is not a name of the LAN certificate: the remote CA's
// leaf carries it (serverCerts).
func tlsGuardConfig(cfg Config, names certNames) Config {
	guard := Config{Listen: cfg.TLSListen}
	for _, ip := range names.IPs {
		guard.AllowedHosts = append(guard.AllowedHosts, ip.String())
	}
	guard.AllowedHosts = append(guard.AllowedHosts, names.DNS...)
	if cfg.RemoteHost != "" {
		guard.AllowedHosts = append(guard.AllowedHosts, cfg.RemoteHost)
	}
	return guard
}

// newTLSHandler assembles the LAN listener's chain: response headers, request
// log, the session gate (public routes pass, everything else needs a valid
// session), the view-only gate, the guard (https origins only), then the mux with
// the TLS-only routes. The login POST passes the guard like any other API call,
// so a cross-site login attempt is refused before a password is looked at.
func newTLSHandler(cfg Config, log *slog.Logger, certs serverCerts, names certNames, auth *authService, opts ...serveOpt) (http.Handler, error) {
	opts = append(opts, withAuth(auth))
	env := newServeEnv(opts)
	mux, err := buildMux(cfg, log, opts...)
	if err != nil {
		return nil, err
	}
	if env.runner != nil {
		auth.runner = env.runner
	}
	auth.remoteHost = cfg.RemoteHost
	auth.routes(mux)
	// The CA files and their fingerprints are for LAN and loopback peers only: a remote peer gets
	// the 404 of a route that does not exist. A CA fetched over the public name could be an
	// attacker's (trust on first use), so a phone installs the remote CA on the home network.
	lanOnly := func(w http.ResponseWriter, r *http.Request) bool {
		if auth.remote(r) {
			log.Warn("certificate authority refused to a remote peer", "path", r.URL.Path, "ip", sourceIP(r))
			http.NotFound(w, r)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/tls/ca.crt", func(w http.ResponseWriter, r *http.Request) {
		if !lanOnly(w, r) {
			return
		}
		mgr, file := certs.lan, "ipcctv-local-ca.crt"
		switch r.URL.Query().Get("ca") {
		case "":
		case "remote":
			if certs.remote == nil {
				http.NotFound(w, r)
				return
			}
			mgr, file = certs.remote, "holocron-remote-ca.crt"
		default:
			http.NotFound(w, r)
			return
		}
		data, err := mgr.CACertPEM()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "the certificate authority is not available")
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/tls/info", func(w http.ResponseWriter, r *http.Request) {
		if !lanOnly(w, r) {
			return
		}
		info, err := certs.lan.CAInfo()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "the certificate authority is not available")
			return
		}
		out := map[string]any{
			"caFingerprintSha256": info.Fingerprint,
			"caSubject":           info.Subject,
			"caNotAfter":          info.NotAfter.Format("2006-01-02"),
			"download":            "api/tls/ca.crt",
			"readOnly":            cfg.TLSReadOnlyEffective(),
		}
		if certs.remote != nil {
			remote, err := certs.remote.CAInfo()
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "the remote certificate authority is not available")
				return
			}
			out["remoteHost"] = certs.remote.remoteHost
			out["remoteCaFingerprintSha256"] = remote.Fingerprint
			out["remoteCaSubject"] = remote.Subject
			out["remoteCaNotAfter"] = remote.NotAfter.Format("2006-01-02")
			out["remoteDownload"] = "api/tls/ca.crt?ca=remote"
		}
		writeJSON(w, http.StatusOK, out)
	})
	chain := newGuardScheme(tlsGuardConfig(cfg, names), log, "https")(mux)
	if cfg.TLSReadOnlyEffective() {
		// After the session check (401 beats 403) and before the guard and any handler.
		chain = readOnlyGate(log, chain)
	}
	chain = logRequestsAt(log, env.reqLevel, auth.gate(mux, chain))
	// Outermost, so the redirects and every refusal carry it too.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		chain.ServeHTTP(w, r)
	}), nil
}

// startTLS prepares and binds the LAN listener. With TLSListen empty it returns
// nils. The returned func serves until the server is shut down.
func startTLS(cfg Config, log *slog.Logger, opts ...serveOpt) (*http.Server, func() error, error) {
	if cfg.TLSListen == "" {
		return nil, nil, nil
	}
	certs, names, note, remoteNote, err := prepareCerts(cfg, log)
	if err != nil {
		return nil, nil, err
	}
	clearUnlockMarkers(cfg.AuthDir)
	auth := newServeEnv(opts).auth
	if auth == nil {
		auth = newAuthService(cfg.AuthDir, newFileUserStore(cfg.AuthDir, time.Now), log, time.Now, defaultPasswordHasher)
	}
	handler, err := newTLSHandler(cfg, log, certs, names, auth, opts...)
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", cfg.TLSListen)
	if err != nil {
		return nil, nil, fmt.Errorf("bind %s: %w", cfg.TLSListen, err)
	}
	server := &http.Server{
		Handler:           handler,
		TLSConfig:         serverTLSConfig(certs),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	info, _ := certs.lan.CAInfo()
	log.Info("camui listening (HTTPS, login required)", "address", listener.Addr().String(), "certificate", note,
		"names", len(names.IPs)+len(names.DNS), "caFingerprintSha256", info.Fingerprint)
	if cfg.RemoteHost != "" {
		remoteInfo, _ := certs.remote.CAInfo()
		log.Info("remote access on for "+cfg.RemoteHost+", viewer-only", "remoteHost", cfg.RemoteHost, "certificate", remoteNote,
			"remoteCaFingerprintSha256", remoteInfo.Fingerprint, "policy", RemotePolicy())
	}
	return server, func() error { return server.ServeTLS(listener, "", "") }, nil
}

// runPrintCA implements -print-ca: it creates the CA if needed and prints where
// it is, its fingerprint and the install steps. It prints no key and no token.
func runPrintCA(cfg Config, out, errOut io.Writer) int {
	mgr := newCertManager(cfg.TLSDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	info, err := mgr.CAInfo()
	if err != nil {
		fmt.Fprintln(errOut, "cannot prepare the local CA:", err)
		return 1
	}
	WriteCAInstructions(out, info)
	return 0
}

// WriteCAInstructions prints where the CA certificate is, its fingerprint and how to install it
// on a device (shared by camui -print-ca and `holocron tls ca`). No key, no token.
func WriteCAInstructions(out io.Writer, info CAInfo) {
	fmt.Fprintf(out, "IPCCTV local CA\n  file:         %s\n  name:         %s\n  valid until:  %s\n  SHA-256:      %s\n\n", info.Path, info.Subject, info.NotAfter.Format("2006-01-02"), info.Fingerprint)
	fmt.Fprintln(out, "Install it once on each device (this file is the CA CERTIFICATE only; its key stays in the same folder and must stay private):")
	fmt.Fprintln(out, "  1. Get the file to the device: open https://<this PC's address>:<port>/ there, click through the first warning, sign in, and use the Phone tab (or Settings > Connect a phone for an admin, or open /api/tls/ca.crt), or copy the file.")
	fmt.Fprintln(out, "  2. Check the SHA-256 shown by the device against the line above before trusting it.")
	fmt.Fprintln(out, "  3. iPhone (not tested with Holocron): Settings > General > VPN & Device Management > install, then Settings > General > About > Certificate Trust Settings > enable it.")
	fmt.Fprintln(out, "     Android: Settings > Security > Install a certificate > CA certificate. Windows: double-click > Install > Local Machine/Current User > Trusted Root Certification Authorities.")
	fmt.Fprintln(out, "  Details and removal: docs/ui.md.")
}

// WriteRemoteCAInstructions prints the remote CA of `holocron tls ca --remote`: where it is, its
// fingerprint and how to install it on a phone, on the home network only. No key, no token.
func WriteRemoteCAInstructions(out io.Writer, info CAInfo, host string) {
	fmt.Fprintf(out, "Holocron remote CA for %s\n  file:         %s\n  name:         %s\n  valid until:  %s\n  SHA-256:      %s\n\n", host, info.Path, info.Subject, info.NotAfter.Format("2006-01-02"), info.Fingerprint)
	fmt.Fprintf(out, "It lets a device trust %s (and names under it) and nothing else: no address, no other name. Install it ONLY while the phone is on your home network:\n", host)
	fmt.Fprintln(out, "  1. Get the file to the phone over the LAN: open https://<this PC's address>:<port>/ there, sign in, and use the Phone tab (\"Reach this PC from outside the home\"), or copy the file. It is never offered over the public name.")
	fmt.Fprintln(out, "  2. Check the SHA-256 shown by the phone against the line above before trusting it.")
	fmt.Fprintln(out, "  3. iPhone (not tested with Holocron): Settings > General > VPN & Device Management > install, then Settings > General > About > Certificate Trust Settings > enable it.")
	fmt.Fprintln(out, "     Android: Settings > Security > Install a certificate > CA certificate. Windows: double-click > Install > Local Machine/Current User > Trusted Root Certification Authorities.")
	fmt.Fprintln(out, "  When web.remoteHost changes, the web app makes a new remote CA at its next start and every phone must install the new one. Details: docs/ui.md.")
}

// ---------------------------------------------------------------------------
// View-only mode for the HTTPS listener. A lost phone or a guessed password must be able
// to WATCH but not to stop recording or reconfigure anything, so the gate is an
// allow-list: a route that is not named here is refused, including one added
// later until someone deliberately allows it.
// ---------------------------------------------------------------------------

const readOnlyMessage = "read-only: this address can only watch"

var (
	readOnlyGETPaths = map[string]bool{
		"/api/status": true, "/api/live": true, "/api/cameras": true, "/api/config": true,
		"/api/footage": true, "/api/clip": true, "/api/tls/ca.crt": true, "/api/tls/info": true,
		"/api/auth/me": true,
	}
	readOnlyWhep    = regexp.MustCompile(`^/api/live/[^/]+/whep$`)
	readOnlySession = regexp.MustCompile(`^/api/live/session/[^/]+$`)
)

// readOnlyAllowed reports whether a request may pass in view-only mode.
// POST /api/acknowledge is refused on purpose: it is harmless but it changes
// state, and the rule is "nothing that changes state except what the Live
// screen needs" (opening and closing its own viewing sessions).
func readOnlyAllowed(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		if strings.HasPrefix(path, "/api/") {
			return readOnlyGETPaths[path]
		}
		return true // the embedded page and its assets
	case http.MethodPost:
		// Signing in and out is not a change to the recorder.
		return readOnlyWhep.MatchString(path) || path == "/api/auth/login" || path == "/api/auth/logout"
	case http.MethodDelete:
		return readOnlySession.MatchString(path)
	}
	return false
}

func readOnlyGate(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readOnlyAllowed(r.Method, r.URL.Path) {
			log.Warn("request refused", "method", r.Method, "path", r.URL.Path, "reason", "read-only listener")
			writeAPIError(w, http.StatusForbidden, readOnlyMessage)
			return
		}
		next.ServeHTTP(w, r)
	})
}
