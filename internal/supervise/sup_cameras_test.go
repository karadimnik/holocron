package supervise

import (
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"holocron/internal/clock"
	"holocron/internal/config"
)

// The relay and the live child follow the camera list: a change to what a child serves restarts it
// once, with the new inputs, within one reconcile; anything else leaves it running.

// cameraRig starts a supervisor with the relay and live view over cams and steps past the first
// reconciles, so the start's own camera signal has been answered.
func cameraRig(t *testing.T, cams []config.Camera, mod func(*config.Config)) *srig {
	t.Helper()
	r := newSRig(t, cams, func(c *config.Config) {
		c.LiveViewEnabled = true
		if mod != nil {
			mod(c)
		}
	})
	r.start()
	if !r.stepUntil(10*time.Second, func() bool { return len(r.st.started("relay")) == 1 && len(r.st.started("live")) == 1 }) {
		t.Fatalf("relay and live did not start: %d %d\n%s", len(r.st.started("relay")), len(r.st.started("live")), r.logText())
	}
	r.step(11 * time.Second)
	return r
}

// reconcileTwice steps until the supervisor has read the camera list twice more, then lets the live
// child (which polls the relay once a second) catch up.
func (r *srig) reconcileTwice() {
	r.t.Helper()
	n := r.src.cameraReads()
	if !r.stepUntil(30*time.Second, func() bool { return r.src.cameraReads() >= n+2 }) {
		r.t.Fatal("the camera list was not read again")
	}
	r.step(3 * time.Second)
}

func (r *srig) last(kind string) *fakeProc {
	p := r.st.started(kind)
	return p[len(p)-1]
}

func envValue(p *fakeProc, name string) (string, bool) {
	for _, kv := range p.cfg.Env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// logLines returns the log lines holding every one of parts.
func (r *srig) logLines(parts ...string) []string {
	var out []string
	for _, l := range r.logs.Lines() {
		if !slices.ContainsFunc(parts, func(p string) bool { return !strings.Contains(l, p) }) {
			out = append(out, l)
		}
	}
	return out
}

func withCamera(cams []config.Camera, i int, f func(*config.Camera)) []config.Camera {
	out := append([]config.Camera(nil), cams...)
	f(&out[i])
	return out
}

// Index in sixCameras: Front 0, Back 1, Garage 2, Garden 3, Side 4, Street 5.
func TestRelayAndLiveFollowCameraChanges(t *testing.T) {
	six := sixCameras()
	cases := []struct {
		name      string
		base      []config.Camera
		edit      []config.Camera
		relay     int    // restarts of the relay child
		live      int    // restarts of the live child
		reason    string // in the relay.restart line
		relayEnv  map[string]string
		liveEnv   map[string]string
		gone      []string // relay env names no longer set
		goneLive  []string // live paths no longer configured
		liveAdded []string // live paths configured after the restart
	}{
		{
			name: "added", base: six,
			edit:  append(append([]config.Camera(nil), six...), config.Camera{Name: "Front Entrance", IP: "192.0.2.30", Quality: "stream1", Record: true}),
			relay: 1, live: 1, reason: "camera set changed: added Front Entrance",
			relayEnv:  map[string]string{"MTX_PATHS_FRONTENTRANCE_SOURCE": "@192.0.2.30/stream1"},
			liveEnv:   map[string]string{"MTX_PATHS_FRONTENTRANCESUB_SOURCE": "@192.0.2.30/stream2"},
			liveAdded: []string{"frontentrancesub", "frontentrancemain"},
		},
		{
			name: "renamed", base: six,
			edit:  withCamera(six, 0, func(c *config.Camera) { c.Name = "Front Door" }),
			relay: 1, live: 1, reason: "camera set changed: added Front Door; removed Front",
			relayEnv:  map[string]string{"MTX_PATHS_FRONTDOOR_SOURCE": "@192.0.2.10/stream1"},
			gone:      []string{"MTX_PATHS_FRONT_SOURCE"},
			goneLive:  []string{"frontsub", "frontmain"},
			liveAdded: []string{"frontdoorsub", "frontdoormain"},
		},
		{
			name: "address", base: six,
			edit:  withCamera(six, 1, func(c *config.Camera) { c.IP = "192.0.2.99" }),
			relay: 1, live: 1, reason: "camera set changed: address or stream changed for Back",
			relayEnv: map[string]string{"MTX_PATHS_BACK_SOURCE": "@192.0.2.99/stream1"},
			liveEnv:  map[string]string{"MTX_PATHS_BACKSUB_SOURCE": "@192.0.2.99/stream2"},
		},
		{
			// the live child's main path reads the relay path by name and its sub path pulls
			// LiveSubStream, so the recorded stream is no input of the live child
			name: "stream", base: six,
			edit:  withCamera(six, 2, func(c *config.Camera) { c.Quality = "stream3" }),
			relay: 1, live: 0, reason: "camera set changed: address or stream changed for Garage",
			relayEnv: map[string]string{"MTX_PATHS_GARAGE_SOURCE": "@192.0.2.12/stream3"},
		},
		{
			name: "records the live sub stream", base: six,
			edit:  withCamera(six, 2, func(c *config.Camera) { c.Quality = "stream2" }),
			relay: 1, live: 1, reason: "camera set changed: address or stream changed for Garage",
			relayEnv: map[string]string{"MTX_PATHS_GARAGE_SOURCE": "@192.0.2.12/stream2"},
			goneLive: []string{"garagesub"},
		},
		{
			name: "no longer records the live sub stream", base: withCamera(six, 2, func(c *config.Camera) { c.Quality = "stream2" }),
			edit:  six,
			relay: 1, live: 1, reason: "camera set changed: address or stream changed for Garage",
			relayEnv:  map[string]string{"MTX_PATHS_GARAGE_SOURCE": "@192.0.2.12/stream1"},
			liveEnv:   map[string]string{"MTX_PATHS_GARAGESUB_SOURCE": "@192.0.2.12/stream2"},
			liveAdded: []string{"garagesub"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := cameraRig(t, tc.base, nil)
			oldRelay, oldLive := r.last("relay"), r.last("live")
			r.src.setCameras(tc.edit)
			r.reconcileTwice()
			r.step(20 * time.Second) // and no further restart follows
			if got := len(r.st.started("relay")) - 1; got != tc.relay {
				t.Errorf("relay restarts %d, want %d\n%s", got, tc.relay, r.logText())
			}
			if got := len(r.st.started("live")) - 1; got != tc.live {
				t.Errorf("live restarts %d, want %d\n%s", got, tc.live, r.logText())
			}
			if tc.relay > 0 && !oldRelay.exited() || tc.live > 0 && !oldLive.exited() {
				t.Errorf("an old child still runs: relay %v live %v", oldRelay.exited(), oldLive.exited())
			}
			if n := len(r.logLines(`relay.restart`, "restreamer pid", tc.reason)); n != tc.relay {
				t.Errorf("%d relay.restart lines with %q, want %d\n%s", n, tc.reason, tc.relay, r.logText())
			}
			if n := len(r.logLines(`live.restart`, "live view pid", "camera set changed")); n != tc.live {
				t.Errorf("%d live.restart lines, want %d\n%s", n, tc.live, r.logText())
			}
			relayP, liveP := r.last("relay"), r.last("live")
			for name, want := range tc.relayEnv {
				if v, ok := envValue(relayP, name); !ok || !strings.HasSuffix(v, want) {
					t.Errorf("relay env %s = %q, want ...%s", name, v, want)
				}
				path := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(name, "MTX_PATHS_"), "_SOURCE"))
				if !strings.Contains(readText(t, r.cfg.RestreamerConfigPath), "\r\n  "+path+":\r\n") {
					t.Errorf("relay config has no path %s", path)
				}
			}
			for _, name := range tc.gone {
				if _, ok := envValue(relayP, name); ok {
					t.Errorf("relay env still sets %s", name)
				}
			}
			for name, want := range tc.liveEnv {
				if v, ok := envValue(liveP, name); !ok || !strings.HasSuffix(v, want) {
					t.Errorf("live env %s = %q, want ...%s", name, v, want)
				}
			}
			liveText := readText(t, r.cfg.LiveConfigPath)
			for _, p := range tc.liveAdded {
				if !strings.Contains(liveText, "\r\n  "+p+":\r\n") {
					t.Errorf("live config has no path %s", p)
				}
			}
			for _, p := range tc.goneLive {
				if strings.Contains(liveText, "\r\n  "+p+":\r\n") {
					t.Errorf("live config still has path %s", p)
				}
			}
			// the supervisor did not restart the recorders of cameras the edit left alone
			for _, name := range []string{"Side", "Street"} {
				if n := len(r.st.of("ffmpeg", name)); n != 1 {
					t.Errorf("%s recorder started %d times, want 1", name, n)
				}
			}
			if strings.Contains(r.logText(), fakeCamPass) {
				t.Error("the camera password is in the log")
			}
			r.stop(StopRequested)
		})
	}
}

// What the children serve does not change: no restart.
func TestRelayAndLiveIgnoreChangesTheyDoNotServe(t *testing.T) {
	six := sixCameras()
	reversed := slices.Clone(six)
	slices.Reverse(reversed)
	cases := map[string][]config.Camera{
		"unchanged": six,
		"reordered": reversed,
		// the relay serves every camera of the list, recorded or not
		"record off": withCamera(six, 4, func(c *config.Camera) { c.Record = false }),
		// the path name keeps letters and digits only: only the config comment would change
		"renamed to the same path": withCamera(six, 3, func(c *config.Camera) { c.Name = "Gar-den" }),
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			r := cameraRig(t, six, nil)
			r.src.setCameras(edit)
			r.reconcileTwice()
			r.step(20 * time.Second)
			if nr, nl := len(r.st.started("relay")), len(r.st.started("live")); nr != 1 || nl != 1 {
				t.Errorf("relay started %d times, live %d; want 1 and 1\n%s", nr, nl, r.logText())
			}
			if strings.Contains(r.logText(), "camera set changed") {
				t.Errorf("a restart was logged\n%s", r.logText())
			}
			r.stop(StopRequested)
		})
	}
}

// Two edits within one reconcile interval give one restart, which serves both.
func TestTwoQuickCameraEditsGiveOneRestart(t *testing.T) {
	six := sixCameras()
	r := cameraRig(t, six, nil)
	n := r.src.cameraReads()
	if !r.stepUntil(10*time.Second, func() bool { return r.src.cameraReads() > n }) {
		t.Fatal("no reconcile")
	}
	one := append(slices.Clone(six), config.Camera{Name: "Front Entrance", IP: "192.0.2.30", Quality: "stream1", Record: true})
	r.src.setCameras(one)
	r.step(time.Second)
	r.src.setCameras(append(slices.Clone(one), config.Camera{Name: "Porch", IP: "192.0.2.31", Quality: "stream1", Record: true}))
	r.reconcileTwice()
	r.step(20 * time.Second)
	if nr, nl := len(r.st.started("relay")), len(r.st.started("live")); nr != 2 || nl != 2 {
		t.Fatalf("relay started %d times, live %d; want 2 and 2\n%s", nr, nl, r.logText())
	}
	for _, env := range []string{"MTX_PATHS_FRONTENTRANCE_SOURCE", "MTX_PATHS_PORCH_SOURCE"} {
		if _, ok := envValue(r.last("relay"), env); !ok {
			t.Errorf("relay env has no %s", env)
		}
	}
	if len(r.logLines("relay.restart", "camera set changed: added Front Entrance, Porch")) != 1 {
		t.Errorf("no single restart line naming both cameras\n%s", r.logText())
	}
	r.stop(StopRequested)
}

// A relay that fails to start after a camera change is retried with the start backoff (3, 6, 12 s),
// and a further edit meanwhile does not hurry it; once it starts it serves the latest list and the
// live child follows it.
func TestCameraChangeWithFailingRelayKeepsTheBackoff(t *testing.T) {
	six := sixCameras()
	var mu sync.Mutex
	var attempts []clock.Mono
	fail := true
	r := cameraRig(t, six, nil)
	r.st.script = func(kind, name string, n int) behaviour {
		if kind != "relay" {
			return behaviour{}
		}
		mu.Lock()
		defer mu.Unlock()
		attempts = append(attempts, r.clk.Mono())
		if fail {
			return behaviour{startErr: errors.New("mediamtx would not start")}
		}
		return behaviour{}
	}
	one := append(slices.Clone(six), config.Camera{Name: "Front Entrance", IP: "192.0.2.30", Quality: "stream1", Record: true})
	r.src.setCameras(one)
	r.reconcileTwice()
	r.src.setCameras(append(slices.Clone(one), config.Camera{Name: "Porch", IP: "192.0.2.31", Quality: "stream1", Record: true}))
	r.step(30 * time.Second)
	mu.Lock()
	got := slices.Clone(attempts)
	fail = false
	mu.Unlock()
	if len(got) < 3 || len(got) > 4 {
		t.Fatalf("%d start attempts in about 35 s, want 3 or 4 (backoff 3, 6, 12 s)\n%s", len(got), r.logText())
	}
	for i := 1; i < len(got); i++ {
		want := min(3*time.Second<<(i-1), 60*time.Second)
		if gap := got[i].Sub(got[i-1]); gap < want {
			t.Errorf("attempt %d came %v after the previous one, want at least %v", i, gap, want)
		}
	}
	if !r.stepUntil(60*time.Second, func() bool { return len(r.st.started("relay")) == 2 }) {
		t.Fatalf("the relay did not start again\n%s", r.logText())
	}
	r.step(5 * time.Second)
	for _, env := range []string{"MTX_PATHS_FRONTENTRANCE_SOURCE", "MTX_PATHS_PORCH_SOURCE"} {
		if _, ok := envValue(r.last("relay"), env); !ok {
			t.Errorf("relay env has no %s", env)
		}
		if _, ok := envValue(r.last("live"), strings.Replace(env, "_SOURCE", "SUB_SOURCE", 1)); !ok {
			t.Errorf("live env has no sub path for %s", env)
		}
	}
	if nr, nl := len(r.st.started("relay")), len(r.st.started("live")); nr != 2 || nl != 2 {
		t.Errorf("relay started %d times, live %d; want 2 and 2\n%s", nr, nl, r.logText())
	}
	r.stop(StopRequested)
}

// A camera list the relay could not serve (two cameras on one path) keeps the running relay, says so
// once, and restarts nothing.
func TestUnservableCameraListKeepsTheRelay(t *testing.T) {
	six := sixCameras()
	r := cameraRig(t, six, nil)
	r.src.setCameras(append(slices.Clone(six), config.Camera{Name: "FRONT!", IP: "192.0.2.30", Quality: "stream1", Record: true}))
	r.reconcileTwice()
	r.step(20 * time.Second)
	if nr, nl := len(r.st.started("relay")), len(r.st.started("live")); nr != 1 || nl != 1 {
		t.Errorf("relay started %d times, live %d; want 1 and 1\n%s", nr, nl, r.logText())
	}
	if n := len(r.logLines("the camera list cannot be served", "the running restreamer keeps")); n != 1 {
		t.Errorf("%d warnings, want 1\n%s", n, r.logText())
	}
	r.stop(StopRequested)
}
