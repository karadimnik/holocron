package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"holocron/internal/app"
	"holocron/internal/config"
	"holocron/internal/lan"
)

// discoverDeps are cam discover's seams: the network, the neighbour table, the interfaces.
type discoverDeps struct {
	dial          func(ctx context.Context, network, addr string) (net.Conn, error)
	table         lan.Table
	interfaces    func() ([]lan.IfaceAddr, error)
	routes        lan.Routes
	timeout       time.Duration // per address and port
	inFlight      int           // probes at once
	allowLoopback bool          // tests only: --subnet may name 127.0.0.0/8
}

func newDiscoverDeps(e *env) *discoverDeps {
	d := &discoverDeps{dial: (&net.Dialer{}).DialContext, table: lan.System(), interfaces: lan.InterfaceAddrs, routes: lan.SystemRoutes(), timeout: 800 * time.Millisecond, inFlight: 64}
	if e.discover != nil {
		e.discover(d)
	}
	return d
}

// discoverReport is one run; --json prints it as is (docs/configuration.md#find-cameras-on-the-network).
type discoverReport struct {
	Schema         int              `json:"schema"`
	Subnets        []string         `json:"subnets"`
	Considered     []lan.Considered `json:"considered"`
	Ports          []int            `json:"ports"`
	Addresses      int              `json:"addresses"`
	DurationMs     int64            `json:"durationMs"`
	NeighbourError string           `json:"neighbourError,omitempty"`
	Notes          []string         `json:"notes"`
	Cameras        []discoverCamera `json:"cameras"`
	Devices        []discoverDevice `json:"devices"`
	Changes        []discoverChange `json:"changes"`
	DryRun         bool             `json:"dryRun"`
	Reload         string           `json:"reload,omitempty"`
}

// discoverCamera is a camera of cameras.json: known (answers at its address), moved (does not; one
// device with its MAC answers elsewhere), ambiguous (several do, or the one is another camera's
// address), missing, or unchecked (a hostname, or outside the scanned subnets).
type discoverCamera struct {
	Name       string   `json:"name"`
	Address    string   `json:"address"`
	MAC        string   `json:"mac,omitempty"`
	State      string   `json:"state"`
	Found      string   `json:"found,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Note       string   `json:"note,omitempty"`
	Command    string   `json:"command,omitempty"`
}

// discoverDevice is an address that answered: known, moved or ambiguous (a camera's, see
// discoverCamera), new (RTSP, in no camera), or open (the port accepts TCP but does not speak RTSP).
type discoverDevice struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	MAC     string `json:"mac,omitempty"`
	State   string `json:"state"`
	Camera  string `json:"camera,omitempty"`
	Answer  string `json:"answer,omitempty"`
	Note    string `json:"note,omitempty"`
	Command string `json:"command,omitempty"`
}

// discoverChange is one edit --apply (IP) or --save-macs (MAC) makes, or would make in a dry run.
type discoverChange struct {
	Camera  string `json:"camera"`
	Field   string `json:"field"`
	From    string `json:"from"`
	To      string `json:"to"`
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

// found is one scanned address that answered on some port.
type found struct {
	IP     netip.Addr
	Port   int
	RTSP   bool
	Answer string // the RTSP status line, or why it is not RTSP
	MAC    string
}

func cmdCamDiscover(ctx context.Context, e *env) error {
	fs := newFlags(e, "cam discover")
	var subnets []string
	fs.Func("subnet", "a private IPv4 subnet to scan, CIDR (repeatable; default: the LAN this host is on, found by its default route)", func(s string) error { subnets = append(subnets, s); return nil })
	saveMACs := fs.Bool("save-macs", false, "store the MAC of each camera that answers at its address (without it: a dry run)")
	apply := fs.Bool("apply", false, "set the new address of each camera that moved (without it: a dry run)")
	pos, err := parseFlags(fs, e.args)
	if err != nil {
		return err
	}
	if err := wantArgs("cam discover", pos, 0, 0); err != nil {
		return err
	}
	d := newDiscoverDeps(e)
	rep := &discoverReport{Schema: 1, DryRun: !*apply && !*saveMACs, Considered: []lan.Considered{}, Notes: []string{}, Cameras: []discoverCamera{}, Devices: []discoverDevice{}, Changes: []discoverChange{}}
	nets, self, err := discoverSubnets(ctx, d, subnets, rep)
	if err != nil {
		return err
	}
	hosts, err := lan.Hosts(nets, self)
	if err != nil {
		return usageErr("holocron cam discover: %v", err)
	}
	cams, err := app.LoadCameras(e.paths.Cameras)
	if err != nil {
		return err
	}
	rep.Ports = discoverPorts(cams.List)
	rep.Addresses = len(hosts)
	if !e.json {
		if len(rep.Considered) > 0 {
			fmt.Fprintf(e.out, "no --subnet given; the networks of this host:\n%s", considerationLines(rep.Considered))
		}
		fmt.Fprintf(e.out, "scanning %s: %d address(es), TCP %s, %d ms each, %d at a time\n", strings.Join(rep.Subnets, ", "), len(hosts), joinInts(rep.Ports), d.timeout.Milliseconds(), d.inFlight)
	}
	start := time.Now()
	devs := scanRTSP(ctx, d, hosts, rep.Ports)
	rep.DurationMs = time.Since(start).Milliseconds()
	if err := ctx.Err(); err != nil {
		return err
	}
	if table, err := d.table.Read(ctx); err != nil {
		rep.NeighbourError = err.Error()
	} else {
		macs := map[netip.Addr]string{}
		for _, n := range table {
			macs[n.IP] = n.MAC
		}
		for i := range devs {
			devs[i].MAC = macs[devs[i].IP]
		}
	}
	inScan := func(a netip.Addr) bool {
		for _, n := range nets {
			if n.Contains(a) {
				return true
			}
		}
		return false
	}
	rep.Cameras, rep.Devices = compareDiscovered(cams.List, devs, inScan)
	rep.Changes = discoverChanges(cams.List, rep.Cameras, devs)
	failed := applyDiscovered(e, rep, *apply, *saveMACs)
	for _, ch := range rep.Changes {
		if ch.Applied {
			rep.Reload = reloadMessage(ctx, e)
			break
		}
	}
	if e.json {
		if err := printJSON(e.out, rep); err != nil {
			return err
		}
	} else {
		printDiscover(e.out, rep)
	}
	if failed > 0 {
		return quietFail("%d change(s) could not be written", failed)
	}
	return nil
}

// discoverSubnets is the --subnet values, or the LAN(s) this host is on (lan.Select); self is this
// host's own addresses (never probed).
func discoverSubnets(ctx context.Context, d *discoverDeps, values []string, rep *discoverReport) ([]netip.Prefix, map[netip.Addr]bool, error) {
	self := map[netip.Addr]bool{}
	addrs, ierr := d.interfaces()
	for _, a := range addrs {
		if a.Flags&net.FlagLoopback == 0 {
			self[a.Addr.Addr()] = true
		}
	}
	var nets []netip.Prefix
	if len(values) > 0 {
		for _, v := range values {
			p, err := lan.ParseSubnet(v, d.allowLoopback)
			if err != nil {
				return nil, nil, usageErr("holocron cam discover: --subnet %v", err)
			}
			nets = append(nets, p)
		}
	} else {
		if ierr != nil {
			return nil, nil, failed("holocron cam discover: this host's network interfaces cannot be read (%v): pass --subnet", ierr)
		}
		routes, rerr := d.routes.Default(ctx)
		sel := lan.Select(addrs, routes, rerr)
		rep.Considered = append(rep.Considered, sel.Considered...)
		rep.Notes = append(rep.Notes, sel.Notes...)
		if sel.Refusal != "" {
			return nil, nil, failed("holocron cam discover: %s\nthe networks of this host:\n%spass --subnet (repeatable) to choose, for example --subnet 192.168.1.0/24", sel.Refusal, considerationLines(sel.Considered))
		}
		for _, l := range sel.Nets {
			nets = append(nets, l.Net)
		}
	}
	for _, n := range nets {
		rep.Subnets = append(rep.Subnets, n.String())
	}
	return nets, self, nil
}

// considerationLines is one line per network considered: kept or skipped, interface, network, why.
func considerationLines(cs []lan.Considered) string {
	width := 0
	for _, c := range cs {
		width = max(width, len(c.Name))
	}
	var b strings.Builder
	for _, c := range cs {
		verdict := "skipped"
		if c.Kept {
			verdict = "kept"
		}
		fmt.Fprintf(&b, "  %-7s %-*s %-18s %s\n", verdict, width, c.Name, c.CIDR, c.Reason)
	}
	return b.String()
}

// cameraHost is a camera's address as an IPv4 literal and its RTSP port (554 unless it names one).
func cameraHost(cam config.Camera) (netip.Addr, int, bool) {
	host, port := strings.TrimSpace(cam.IP), 554
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		port, _ = strconv.Atoi(p)
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !a.Is4() {
		return netip.Addr{}, 0, false
	}
	return a, port, true
}

// discoverPorts is 554 and every other port a camera's address names, in that order.
func discoverPorts(cams []config.Camera) []int {
	ports := []int{554}
	seen := map[int]bool{554: true}
	var extra []int
	for _, c := range cams {
		if _, p, ok := cameraHost(c); ok && !seen[p] {
			seen[p] = true
			extra = append(extra, p)
		}
	}
	sort.Ints(extra)
	return append(ports, extra...)
}

// scanRTSP probes every host on every port with cam test's TCP connect + RTSP OPTIONS (no login), at
// most d.inFlight at once, each bounded by d.timeout. It returns the hosts that answered, in address
// order: on the first port (in ports order) that speaks RTSP, else the first that accepted TCP.
func scanRTSP(ctx context.Context, d *discoverDeps, hosts []netip.Addr, ports []int) []found {
	type target struct {
		host, port int
	}
	targets := make([]target, 0, len(hosts)*len(ports))
	for h := range hosts {
		for p := range ports {
			targets = append(targets, target{h, p})
		}
	}
	results := make([]*found, len(targets))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(d.inFlight, len(targets)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				t := targets[i]
				addr := netip.AddrPortFrom(hosts[t.host], uint16(ports[t.port]))
				answer, err := probeRTSPWith(ctx, d.dial, config.Camera{IP: addr.String()}, d.timeout)
				var notRTSP *notRTSPError
				switch {
				case err == nil:
					results[i] = &found{IP: addr.Addr(), Port: int(addr.Port()), RTSP: true, Answer: answer}
				case errors.As(err, &notRTSP):
					results[i] = &found{IP: addr.Addr(), Port: int(addr.Port()), Answer: err.Error()}
				}
			}
		}()
	}
feed:
	for i := range targets {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	var out []found
	for h := range hosts {
		var best *found
		for p := range ports {
			r := results[h*len(ports)+p]
			if r != nil && (best == nil || r.RTSP && !best.RTSP) {
				best = r
			}
		}
		if best != nil {
			out = append(out, *best)
		}
	}
	return out
}

// addrText is ip, or ip:port when the port is not RTSP's 554.
func addrText(ip netip.Addr, port int) string {
	if port == 554 {
		return ip.String()
	}
	return netip.AddrPortFrom(ip, uint16(port)).String()
}

// compareDiscovered matches the answering devices with the camera list. Pure: no network, no file.
// A camera that does not answer at its address moves only by an exact MAC match with exactly one
// device that is no other camera's address and that no other camera claims; a camera without a stored
// MAC is never moved.
func compareDiscovered(cams []config.Camera, devs []found, inScan func(netip.Addr) bool) ([]discoverCamera, []discoverDevice) {
	rtspAt := map[netip.Addr]int{}
	for i, d := range devs {
		if d.RTSP {
			rtspAt[d.IP] = i
		}
	}
	camAt := map[netip.Addr]string{}
	for _, c := range cams {
		if a, _, ok := cameraHost(c); ok {
			if _, dup := camAt[a]; !dup {
				camAt[a] = c.Name
			}
		}
	}
	owner := map[int]string{} // device index -> camera name, and its state
	ownerState := map[int]string{}
	claims := map[int][]int{} // device index -> cameras moving to it
	out := make([]discoverCamera, len(cams))
	for ci, c := range cams {
		r := discoverCamera{Name: c.Name, Address: c.IP, MAC: c.MAC}
		a, _, ok := cameraHost(c)
		switch {
		case !ok:
			r.State, r.Note = "unchecked", "not an IPv4 address; not checked (cam test checks it)"
		case !inScan(a):
			r.State, r.Note = "unchecked", "outside the scanned subnets; not checked"
		default:
			if di, ok := rtspAt[a]; ok {
				r.State = "known"
				owner[di], ownerState[di] = c.Name, "known"
				if m := devs[di].MAC; c.MAC != "" && m != "" && m != c.MAC {
					r.Note = fmt.Sprintf("answers with MAC %s, not the stored one: another device may hold this address", m)
				}
				break
			}
			if c.MAC == "" {
				r.State, r.Note = "missing", "does not answer; no MAC stored, so a move cannot be followed: run cam discover --save-macs while it answers"
				break
			}
			var match []int
			for di, d := range devs {
				if d.RTSP && d.MAC == c.MAC {
					match = append(match, di)
				}
			}
			switch {
			case len(match) == 0:
				r.State, r.Note = "missing", "does not answer, and no device with its MAC answers in the scanned subnets"
			case len(match) > 1:
				r.State, r.Note = "ambiguous", fmt.Sprintf("does not answer; %d devices answer with its MAC: nothing is changed", len(match))
				for _, di := range match {
					r.Candidates = append(r.Candidates, addrText(devs[di].IP, devs[di].Port))
				}
			default:
				d := devs[match[0]]
				r.Found = addrText(d.IP, d.Port)
				if other, taken := camAt[d.IP]; taken {
					r.State, r.Note = "ambiguous", fmt.Sprintf("does not answer; its MAC answers at %s, which is the address of %q: nothing is changed", r.Found, other)
					r.Candidates, r.Found = []string{r.Found}, ""
					break
				}
				r.State = "moved"
				r.Command = fmt.Sprintf("holocron cam set %q --ip %s", c.Name, r.Found)
				claims[match[0]] = append(claims[match[0]], ci)
			}
		}
		out[ci] = r
	}
	for di, cs := range claims {
		if len(cs) == 1 {
			owner[di], ownerState[di] = cams[cs[0]].Name, "moved"
			continue
		}
		for _, ci := range cs {
			r := &out[ci]
			r.State, r.Note, r.Command = "ambiguous", fmt.Sprintf("does not answer; its MAC answers at %s, which %d cameras of the list claim: nothing is changed", r.Found, len(cs)), ""
			r.Candidates, r.Found = []string{r.Found}, ""
		}
	}
	for _, r := range out {
		if r.State != "ambiguous" {
			continue
		}
		for _, cand := range r.Candidates {
			for di, d := range devs {
				if addrText(d.IP, d.Port) == cand && ownerState[di] == "" {
					owner[di], ownerState[di] = r.Name, "ambiguous"
				}
			}
		}
	}
	names := map[string]bool{}
	for _, c := range cams {
		names[strings.ToLower(c.Name)] = true
	}
	devOut := make([]discoverDevice, len(devs))
	for di, d := range devs {
		r := discoverDevice{IP: d.IP.String(), Port: d.Port, MAC: d.MAC, Camera: owner[di], State: ownerState[di]}
		switch {
		case !d.RTSP:
			r.State, r.Camera, r.Note = "open", "", d.Answer
		case r.State == "":
			r.State, r.Answer = "new", d.Answer
			name := suggestName(d.IP, names)
			names[strings.ToLower(name)] = true
			r.Command = fmt.Sprintf("holocron cam add --name %q --ip %s --quality stream1", name, addrText(d.IP, d.Port))
			if d.MAC != "" {
				r.Command += " --mac " + d.MAC
			}
		default:
			r.Answer = d.Answer
		}
		devOut[di] = r
	}
	return out, devOut
}

// suggestName is "Camera <last octet>", or "Camera <third>-<last>" when that name is taken.
func suggestName(ip netip.Addr, taken map[string]bool) string {
	b := ip.As4()
	name := fmt.Sprintf("Camera %d", b[3])
	if taken[strings.ToLower(name)] {
		name = fmt.Sprintf("Camera %d-%d", b[2], b[3])
	}
	return name
}

// discoverChanges lists the edits: the new address of each moved camera, and the learned MAC of each
// camera that answers at its address and has none stored (a different stored MAC is never replaced).
func discoverChanges(cams []config.Camera, rows []discoverCamera, devs []found) []discoverChange {
	out := []discoverChange{}
	for i, r := range rows {
		switch r.State {
		case "moved":
			out = append(out, discoverChange{Camera: r.Name, Field: "IP", From: cams[i].IP, To: r.Found})
		case "known":
			if cams[i].MAC != "" {
				continue
			}
			a, _, _ := cameraHost(cams[i])
			for _, d := range devs {
				if d.RTSP && d.IP == a && d.MAC != "" {
					out = append(out, discoverChange{Camera: r.Name, Field: "MAC", From: "", To: d.MAC})
				}
			}
		}
	}
	return out
}

// applyDiscovered writes the changes the flags allow, through cam set's helper (validated, atomic,
// other fields kept). It returns how many failed.
func applyDiscovered(e *env, rep *discoverReport, apply, saveMACs bool) int {
	failed := 0
	for i := range rep.Changes {
		ch := &rep.Changes[i]
		var change app.CameraChange
		switch {
		case ch.Field == "IP" && apply:
			change.IP = &ch.To
		case ch.Field == "MAC" && saveMACs:
			change.MAC = &ch.To
		default:
			continue
		}
		if _, err := app.SetCamera(e.paths.Cameras, ch.Camera, change); err != nil {
			ch.Error = err.Error()
			failed++
			continue
		}
		ch.Applied = true
	}
	return failed
}

// wrapLine prints text after first, breaking between words before column 80 and starting each
// further line with rest.
func wrapLine(w io.Writer, first, rest, text string) {
	line := first
	for i, word := range strings.Fields(text) {
		if i > 0 && len(line)+1+len(word) > 80 {
			fmt.Fprintln(w, line)
			line = rest + word
			continue
		}
		if i > 0 {
			line += " "
		}
		line += word
	}
	fmt.Fprintln(w, line)
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// printDiscover is the text report: short lines (80 columns but for the commands to copy).
func printDiscover(w io.Writer, rep *discoverReport) {
	rtsp, open := 0, 0
	for _, d := range rep.Devices {
		if d.State == "open" {
			open++
		} else {
			rtsp++
		}
	}
	fmt.Fprintf(w, "done in %.1f s: %d RTSP device(s), %d port(s) open but not RTSP\n", float64(rep.DurationMs)/1000, rtsp, open)
	for _, n := range rep.Notes {
		wrapLine(w, "note: ", "      ", n)
	}
	if rep.NeighbourError != "" {
		wrapLine(w, "note: ", "      ", fmt.Sprintf("MAC addresses unknown (%s): moves cannot be followed", rep.NeighbourError))
	}
	fmt.Fprintln(w, "\nCAMERAS")
	if len(rep.Cameras) == 0 {
		fmt.Fprintln(w, "  (cameras.json lists none)")
	}
	for _, c := range rep.Cameras {
		fmt.Fprintf(w, "  %-9s %-20s %-21s %s\n", c.State, c.Name, c.Address, orDash(c.MAC))
		switch {
		case c.Found != "":
			fmt.Fprintf(w, "            now at %s\n", c.Found)
		case len(c.Candidates) > 0:
			fmt.Fprintf(w, "            its MAC answers at %s\n", strings.Join(c.Candidates, ", "))
		}
		if c.Note != "" && c.State != "moved" {
			wrapLine(w, "            ", "            ", c.Note)
		}
		if c.Command != "" {
			fmt.Fprintf(w, "            %s\n", c.Command)
		}
	}
	fmt.Fprintln(w, "\nDEVICES")
	if len(rep.Devices) == 0 {
		fmt.Fprintln(w, "  (none answered)")
	}
	for _, d := range rep.Devices {
		ip, _ := netip.ParseAddr(d.IP)
		fmt.Fprintf(w, "  %-9s %-21s %-17s %s\n", d.State, addrText(ip, d.Port), orDash(d.MAC), d.Camera)
		if d.State == "open" {
			fmt.Fprintln(w, "            port open, not RTSP: not a camera")
		}
		if d.Command != "" {
			fmt.Fprintf(w, "            %s\n", d.Command)
		}
	}
	fmt.Fprintln(w, "\nCHANGES")
	if len(rep.Changes) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, ch := range rep.Changes {
		flag := map[string]string{"IP": "--apply", "MAC": "--save-macs"}[ch.Field]
		verb := "would set"
		switch {
		case ch.Error != "":
			verb = "FAILED to set"
		case ch.Applied:
			verb = "set"
		}
		fmt.Fprintf(w, "  %s %s %s %s -> %s", verb, ch.Camera, ch.Field, orDash(ch.From), ch.To)
		if !ch.Applied && ch.Error == "" {
			fmt.Fprintf(w, " (%s)", flag)
		}
		fmt.Fprintln(w)
		if ch.Error != "" {
			fmt.Fprintf(w, "    %s\n", ch.Error)
		}
	}
	pending := 0
	for _, ch := range rep.Changes {
		if !ch.Applied && ch.Error == "" {
			pending++
		}
	}
	if pending > 0 {
		fmt.Fprintf(w, "dry run: %d change(s) not written; run again with the flag shown\n", pending)
	}
	if rep.Reload != "" {
		fmt.Fprintln(w, rep.Reload)
	}
}
