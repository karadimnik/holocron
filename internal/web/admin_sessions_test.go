package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The "who is online" feature: the live table remembers who opened each session, the gate notes the
// page polls in memory, and GET /api/admin/sessions (admins only) joins them. These tests run the
// real LAN handler over a fake clock shared by the auth service and the live table.

type whoRig struct {
	*tlsRig
	hub  *liveHub
	up   *fakeUpstream
	cfg  Config
	root string
}

func newWhoRig(t *testing.T) *whoRig {
	t.Helper()
	up := newFakeUpstream(t)
	lr := okRig(t, up)
	hub := newLiveHub()
	rig := newTLSRig(t, func(c *Config) {
		c.RemoteHost = remoteName
		c.TLSReadOnly = boolPtr(false)
		c.ScriptDir = lr.dir
		c.StatusFile = filepath.Join(lr.dir, "status.json")
	}, withLive(hub))
	hub.now = rig.clock.now
	rig.addViewer(t)
	return &whoRig{tlsRig: rig, hub: hub, up: up, cfg: rig.cfg, root: lr.dir}
}

// openLive opens a live session as the cookie's login and returns its id.
func (w *whoRig) openLive(t *testing.T, cookie, host, peer, camera, stream string) string {
	t.Helper()
	rec := w.do(tlsReq{method: "POST", path: "/api/live/" + camera + "/whep?stream=" + stream, host: host, remote: peer, cookie: cookie,
		headers: map[string]string{"Content-Type": "application/sdp", "Origin": "https://" + host}, body: testOffer})
	if rec.Code != 201 {
		t.Fatalf("open live %s/%s: %d %s", camera, stream, rec.Code, rec.Body.String())
	}
	return strings.TrimPrefix(rec.Header().Get("Location"), "/api/live/session/")
}

func (w *whoRig) poll(t *testing.T, path, cookie, host, peer string) {
	t.Helper()
	if rec := w.do(tlsReq{path: path, host: host, remote: peer, cookie: cookie}); rec.Code != 200 {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
}

func (w *whoRig) sessions(t *testing.T, cookie string) SessionsReport {
	t.Helper()
	rec := w.do(tlsReq{path: "/api/admin/sessions", remote: lanPeer, cookie: cookie})
	if rec.Code != 200 {
		t.Fatalf("GET /api/admin/sessions: %d %s", rec.Code, rec.Body.String())
	}
	var out SessionsReport
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v %q", err, rec.Body.String())
	}
	return out
}

func rowOf(t *testing.T, a SessionsReport, user string) SignedInRow {
	t.Helper()
	for _, r := range a.SignedIn {
		if r.User == user {
			return r
		}
	}
	t.Fatalf("no signed-in row for %q in %+v", user, a.SignedIn)
	return SignedInRow{}
}

func TestLiveTableRecordsWhoOpenedASession(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	opened := w.clock.now()
	id := w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "sub")

	open := w.hub.open()
	if len(open) != 1 {
		t.Fatalf("%d sessions, want 1", len(open))
	}
	got := open[0]
	want := liveWho{user: testUser, loginID: loginIDOf(hashToken(cookie)), stream: "sub", peer: "192.168.1.20", remote: false}
	if got.liveWho != want || got.camera != "Cam One" || !got.started.Equal(opened) || !got.seen.Equal(opened) {
		t.Errorf("recorded %+v camera %q started %v, want %+v at %v", got.liveWho, got.camera, got.started, want, opened)
	}
	if len(want.loginID) != loginIDLen || strings.Contains(cookie, want.loginID) || strings.Contains(w.hub.sessions[id].loginID, cookie) {
		t.Errorf("the login id %q is not a short opaque id", want.loginID)
	}

	// A remote peer is marked remote and its public address kept (memory only).
	rc, _ := w.signInFrom(t, testUser, publicPeer, remoteName)
	w.openLive(t, rc, remoteName, publicPeer, "Cam%20One", "main")
	var remote *liveSession
	for _, s := range w.hub.open() {
		if s.stream == "main" {
			s := s
			remote = &s
		}
	}
	if remote == nil || !remote.remote || remote.peer != "203.0.113.7" {
		t.Errorf("remote session recorded as %+v", remote)
	}
}

func TestWhoIsOnlineStates(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)

	if r := rowOf(t, w.sessions(t, cookie), testUser); r.State != stateNotOnline || r.Online || r.Peer != "" || r.Precise {
		t.Errorf("signed in, never polled: %+v, want not-online", r)
	}

	w.poll(t, "/api/status", cookie, lanHost, lanPeer)
	r := rowOf(t, w.sessions(t, cookie), testUser)
	if r.State != stateOnline || !r.Online || r.Peer != "192.168.1.20" || r.Network != netLAN || !r.Precise || len(r.Cameras) != 0 || r.Role != roleAdmin {
		t.Errorf("after a status poll: %+v, want online on the LAN", r)
	}
	if !r.LastActivity.Equal(w.clock.now()) {
		t.Errorf("last activity %v, want the poll at %v", r.LastActivity, w.clock.now())
	}

	w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "sub")
	w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "main")
	a := w.sessions(t, cookie)
	if r := rowOf(t, a, testUser); r.State != stateWatching || len(r.Cameras) != 1 || r.Cameras[0] != "Cam One" {
		t.Errorf("with live sessions open: %+v, want watching Cam One", r)
	}
	if len(a.Watching) != 1 || a.Watching[0].Count != 2 || a.Watching[0].Streams != "main, sub" || a.Watching[0].User != testUser || a.Watching[0].ThisPC {
		t.Errorf("watching rows %+v, want one grouped row of two sessions", a.Watching)
	}

	// The page stops polling (a crashed tab, a dead phone): within a minute the stale entry is hidden,
	// though it holds its slot until the hour is up.
	w.clock.advance(presenceWindow + time.Second)
	a = w.sessions(t, cookie)
	if r := rowOf(t, a, testUser); r.State != stateNotOnline || r.Online || len(r.Cameras) != 0 || !r.Precise {
		t.Errorf("a minute without a poll: %+v, want not-online", r)
	}
	if len(a.Watching) != 0 || len(w.hub.open()) != 2 {
		t.Errorf("stale entries listed (%d) or dropped early (%d open)", len(a.Watching), len(w.hub.open()))
	}

	// A poll of the Live page brings the user back as watching.
	w.poll(t, "/api/live", cookie, lanHost, lanPeer)
	if r := rowOf(t, w.sessions(t, cookie), testUser); r.State != stateWatching {
		t.Errorf("after a live poll: %+v, want watching", r)
	}

	// Online without a live session is "online", not "watching": the other user only reads the dashboard.
	bob := w.loginAs(t, viewerUser, viewerPassword)
	w.poll(t, "/api/status", bob, lanHost, "192.168.1.31:50000")
	if r := rowOf(t, w.sessions(t, cookie), viewerUser); r.State != stateOnline || r.Role != roleViewer || r.Peer != "192.168.1.31" {
		t.Errorf("a viewer reading the dashboard: %+v", r)
	}
}

func TestPresenceComesFromThePollsOnly(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	w.poll(t, "/api/auth/me", cookie, lanHost, lanPeer)
	w.poll(t, "/api/footage", cookie, lanHost, lanPeer)
	w.poll(t, "/api/admin/sessions", cookie, lanHost, lanPeer)
	if _, ok := w.auth.presence.get(loginIDOf(hashToken(cookie))); ok {
		t.Error("a route that is not a page poll counted as activity")
	}
	w.poll(t, "/api/status", cookie, lanHost, lanPeer)
	if _, ok := w.auth.presence.get(loginIDOf(hashToken(cookie))); !ok {
		t.Error("GET /api/status did not count as activity")
	}
}

func TestPresenceWritesNothingToDisk(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	file := filepath.Join(w.cfg.AuthDir, sessionsFileName)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	for i := 0; i < 50; i++ {
		w.clock.advance(5 * time.Second) // 250 s: inside the 10-minute LastSeen granularity
		w.poll(t, "/api/status", cookie, lanHost, lanPeer)
		w.poll(t, "/api/live?open=00000000000000000000000000000000", cookie, lanHost, lanPeer)
		w.sessions(t, cookie)
	}
	after, _ := os.ReadFile(file)
	again, _ := os.Stat(file)
	if string(before) != string(after) || !info.ModTime().Equal(again.ModTime()) {
		t.Error("polling and listing rewrote sessions.json")
	}
	// The ten-minute granularity itself is unchanged.
	w.clock.advance(lastSeenGranularity)
	w.poll(t, "/api/status", cookie, lanHost, lanPeer)
	if changed, _ := os.ReadFile(file); string(changed) == string(before) {
		t.Error("LastSeen was not written after ten minutes: the granularity changed")
	}
}

func TestSessionsAnswerHoldsNoSecret(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	w.poll(t, "/api/status", cookie, lanHost, lanPeer)
	w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "sub")
	rec := w.do(tlsReq{path: "/api/admin/sessions", cookie: cookie})
	body := rec.Body.String()
	for _, secret := range []string{cookie, hashToken(cookie), testPassword, "hash", testSecret} {
		if strings.Contains(body, secret) {
			t.Errorf("the answer contains %q", secret[:min(len(secret), 8)])
		}
	}
	if !strings.Contains(body, loginIDOf(hashToken(cookie))) {
		t.Error("the answer has no login id")
	}
}

func TestSessionsRouteIsAdminOnly(t *testing.T) {
	w := newWhoRig(t)
	admin := w.login(t)
	bob := w.loginAs(t, viewerUser, viewerPassword)

	if rec := w.do(tlsReq{path: "/api/admin/sessions"}); rec.Code != 401 {
		t.Errorf("no session: %d, want 401", rec.Code)
	}
	if rec := w.do(tlsReq{path: "/api/admin/sessions", cookie: bob}); !isAdminRefusal(rec) {
		t.Errorf("a viewer: %d %s, want the admins-only 403", rec.Code, rec.Body.String())
	}
	// The admin account signing in from outside the home is a viewer: it never reaches the route.
	remote, _ := w.signInFrom(t, testUser, publicPeer, remoteName)
	if rec := w.do(tlsReq{path: "/api/admin/sessions", host: remoteName, remote: publicPeer, cookie: remote}); !isAdminRefusal(rec) {
		t.Errorf("a remote admin account: %d %s, want the admins-only 403", rec.Code, rec.Body.String())
	}
	if rec := w.do(tlsReq{path: "/api/admin/sessions", cookie: admin}); rec.Code != 200 {
		t.Errorf("a LAN admin: %d", rec.Code)
	}
	if !strings.Contains(w.logs.String(), "admins only") || !strings.Contains(w.logs.String(), "remote access is viewer-only") {
		t.Error("the refusals were not audited")
	}
}

// On the default view-only listener the route stays open to an admin (it only reads) and shut to a viewer.
func TestSessionsRouteOnAViewOnlyListener(t *testing.T) {
	rig := newTLSRig(t, nil) // tlsReadOnly defaults to true
	rig.addViewer(t)
	admin, bob := rig.login(t), rig.loginAs(t, viewerUser, viewerPassword)
	if rec := rig.do(tlsReq{path: "/api/admin/sessions", cookie: admin}); rec.Code != 200 {
		t.Errorf("an admin on a view-only listener: %d %s", rec.Code, rec.Body.String())
	}
	if rec := rig.do(tlsReq{path: "/api/admin/sessions", cookie: bob}); !isAdminRefusal(rec) {
		t.Errorf("a viewer on a view-only listener: %d %s", rec.Code, rec.Body.String())
	}
	if rec := rig.do(tlsReq{path: "/api/admin/users", cookie: admin}); rec.Code != 403 || !isReadOnlyRefusal(rec.Body.String()) {
		t.Errorf("the rest of the Admin tab stays refused: %d", rec.Code)
	}
}

func TestLoopbackSessionShowsAsThisPC(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	w.poll(t, "/api/status", cookie, lanHost, lanPeer)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop, err := newHandler(w.cfg, log, withLive(w.hub), withAuth(w.auth))
	if err != nil {
		t.Fatal(err)
	}
	post := probe{method: "POST", path: "/api/live/Cam%20Two/whep?stream=main", headers: map[string]string{"Content-Type": "application/sdp"}, body: testOffer}.do(loop)
	if post.Code != 201 {
		t.Fatalf("loopback open: %d %s", post.Code, post.Body.String())
	}
	id := strings.TrimPrefix(post.Header().Get("Location"), "/api/live/session/")

	a := w.sessions(t, cookie)
	if len(a.Watching) != 1 {
		t.Fatalf("watching %+v, want the loopback row", a.Watching)
	}
	row := a.Watching[0]
	if !row.ThisPC || row.User != thisPC || row.LoginID != "" || row.Network != netLocal || row.Peer != "192.0.2.1" || row.Camera != "Cam Two" {
		t.Errorf("loopback row %+v, want this PC (no login), never a username", row)
	}
	for _, s := range a.SignedIn {
		if s.State == stateWatching {
			t.Errorf("a login shows as watching because of the loopback session: %+v", s)
		}
	}

	// No login means no presence: the loopback page keeps its entry alive by naming it in its list poll.
	w.clock.advance(presenceWindow + time.Second)
	if got := w.sessions(t, cookie); len(got.Watching) != 0 {
		t.Errorf("a loopback entry nobody polls for is still listed: %+v", got.Watching)
	}
	if rec := (probe{path: "/api/live?open=" + id}).do(loop); rec.Code != 200 {
		t.Fatalf("loopback list: %d", rec.Code)
	}
	if got := w.sessions(t, cookie); len(got.Watching) != 1 || !got.Watching[0].ThisPC {
		t.Errorf("after the page named it: %+v", got.Watching)
	}

	// And the loopback listener answers the same route itself (the CLI asks it there), as an admin.
	rec := probe{path: "/api/admin/sessions"}.do(loop)
	var viaLoop SessionsReport
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &viaLoop) != nil || len(viaLoop.Watching) != 1 || !viaLoop.LoginRequired {
		t.Errorf("loopback route: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoopbackWithoutLoginsListsNoLogins(t *testing.T) {
	h, _ := loopbackHandler(t, nil)
	rec := probe{path: "/api/admin/sessions"}.do(h)
	var a SessionsReport
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &a) != nil || a.LoginRequired || len(a.SignedIn) != 0 || len(a.Watching) != 0 {
		t.Errorf("no HTTPS listener: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPollRefreshesOnlyTheNamedSessionsOfTheCaller(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	bob := w.loginAs(t, viewerUser, viewerPassword)
	kept := w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "sub")
	lost := w.openLive(t, cookie, lanHost, lanPeer, "Cam%20One", "sub")
	bobs := w.openLive(t, bob, lanHost, "192.168.1.31:50000", "Cam%20Two", "main")

	// Fifty minutes in, the page names the session it still holds; bob names two that are not his.
	w.clock.advance(50 * time.Minute)
	w.poll(t, "/api/live?open="+kept+","+bobs, cookie, lanHost, lanPeer)
	w.poll(t, "/api/live?open="+kept+","+lost, bob, lanHost, "192.168.1.31:50000")

	// Past the original hour: only the named one of the caller's own is still there.
	w.clock.advance(50 * time.Minute)
	have := map[string]bool{}
	w.hub.keep("", nil) // purges what has expired
	for id := range w.hub.sessions {
		have[id] = true
	}
	if !have[kept] || have[lost] || have[bobs] {
		t.Errorf("after 100 minutes: kept=%v lost=%v bobs=%v, want only the one its owner named", have[kept], have[lost], have[bobs])
	}

	// A viewer who stays for hours keeps showing as watching while the page polls (every 10 s in
	// reality; every 50 minutes is already enough to hold the entry).
	w.poll(t, "/api/live?open="+kept, cookie, lanHost, lanPeer)
	for i := 0; i < 6; i++ {
		w.clock.advance(50 * time.Minute)
		w.poll(t, "/api/live?open="+kept, cookie, lanHost, lanPeer)
	}
	w.poll(t, "/api/status", cookie, lanHost, lanPeer)
	if r := rowOf(t, w.sessions(t, cookie), testUser); r.State != stateWatching {
		t.Errorf("after 6 more hours: %+v, want still watching", r)
	}

	// Garbage in the list is ignored, and the list is bounded.
	ids := strings.Repeat("zz,", 500) + kept
	w.poll(t, "/api/live?open="+ids, cookie, lanHost, lanPeer)
}

func TestLiveTTLIsUnchanged(t *testing.T) {
	if liveSessionTTL != time.Hour {
		t.Errorf("liveSessionTTL is %v: a lost entry must still expire after one hour", liveSessionTTL)
	}
}

func TestWatchingRowsAreGroupedAndOrdered(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mk := func(user, login, camera, stream string, min int) liveSession {
		return liveSession{camera: camera, started: t0.Add(time.Duration(min) * time.Minute),
			liveWho: liveWho{user: user, loginID: login, stream: stream, peer: "192.168.1.5"}}
	}
	in := []liveSession{
		mk("", "", "Back", "sub", 0),
		mk("bob", "bbbbbbbb", "Front Door", "sub", 1),
		mk("anna", "aaaaaaaa", "Front Door", "main", 9),
		mk("anna", "aaaaaaaa", "Front Door", "sub", 2),
		mk("anna", "aaaaaaaa", "Back", "sub", 3),
		mk("Anna", "a2a2a2a2", "Back", "sub", 4),
	}
	order := func(rows []WatchRow) string {
		var parts []string
		for _, r := range rows {
			parts = append(parts, r.User+"/"+r.Camera+"/"+r.Streams)
		}
		return strings.Join(parts, "|")
	}
	first := order(groupWatching(in))
	const expect = "Anna/Back/sub|anna/Back/sub|anna/Front Door/main, sub|bob/Front Door/sub|" + thisPC + "/Back/sub"
	if first != expect {
		t.Errorf("order\n got %s\nwant %s", first, expect)
	}
	rows := groupWatching(in)
	for _, r := range rows {
		if r.User == "anna" && r.Camera == "Front Door" && (r.Count != 2 || !r.Since.Equal(t0.Add(2*time.Minute))) {
			t.Errorf("grouped row %+v, want count 2 since the earliest", r)
		}
	}
	// The same rows in any input order come out the same.
	for i := range in {
		shuffled := append(append([]liveSession{}, in[i:]...), in[:i]...)
		if got := order(groupWatching(shuffled)); got != first {
			t.Errorf("rotation %d: %s, want %s", i, got, first)
		}
	}
}

func TestSignedInRowsAreOrdered(t *testing.T) {
	w := newWhoRig(t)
	first := w.login(t)
	w.loginAs(t, viewerUser, viewerPassword)
	w.signInFrom(t, testUser, lanPeer, lanHost) // a second device of the same user
	a := w.sessions(t, first)
	var names []string
	for _, r := range a.SignedIn {
		names = append(names, r.User)
	}
	if strings.Join(names, ",") != testUser+","+testUser+","+viewerUser {
		t.Errorf("order %v, want by user then sign-in time", names)
	}
	b := w.sessions(t, first)
	if x, y := mustJSON(a.SignedIn), mustJSON(b.SignedIn); x != y {
		t.Errorf("two reads differ:\n%s\n%s", x, y)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestPresenceMapStaysBounded(t *testing.T) {
	var p presenceMap
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < presenceKeep+50; i++ {
		p.touch(string(rune('a'+i%26))+strings.Repeat("x", i), "u", "10.0.0.1", false, now.Add(-2*presenceIdle))
	}
	p.touch("fresh", "u", "10.0.0.1", false, now)
	if len(p.m) > presenceKeep {
		t.Errorf("%d entries, want at most %d once the idle ones are swept", len(p.m), presenceKeep)
	}
	if _, ok := p.get("fresh"); !ok {
		t.Error("the fresh entry was swept")
	}
}

// The Live page must send what it holds with the poll it already makes (the server only extends named
// entries), also while another camui tab is showing and the streams are kept for the grace period.
func TestLivePageNamesItsOpenSessions(t *testing.T) {
	js, err := staticFiles.ReadFile("static/live.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, want := range []string{`getJSON("api/live" + openQuery())`, `"?open="`, "openSessionIds()", "conn.session.lastIndexOf"} {
		if !strings.Contains(src, want) {
			t.Errorf("live.js lost %q: a viewer would drop out of the watching list after an hour", want)
		}
	}
	if strings.Count(src, `getJSON("api/live"`) != 2 {
		t.Error("live.js reads the live list in an unexpected number of places; each must name the open sessions")
	}
}
