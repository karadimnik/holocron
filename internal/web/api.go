package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"holocron/internal/archive"
	"holocron/internal/cli"
)

// StatusResponse is the body of GET /api/status.
type StatusResponse struct {
	Schema     int                 `json:"schema"`
	ServerTime string              `json:"serverTime"`
	StatusFile StatusFileInfo      `json:"statusFile"`
	Supervisor SupervisorState     `json:"supervisor"`
	Status     map[string]any      `json:"status"`
	Tasks      map[string]TaskInfo `json:"tasks"`
	// Controller is how control actions are carried out: "api" (the Go supervisor's control
	// endpoint).
	Controller string `json:"controller"`

	// Acknowledged maps a persistent notice's key to the timestamp of the
	// event the Operator has said he has seen. See notice.go.
	Acknowledged map[string]string `json:"acknowledged"`

	// App is the build this web app runs. The page compares App.Version with the version it was
	// served with (index.html) to notice a Holocron upgraded under an open page.
	App AppInfo `json:"app"`
}

// AppInfo is the running build: the version `holocron version` prints and the Go runtime.
type AppInfo struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Go      string `json:"go"`
}

func appInfo() AppInfo {
	return AppInfo{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version()}
}

// StatusFileInfo describes the status.json read, so the page can tell live data
// from a stale file left behind by a supervisor that died.
type StatusFileInfo struct {
	Path       string  `json:"path"`
	Present    bool    `json:"present"`
	ModifiedAt string  `json:"modifiedAt"`
	AgeSeconds float64 `json:"ageSeconds"`
	Error      string  `json:"error"`
}

// TaskInfo is one chip of the page's "Tasks" strip: the supervisor or the web
// app (APIController.TaskStates).
type TaskInfo struct {
	Name       string `json:"name"`
	Exists     bool   `json:"exists"`
	Status     string `json:"status"`
	Enabled    string `json:"enabled"`
	LastResult string `json:"lastResult"`
	Error      string `json:"error"`
}

// API serves the whole HTTP API: status from a file read, actions through the
// Controller and settings writes through the SettingsWriter.
type API struct {
	cfg Config
	log *slog.Logger

	// alive reports whether a PID is a live process, injected so the handlers
	// can be tested. It is what makes a stale "running" status detectable.
	alive func(int) bool

	// actionMu serialises every state change: one action at a time.
	actionMu sync.Mutex

	// ackMu serialises read-modify-write on the acknowledgement sidecar.
	ackMu sync.Mutex

	// footageCache is the footage index's archive.Cache, created on first use (footage.go).
	footageCache *archive.Cache
	footageOnce  sync.Once

	// live is the live-view session table, created on first use (live.go).
	live     *liveHub
	liveOnce sync.Once

	// The Controller seam (controller.go) and the Runner of the mutating routes (actions.go).
	runner    *cli.Runner
	ctlFixed  Controller // Options.Controller or a test: used as is
	dialCtl   func(root string) (apiClient, error)
	launch    *HolocronLaunch
	settings  SettingsWriter // the Go system's settings writer (nil: PUT /api/cameras and /api/config answer 501 there)
	admin     AdminTools     // the Admin tab's command-line helpers (nil: those sections answer 501)
	auth      *authService   // the LAN listener's running sign-in service, when there is one
	usersOnce sync.Once
	userAdmin *UserAdmin
	pickMu    sync.Mutex
	picked    Controller
	pickedAt  time.Time
}

// NewAPI returns an API serving from cfg.
func NewAPI(cfg Config, log *slog.Logger) *API {
	api := &API{cfg: cfg, log: log, alive: processAlive}
	api.runner = runnerFor(log)
	return api
}

// Routes registers the API handlers on mux.
func (a *API) Routes(mux *http.ServeMux) {
	if a.runner == nil { // an API built as a literal (tests)
		a.runner = runnerFor(a.log)
	}
	mux.HandleFunc("GET /api/status", a.handleStatus)
	handleAction(mux, a.runner, "POST /api/acknowledge", ActionAcknowledge, a.handleAcknowledge)
	handleAction(mux, a.runner, "POST /api/control", ActionControl, a.handleControl)
	handleAction(mux, a.runner, "POST /api/cameras/{name}/record", ActionRecord, a.handleRecordToggle)
	mux.HandleFunc("GET /api/cameras", a.handleGetCameras)
	handleAction(mux, a.runner, "PUT /api/cameras", ActionCamerasPut, a.handlePutCameras)
	mux.HandleFunc("GET /api/config", a.handleGetConfig)
	handleAction(mux, a.runner, "PUT /api/config", ActionConfigPut, a.handlePutConfig)
	mux.HandleFunc("GET /api/footage", a.handleFootage)
	mux.HandleFunc("GET /api/clip", a.handleClip)
	a.liveRoutes(mux)
	a.adminRoutes(mux)
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	body := StatusResponse{
		Schema:     1,
		ServerTime: time.Now().Format(time.RFC3339),
		StatusFile: StatusFileInfo{Path: a.cfg.StatusFile},
		Tasks:      a.controller().TaskStates(),
		Controller: a.controller().Name(),
		App:        appInfo(),
	}

	status, info, err := a.readStatus()
	body.StatusFile.Present = info.Present
	body.StatusFile.ModifiedAt = info.ModifiedAt
	body.StatusFile.AgeSeconds = info.AgeSeconds
	if err != nil {
		// A missing or malformed status file is a state the page must
		// render, not a reason to fail the request.
		body.StatusFile.Error = err.Error()
		a.log.Warn("status file unreadable", "path", a.cfg.StatusFile, "error", err)
	} else {
		dropLivePaths(status)
		body.Status = status
	}

	// The verdict the page branches on, decided here rather than in the
	// browser: State alone is not liveness (supervisor.go has the rule).
	body.Supervisor = SupervisorStateOf(body.Status, a.statusIntervalSeconds(), time.Now(), a.alive)

	// Which persistent notices have been seen. Read here so the page knows
	// what to hide without holding any of that state in the browser, where it
	// would not survive a reload or reach a second browser. An unreadable
	// sidecar reports nothing acknowledged, so the banner shows - the safe
	// direction for a notice about an outage.
	if acknowledged, err := a.acknowledgedNotices(); err != nil {
		a.log.Warn("acknowledgement sidecar unreadable", "path", a.cfg.AcknowledgedFile(), "error", err)
	} else {
		body.Acknowledged = acknowledged
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(body); err != nil {
		a.log.Error("write status response", "error", err)
	}
}

// readStatus reads status.json and returns it redacted.
func (a *API) readStatus() (map[string]any, StatusFileInfo, error) {
	info := StatusFileInfo{Path: a.cfg.StatusFile}

	stat, err := os.Stat(a.cfg.StatusFile)
	if err != nil {
		return nil, info, fmt.Errorf("status file not readable: %w", err)
	}
	info.Present = true
	info.ModifiedAt = stat.ModTime().Format(time.RFC3339)
	info.AgeSeconds = roundTo(time.Since(stat.ModTime()).Seconds(), 1)

	raw, err := readStatusFile(a.cfg.StatusFile)
	if err != nil {
		return nil, info, fmt.Errorf("read status file: %w", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(stripBOM(raw), &parsed); err != nil {
		return nil, info, fmt.Errorf("parse status file: %w", err)
	}

	redacted, ok := Redact(parsed).(map[string]any)
	if !ok {
		return nil, info, fmt.Errorf("status file is not a JSON object")
	}
	return redacted, info, nil
}

// ---------------------------------------------------------------------------
// Redaction
//
// Frigate's CVE-2026-33469 handed camera RTSP credentials to any logged-in
// viewer. Cameras here are addressed by name, so nothing in the browser needs
// an address at all, and anything shaped like one is removed on the way out.
// ---------------------------------------------------------------------------

var (
	// urlPattern matches any scheme://... token, which is the shape that
	// carries both a host and any userinfo credential.
	urlPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.\-]*://[^\s"']*`)

	// ipv4Pattern matches a bare dotted-quad address, which is how a camera
	// appears in error text with no scheme in front of it.
	ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

	// deniedKeys are dropped wherever they appear, at any depth. RelayBase is
	// the restreamer's own rtsp:// base and the page has no use for it.
	deniedKeys = map[string]bool{
		"relaybase": true,
		"password":  true,
		"username":  true,
	}
)

// Redact returns a copy of a decoded JSON value with address-shaped and
// credential-shaped content removed.
func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			if deniedKeys[strings.ToLower(key)] {
				continue
			}
			out[key] = Redact(inner)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = Redact(inner)
		}
		return out
	case string:
		return ScrubText(typed)
	default:
		return value
	}
}

// ScrubText removes URLs and bare IPv4 addresses from a string. The scheme goes
// too: leaving "rtsp://[redacted]" behind would make "no URL of any scheme
// survives" an invariant with an exception in it, and an invariant with an
// exception is one a later change can widen without anyone noticing.
func ScrubText(text string) string {
	scrubbed := urlPattern.ReplaceAllString(text, "[redacted]")
	return ipv4Pattern.ReplaceAllString(scrubbed, "[redacted]")
}

func stripBOM(raw []byte) []byte {
	return []byte(strings.TrimPrefix(string(raw), "\ufeff"))
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		return strings.TrimSpace(text[:i])
	}
	return text
}

func roundTo(value float64, places int) float64 {
	factor := 1.0
	for i := 0; i < places; i++ {
		factor *= 10
	}
	return float64(int64(value*factor+0.5)) / factor
}
