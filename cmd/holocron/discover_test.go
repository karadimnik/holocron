package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"holocron/internal/config"
	"holocron/internal/lan"
)

func dev(ip string, rtsp bool, mac string) found {
	return found{IP: netip.MustParseAddr(ip), Port: 554, RTSP: rtsp, MAC: mac, Answer: map[bool]string{true: "RTSP/1.0 200 OK", false: "TCP open, but it does not speak RTSP"}[rtsp]}
}

func mac(n string) string { return "aa:bb:cc:dd:ee:" + n }

// The comparison matrix, pure: known, new, moved (one exact MAC match), ambiguous (two devices with
// the MAC; the one device at another camera's address; two cameras claiming one device), missing
// (with and without a stored MAC), unchecked (a hostname, outside the scan), open (not RTSP).
func TestCompareDiscovered(t *testing.T) {
	cams := []config.Camera{
		{Name: "Front Door", IP: "192.168.1.50", MAC: mac("01")},
		{Name: "Garage", IP: "192.168.1.51", MAC: mac("02")},
		{Name: "Yard", IP: "192.168.1.52", MAC: mac("03")},
		{Name: "Shed", IP: "192.168.1.53", MAC: mac("04")},
		{Name: "Pool", IP: "192.168.1.54"},
		{Name: "Gate", IP: "cam.example.com"},
		{Name: "Far", IP: "10.0.0.5", MAC: mac("09")},
		{Name: "Side", IP: "192.168.1.55", MAC: mac("05")},
		{Name: "Porch", IP: "192.168.1.57:8554", MAC: mac("06")},
		{Name: "Hall", IP: "192.168.1.58"},
		{Name: "Dup1", IP: "192.168.1.60", MAC: mac("07")},
		{Name: "Dup2", IP: "192.168.1.61", MAC: mac("07")},
	}
	devs := []found{
		dev("192.168.1.15", true, "bb:bb:cc:dd:ee:0f"), dev("192.168.1.20", false, "bb:bb:cc:dd:ee:14"),
		dev("192.168.1.50", true, mac("01")), dev("192.168.1.57", true, mac("05")), dev("192.168.1.58", true, mac("08")),
		dev("192.168.1.77", true, mac("02")), dev("192.168.1.80", true, mac("03")), dev("192.168.1.81", true, mac("03")),
		dev("192.168.1.90", true, mac("07")), dev("192.168.1.91", true, ""), dev("192.168.1.30", true, mac("09")),
	}
	devs[9].Port = 8554
	inScan := func(a netip.Addr) bool { return netip.MustParsePrefix("192.168.1.0/24").Contains(a) }
	rows, drows := compareDiscovered(cams, devs, inScan)
	want := map[string]string{"Front Door": "known", "Garage": "moved", "Yard": "ambiguous", "Shed": "missing", "Pool": "missing", "Gate": "unchecked",
		"Far": "unchecked", "Side": "ambiguous", "Porch": "known", "Hall": "known", "Dup1": "ambiguous", "Dup2": "ambiguous"}
	for _, r := range rows {
		if r.State != want[r.Name] {
			t.Errorf("%s: %s, want %s (%s)", r.Name, r.State, want[r.Name], r.Note)
		}
		if (r.Command != "") != (r.State == "moved") {
			t.Errorf("%s: command %q in state %s", r.Name, r.Command, r.State)
		}
	}
	byName := map[string]discoverCamera{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if g := byName["Garage"]; g.Found != "192.168.1.77" || g.Command != `holocron cam set "Garage" --ip 192.168.1.77` {
		t.Errorf("moved: %+v", g)
	}
	if y := byName["Yard"]; len(y.Candidates) != 2 || !strings.Contains(y.Note, "2 devices") {
		t.Errorf("two devices with one MAC: %+v", y)
	}
	if !strings.Contains(byName["Pool"].Note, "--save-macs") || strings.Contains(byName["Shed"].Note, "--save-macs") {
		t.Errorf("missing hints: %q / %q", byName["Pool"].Note, byName["Shed"].Note)
	}
	if !strings.Contains(byName["Side"].Note, `"Porch"`) || !strings.Contains(byName["Porch"].Note, "another device") {
		t.Errorf("a MAC at another camera's address: %q / %q", byName["Side"].Note, byName["Porch"].Note)
	}
	if !strings.Contains(byName["Dup1"].Note, "2 cameras") {
		t.Errorf("two cameras, one device: %q", byName["Dup1"].Note)
	}
	wantDev := map[string]string{"192.168.1.15": "new", "192.168.1.20": "open", "192.168.1.50": "known", "192.168.1.57": "known", "192.168.1.58": "known",
		"192.168.1.77": "moved", "192.168.1.80": "ambiguous", "192.168.1.81": "ambiguous", "192.168.1.90": "ambiguous", "192.168.1.91": "new", "192.168.1.30": "new"}
	for _, d := range drows {
		if d.State != wantDev[d.IP] {
			t.Errorf("device %s: %s, want %s", d.IP, d.State, wantDev[d.IP])
		}
		switch d.IP {
		case "192.168.1.15":
			if d.Command != `holocron cam add --name "Camera 15" --ip 192.168.1.15 --quality stream1 --mac bb:bb:cc:dd:ee:0f` {
				t.Errorf("new: %q", d.Command)
			}
		case "192.168.1.91":
			if d.Command != `holocron cam add --name "Camera 91" --ip 192.168.1.91:8554 --quality stream1` {
				t.Errorf("new without a MAC, on 8554: %q", d.Command)
			}
		case "192.168.1.77":
			if d.Camera != "Garage" {
				t.Errorf("moved device: %+v", d)
			}
		}
	}
	ch := discoverChanges(cams, rows, devs)
	if len(ch) != 2 || ch[0] != (discoverChange{Camera: "Garage", Field: "IP", From: "192.168.1.51", To: "192.168.1.77"}) ||
		ch[1] != (discoverChange{Camera: "Hall", Field: "MAC", To: mac("08")}) {
		t.Errorf("changes: %+v", ch)
	}
	if n := suggestName(netip.MustParseAddr("192.168.2.15"), map[string]bool{"camera 15": true}); n != "Camera 2-15" {
		t.Error(n)
	}
}

// rtspServer answers every connection with reply after the first request line; an empty reply
// accepts and stays silent.
func rtspServer(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				bufio.NewReader(c).ReadString('\n')
				if reply == "" {
					time.Sleep(3 * time.Second)
					return
				}
				c.Write([]byte(reply))
			}()
		}
	}()
	return ln.Addr().String()
}

// lab routes the scanned private addresses to local fake servers: real TCP and the real probe, the
// addresses invented. A host without a route drops the connection attempt (silent until the timeout).
type lab struct {
	routes            map[string]string // "192.168.1.1:554" -> loopback address
	closed            string
	inFlight, maxSeen atomic.Int32
	mu                sync.Mutex
	slowest           time.Duration
}

func (l *lab) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	n := l.inFlight.Add(1)
	defer l.inFlight.Add(-1)
	for {
		m := l.maxSeen.Load()
		if n <= m || l.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	if to, ok := l.routes[addr]; ok {
		return (&net.Dialer{}).DialContext(ctx, network, to)
	}
	if strings.HasSuffix(addr, ".6:554") {
		return (&net.Dialer{}).DialContext(ctx, network, l.closed) // refused at once
	}
	start := time.Now()
	<-ctx.Done()
	l.mu.Lock()
	l.slowest = max(l.slowest, time.Since(start))
	l.mu.Unlock()
	return nil, ctx.Err()
}

func (r *rig) useLab(l *lab, table []lan.Neighbour, timeout time.Duration, inFlight int) {
	r.discover = func(d *discoverDeps) {
		d.dial, d.table, d.timeout, d.inFlight = l.dial, lan.FakeTable{Entries: table}, timeout, inFlight
		d.interfaces = func() ([]netip.Prefix, error) {
			return []netip.Prefix{netip.MustParsePrefix("192.168.1.100/24"), netip.MustParsePrefix("172.27.80.1/20")}, nil
		}
	}
}

func neighbours(pairs ...string) []lan.Neighbour {
	var out []lan.Neighbour
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, lan.Neighbour{IP: netip.MustParseAddr(pairs[i]), MAC: pairs[i+1]})
	}
	return out
}

func newLab(t *testing.T) *lab {
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := closed.Addr().String()
	closed.Close()
	return &lab{closed: addr, routes: map[string]string{
		"192.168.1.1:554": rtspServer(t, "RTSP/1.0 200 OK\r\nCSeq: 1\r\nPublic: OPTIONS, DESCRIBE\r\n\r\n"),
		"192.168.1.2:554": rtspServer(t, "HTTP/1.0 400 Bad Request\r\n\r\n"),
		"192.168.1.3:554": rtspServer(t, ""),
		"192.168.1.5:554": rtspServer(t, "RTSP/1.0 401 Unauthorized\r\nCSeq: 1\r\n\r\n"),
	}}
}

func discoverJSON(t *testing.T, out string) discoverReport {
	t.Helper()
	var rep discoverReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return rep
}

// The scan over real TCP: an RTSP responder is found and handshake-confirmed (200 and 401 alike), a
// port that is not RTSP is "open", a silent one counts as nothing, a refused one too; a silent host
// costs at most the timeout, and never more than inFlight probes run at once.
func TestCamDiscoverScan(t *testing.T) {
	r := newRig(t)
	l := newLab(t)
	r.useLab(l, neighbours("192.168.1.1", mac("01"), "192.168.1.5", mac("05")), 300*time.Millisecond, 3)
	out, errw, code := r.run("--json", "cam", "discover", "--subnet", "192.168.1.0/29")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errw)
	}
	rep := discoverJSON(t, out)
	got := map[string]string{}
	for _, d := range rep.Devices {
		got[d.IP] = d.State + " " + d.MAC + " " + d.Answer + d.Note
	}
	if len(got) != 4 || !strings.HasPrefix(got["192.168.1.1"], "new "+mac("01")+" RTSP/1.0 200 OK") || !strings.HasPrefix(got["192.168.1.5"], "new "+mac("05")+" RTSP/1.0 401") ||
		!strings.Contains(got["192.168.1.2"], "does not speak RTSP") || !strings.HasPrefix(got["192.168.1.2"], "open") || !strings.HasPrefix(got["192.168.1.3"], "open") {
		t.Errorf("devices: %v", got)
	}
	if rep.Schema != 1 || rep.Addresses != 6 || strings.Join(rep.Subnets, ",") != "192.168.1.0/29" || len(rep.Ports) != 1 || !rep.DryRun {
		t.Errorf("report: %+v", rep)
	}
	if l.maxSeen.Load() > 3 || l.slowest > 300*time.Millisecond+150*time.Millisecond {
		t.Errorf("in flight %d (bound 3), slowest silent host %v (bound 300 ms)", l.maxSeen.Load(), l.slowest)
	}
	// a whole silent /25: the bound holds and the run takes about ceil(126/8) timeouts
	l2 := &lab{}
	r.useLab(l2, nil, 100*time.Millisecond, 8)
	start := time.Now()
	if out, _, code := r.run("--json", "cam", "discover", "--subnet", "192.168.1.0/25"); code != 0 || len(discoverJSON(t, out).Devices) != 0 {
		t.Fatalf("%d %s", code, out)
	}
	took := time.Since(start)
	if l2.maxSeen.Load() != 8 || took < 1500*time.Millisecond || took > 4*time.Second {
		t.Errorf("silent /25: max in flight %d (want 8), took %v (about 1.6 s)", l2.maxSeen.Load(), took)
	}
}

// The automatic subnets: this host's private networks, a wider one narrowed to its /24, its own
// address skipped; refusals for public, too wide and loopback (no test hook) values, nothing probed.
func TestCamDiscoverSubnets(t *testing.T) {
	r := newRig(t)
	l := &lab{}
	r.useLab(l, nil, 20*time.Millisecond, 64)
	out, _, code := r.run("--json", "cam", "discover")
	rep := discoverJSON(t, out)
	if code != 0 || strings.Join(rep.Subnets, ",") != "192.168.1.0/24,172.27.80.0/24" || rep.Addresses != 506 || len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "172.27.80.0/20") {
		t.Errorf("%d %+v", code, rep)
	}
	for _, bad := range [][]string{{"--subnet", "8.8.8.0/24"}, {"--subnet", "192.168.0.0/21"}, {"--subnet", "127.0.0.1/32"}, {"--subnet", "192.168.1.0/22", "--subnet", "10.0.0.0/24"}} {
		l.maxSeen.Store(0)
		out, errw, code := r.run(append([]string{"cam", "discover"}, bad...)...)
		if code != exitUsage || l.maxSeen.Load() != 0 || !strings.Contains(errw, "cam discover") {
			t.Errorf("%v: %d probes %d\n%s%s", bad, code, l.maxSeen.Load(), out, errw)
		}
	}
}

const discoverCams = `[{"Name":"Front","IP":"192.168.1.1","Record":true,"Note":"kept"},{"Name":"Back","IP":"192.168.1.4","MAC":"aa:bb:cc:dd:ee:02","Record":true}]`

// --apply and --save-macs: a dry run writes nothing; each flag writes only its own changes through
// cam set's helper (other fields kept) and the running supervisor is asked to reload once; an
// ambiguous or non-matching MAC writes nothing.
func TestCamDiscoverApplyAndSaveMACs(t *testing.T) {
	r := newRig(t)
	os.WriteFile(r.cameras(), []byte(discoverCams), 0o644)
	r.running()
	l := newLab(t)
	r.useLab(l, neighbours("192.168.1.1", mac("01"), "192.168.1.5", mac("02")), 200*time.Millisecond, 8)
	before, _ := os.ReadFile(r.cameras())
	out, _, code := r.run("cam", "discover", "--subnet", "192.168.1.0/29")
	if after, _ := os.ReadFile(r.cameras()); code != 0 || !bytes.Equal(before, after) || len(r.ctl.calls) != 0 {
		t.Fatalf("dry run: %d, file changed %v, calls %v\n%s", code, !bytes.Equal(before, after), r.ctl.calls, out)
	}
	for _, want := range []string{"would set Back IP 192.168.1.4 -> 192.168.1.5 (--apply)", "would set Front MAC - -> aa:bb:cc:dd:ee:01 (--save-macs)", "dry run: 2 change(s) not written",
		`holocron cam set "Back" --ip 192.168.1.5`} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 80 && !strings.Contains(line, "holocron cam ") {
			t.Errorf("wider than 80 columns: %q", line)
		}
	}

	out, _, code = r.run("--json", "cam", "discover", "--subnet", "192.168.1.0/29", "--apply")
	rep := discoverJSON(t, out)
	c, _ := config.LoadCameras(r.cameras())
	if code != 0 || c.List[1].IP != "192.168.1.5" || c.List[1].MAC != mac("02") || c.List[0].MAC != "" || c.List[0].IP != "192.168.1.1" ||
		strings.Join(r.ctl.calls, ",") != "reload" || !strings.Contains(rep.Reload, "reloaded") || rep.DryRun {
		t.Fatalf("apply: %d %+v %v\n%s", code, c.List, r.ctl.calls, out)
	}
	if b, _ := os.ReadFile(r.cameras()); !strings.Contains(string(b), `"Note": "kept"`) {
		t.Errorf("a field was lost:\n%s", b)
	}

	r.ctl.calls = nil
	if _, _, code := r.run("cam", "discover", "--subnet", "192.168.1.0/29", "--save-macs"); code != 0 {
		t.Fatal(code)
	}
	if c, _ := config.LoadCameras(r.cameras()); c.List[0].MAC != mac("01") || c.List[1].IP != "192.168.1.5" || strings.Join(r.ctl.calls, ",") != "reload" {
		t.Errorf("save-macs: %+v %v", c.List, r.ctl.calls)
	}

	// Back's old address (.6) refuses; two devices with its MAC (ambiguous) or one with another MAC
	// (missing): nothing written, nothing reloaded
	os.WriteFile(r.cameras(), []byte(strings.Replace(discoverCams, "192.168.1.4", "192.168.1.6", 1)), 0o644)
	l.routes["192.168.1.4:554"] = l.routes["192.168.1.1:554"]
	l.routes["192.168.1.5:554"] = l.routes["192.168.1.1:554"]
	for name, table := range map[string][]lan.Neighbour{
		"ambiguous":    neighbours("192.168.1.1", mac("01"), "192.168.1.4", mac("02"), "192.168.1.5", mac("02")),
		"non-matching": neighbours("192.168.1.1", mac("01"), "192.168.1.4", mac("09"), "192.168.1.5", mac("09")),
	} {
		r.useLab(l, table, 200*time.Millisecond, 8)
		before, _ := os.ReadFile(r.cameras())
		r.ctl.calls = nil
		out, _, code := r.run("--json", "cam", "discover", "--subnet", "192.168.1.0/29", "--apply")
		rep := discoverJSON(t, out)
		after, _ := os.ReadFile(r.cameras())
		want := map[string]string{"ambiguous": "ambiguous", "non-matching": "missing"}[name]
		if code != 0 || !bytes.Equal(before, after) || len(r.ctl.calls) != 0 || rep.Cameras[1].State != want {
			t.Errorf("%s: %d changed %v calls %v %+v", name, code, !bytes.Equal(before, after), r.ctl.calls, rep.Cameras)
		}
	}
}

// A loopback rig through the real dialer and the test-only hook: the run the report quotes
// (go test -v -run TestCamDiscoverLoopbackRig ./cmd/holocron).
func TestCamDiscoverLoopbackRig(t *testing.T) {
	r := newRig(t)
	addr := rtspServer(t, "RTSP/1.0 200 OK\r\nCSeq: 1\r\n\r\n")
	os.WriteFile(r.cameras(), []byte(`[{"Name":"Front Door","IP":"`+addr+`","Record":true}]`), 0o644)
	r.discover = func(d *discoverDeps) {
		d.allowLoopback = true
		d.table = lan.FakeTable{Entries: neighbours("127.0.0.1", mac("50"))}
	}
	out, errw, code := r.run("cam", "discover", "--subnet", "127.0.0.1/32")
	t.Logf("text (exit %d):\n%s%s", code, out, errw)
	if code != 0 || !strings.Contains(out, "known") || !strings.Contains(out, "would set Front Door MAC") {
		t.Errorf("text run")
	}
	out, _, code = r.run("--json", "cam", "discover", "--subnet", "127.0.0.1/32", "--save-macs")
	t.Logf("json --save-macs (exit %d):\n%s", code, out)
	if c, _ := config.LoadCameras(r.cameras()); c.List[0].MAC != mac("50") {
		t.Errorf("not saved: %+v", c.List)
	}
}
