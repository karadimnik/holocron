package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSupervisor is a stand-in for the Go supervisor's control endpoint: it answers the three
// calls the APIController makes and remembers what it was asked and by whom.
type fakeSupervisor struct {
	srv  *httptest.Server
	root string

	mu      sync.Mutex
	calls   []string
	headers []http.Header
	busy    bool
	running bool
}

const fakeSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newFakeSupervisor(t *testing.T, root string) *fakeSupervisor {
	t.Helper()
	f := &fakeSupervisor{root: root, running: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Holocron-Key") != fakeSecret {
			http.Error(w, `{"ok":false,"error":"bad key"}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
		f.headers = append(f.headers, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		if f.busy && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"ok":false,"error":"another control action is running"}`)
			return
		}
		switch {
		case r.URL.Path == "/v1/state":
			io.WriteString(w, fmt.Sprintf(`{"ok":true,"data":{"running":%v,"pid":4242,"cameras":[{"name":"Front","record":true,"alive":true}]}}`, f.running))
		case r.URL.Path == "/v1/reload":
			io.WriteString(w, `{"ok":true}`)
		case r.URL.Path == "/v1/stop":
			io.WriteString(w, `{"ok":true,"data":{"kind":"requested","reason":"stop requested","survivors":[],"final":true}}`)
		case strings.HasPrefix(r.URL.Path, "/v1/cameras/") && strings.HasSuffix(r.URL.Path, "/record"):
			io.WriteString(w, `{"ok":true,"data":{"target":"Front","enabled":false,"changed":true}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	state := filepath.Join(root, "_state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(state, "ctl.addr"), []byte(strings.TrimPrefix(f.srv.URL, "http://")), 0o600)
	os.WriteFile(filepath.Join(state, "ctl.secret"), []byte(fakeSecret), 0o600)
	return f
}

func (f *fakeSupervisor) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSupervisor) count(prefix string) int {
	n := 0
	for _, c := range f.callList() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// controllerRig is an API over a sandbox root with (optionally) a fake supervisor.
type controllerRig struct {
	api  *API
	root string
}

func newControllerRig(t *testing.T) *controllerRig {
	t.Helper()
	root := t.TempDir()
	scripts := t.TempDir()
	os.WriteFile(filepath.Join(scripts, "cameras.json"), []byte(`[{"Name":"Front","IP":"192.0.2.10","Quality":"stream1","Record":true}]`), 0o600)
	os.WriteFile(filepath.Join(scripts, "recorder.config.json"), []byte(`{}`), 0o600)
	rig := &controllerRig{root: root}
	api := NewAPI(Config{ArchiveRoot: root, StatusFile: filepath.Join(root, "_state", "status.json"), ScriptDir: scripts}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rig.api = api
	return rig
}

func (r *controllerRig) post(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	r.api.Routes(mux)
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (r *controllerRig) statusController(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	r.api.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/status", nil))
	var out struct{ Controller string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return out.Controller
}

// With a live ctl.addr the page's Stop and record toggle go to the control API.
func TestControllerAPIWhenEndpointAnswers(t *testing.T) {
	rig := newControllerRig(t)
	sup := newFakeSupervisor(t, rig.root)
	if got := rig.statusController(t); got != "api" {
		t.Fatalf("controller %q", got)
	}
	code, body := rig.post(t, "/api/control", `{"scope":"all","action":"stop"}`)
	if code != 200 || body["ok"] != true || body["command"] != "POST /v1/stop" {
		t.Fatalf("stop: %d %v", code, body)
	}
	code, body = rig.post(t, "/api/cameras/Front/record", `{"enabled":false}`)
	if code != 200 || body["ok"] != true || !strings.Contains(fmt.Sprint(body["output"]), "Front: record off") {
		t.Fatalf("record: %d %v", code, body)
	}
	code, body = rig.post(t, "/api/admin/reload", `{}`)
	if code != 200 || body["ok"] != true {
		t.Fatalf("reload: %d %v", code, body)
	}
	calls := sup.callList()
	if sup.count("POST /v1/stop") != 1 || sup.count(`POST /v1/cameras/Front/record {"enabled":false}`) != 1 || sup.count("POST /v1/reload") != 1 {
		t.Errorf("calls %v", calls)
	}
	// the actor and the action id travel to the supervisor's records
	sup.mu.Lock()
	defer sup.mu.Unlock()
	for _, h := range sup.headers {
		if h.Get("X-Holocron-Actor") != "web:loopback" && !strings.HasPrefix(h.Get("X-Holocron-Actor"), "web:") && h.Get("X-Holocron-Actor") != "" {
			t.Errorf("actor %q", h.Get("X-Holocron-Actor"))
		}
	}
	last := sup.headers[len(sup.headers)-1]
	if last.Get("X-Holocron-Actor") != "web:loopback" || len(last.Get("X-Action-Id")) != 16 {
		t.Errorf("headers actor %q action id %q", last.Get("X-Holocron-Actor"), last.Get("X-Action-Id"))
	}
}

// The choice is cached for a few seconds; after that the endpoint is asked again, and one that
// stopped answering leaves the API controller in its "stopped" form.
func TestControllerChoiceIsCachedThenRefreshed(t *testing.T) {
	rig := newControllerRig(t)
	sup := newFakeSupervisor(t, rig.root)
	rig.statusController(t)
	rig.statusController(t)
	rig.statusController(t)
	if n := sup.count("GET /v1/state"); n != 1 {
		t.Errorf("%d probes for 3 requests, want 1", n)
	}
	sup.srv.Close()
	rig.api.pickedAt = time.Now().Add(-time.Minute)
	if got := rig.statusController(t); got != "api" {
		t.Errorf("after the endpoint went away the controller is %q", got)
	}
	if c, ok := rig.api.picked.(*APIController); !ok || !c.Stopped {
		t.Errorf("after the endpoint went away the controller is %#v, want the stopped form", rig.api.picked)
	}
}

// A busy supervisor is a 409 on the page, a missing record is not swallowed.
func TestControllerAPIBusyIs409(t *testing.T) {
	rig := newControllerRig(t)
	sup := newFakeSupervisor(t, rig.root)
	sup.busy = true
	code, body := rig.post(t, "/api/control", `{"scope":"all","action":"stop"}`)
	if code != http.StatusConflict || body["ok"] != false || !strings.Contains(fmt.Sprint(body["error"]), "another control action") {
		t.Errorf("%d %v", code, body)
	}
	code, body = rig.post(t, "/api/admin/reload", `{}`)
	if code != http.StatusConflict || body["ok"] != false || !strings.Contains(fmt.Sprint(body["error"]), "another control action") {
		t.Errorf("reload: %d %v", code, body)
	}
}

// The holocron web role with no supervisor: a clear message.
func TestControllerStoppedWithNoSupervisor(t *testing.T) {
	rig := newControllerRig(t)
	if got := rig.statusController(t); got != "api" {
		t.Fatalf("controller %q", got)
	}
	code, body := rig.post(t, "/api/control", `{"scope":"all","action":"start"}`)
	if code != http.StatusConflict || !strings.Contains(fmt.Sprint(body["error"]), "holocron up") {
		t.Errorf("start: %d %v", code, body)
	}
	if code, body = rig.post(t, "/api/control", `{"scope":"all","action":"stop"}`); code != http.StatusConflict || !strings.Contains(fmt.Sprint(body["error"]), "nothing is running") {
		t.Errorf("stop: %d %v", code, body)
	}
	if code, body = rig.post(t, "/api/cameras/Front/record", `{"enabled":false}`); code != http.StatusConflict || !strings.Contains(fmt.Sprint(body["error"]), "holocron cam") {
		t.Errorf("record: %d %v", code, body)
	}
	if code, body = rig.post(t, "/api/admin/reload", `{}`); code != http.StatusConflict || body["ok"] != false || !strings.Contains(fmt.Sprint(body["error"]), "not running") {
		t.Errorf("reload: %d %v", code, body)
	}
	// settings writes without a SettingsWriter are refused plainly
	mux := http.NewServeMux()
	rig.api.Routes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/config", strings.NewReader(`{"config":{"SegmentSeconds":300}}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "holocron config set") {
		t.Errorf("config PUT: %d %s", rec.Code, rec.Body.String())
	}
}

// Start runs `<this executable> up --detach` with the web role's own flags, only when it was given the executable.
func TestControllerStartRunsUpDetach(t *testing.T) {
	if os.Getenv("WEB_TEST_HELPER") == "1" {
		fmt.Println("helper args:", strings.Join(os.Args[1:], " "))
		os.Exit(0)
	}
	rig := newControllerRig(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEB_TEST_HELPER", "1")
	rig.api.launch = &HolocronLaunch{Exe: exe, Args: []string{"-test.run=TestControllerStartRunsUpDetach", "--"}}
	code, body := rig.post(t, "/api/control", `{"scope":"recorder","action":"start"}`)
	if code != 200 || body["ok"] != true || !strings.Contains(fmt.Sprint(body["output"]), "helper args: ") || !strings.Contains(fmt.Sprint(body["output"]), "up --detach") {
		t.Errorf("%d %v", code, body)
	}
	if body["command"] != "holocron up --detach" {
		t.Errorf("command %v", body["command"])
	}
	// the dashboard scope is never started from the page
	if code, _ = rig.post(t, "/api/control", `{"scope":"dashboard","action":"start"}`); code != http.StatusConflict {
		t.Errorf("dashboard start: %d", code)
	}
}

// A Start right after a Stop runs `up --detach`: the stop expired the cached choice, so the start
// sees that the endpoint no longer answers instead of refusing with "already running".
func TestControllerStartRightAfterStop(t *testing.T) {
	rig := newControllerRig(t)
	sup := newFakeSupervisor(t, rig.root)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEB_TEST_HELPER", "1")
	rig.api.launch = &HolocronLaunch{Exe: exe, Args: []string{"-test.run=TestControllerStartRunsUpDetach", "--"}}
	if code, body := rig.post(t, "/api/control", `{"scope":"all","action":"stop"}`); code != 200 || body["ok"] != true {
		t.Fatalf("stop: %d %v", code, body)
	}
	sup.srv.Close() // the supervisor has stopped
	code, body := rig.post(t, "/api/control", `{"scope":"all","action":"start"}`)
	if code != 200 || body["ok"] != true || body["command"] != "holocron up --detach" {
		t.Errorf("start right after the stop: %d %v", code, body)
	}
}

// The tasks strip shows the supervisor and the web app themselves.
func TestControllerAPITaskStates(t *testing.T) {
	rig := newControllerRig(t)
	sup := newFakeSupervisor(t, rig.root)
	c := &APIController{Root: rig.root}
	tasks := c.TaskStates()
	if tasks["recorder"].Status != "Running" || !tasks["recorder"].Exists || tasks["dashboard"].Status != "Running" {
		t.Errorf("%+v", tasks)
	}
	sup.srv.Close()
	c2 := &APIController{Root: rig.root}
	if got := c2.TaskStates()["recorder"].Status; got != "Stopped" {
		t.Errorf("recorder %q with no endpoint", got)
	}
}

// The Dashboard shows state, not commands (D24): no Start/Stop-everything row; its top is a polite
// status region; the only button in its markup is the standalone Start, hidden until a status says
// the supervisor is not running, and never shown to a viewer (app.js renderStandalone).
func TestDashboardShowsStateNotCommands(t *testing.T) {
	page, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{`id="controls"`, "Start everything", "Stop everything", `data-action="stop"`} {
		if strings.Contains(string(page), gone) {
			t.Errorf("index.html still has %s", gone)
		}
	}
	dash := regexp.MustCompile(`(?s)<section data-tab="dash"[^>]*>(.*?)</section>`).FindSubmatch(page)
	if dash == nil {
		t.Fatal("no Dashboard section in index.html")
	}
	if !strings.Contains(string(dash[1]), `<div id="summary" role="status" aria-live="polite" aria-atomic="true">`) {
		t.Error("the Dashboard has no polite status summary")
	}
	if i, j := strings.Index(string(dash[1]), `id="summary"`), strings.Index(string(dash[1]), `id="stats"`); i < 0 || j < i {
		t.Error("the status summary is not above the figures")
	}
	buttons := regexp.MustCompile(`<button[^>]*>[^<]*</button>`).FindAllString(string(dash[1]), -1)
	if want := `<button class="btn primary" id="startHolocron" type="button" hidden>Start Holocron</button>`; strings.Join(buttons, "\n") != want {
		t.Errorf("Dashboard buttons:\n%s\nwant:\n%s", strings.Join(buttons, "\n"), want)
	}
	app, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{`el("standalone").hidden = systemState !== "stopped" && !starting;`, `button.hidden = viewOnly || !accessKnown;`} {
		if !strings.Contains(string(app), rule) {
			t.Errorf("app.js lost the rule %s", rule)
		}
	}
	keys := regexp.MustCompile(`"((?:start|stop):\w+)": \{ seconds:`).FindAllStringSubmatch(string(app), -1)
	if len(keys) != 2 || keys[0][1] != "start:all" || keys[1][1] != "stop:all" {
		t.Errorf("ACTION_EXPECTATION entries %v, want start:all and stop:all", keys)
	}
}

// A camera's LastError is put into words (B8, D24): app.js's CAMERA_ERRORS must match the texts the
// supervisor really writes (its format strings are read from internal/supervise, so a change there
// fails here), an unknown text matches nothing and is shown as it is, the note keeps the raw text
// in its tooltip and under Details, is no live region, and the status summary does not look at it.
func TestCameraNoteWording(t *testing.T) {
	formats := map[string]string{
		"account.go": `"ffmpeg exited with code %d after %ds"`,
		"plan.go":    `"stalled, no segment write for %ds (limit %ds) while the process was alive"`,
	}
	for file, format := range formats {
		src, err := os.ReadFile(filepath.Join("..", "supervise", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), format) {
			t.Errorf("internal/supervise/%s no longer writes %s: update CAMERA_ERRORS in app.js", file, format)
		}
	}
	if src, err := os.ReadFile(filepath.Join("..", "supervise", "account.go")); err != nil || !strings.Contains(string(src), `e.lastError = "failed to start: " + err.Error()`) {
		t.Errorf("internal/supervise/account.go no longer writes \"failed to start: \" (%v): update CAMERA_ERRORS in app.js", err)
	}

	appJS, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appJS)
	table := regexp.MustCompile(`(?s)\nconst CAMERA_ERRORS = \[\n(.*?)\n\];\n`).FindStringSubmatch(app)
	if table == nil {
		t.Fatal("no CAMERA_ERRORS in app.js")
	}
	var patterns []*regexp.Regexp
	for _, m := range regexp.MustCompile(`(?m)^  \[/(.+?)/, \(m?\) => "`).FindAllStringSubmatch(table[1], -1) {
		patterns = append(patterns, regexp.MustCompile(m[1]))
	}
	if len(patterns) != 4 {
		t.Fatalf("CAMERA_ERRORS has %d entries the test can read, want 4:\n%s", len(patterns), table[1])
	}
	first := func(message string) int {
		for i, p := range patterns {
			if p.MatchString(message) {
				return i
			}
		}
		return -1
	}
	for _, c := range []struct {
		message string
		want    int
	}{
		{fmt.Sprintf("ffmpeg exited with code %d after %ds", 0, 16827), 0},
		{fmt.Sprintf("ffmpeg exited with code %d after %ds", 1, 95), 1},
		{fmt.Sprintf("ffmpeg exited with code %d after %ds", -1, 0), 1},
		{fmt.Sprintf("ffmpeg exited with code %d after %ds", 3221225786, 7200), 1},
		{fmt.Sprintf("stalled, no segment write for %ds (limit %ds) while the process was alive", 1950, 1800), 2},
		{"failed to start: exec: \"ffmpeg\": executable file not found in $PATH", 3},
		{"rtsp: connection refused (192.168.1.50)", -1},
		{"ffmpeg exited with code 0 after 16827s and more", -1},
	} {
		if got := first(c.message); got != c.want {
			t.Errorf("%q matches CAMERA_ERRORS entry %d, want %d", c.message, got, c.want)
		}
	}

	for _, want := range []string{
		`(m) => "the camera stream ended after " + duration(+m[1])`,
		`(m) => "the recorder stopped unexpectedly (exit code " + m[1] + ") after " + duration(+m[2])`,
		`let words = "the recorder reported: " + message;`,
		`(alive ? "Restarted automatically; recording resumed." : "Not recording now.")`,
		`const NOTE_VISIBLE_MS = 24 * 3600 * 1000;`,
		`const quiet = alive && at !== null && now - at.getTime() > NOTE_VISIBLE_MS;`,
		`'<div class="cnote' + (alive ? "" : " bad") + '" title="' + esc(raw) + '">`,
		`"><summary>Details</summary>" + (quiet ? "<p>" + esc(sentence) + "</p>" : "") + "<code>" + esc(raw) + "</code></details>"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js has no %s", want)
		}
	}
	note := regexp.MustCompile(`(?s)\nfunction cameraNote\(cam, open\) \{(.*?)\n\}\n`).FindStringSubmatch(app)
	if note == nil {
		t.Fatal("no cameraNote in app.js")
	}
	for _, gone := range []string{"aria-live", `role="status"`, `role="alert"`} {
		if strings.Contains(note[1], gone) {
			t.Errorf("the camera note is a live region (%s): it would be read out on every poll", gone)
		}
	}
	if !strings.Contains(note[1], `aria-hidden="true"`) {
		t.Error("the camera note's icon is not hidden from screen readers (the words carry the meaning)")
	}
	for _, use := range []string{"const note = cameraNote(cam, open);", "cameraDetail(cam, live, openNotes)", "? cameraNote(cam, openNotes) : null;"} {
		if !strings.Contains(app, use) {
			t.Errorf("app.js has no %s: both the table and the compact rows show the note", use)
		}
	}
	summary := regexp.MustCompile(`(?s)// ---- summary: the verdict, then what needs attention ----(.*?)renderSummary\(summary\);`).FindStringSubmatch(app)
	if summary == nil {
		t.Fatal("no status summary in render")
	}
	for _, gone := range []string{"LastError", "RestartsLastHour"} {
		if strings.Contains(summary[1], gone) {
			t.Errorf("the status summary looks at %s: a restart the supervisor recovered from is not a problem", gone)
		}
	}
}

// Switching a camera off asks nothing and offers Undo (D24): toggleRecord holds no confirmation, the
// Undo calls the same endpoint to switch it back on, and the toast region is a polite live region.
func TestRecordSwitchOffOffersUndo(t *testing.T) {
	app, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)\nasync function toggleRecord\(name, enable\) \{(.*?)\n\}\n`).FindSubmatch(app)
	if body == nil {
		t.Fatal("no toggleRecord in app.js")
	}
	if strings.Contains(string(body[1]), "confirmAction") {
		t.Error("switching a camera off asks for a confirmation")
	}
	if !strings.Contains(string(body[1]), "else undoToast(name);") {
		t.Error("switching a camera off offers no Undo")
	}
	for _, want := range []string{"const UNDO_MS = 8000;", `toggleRecord(name, true);`, `id="toastUndo"`} {
		if !strings.Contains(string(app), want) {
			t.Errorf("app.js has no %s", want)
		}
	}
	page, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `<div id="toast" role="status" aria-live="polite"></div>`) {
		t.Error("the toast region is not a polite live region")
	}
}

// Stop lives in Admin > System only (D24): its dialog and the reload call are in admin.js, which only
// an admin receives; the dialog needs STOP typed, starts on Cancel, and the page every user gets has
// the "Holocron has stopped" panel that replaces "Server unreachable" after a stop it asked for.
func TestStopIsAdminOnlyAndTypedConfirmed(t *testing.T) {
	admin, err := staticFiles.ReadFile("static/admin.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`post("api/admin/reload", {}, "aReloadNote")`, `<dialog id="aStopDialog"`, `class="dzone"`,
		`const stopTyped = () => el("aStopWord").value.trim().toLowerCase() === "stop";`,
		`<button class="btn" value="cancel" id="aStopCancel" autofocus>Cancel</button>`,
		`<button class="btn danger" value="stop" id="aStopGo" disabled>Stop Holocron</button>`,
		`el("aStopDialog").returnValue === "stop" && stopTyped()`, "holocron up --detach"} {
		if !strings.Contains(string(admin), want) {
			t.Errorf("admin.js has no %s", want)
		}
	}
	page, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"aStopDialog", "api/admin/reload", `"stop"`} {
		if strings.Contains(string(page), gone) {
			t.Errorf("index.html, served to viewers too, has %s", gone)
		}
	}
	if !strings.Contains(string(page), `<section id="stoppedPanel" class="stopped" hidden`) {
		t.Error("index.html has no hidden stopped panel")
	}
	app, err := staticFiles.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(app), "if (fetchError && stopped) {") || !strings.Contains(string(app), "if (!fetchError && live) stopped = null;") {
		t.Error("app.js does not show the stopped panel only after its own stop")
	}
}
