package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"holocron/internal/archive"
	"holocron/internal/config"
	"holocron/internal/osx"
	"holocron/internal/status"
)

// routeRT sends the requests for host to rt and refuses every other: no real port is reached.
type routeRT struct {
	host string
	rt   http.RoundTripper
}

func (x routeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == x.host {
		return x.rt.RoundTrip(req)
	}
	return nil, errRefusedInTest
}

// startupRig is a supervisor that started moments ago, as on the Operator's PC on 2026-10-30: a
// camera not reported yet, relay paths not ready, the live instance not alive, no listener up, no
// pruner state, no new segment data. uptime is its age at its last heartbeat (2 s before now); a
// negative uptime writes no UptimeSeconds. reachable: the cameras answer RTSP OPTIONS.
func startupRig(t *testing.T, uptime time.Duration, reachable bool) (*rig, time.Time) {
	now := time.Now().Truncate(time.Second)
	r := newRig(t)
	os.WriteFile(r.cameras(), []byte(`[{"Name":"Front","IP":"192.0.2.10","Record":true},{"Name":"Side","IP":"192.0.2.12","Record":true}]`), 0o644)
	srv := relayServer(t, strings.Repeat("ab", 32), `{"pageCount":1,"items":[]}`)
	os.WriteFile(filepath.Join(r.platformAuth(), "relay_api_secret.txt"), []byte(strings.Repeat("ab", 32)), 0o600)
	os.WriteFile(filepath.Join(r.platformAuth(), "live_view_secret.txt"), []byte(strings.Repeat("cd", 32)), 0o600)
	r.setConfig(`"RelayApiBase": "` + srv.URL + `", "LiveViewEnabled": true, "LiveWhepAddress": "127.0.0.1:18897",`)
	r.runningWith(func(d *status.Document) {
		d.UpdatedAt = status.NewTime(now.Add(-2 * time.Second))
		if uptime >= 0 {
			u := (uptime - 2*time.Second).Seconds()
			d.UptimeSeconds = &u
		}
		d.Prune = nil
		d.Live = &status.Live{Enabled: true}
	})
	if _, err := archive.ClaimRoot(r.root, osx.Current(), ".mkv", now); err != nil {
		t.Fatal(err)
	}
	r.segment("Front", now.Add(-10*time.Minute), now.Add(-9*time.Minute), 1000) // written before the start
	var up []string
	if reachable {
		up = []string{"192.0.2.10:554", "192.0.2.12:554"}
	}
	r.doctorTools(func(d *doctorDeps) {
		d.now = func() time.Time { return now }
		d.http = &http.Client{Transport: routeRT{strings.TrimPrefix(srv.URL, "http://"), srv.Client().Transport}}
		d.dial = rtspDial(up...)
		d.platform = freePlatform{d.platform, 100 << 30}
	})
	return r, now
}

const startupGroups = "setup,recording,connections,live,storage"

func doctorJSON(t *testing.T, r *rig, args ...string) (doctorReport, int) {
	t.Helper()
	out, _, code := r.run(append([]string{"--json", "doctor", "--group", startupGroups}, args...)...)
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return rep, code
}

// The problems a start explains, by check id and the start of their steady-state detail.
var startupProblems = []struct{ id, title, detail string }{
	{"recording.camera", "Side", "set to record, but the supervisor reports no such camera"},
	{"recording.growth", "Front", "not recording: no new data for"},
	{"recording.growth", "Side", "not recording: no segment of the last 25 h"},
	{"connections.relay", "Front", "relay path front is not ready"},
	{"connections.relay", "Side", "relay path side is not ready"},
	{"connections.port", "relay RTSP", "nothing answers on tcp"},
	{"live.process", "live instance", "not alive"},
	{"live.login", "live login", "the live instance does not answer"},
	{"live.paths", "Front", "live path(s)"},
	{"storage.prune", "pruner", "the supervisor reports no pruner state yet"},
}

func findCheck(rep doctorReport, id, title string) (check, bool) {
	for _, c := range rep.Checks {
		if c.ID == id && c.Title == title {
			return c, true
		}
	}
	return check{}, false
}

func TestDoctorStartupGrace(t *testing.T) {
	young, _ := startupRig(t, 20*time.Second, true)
	rep, code := doctorJSON(t, young)
	for _, p := range startupProblems {
		c, ok := findCheck(rep, p.id, p.title)
		want := "starting up (20 s old): re-run in a minute - " + p.detail
		if !ok || c.Level != lvWarn || !c.StartingUp || !strings.HasPrefix(c.Detail, want) || !strings.HasPrefix(c.Fix, "run holocron doctor again in a minute; if it repeats: ") {
			t.Errorf("20 s: %s %s: %+v, want warn %q", p.id, p.title, c, want)
		}
	}
	for _, c := range rep.Checks {
		if c.Level == lvFail {
			t.Errorf("20 s: a failure in the grace: %+v", c)
		}
	}
	if rep.Summary.Verdict != "STARTING" || rep.Summary.Failures != 0 || code != 0 {
		t.Errorf("20 s: verdict %+v, exit %d", rep.Summary, code)
	}
	out, _, code := young.run("doctor", "--group", startupGroups, "--quiet")
	if last := lastLine(out); !strings.HasPrefix(last, "STARTING (") || code != 0 || !strings.Contains(out, "warn Side           starting up (20 s old): re-run in a minute - set to record") {
		t.Errorf("--quiet, exit %d:\n%s", code, out)
	}
	t.Logf("doctor --quiet 20 s after the start (exit %d):\n%s", code, out)

	// The same snapshot five minutes after the start, and one without UptimeSeconds: the normal verdict.
	for _, c := range []struct {
		name   string
		uptime time.Duration
	}{{"5 min", 5 * time.Minute}, {"no uptime", -1}} {
		old, _ := startupRig(t, c.uptime, true)
		rep, code := doctorJSON(t, old)
		for _, p := range startupProblems {
			ch, ok := findCheck(rep, p.id, p.title)
			if !ok || ch.StartingUp || !strings.HasPrefix(ch.Detail, p.detail) || ch.Level == lvOK {
				t.Errorf("%s: %s %s: %+v", c.name, p.id, p.title, ch)
			}
			if p.id != "storage.prune" && ch.Level != lvFail {
				t.Errorf("%s: %s %s is %s, want fail", c.name, p.id, p.title, ch.Level)
			}
		}
		if rep.Summary.Verdict != "BROKEN" || code != 1 {
			t.Errorf("%s: verdict %+v, exit %d", c.name, rep.Summary, code)
		}
	}
}

// A failure no start explains stays a failure inside the grace, and the verdict is BROKEN.
func TestDoctorStartupGraceKeepsOtherFailures(t *testing.T) {
	r, _ := startupRig(t, 20*time.Second, false)
	r.useFileSecrets() // never this machine's credential store
	r.noCreds = true
	rep, code := doctorJSON(t, r)
	for _, w := range []struct{ id, title, detail string }{
		{"connections.camera", "Front", "unreachable ("},
		{"connections.camera", "Side", "unreachable ("},
		{"setup.credentials", "credentials", ""},
	} {
		if c, ok := findCheck(rep, w.id, w.title); !ok || c.Level != lvFail || c.StartingUp || !strings.HasPrefix(c.Detail, w.detail) {
			t.Errorf("%s %s: %+v", w.id, w.title, c)
		}
	}
	if c, _ := findCheck(rep, "live.process", "live instance"); !c.StartingUp {
		t.Errorf("the start-up lines are still softened: %+v", c)
	}
	if rep.Summary.Verdict != "BROKEN" || code != 1 {
		t.Errorf("verdict %+v, exit %d", rep.Summary, code)
	}
}

func TestStartupGraceFollowsTheConfiguredWaits(t *testing.T) {
	cfg := config.Defaults(osx.Current())
	if g := startupGrace(cfg); g != 90*time.Second {
		t.Errorf("defaults: %s", g)
	}
	cfg.RelayStartupWaitSeconds = 60
	if g := startupGrace(cfg); g != 170*time.Second {
		t.Errorf("RelayStartupWaitSeconds 60: %s", g)
	}
}
