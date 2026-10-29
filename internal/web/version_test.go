package web

import (
	"encoding/json"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// setWebVersion stands in for the -X holocron/internal/web.version build flag for one test. The
// page is stamped when the handler is built, so set it before building one.
func setWebVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// GET /api/status carries the running build as an additive top-level "app" object.
func TestStatusReportsTheAppVersion(t *testing.T) {
	setWebVersion(t, "v9.9.9")
	h, _ := loopbackHandler(t, nil)
	rec := (probe{path: "/api/status"}).do(h)
	var body struct {
		Schema int
		App    map[string]string `json:"app"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != 200 || err != nil {
		t.Fatalf("GET /api/status: %d %v", rec.Code, err)
	}
	want := map[string]string{"version": "v9.9.9", "os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version()}
	if body.Schema != 1 || len(body.App) != len(want) {
		t.Fatalf("schema %d, app %v", body.Schema, body.App)
	}
	for k, v := range want {
		if body.App[k] != v {
			t.Errorf("app.%s = %q, want %q", k, body.App[k], v)
		}
	}
}

// The page is served with the version it belongs to written into it (escaped), keeps the version
// as its ETag so a revalidation is a 304, and is the only file so stamped.
func TestPageIsStampedWithTheVersion(t *testing.T) {
	setWebVersion(t, `v9.9.9<"b9">`)
	h, _ := loopbackHandler(t, nil)
	page := (probe{path: "/"}).do(h)
	if page.Code != 200 || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET /: %d %v", page.Code, page.Header())
	}
	if want := `<meta name="holocron-version" content="v9.9.9&lt;&#34;b9&#34;&gt;">`; !strings.Contains(page.Body.String(), want) {
		t.Errorf("the page lacks %s", want)
	}
	setWebVersion(t, "v9.9.9")
	h, _ = loopbackHandler(t, nil)
	etag := (probe{path: "/"}).do(h).Header().Get("ETag")
	if etag != `"v9.9.9"` {
		t.Errorf("ETag %q", etag)
	}
	if rec := (probe{path: "/", headers: map[string]string{"If-None-Match": etag}}).do(h); rec.Code != 304 {
		t.Errorf("revalidation: %d, want 304", rec.Code)
	}
	if js := (probe{path: "/app.js"}).do(h); js.Code != 200 || strings.Contains(js.Body.String(), "v9.9.9") {
		t.Errorf("app.js: %d, stamped %v", js.Code, strings.Contains(js.Body.String(), "v9.9.9"))
	}
}

// The version is shown only to signed-in users and on loopback: the sign-in page, its script, the
// sign-in redirect, the session answer and the first-user page carry none of it.
func TestPublicPagesCarryNoVersion(t *testing.T) {
	const v = "v9.9.9-public-probe"
	setWebVersion(t, v)
	rig := newTLSRig(t, nil)
	for _, q := range []tlsReq{
		{path: "/login", headers: map[string]string{"Accept": "text/html"}},
		{path: "/login.js"},
		{path: "/", headers: map[string]string{"Accept": "text/html"}},
		{path: "/api/auth/me"},
		{path: "/api/status"},
	} {
		if rec := rig.do(q); strings.Contains(rec.Body.String(), v) {
			t.Errorf("%s without a session (%d) shows the version", q.path, rec.Code)
		}
	}
	if rec := rig.do(tlsReq{path: "/", headers: map[string]string{"Accept": "text/html"}}); rec.Code != 303 {
		t.Errorf("GET / without a session: %d, want the sign-in redirect", rec.Code)
	}
	if rec := rig.do(tlsReq{path: "/api/status", cookie: rig.login(t)}); rec.Code != 200 || !strings.Contains(rec.Body.String(), v) {
		t.Errorf("signed in, /api/status: %d, version shown %v", rec.Code, strings.Contains(rec.Body.String(), v))
	}
	h, _ := loopbackHandler(t, nil)
	for _, p := range []string{"/setup", "/setup.js"} {
		if rec := (probe{path: p}).do(h); rec.Code != 200 || strings.Contains(rec.Body.String(), v) {
			t.Errorf("loopback %s: %d, version shown %v", p, rec.Code, strings.Contains(rec.Body.String(), v))
		}
	}
}

// The markup and script of the version line, the stale-page notice and the Admin About block.
func TestVersionMarkupAndStalePageRules(t *testing.T) {
	read := func(name string) string {
		b, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	page, app, admin := read("index.html"), read("app.js"), read("admin.js")
	if strings.Count(page, versionMeta) != 1 {
		t.Errorf("index.html must hold the placeholder %s once", versionMeta)
	}
	// The footer is inside <main>, which the stopped panel replaces (body.is-stopped main).
	main := regexp.MustCompile(`(?s)<main id="main">(.*)</main>`).FindStringSubmatch(page)
	if main == nil || !strings.Contains(main[1], `<footer class="appver" id="appVersion"></footer>`) {
		t.Error("the version footer is not under every tab inside <main>")
	}
	stoppedPanel := regexp.MustCompile(`(?s)<section id="stoppedPanel".*?</section>`).FindString(page)
	if stoppedPanel == "" || strings.Contains(stoppedPanel, "ersion") {
		t.Error("the stopped panel must not show a version")
	}
	notice := regexp.MustCompile(`(?s)<div class="update-notice" id="updateNotice">(.*?)</div>`).FindStringSubmatch(page)
	if notice == nil || !strings.Contains(notice[1], `<p id="updateText" role="status" aria-live="polite"></p>`) ||
		!strings.Contains(notice[1], `<button class="btn primary" id="updateReload" type="button" hidden>Reload</button>`) {
		t.Error("the update notice is not an always-present polite status region with a hidden Reload button")
	}
	for _, rule := range []string{
		`const pageVersion = (document.querySelector('meta[name="holocron-version"]') || {}).content || "";`,
		`el("appVersion").textContent = pageVersion ? "Holocron " + pageVersion : "";`,
		`const changed = pageVersion && running && running !== pageVersion ? running : "";`,
		`if (changed === announcedVersion) return;`,
		`"Holocron was updated to " + changed + ". Reload this page (Ctrl+F5) to use it."`,
		`el("updateReload").onclick = () => location.reload();`,
		"    checkVersion(last);\n    render(last, null);\n    if (window.Admin) window.Admin.status(last);",
	} {
		if !strings.Contains(app, rule) {
			t.Errorf("app.js lost the rule %s", rule)
		}
	}
	for _, rule := range []string{`<h3>About this Holocron</h3>`, `id="aAboutVersion"`, `id="aAboutPlatform"`, `id="aAboutGo"`, `id="aAboutStarted"`, `window.Admin = { show, status };`} {
		if !strings.Contains(admin, rule) {
			t.Errorf("admin.js lacks %s", rule)
		}
	}
	if strings.Contains(admin, "RootPath") || strings.Contains(admin, "ConfigPath") {
		t.Error("the About block discloses a path")
	}
}
