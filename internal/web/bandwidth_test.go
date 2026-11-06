package web

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The bandwidth figures: pages report what they receive with the status check-in, the live table's
// meter sums it in memory, and only an admin can read the sum (GET /api/admin/sessions).

const pageA = "0a1b2c3d4e5f6071"
const pageB = "ffeeddccbbaa9988"

func checkin(page, rate, bytes string) *http.Request {
	q := ""
	if page != "" || rate != "" || bytes != "" {
		parts := []string{}
		if page != "" {
			parts = append(parts, "pg="+page)
		}
		if rate != "" {
			parts = append(parts, "bw="+rate)
		}
		if bytes != "" {
			parts = append(parts, "bwb="+bytes)
		}
		q = "?" + strings.Join(parts, "&")
	}
	return httptest.NewRequest("GET", "/api/status"+q, nil)
}

func TestBandwidthRejectsImplausibleReports(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	tooMany := fmt.Sprint(uint64(bwMaxRate/8) * uint64(bwMaxElapsed/time.Second))
	cases := []struct {
		name, page, rate, bytes string
	}{
		{"no page id", "", "1000", "100"},
		{"short page id", "abc", "1000", "100"},
		{"page id not hex", "zzzzzzzzzzzz", "1000", "100"},
		{"page id with a slash", "0a1b2c3d/4e5f", "1000", "100"},
		{"no bytes", pageA, "1000", ""},
		{"no rate", pageA, "", "100"},
		{"negative rate", pageA, "-1", "100"},
		{"negative bytes", pageA, "1000", "-5"},
		{"NaN rate", pageA, "NaN", "100"},
		{"infinite rate", pageA, "Inf", "100"},
		{"fractional rate", pageA, "1.5", "100"},
		{"exponent", pageA, "1e3", "100"},
		{"rate over the cap", pageA, fmt.Sprint(uint64(bwMaxRate) + 1), "100"},
		{"rate overflowing 64 bits", pageA, "99999999999999999999999", "100"},
		{"bytes over the first-report cap", pageA, "1000", fmt.Sprint(uint64(bwMaxRate/8)*uint64(bwMaxElapsed/time.Second) + 1)},
	}
	for _, c := range cases {
		var m bandwidthMeter
		m.report(checkin(c.page, c.rate, c.bytes), "login01", netLAN, now)
		got := m.snapshot(now, now)
		if got.RejectedReport != 1 || got.TotalBytes != 0 || got.CurrentBps != 0 || got.Viewers != 0 {
			t.Errorf("%s: %+v, want one rejection and nothing counted", c.name, got)
		}
	}
	// The cap itself is accepted.
	var m bandwidthMeter
	m.report(checkin(pageA, fmt.Sprint(uint64(bwMaxRate)), tooMany), "login01", netLAN, now)
	if got := m.snapshot(now, now); got.RejectedReport != 0 || got.TotalBytes != uint64(bwMaxRate/8)*120 || got.CurrentBps != bwMaxRate {
		t.Errorf("at the cap: %+v, want accepted", got)
	}
	// A check-in without the parameters is ordinary, not a rejection.
	var plain bandwidthMeter
	plain.report(checkin("", "", ""), "login01", netLAN, now)
	if got := plain.snapshot(now, now); got.RejectedReport != 0 || got.Viewers != 0 {
		t.Errorf("a plain check-in changed the meter: %+v", got)
	}
}

func TestBandwidthBoundsBytesByTheIntervalOfThePage(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var m bandwidthMeter
	m.report(checkin(pageA, "1000000", "625000"), "l", netLAN, now)
	// 5 s later: at most 1 Gbit/s for 5 s, that is 625 000 000 bytes.
	m.report(checkin(pageA, "1000000", "625000001"), "l", netLAN, now.Add(5*time.Second))
	if got := m.snapshot(now.Add(5*time.Second), now); got.RejectedReport != 1 || got.TotalBytes != 625000 {
		t.Errorf("too many bytes for the interval: %+v", got)
	}
	m.report(checkin(pageA, "1000000", "625000000"), "l", netLAN, now.Add(10*time.Second))
	if got := m.snapshot(now.Add(10*time.Second), now); got.RejectedReport != 1 || got.TotalBytes != 625000+625000000 {
		t.Errorf("the most the interval allows: %+v", got)
	}
	// Reports closer together than the floor still get the floor.
	m.report(checkin(pageA, "0", "1000"), "l", netLAN, now.Add(10*time.Second+time.Second))
	if got := m.snapshot(now.Add(11*time.Second), now); got.RejectedReport != 1 || got.TotalBytes != 625000+625000000+1000 {
		t.Errorf("a quick second report: %+v", got)
	}
}

func TestBandwidthSumsLatestRatesAndRunningBytes(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var m bandwidthMeter
	m.report(checkin(pageA, "1800000", "1000000"), "login01", netLAN, now)
	m.report(checkin(pageB, "2000000", "1250000"), "login02", netRemote, now)
	// The same page replaces its rate; bytes add up.
	m.report(checkin(pageA, "1000000", "500000"), "login01", netLAN, now.Add(5*time.Second))
	got := m.snapshot(now.Add(5*time.Second), now)
	if got.CurrentBps != 3_000_000 || got.HomeBps != 1_000_000 || got.InternetBps != 2_000_000 || got.Viewers != 2 {
		t.Errorf("rates %+v, want 1 + 2 Mb/s from two pages", got)
	}
	if got.HomeBytes != 1_500_000 || got.InternetBytes != 1_250_000 || got.TotalBytes != 2_750_000 {
		t.Errorf("bytes %+v, want home 1.5 MB, internet 1.25 MB", got)
	}
	// One login with two tabs is two pages.
	m.report(checkin(pageB, "700000", "10"), "login01", netLAN, now.Add(5*time.Second))
	if got := m.snapshot(now.Add(5*time.Second), now); got.Viewers != 3 || got.CurrentBps != 1_000_000+2_000_000+700_000 {
		t.Errorf("two tabs of one login: %+v", got)
	}
}

func TestBandwidthRatesExpireButTotalsStay(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var m bandwidthMeter
	m.report(checkin(pageA, "1800000", "225000"), "l1", netLAN, now)
	m.report(checkin(pageB, "900000", "112500"), "l2", netRemote, now.Add(10*time.Second))
	at := func(d time.Duration) BandwidthReport { return m.snapshot(now.Add(d), now) }
	if got := at(10 * time.Second); got.CurrentBps != 2_700_000 || got.Viewers != 2 {
		t.Errorf("both fresh: %+v", got)
	}
	if got := at(bwWindow); got.CurrentBps != 2_700_000 {
		t.Errorf("at the window's edge a rate still counts: %+v", got)
	}
	if got := at(bwWindow + time.Second); got.CurrentBps != 900_000 || got.HomeBps != 0 || got.InternetBps != 900_000 || got.Viewers != 1 {
		t.Errorf("the first page went quiet: %+v", got)
	}
	got := at(bwWindow + 11*time.Second)
	if got.CurrentBps != 0 || got.Viewers != 0 || got.TotalBytes != 337_500 {
		t.Errorf("everyone quiet: %+v, want 0 now and the bytes kept", got)
	}
}

func TestBandwidthMapIsBounded(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var m bandwidthMeter
	page := func(i int) string { return fmt.Sprintf("%016x", i) }
	for i := 0; i < bwKeep; i++ {
		m.report(checkin(page(i), "1000", "10"), "l", netLAN, now)
	}
	// Full of fresh pages: a newcomer is refused, a known page still updates.
	m.report(checkin(page(bwKeep), "1000", "10"), "l", netLAN, now.Add(time.Second))
	m.report(checkin(page(0), "2000", "10"), "l", netLAN, now.Add(time.Second))
	if len(m.pages) != bwKeep {
		t.Fatalf("%d pages, want at most %d", len(m.pages), bwKeep)
	}
	if got := m.snapshot(now.Add(time.Second), now); got.RejectedReport != 1 || got.CurrentBps != uint64(bwKeep-1)*1000+2000 {
		t.Errorf("full map: %+v", got)
	}
	// Once they have been silent a while the newcomer replaces them.
	m.report(checkin(page(bwKeep), "1000", "10"), "l", netLAN, now.Add(bwMaxElapsed+2*time.Second))
	if len(m.pages) != 1 {
		t.Errorf("%d pages after the sweep, want 1", len(m.pages))
	}
}

func (w *whoRig) statusWith(t *testing.T, cookie, host, peer, query string) {
	t.Helper()
	w.poll(t, "/api/status?"+query, cookie, host, peer)
}

func TestBandwidthIsBucketedByHowTheCheckInArrived(t *testing.T) {
	w := newWhoRig(t)
	admin := w.login(t)
	remoteCookie, _ := w.signInFrom(t, testUser, publicPeer, remoteName)

	w.statusWith(t, admin, lanHost, lanPeer, "pg="+pageA+"&bw=1500000&bwb=187500")
	w.statusWith(t, remoteCookie, remoteName, publicPeer, "pg="+pageB+"&bw=800000&bwb=100000")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	loop, err := newHandler(w.cfg, log, withLive(w.hub), withAuth(w.auth))
	if err != nil {
		t.Fatal(err)
	}
	if rec := (probe{path: "/api/status?pg=" + pageA + "&bw=300000&bwb=37500"}).do(loop); rec.Code != 200 {
		t.Fatalf("loopback status: %d", rec.Code)
	}

	b := w.sessions(t, admin).Bandwidth
	if b.HomeBps != 1_800_000 || b.InternetBps != 800_000 || b.CurrentBps != 2_600_000 || b.Viewers != 3 {
		t.Errorf("rates %+v: the LAN page and the PC itself are home, the remote one is internet", b)
	}
	if b.HomeBytes != 225_000 || b.InternetBytes != 100_000 || b.TotalBytes != 325_000 {
		t.Errorf("bytes %+v", b)
	}
	// The same page id from the PC and from a login are different pages (three viewers above).
	// The figures expire from the report with the clock.
	w.clock.advance(bwWindow + time.Second)
	if b := w.sessions(t, admin).Bandwidth; b.CurrentBps != 0 || b.TotalBytes != 325_000 {
		t.Errorf("after the window: %+v", b)
	}
}

func TestBadBandwidthDoesNotSpoilTheStatusAnswer(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	w.statusWith(t, cookie, lanHost, lanPeer, "pg=nope&bw=-4&bwb=NaN")
	w.statusWith(t, cookie, lanHost, lanPeer, "pg="+pageA+"&bw=99999999999&bwb=1")
	b := w.sessions(t, cookie).Bandwidth
	if b.RejectedReport != 2 || b.TotalBytes != 0 || b.CurrentBps != 0 {
		t.Errorf("%+v, want two ignored reports", b)
	}
}

func TestOnlyAdminsReadTheBandwidth(t *testing.T) {
	w := newWhoRig(t)
	admin := w.login(t)
	bob := w.loginAs(t, viewerUser, viewerPassword)
	remoteCookie, _ := w.signInFrom(t, testUser, publicPeer, remoteName)

	// A viewer reports like anyone (it is just a check-in) ...
	w.statusWith(t, bob, lanHost, "192.168.1.31:50000", "pg="+pageB+"&bw=1000000&bwb=125000")
	if b := w.sessions(t, admin).Bandwidth; b.TotalBytes != 125_000 {
		t.Errorf("a viewer's report was not counted: %+v", b)
	}
	// ... but cannot read the sum, on the LAN or from outside; the status answer carries none of it.
	for _, req := range []tlsReq{
		{path: "/api/admin/sessions", remote: "192.168.1.31:50000", cookie: bob},
		{path: "/api/admin/sessions", host: remoteName, remote: publicPeer, cookie: remoteCookie},
		{path: "/api/admin/sessions", remote: lanPeer},
	} {
		rec := w.do(req)
		if rec.Code == 200 || strings.Contains(rec.Body.String(), "currentBps") || strings.Contains(rec.Body.String(), "totalBytes") {
			t.Errorf("%+v: %d %s", req, rec.Code, rec.Body.String())
		}
	}
	rec := w.do(tlsReq{path: "/api/status", remote: "192.168.1.31:50000", cookie: bob})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "currentBps") || strings.Contains(rec.Body.String(), "totalBytes") {
		t.Errorf("status answer for a viewer: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBandwidthWritesNothingToDisk(t *testing.T) {
	w := newWhoRig(t)
	cookie := w.login(t)
	before := dirListing(t, w.cfg.AuthDir)
	for i := 0; i < 20; i++ {
		w.clock.advance(5 * time.Second)
		w.statusWith(t, cookie, lanHost, lanPeer, "pg="+pageA+"&bw=1000000&bwb=625000")
	}
	if after := dirListing(t, w.cfg.AuthDir); after != before {
		t.Errorf("the auth folder changed:\n%s\n--\n%s", before, after)
	}
}

// dirListing is the names, sizes and modification times of a folder, to see that nothing was written.
func dirListing(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %d %d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// The page side rides the existing check-in: no new poll loop, no new route, and the Dashboard section
// and the Live chip exist.
func TestBandwidthPageRidesTheStatusCheckIn(t *testing.T) {
	live, _ := staticFiles.ReadFile("static/live.js")
	app, _ := staticFiles.ReadFile("static/app.js")
	index, _ := staticFiles.ReadFile("static/index.html")
	for name, want := range map[string]string{
		"live.js reads bytesReceived":   `r.type === "inbound-rtp" && Number.isFinite(r.bytesReceived)`,
		"app.js adds it to the poll":    `fetch("api/status" + (bandwidth ? "?" + bandwidth : "")`,
		"app.js settles the bytes owed": `Live.bandwidthSent(answered)`,
	} {
		src := string(app)
		if strings.HasPrefix(name, "live.js") {
			src = string(live)
		}
		if !strings.Contains(src, want) {
			t.Errorf("%s: %q is gone", name, want)
		}
	}
	if n := strings.Count(string(app), "setInterval(poll"); n != 1 {
		t.Errorf("%d status poll loops, want the one that exists", n)
	}
	if !strings.Contains(string(index), `id="lbw"`) || !strings.Contains(string(index), `id="onlinePanel"`) {
		t.Error("the Live chip or the Dashboard section is missing")
	}
}
