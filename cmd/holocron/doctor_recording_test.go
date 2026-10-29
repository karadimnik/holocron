package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"holocron/internal/archive"
	"holocron/internal/config"
	"holocron/internal/shadow"
	"holocron/internal/status"
)

// runningWith writes a running status.json (this test process as the supervisor) changed by mod.
func (r *rig) runningWith(mod func(d *status.Document)) {
	r.t.Helper()
	r.running()
	p := filepath.Join(r.root, "_state", "status.json")
	d, err := status.Read(p)
	if err != nil {
		r.t.Fatal(err)
	}
	mod(d)
	if err := status.NewWriter(p, status.Options{}).Write(d); err != nil {
		r.t.Fatal(err)
	}
}

// segment writes a segment of camera started at start, last written at end, of size bytes.
func (r *rig) segment(camera string, start, end time.Time, size int) string {
	r.t.Helper()
	dir := filepath.Join(r.root, camera)
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, archive.FormatSegmentName(camera, start, ".mkv"))
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		r.t.Fatal(err)
	}
	os.Chtimes(p, end, end)
	return p
}

// lineOf is the doctor line whose title is title in group, with its fix line.
func lineOf(out, group, title string) (line, fix string) {
	in := false
	ls := strings.Split(out, "\n")
	for i, l := range ls {
		if strings.HasPrefix(l, "== ") {
			in = l == "== "+group+" =="
			continue
		}
		if in && len(l) > 5 && strings.HasPrefix(l[5:], title+" ") {
			if i+1 < len(ls) && strings.HasPrefix(ls[i+1], "     fix: ") {
				fix = strings.TrimPrefix(ls[i+1], "     fix: ")
			}
			return l, fix
		}
	}
	return "", ""
}

func TestDoctorSaysNotRunningOncePerRuntimeGroup(t *testing.T) {
	r := newRig(t)
	r.doctorTools(nil)
	out, _, code := r.run("doctor")
	for g := range runtimeGroups {
		if !groupHasChecks(g) {
			continue
		}
		l, fix := lineOf(out, g, g)
		if !strings.HasPrefix(l, "warn") || !strings.Contains(l, "not checked: nothing is running (no status file") || fix != "holocron up --detach" {
			t.Errorf("%s: %q %q", g, l, fix)
		}
	}
	if code != 0 || !strings.HasPrefix(lastLine(out), "NOT RUNNING (") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func groupHasChecks(g string) bool {
	for _, c := range doctorTable {
		if c.group == g {
			return true
		}
	}
	return false
}

func lastLine(out string) string {
	ls := strings.Split(strings.TrimSpace(out), "\n")
	return ls[len(ls)-1]
}

func TestDoctorHeartbeatAge(t *testing.T) {
	for _, c := range []struct {
		age  time.Duration
		want string
	}{{500 * time.Millisecond, "ok"}, {4 * time.Second, "warn"}, {7 * time.Second, "fail"}} {
		r := newRig(t)
		r.setConfig(`"StatusIntervalSeconds": 1,`)
		r.running()
		r.doctorTools(func(d *doctorDeps) { d.now = func() time.Time { return time.Now().Add(c.age) } })
		out, _, _ := r.run("doctor", "--group", "recording")
		if l, _ := lineOf(out, "recording", "heartbeat"); !strings.HasPrefix(l, c.want+" ") || !strings.Contains(l, "no holocron host process") {
			t.Errorf("age %s: %q\n%s", c.age, l, out)
		}
	}
}

func TestDoctorCameraRows(t *testing.T) {
	pid := int64(4242)
	for _, c := range []struct {
		name string
		mod  func(*status.Camera)
		want []string
	}{
		{"alive", func(c *status.Camera) { c.Pid = &pid }, []string{"ok   Front", "alive pid 4242, input relay, 0 restart(s)"}},
		{"down", func(c *status.Camera) {
			c.Alive, c.LastError, c.DownSince = false, "RTSP EOF", status.NewTime(time.Now().Add(-time.Minute))
		}, []string{"fail Front", "DOWN since", "RTSP EOF", `fix: holocron cam test "Front" ; holocron logs --component ffmpeg:Front --since 10m`}},
		{"restarts warn", func(c *status.Camera) { c.RestartsLastHour, c.LastError = 3, "exit 1" }, []string{"warn Front", "3 restart(s)", "last error: exit 1", "fix: holocron cam test"}},
		{"restarts fail", func(c *status.Camera) { c.RestartsLastHour = 10 }, []string{"fail Front", "10 restart(s)"}},
		{"direct", func(c *status.Camera) { c.InputMode = "direct" }, []string{"warn Front", "DIRECTLY", "second session", "fix: holocron doctor --group connections"}},
	} {
		r := newRig(t)
		r.runningWith(func(d *status.Document) { c.mod(&d.Cameras[0]) })
		r.doctorTools(nil)
		out, _, _ := r.run("doctor", "--group", "recording")
		sec := out[strings.Index(out, "== recording =="):]
		for _, w := range c.want {
			if !strings.Contains(sec, w) {
				t.Errorf("%s: no %q in\n%s", c.name, w, sec)
			}
		}
	}
}

// Incident 22 Oct 2026: a camera stream ended (RTSP EOF) and nothing new reached the disk. A file
// that grows between the two samples is recording; a frozen one with no recent closed segment is
// "not recording" with the camera's fix, whatever the supervisor says.
func TestDoctorGrowthCatchesAStalledCamera(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name   string
		grow   bool
		closed time.Duration // age of the newest closed segment's end
		want   string
		fix    string
	}{
		{"growing", true, 10 * time.Minute, "ok   Front          recording: ", ""},
		{"frozen", false, 10 * time.Minute, "fail Front          not recording: no new data for ", `holocron cam test "Front" ; holocron logs --component ffmpeg:Front --since 10m`},
		{"frozen after a rollover", false, 20 * time.Second, "warn Front          ", "run holocron doctor --group recording again in a minute"},
	} {
		r := newRig(t)
		r.running()
		r.segment("Front", now.Add(-c.closed-time.Minute), now.Add(-c.closed), 4000)
		cur := r.segment("Front", now.Add(-c.closed), now.Add(-c.closed+time.Second), 1000)
		r.doctorTools(func(d *doctorDeps) {
			d.sleep = func(context.Context, time.Duration) error {
				if c.grow {
					f, _ := os.OpenFile(cur, os.O_APPEND|os.O_WRONLY, 0)
					f.Write(make([]byte, 5000))
					f.Close()
				}
				return nil
			}
		})
		out, _, _ := r.run("doctor", "--group", "recording")
		sec := out[strings.Index(out, "== recording =="):]
		l, fix := growthLine(sec)
		if !strings.HasPrefix(l, c.want) || (c.fix != "" && !strings.HasPrefix(fix, c.fix)) {
			t.Errorf("%s: %q fix %q\n%s", c.name, l, fix, out)
		}
		if c.name == "frozen" {
			t.Logf("incident fixture (stalled camera):\n%s\n     fix: %s", l, fix)
		}
	}
}

// The Matroska muxer writes in bursts: a file quiet for the first 2 s is not stalled. It is sampled
// again every 2 s for up to 10 s; the first growth ends the wait, and a frozen file costs 10 s, no more.
func TestDoctorGrowthWaitsForABurst(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name       string
		growOn     int // the sleep after which the file grows; 0: never
		wantSleeps int
		want       string
	}{
		{"first sample", 1, 1, "ok   Front          recording: "},
		{"third sample", 3, 3, "ok   Front          recording: "},
		{"last sample", 5, 5, "ok   Front          recording: "},
		{"frozen", 0, 5, "fail Front          not recording: no new data for "},
		{"frozen after a rollover", 0, 5, "warn Front          "},
	} {
		r := newRig(t)
		r.running()
		closed := 10 * time.Minute
		if c.name == "frozen after a rollover" {
			closed = 20 * time.Second
		}
		r.segment("Front", now.Add(-closed-time.Minute), now.Add(-closed), 4000)
		cur := r.segment("Front", now.Add(-closed), now.Add(-closed+time.Second), 1000)
		var sleeps int
		var slept time.Duration
		r.doctorTools(func(d *doctorDeps) {
			d.sleep = func(_ context.Context, w time.Duration) error {
				sleeps++
				slept += w
				if sleeps == c.growOn {
					f, _ := os.OpenFile(cur, os.O_APPEND|os.O_WRONLY, 0)
					f.Write(make([]byte, 5000))
					f.Close()
				}
				return nil
			}
		})
		out, _, _ := r.run("doctor", "--group", "recording")
		l, _ := growthLine(out[strings.Index(out, "== recording =="):])
		if !strings.HasPrefix(l, c.want) {
			t.Errorf("%s: %q\n%s", c.name, l, out)
		}
		if c.growOn == 0 && !strings.Contains(l, "did not grow in 10 s") {
			t.Errorf("%s: the window is not 10 s: %q", c.name, l)
		}
		if sleeps != c.wantSleeps || slept > 10*time.Second {
			t.Errorf("%s: %d sleeps, %s slept; want %d sleeps within 10 s", c.name, sleeps, slept, c.wantSleeps)
		}
	}
}

// Six cameras share each wait: three frozen ones cost 10 s in all, not 30, and the ones that grow
// are reported as soon as they do.
func TestDoctorGrowthSharesTheWaitBetweenCameras(t *testing.T) {
	now := time.Now()
	names := []string{"Cam1", "Cam2", "Cam3", "Cam4", "Cam5", "Cam6"}
	var cams []string
	for _, n := range names {
		cams = append(cams, `{"Name":"`+n+`","IP":"192.0.2.10","Record":true}`)
	}
	r := newRig(t)
	if err := os.WriteFile(r.cameras(), []byte("["+strings.Join(cams, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.running()
	cur := map[string]string{}
	for _, n := range names {
		r.segment(n, now.Add(-11*time.Minute), now.Add(-10*time.Minute), 4000)
		cur[n] = r.segment(n, now.Add(-10*time.Minute), now.Add(-10*time.Minute+time.Second), 1000)
	}
	var sleeps int
	var slept time.Duration
	r.doctorTools(func(d *doctorDeps) {
		d.sleep = func(_ context.Context, w time.Duration) error {
			sleeps++
			slept += w
			if sleeps == 2 {
				for _, n := range names[:3] {
					f, _ := os.OpenFile(cur[n], os.O_APPEND|os.O_WRONLY, 0)
					f.Write(make([]byte, 5000))
					f.Close()
				}
			}
			return nil
		}
	})
	out, _, _ := r.run("doctor", "--group", "recording")
	if n := len(regexp.MustCompile(`(?m)^ok   Cam\d +recording: `).FindAllString(out, -1)); n != 3 {
		t.Errorf("%d growing cameras reported:\n%s", n, out)
	}
	if n := strings.Count(out, "not recording: no new data for "); n != 3 || !strings.Contains(out, "did not grow in 10 s") {
		t.Errorf("%d stalled cameras reported:\n%s", n, out)
	}
	if sleeps != 5 || slept != 10*time.Second {
		t.Errorf("%d waits, %s in all; want 5 waits, 10 s", sleeps, slept)
	}
}

// growthLine is the second "Front" line of the recording group (after the camera row).
func growthLine(sec string) (string, string) {
	ls := strings.Split(sec, "\n")
	n := 0
	for i, l := range ls {
		if len(l) > 5 && strings.HasPrefix(l[5:], "Front ") {
			if n++; n == 2 {
				fix := ""
				if i+1 < len(ls) && strings.HasPrefix(ls[i+1], "     fix: ") {
					fix = strings.TrimPrefix(ls[i+1], "     fix: ")
				}
				return l, fix
			}
		}
	}
	return "", ""
}

func TestDoctorGrowthWithoutAnySegment(t *testing.T) {
	r := newRig(t)
	r.running()
	r.doctorTools(func(d *doctorDeps) { d.sleep = func(context.Context, time.Duration) error { return nil } })
	out, _, _ := r.run("doctor", "--group", "recording")
	if l, _ := growthLine(out); !strings.Contains(l, "fail Front          not recording: no segment of the last 25 h") {
		t.Errorf("%q\n%s", l, out)
	}
}

func TestDoctorGaps(t *testing.T) {
	base := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	for _, c := range []struct {
		name, log, want string
	}{
		{"none", "", ""},
		{"unexplained", "", "warn Front          1 gap(s) larger than 2s in the last 24 h, longest 33 s"},
		{"at a restart", "WARN  [supervisor] exit 1 after 60s for Front; restart #1 in 3s", "info Front          1 gap(s)"},
	} {
		r := newRig(t)
		r.running()
		r.segment("Front", base, base.Add(time.Minute), 100)
		next := base.Add(time.Minute)
		if c.name != "none" {
			next = next.Add(33 * time.Second)
		}
		r.segment("Front", next, next.Add(time.Minute), 100)
		if c.log != "" {
			os.MkdirAll(filepath.Join(r.root, "_logs"), 0o755)
			os.WriteFile(filepath.Join(r.root, "_logs", "holocron.log"), []byte(base.Add(time.Minute+5*time.Second).Format("2006-01-02T15:04:05.000000Z07:00")+" "+c.log+"\n"), 0o644)
		}
		r.doctorTools(func(d *doctorDeps) { d.sleep = func(context.Context, time.Duration) error { return nil } })
		out, _, _ := r.run("--json", "doctor", "--group", "recording")
		var got string
		for _, l := range strings.Split(textOf(t, out), "\n") {
			if strings.HasPrefix(l, "recording.gaps ") {
				got = strings.TrimPrefix(l, "recording.gaps ")
			}
		}
		if c.want == "" {
			if !strings.HasPrefix(got, "ok   Front          no gap larger than 2s") {
				t.Errorf("%s: %q", c.name, got)
			}
		} else if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

// textOf renders a --json report one line per check: "<id> <level> <title> <detail>".
func textOf(t *testing.T, js string) string {
	t.Helper()
	var rep doctorReport
	if err := json.Unmarshal([]byte(js), &rep); err != nil {
		t.Fatalf("%v\n%s", err, js)
	}
	var b strings.Builder
	for _, c := range rep.Checks {
		b.WriteString(c.ID + " " + padRight(c.Level, 4) + " " + padRight(c.Title, 14) + " " + c.Detail + "\n")
	}
	return b.String()
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

type fakeProber struct {
	p   shadow.Probe
	err error
}

func (f fakeProber) Probe(context.Context, string) (shadow.Probe, error) { return f.p, f.err }

func TestDoctorSegmentSanityAndDeep(t *testing.T) {
	now := time.Now()
	alaw := shadow.Probe{Duration: 60, Video: &shadow.Stream{Codec: "h264"}, Audio: &shadow.Stream{Codec: "pcm_alaw"}}
	for _, c := range []struct {
		name  string
		last  int
		deep  bool
		probe fakeProber
		want  []string
	}{
		{"normal", 6000, false, fakeProber{}, []string{"ok", "100% of the median rate"}},
		{"half empty", 2000, false, fakeProber{}, []string{"warn", "33% of the median rate"}},
		{"deep ok", 6000, true, fakeProber{p: alaw}, []string{"ok", "ffprobe: video h264, audio pcm_alaw"}},
		{"deep no video", 6000, true, fakeProber{p: shadow.Probe{Audio: alaw.Audio}}, []string{"fail", "NO video stream"}},
		{"deep aac", 6000, true, fakeProber{p: shadow.Probe{Video: alaw.Video, Audio: &shadow.Stream{Codec: "aac"}}}, []string{"warn", "audio aac (pcm_alaw expected)"}},
		{"deep error", 6000, true, fakeProber{err: errors.New("ffprobe: exit 1")}, []string{"fail", "ffprobe: ffprobe: exit 1"}},
	} {
		r := newRig(t)
		r.running()
		start := now.Add(-50 * time.Minute)
		for i := 0; i < 4; i++ {
			size := 6000
			if i == 3 {
				size = c.last
			}
			r.segment("Front", start, start.Add(time.Minute), size)
			start = start.Add(time.Minute)
		}
		r.segment("Front", start, now, 10)
		r.doctorTools(func(d *doctorDeps) {
			d.sleep = func(context.Context, time.Duration) error { return nil }
			d.prober = func(config.Config) shadow.Prober { return c.probe }
		})
		args := []string{"--json", "doctor", "--group", "recording"}
		if c.deep {
			args = append(args, "--deep")
		}
		out, _, _ := r.run(args...)
		var got string
		for _, l := range strings.Split(textOf(t, out), "\n") {
			if strings.HasPrefix(l, "recording.segment ") {
				got = l
			}
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: no %q in %q", c.name, w, got)
			}
		}
	}
}

// perMiB stands for one MiB of the Operator's camera in the replays below: the files stay small and
// the rates and ratios are the real ones.
const perMiB = 64

// span is one closed segment: its size in MiB and its length (15 minutes when 0).
type span struct {
	mib float64
	dur time.Duration
}

func repeat(mib float64, n int) []span {
	s := make([]span, n)
	for i := range s {
		s[i] = span{mib: mib}
	}
	return s
}

func join(parts ...[]span) []span {
	var s []span
	for _, p := range parts {
		s = append(s, p...)
	}
	return s
}

// replaySegments writes spans back to back as camera Front's closed segments, the last ending 40 s
// ago, then the segment being written, and returns the recording.segment check.
func replaySegments(t *testing.T, spans []span) check {
	t.Helper()
	r := newRig(t)
	r.running()
	now := time.Now().Truncate(time.Second) // segment names hold whole seconds
	start := now.Add(-40 * time.Second)
	for _, s := range spans {
		if s.dur == 0 {
			s.dur = 15 * time.Minute
		}
		start = start.Add(-s.dur)
	}
	for _, s := range spans {
		if s.dur == 0 {
			s.dur = 15 * time.Minute
		}
		r.segment("Front", start, start.Add(s.dur), int(s.mib*perMiB))
		start = start.Add(s.dur)
	}
	r.segment("Front", now.Add(-38*time.Second), now, 10)
	r.doctorTools(func(d *doctorDeps) { d.sleep = func(context.Context, time.Duration) error { return nil } })
	out, _, _ := r.run("--json", "doctor", "--group", "recording")
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range rep.Checks {
		if c.ID == "recording.segment" {
			return c
		}
	}
	t.Fatalf("no recording.segment check in\n%s", out)
	return check{}
}

// The Operator's Field Front camera on 29 Oct 2026, in MiB per 15-minute segment: an infrared night
// profile, a colour day profile and the ramps between them at dawn and dusk. A healthy camera.
var (
	camDay     = repeat(180, 40)
	camSunset  = []span{{mib: 179}, {mib: 178}, {mib: 114}, {mib: 51}, {mib: 45}}
	camNight   = repeat(46, 50)
	camSunrise = []span{{mib: 45}, {mib: 51}, {mib: 114}, {mib: 178}, {mib: 179}, {mib: 180}}
	// camSwap is the segment a program swap cut short at 07:22:48: 95744 KB in 467 s.
	camSwap = span{mib: 93.5, dur: 467 * time.Second}
)

func TestDoctorSegmentRateFollowsDayAndNight(t *testing.T) {
	for _, c := range []struct {
		name  string
		spans []span
		level string
		want  []string
	}{
		{"the real sunrise", join(camDay, camSunset, camNight, camSunrise, []span{camSwap}), "ok",
			[]string{", 158% of the median rate of 9 closed segment(s) of the last 2 h, 100% of the 24 h peak rate"}},
		{"the real sunrise on a new install", join(repeat(46, 10), camSunrise, []span{camSwap}), "ok",
			[]string{"158% of the median rate of 9 closed segment(s)", "100% of the 24 h peak rate"}},
		{"steady day", repeat(180, 96), "ok", []string{"100% of the median rate", "100% of the 24 h peak rate and 100% of the 24 h lowest rate (95 closed segment(s))"}},
		{"steady night", repeat(46, 96), "ok", []string{"100% of the 24 h peak rate and 100% of the 24 h lowest rate"}},
		{"stalled by day", join(repeat(180, 95), []span{{mib: 54}}), "warn",
			[]string{"30% of the median rate", "30% of the 24 h lowest rate", "; far below the 24 h lowest rate (limit 50%)"}},
		{"stalled by night", join(camDay, camSunset, camNight, []span{{mib: 13.5}}), "warn",
			[]string{"30% of the 24 h lowest rate", "far below"}},
		// The day profile dropping to the night rate is what dusk looks like: not judged a stall.
		{"by day at the night rate", join(camNight, camSunrise, camDay, []span{{mib: 54}}), "ok",
			[]string{"30% of the median rate", "120% of the 24 h lowest rate"}},
		// The 600 MiB segment ended more than 24 h ago: it is no part of the peak.
		{"runaway", join([]span{{mib: 600}}, camDay, camSunset, repeat(46, 45), camSunrise, []span{{mib: 288}}), "warn",
			[]string{"160% of the 24 h peak rate", "; far above the 24 h peak rate (limit 150%)"}},
		{"high but under the limit", join(camDay, camSunset, camNight, camSunrise, []span{{mib: 252}}), "ok",
			[]string{"140% of the 24 h peak rate"}},
		{"cut short by a restart", join(repeat(46, 20), []span{{mib: 11.5, dur: 225 * time.Second}}), "ok",
			[]string{"100% of the median rate", "100% of the 24 h peak rate"}},
		{"fewer than 3", repeat(180, 3), "ok", []string{"; fewer than 3 closed segments of the last 24 h to compare"}},
		{"new install, 4 segments", repeat(180, 4), "ok", []string{"100% of the 24 h peak rate and 100% of the 24 h lowest rate (3 closed segment(s))"}},
		{"new install, 4 segments, a runaway", join(repeat(180, 3), []span{{mib: 288}}), "warn", []string{"far above the 24 h peak rate"}},
		// An ffmpeg crash can leave an empty file: never a divisor, and as the newest one it warns.
		// replaySegments fails when doctor --json does not encode.
		{"an empty segment in the baseline", join(repeat(46, 10), []span{{}}, repeat(46, 10)), "ok",
			[]string{"100% of the median rate of 8 closed segment(s)", "100% of the 24 h lowest rate (19 closed segment(s))"}},
		{"stalled after empty segments", join(repeat(46, 10), repeat(0, 5), []span{{mib: 13.8}}), "warn",
			[]string{"30% of the 24 h lowest rate (10 closed segment(s)); far below"}},
		{"an empty newest segment", join(repeat(46, 20), []span{{}}), "warn", []string{" 0 B; no data in 900 s, far below any rate"}},
		{"an empty newest segment on a new install", []span{{mib: 46}, {}}, "warn", []string{"no data in 900 s"}},
		{"only empty segments", repeat(0, 6), "warn", []string{"no data in 900 s"}},
	} {
		got := replaySegments(t, c.spans)
		if got.Level != c.level {
			t.Errorf("%s: %s %s, want %s", c.name, got.Level, got.Detail, c.level)
		}
		for _, w := range c.want {
			if !strings.Contains(got.Detail, w) {
				t.Errorf("%s: no %q in %q", c.name, w, got.Detail)
			}
		}
		if wantFix := c.level == "warn"; (got.Fix != "") != wantFix || wantFix && !strings.HasPrefix(got.Fix, "holocron doctor --deep --group recording ; ") {
			t.Errorf("%s: fix %q", c.name, got.Fix)
		}
	}
}

// Every step of both ramps of a healthy camera, judged as the newest closed segment: no warning.
func TestDoctorSegmentRateRampsNeverWarn(t *testing.T) {
	for i := range camSunrise {
		if c := replaySegments(t, join(camDay, camSunset, camNight, camSunrise[:i+1])); c.Level != "ok" {
			t.Errorf("sunrise step %d (%.0f MiB): %s %s", i, camSunrise[i].mib, c.Level, c.Detail)
		}
	}
	dusk := append(append([]span{}, camSunset...), repeat(46, 2)...)
	for i := range dusk {
		if c := replaySegments(t, join(camNight, camSunrise, camDay, dusk[:i+1])); c.Level != "ok" {
			t.Errorf("sunset step %d (%.0f MiB): %s %s", i, dusk[i].mib, c.Level, c.Detail)
		}
	}
}

func TestDoctorSegmentRateJSON(t *testing.T) {
	c := replaySegments(t, join(camDay, camSunset, camNight, camSunrise, []span{camSwap}))
	b, _ := json.Marshal(c.Data)
	var d map[string]any
	json.Unmarshal(b, &d)
	for k, want := range map[string]float64{"compared": 9, "peakCompared": 96, "bytes": 93.5 * perMiB} {
		if d[k] != want {
			t.Errorf("%s = %v, want %v", k, d[k], want)
		}
	}
	for k, want := range map[string]float64{"ratio": 1.58, "peakRatio": 1.00, "lowestRatio": 4.00} {
		if v, _ := d[k].(float64); v < want-0.005 || v > want+0.005 {
			t.Errorf("%s = %v, want %.2f", k, d[k], want)
		}
	}
	if d["camera"] != "Front" || !strings.HasPrefix(d["segment"].(string), "Front_") {
		t.Errorf("camera, segment: %v", d)
	}
}

func TestDoctorSegmentRateJSONWithoutData(t *testing.T) {
	c := replaySegments(t, join(repeat(0, 3), repeat(46, 3), []span{{}}))
	b, err := json.Marshal(c.Data)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	json.Unmarshal(b, &d)
	for _, k := range []string{"ratio", "peakRatio", "lowestRatio", "bytes"} {
		if d[k] != 0.0 {
			t.Errorf("%s = %v, want 0", k, d[k])
		}
	}
	if d["peakCompared"] != 3.0 || d["compared"] != 3.0 {
		t.Errorf("compared: %v", d)
	}
}
