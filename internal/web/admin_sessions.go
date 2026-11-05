package web

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// GET /api/admin/sessions: who is signed in and who is watching which camera, right now. Admins on
// the LAN or the loopback listener only (access.go; a remote peer is never an admin). It is also on
// the view-only allow-list (readOnlyGETPaths): it only reads, so "view-only means nobody can change
// anything" still holds, and the admin strip of the page works there. `holocron user sessions` asks
// this route on the loopback listener. Everything comes from memory (the live table, the presence
// map) plus one read of sessions.json when it changed; nothing is written.

// thisPC is how a session opened on the loopback listener (no login) is shown, never as a username.
// A real user name cannot contain a space, so it cannot be mistaken for one.
const thisPC = "this PC (no login)"

// Presence states of a login session.
const (
	stateWatching  = "watching"   // active in the last minute and holds a live-view session
	stateOnline    = "online"     // active in the last minute, no live-view session
	stateNotOnline = "not-online" // not active in the last minute (or not since the web app started)
)

// Where a peer is: the home network, outside it, or the PC itself.
const (
	netLAN    = "lan"
	netRemote = "remote"
	netLocal  = "local"
)

// SignedInRow is one login session of GET /api/admin/sessions.
type SignedInRow struct {
	LoginID      string    `json:"loginId"`
	User         string    `json:"user"`
	Role         string    `json:"role"`
	Since        time.Time `json:"since"`
	LastActivity time.Time `json:"lastActivity"`
	// Precise: LastActivity is the last poll seen by this run of the web app. Otherwise it is the
	// login session's own LastSeen, which is written at most every 10 minutes.
	Precise bool     `json:"precise"`
	Online  bool     `json:"online"`
	State   string   `json:"state"`
	Cameras []string `json:"cameras"` // what it is watching, empty unless State is watching
	Peer    string   `json:"peer"`    // "" until the first poll of this run
	Network string   `json:"network"` // lan, remote, or "" when unknown
}

// WatchRow is the live-view sessions of one login on one camera.
type WatchRow struct {
	LoginID string    `json:"loginId"`
	User    string    `json:"user"`
	ThisPC  bool      `json:"thisPC"`
	Camera  string    `json:"camera"`
	Streams string    `json:"streams"` // sub, main or "main, sub"
	Count   int       `json:"count"`   // open live-view sessions of this user on this camera
	Since   time.Time `json:"since"`   // the earliest of them
	Peer    string    `json:"peer"`
	Network string    `json:"network"`
}

// SessionsReport is the answer of GET /api/admin/sessions (`holocron user sessions` decodes it).
type SessionsReport struct {
	Ok            bool          `json:"ok"`
	Now           time.Time     `json:"now"`
	ActiveSeconds int           `json:"activeSeconds"`
	LoginRequired bool          `json:"loginRequired"` // false: no HTTPS listener, so there are no logins to list
	SignedIn      []SignedInRow `json:"signedIn"`
	Watching      []WatchRow    `json:"watching"`
	// Bandwidth is what the viewers receive, as their pages report it (bandwidth.go).
	Bandwidth BandwidthReport `json:"bandwidth"`
}

func networkOf(remote, local bool) string {
	switch {
	case local:
		return netLocal
	case remote:
		return netRemote
	}
	return netLAN
}

func (a *API) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if a.auth != nil {
		now = a.auth.now()
	}
	out := SessionsReport{Ok: true, Now: now, ActiveSeconds: int(presenceWindow / time.Second), LoginRequired: a.auth != nil,
		SignedIn: []SignedInRow{}, Watching: []WatchRow{}}

	// Live-view sessions that are live now: the login polled lately, or (the loopback has no login) the
	// page named the session lately. A stale entry (a crashed tab) is left out until its TTL removes it.
	hub := a.hub()
	out.Bandwidth = hub.bw.snapshot(hub.now(), hub.started)
	live := hub.open()
	hubNow := hub.now()
	watching := live[:0]
	for _, s := range live {
		if s.loginID == "" {
			if hubNow.Sub(s.seen) <= presenceWindow {
				watching = append(watching, s)
			}
		} else if a.auth != nil {
			if e, ok := a.auth.presence.get(s.loginID); ok && e.active(now) {
				watching = append(watching, s)
			}
		}
	}
	out.Watching = groupWatching(watching)
	byLogin := map[string][]string{}
	for _, row := range out.Watching {
		byLogin[row.LoginID] = append(byLogin[row.LoginID], row.Camera)
	}

	if a.auth != nil {
		names := map[string]User{}
		if users, err := a.auth.users.List(); err == nil {
			for _, u := range users {
				names[u.ID] = u
			}
		}
		for _, sess := range a.auth.sessions.List() {
			user, ok := names[sess.UserID]
			if !ok {
				continue // the user was removed; the session is dead
			}
			id := loginIDOf(sess.TokenHash)
			row := SignedInRow{LoginID: id, User: user.Username, Role: effectiveRole(user.Role), Since: sess.CreatedAt,
				LastActivity: sess.LastSeen, Cameras: []string{}, State: stateNotOnline}
			if e, ok := a.auth.presence.get(id); ok {
				row.Peer, row.Network = e.peer, networkOf(e.remote, false)
				if !e.last.Before(row.LastActivity) {
					row.LastActivity, row.Precise = e.last, true
				}
				if e.active(now) {
					row.Online, row.State = true, stateOnline
					if cams := byLogin[id]; len(cams) > 0 {
						row.State, row.Cameras = stateWatching, cams
					}
				}
			}
			out.SignedIn = append(out.SignedIn, row)
		}
		sort.SliceStable(out.SignedIn, func(i, j int) bool {
			x, y := out.SignedIn[i], out.SignedIn[j]
			if xu, yu := strings.ToLower(x.User), strings.ToLower(y.User); xu != yu {
				return xu < yu
			}
			if !x.Since.Equal(y.Since) {
				return x.Since.Before(y.Since)
			}
			return x.LoginID < y.LoginID
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// groupWatching folds the live-view sessions into one row per login and camera, in a stable order:
// user (the PC itself last), camera, login id.
func groupWatching(live []liveSession) []WatchRow {
	type key struct{ login, camera string }
	rows := map[key]*WatchRow{}
	streams := map[key]map[string]bool{}
	for _, s := range live {
		k := key{s.loginID, s.camera}
		row := rows[k]
		if row == nil {
			row = &WatchRow{LoginID: s.loginID, User: s.user, Camera: s.camera, Since: s.started, Peer: s.peer,
				Network: networkOf(s.remote, s.loginID == ""), ThisPC: s.loginID == ""}
			if row.ThisPC {
				row.User = thisPC
			}
			rows[k], streams[k] = row, map[string]bool{}
		}
		row.Count++
		streams[k][s.stream] = true
		if s.started.Before(row.Since) {
			row.Since, row.Peer = s.started, s.peer
		}
	}
	out := make([]WatchRow, 0, len(rows))
	for k, row := range rows {
		var names []string
		for _, n := range []string{"main", "sub"} {
			if streams[k][n] {
				names = append(names, n)
			}
		}
		row.Streams = strings.Join(names, ", ")
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.ThisPC != y.ThisPC {
			return !x.ThisPC
		}
		if xu, yu := strings.ToLower(x.User), strings.ToLower(y.User); xu != yu {
			return xu < yu
		}
		if x.Camera != y.Camera {
			return x.Camera < y.Camera
		}
		return x.LoginID < y.LoginID
	})
	return out
}
