package web

import (
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// How much video the viewers receive. Live video goes from MediaMTX straight to each browser, so
// Holocron never sees the bytes. Each page measures what it receives itself (WebRTC stats) and
// tells Holocron with the check-in it already sends, GET /api/status every 5 s, as three query
// parameters:
//
//	pg   the page's random id (8 to 32 hex digits), so two tabs of one login are two pages
//	bw   its current receive rate in bits per second, an integer
//	bwb  the bytes it received since its previous check-in, an integer
//
// Holocron keeps, in memory only (no file, no timer, no goroutine, no MediaMTX API, no connection):
// the latest rate of every page with the time it arrived, and a running total of the reported
// bytes. The current combined rate is the sum of the rates that arrived within bwWindow; a page
// that closed or went quiet drops out of it by itself. A report is counted as home (the LAN
// listener's non-remote peers and the PC itself) or internet (the remote-access peers) by how its
// check-in arrived: the gate's own remote test (authService.remote), the one that caps a remote
// login to viewing.
//
// Reports are not trusted. One that is malformed or implausible is ignored whole (the status answer
// is unaffected) and counted in Rejected:
//   - pg, bw and bwb must all be present; pg must match bwPageID; bw and bwb must be unsigned
//     decimal integers (so never negative, NaN, infinite or fractional);
//   - bw must not exceed bwMaxRate (1 Gbit/s: more than the Pi's network port can carry);
//   - bwb must not exceed bwMaxRate/8 for the time since that page's previous report, taken as at
//     least bwMinElapsed and at most bwMaxElapsed (a page's first report counts as the maximum).
const (
	bwWindow     = 15 * time.Second  // a page's rate counts toward the current total this long
	bwMaxRate    = 1_000_000_000     // bits per second
	bwMinElapsed = 5 * time.Second   // floor of the interval a report's bytes may span
	bwMaxElapsed = 120 * time.Second // ceiling of it, and the interval of a page's first report
	bwKeep       = 256               // pages remembered; past it, pages silent for bwMaxElapsed are dropped
)

var bwPageID = regexp.MustCompile(`^[0-9a-f]{8,32}$`)

type bwPage struct {
	key    string // login id + "/" + page id
	rate   uint64 // bits per second
	at     time.Time
	remote bool
}

// bandwidthMeter is the in-memory state. The zero value is ready.
type bandwidthMeter struct {
	mu       sync.Mutex
	pages    map[string]bwPage
	home     uint64 // bytes since start
	internet uint64
	rejected int
}

// BandwidthReport is the bandwidth block of GET /api/admin/sessions.
type BandwidthReport struct {
	CurrentBps     uint64    `json:"currentBps"`  // all viewers, bits per second
	HomeBps        uint64    `json:"homeBps"`     // ... of which on the home network (and the PC itself)
	InternetBps    uint64    `json:"internetBps"` // ... of which over remote access
	Viewers        int       `json:"viewers"`     // pages that reported within WindowSeconds
	WindowSeconds  int       `json:"windowSeconds"`
	TotalBytes     uint64    `json:"totalBytes"` // since Holocron's web app started; resets at a restart
	HomeBytes      uint64    `json:"homeBytes"`
	InternetBytes  uint64    `json:"internetBytes"`
	Since          time.Time `json:"since"`
	RejectedReport int       `json:"rejectedReports"` // implausible reports ignored since start
}

// parseBandwidth reads the three parameters of a check-in. present is false when the request
// carries none of them (an ordinary check-in); ok is false when it carries them wrongly.
func parseBandwidth(r *http.Request) (page string, rate, bytes uint64, present, ok bool) {
	q := r.URL.Query()
	page, hasPg := q.Get("pg"), q.Has("pg")
	bw, hasBw := q.Get("bw"), q.Has("bw")
	bwb, hasBwb := q.Get("bwb"), q.Has("bwb")
	if !hasPg && !hasBw && !hasBwb {
		return "", 0, 0, false, true
	}
	if !hasPg || !hasBw || !hasBwb || !bwPageID.MatchString(page) {
		return "", 0, 0, true, false
	}
	rate, err1 := strconv.ParseUint(bw, 10, 64)
	bytes, err2 := strconv.ParseUint(bwb, 10, 64)
	if err1 != nil || err2 != nil || rate > bwMaxRate {
		return "", 0, 0, true, false
	}
	return page, rate, bytes, true, true
}

// report records one check-in's bandwidth parameters, if it carries any. who is the login id ("" on
// the loopback listener), remote is the gate's verdict for the peer.
func (m *bandwidthMeter) report(r *http.Request, who string, remote bool, now time.Time) {
	page, rate, bytes, present, ok := parseBandwidth(r)
	if !present {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ok {
		m.rejected++
		return
	}
	key := who + "/" + page
	prev, known := m.pages[key]
	elapsed := bwMaxElapsed
	if known {
		elapsed = min(max(now.Sub(prev.at), bwMinElapsed), bwMaxElapsed)
	}
	if bytes > uint64(bwMaxRate/8)*uint64(elapsed/time.Second) {
		m.rejected++
		return
	}
	if m.pages == nil {
		m.pages = map[string]bwPage{}
	}
	if !known && len(m.pages) >= bwKeep {
		for k, p := range m.pages {
			if now.Sub(p.at) > bwMaxElapsed {
				delete(m.pages, k)
			}
		}
		if len(m.pages) >= bwKeep {
			m.rejected++
			return
		}
	}
	m.pages[key] = bwPage{key: key, rate: rate, at: now, remote: remote}
	if remote {
		m.internet += bytes
	} else {
		m.home += bytes
	}
}

// snapshot is the aggregate as of now.
func (m *bandwidthMeter) snapshot(now time.Time, since time.Time) BandwidthReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := BandwidthReport{WindowSeconds: int(bwWindow / time.Second), Since: since,
		HomeBytes: m.home, InternetBytes: m.internet, TotalBytes: m.home + m.internet, RejectedReport: m.rejected}
	for _, p := range m.pages {
		if now.Sub(p.at) > bwWindow {
			continue
		}
		out.Viewers++
		out.CurrentBps += p.rate
		if p.remote {
			out.InternetBps += p.rate
		} else {
			out.HomeBps += p.rate
		}
	}
	return out
}

// noteBandwidth is called by GET /api/status for the page's check-in.
func (a *API) noteBandwidth(r *http.Request) {
	h := a.hub()
	who, remote := "", false
	if _, signedIn := userFrom(r); signedIn {
		who = loginFrom(r)
		remote = a.auth != nil && a.auth.remote(r)
	}
	h.bw.report(r, who, remote, h.now())
}
