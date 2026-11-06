package web

import (
	"net/http"
	"sync"
	"time"
)

// Who is online. The login sessions on disk say who signed in and (to within 10 minutes, on purpose:
// lastSeenGranularity) when they were last seen. "Online right now" needs a finer clock, and it must
// not cost a disk write, so the LAN listener's gate notes the page polls that already exist in a map
// in memory: GET /api/status (every open page, every 5 s) and GET /api/live (the Live page, every
// 10 s). No timer, no goroutine, no file. The map is lost at a restart; every open page is back in
// it within 5 s.

const (
	// presenceWindow is how recent a poll must be for a login to count as online.
	presenceWindow = 60 * time.Second

	// loginIDLen is the length of the short opaque id of a login session: the first characters of
	// the token hash, which is not the cookie and cannot be turned into it.
	loginIDLen = 8

	// presenceKeep bounds the map: past this many entries the ones idle for an hour are dropped.
	presenceKeep = 256
	presenceIdle = time.Hour
)

// pollRoutes are the polls that count as activity.
var pollRoutes = map[string]bool{"GET /api/status": true, "GET /api/live": true}

type loginKey struct{}

// loginIDOf is the short id of the login session whose token hash is tokenHash.
func loginIDOf(tokenHash string) string {
	if len(tokenHash) > loginIDLen {
		return tokenHash[:loginIDLen]
	}
	return tokenHash
}

// loginFrom is the login id the gate put into the request context; "" on the loopback listener,
// which has no login.
func loginFrom(r *http.Request) string {
	id, _ := r.Context().Value(loginKey{}).(string)
	return id
}

type presenceEntry struct {
	user    string
	peer    string
	network string // netLAN, netTailnet or netRemote (admin_sessions.go)
	last    time.Time
}

// presenceMap is the last poll of every login session, by login id. The zero value is ready.
type presenceMap struct {
	mu sync.Mutex
	m  map[string]presenceEntry
}

func (p *presenceMap) touch(loginID, user, peer, network string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]presenceEntry{}
	}
	p.m[loginID] = presenceEntry{user: user, peer: peer, network: network, last: now}
	if len(p.m) > presenceKeep {
		for id, e := range p.m {
			if now.Sub(e.last) > presenceIdle {
				delete(p.m, id)
			}
		}
	}
}

// get returns the entry of a login id.
func (p *presenceMap) get(loginID string) (presenceEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[loginID]
	return e, ok
}

// active reports whether the login polled within presenceWindow of now.
func (e presenceEntry) active(now time.Time) bool { return now.Sub(e.last) <= presenceWindow }
