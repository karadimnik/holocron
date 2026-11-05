package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"holocron/internal/web"
)

// sessionsTimeout bounds the one call to the running web app.
const sessionsTimeout = 5 * time.Second

// sessionsOut is the --json answer: what the running web app said, or why there is nothing to say.
type sessionsOut struct {
	Running bool                `json:"running"`
	Address string              `json:"address"`
	Reason  string              `json:"reason,omitempty"`
	Report  *web.SessionsReport `json:"report,omitempty"`
}

// cmdUserSessions is `holocron user sessions`: who is signed in and who is watching which camera.
// That lives in the running web app's memory (the live-view table and the page polls), so the
// command asks the app on its loopback listener, the way `holocron doctor` and `holocron open` reach
// it: no login is needed there and no credential file is opened. With the app stopped there is
// nothing to show, and the output says so (exit 1).
func cmdUserSessions(ctx context.Context, e *env) error {
	fs := newFlags(e, "user sessions")
	pos, err := parseFlags(fs, e.args)
	if err != nil {
		return err
	}
	if err := wantArgs("user sessions", pos, 0, 0); err != nil {
		return err
	}
	if err := e.needConfig(); err != nil {
		return err
	}
	addr := dialable(e.cfg.Web.Listen)
	out := sessionsOut{Address: addr}
	if !e.cfg.Web.Enabled {
		out.Reason = "the web app is switched off (web.enabled is false), so nobody is signed in or watching"
		return sessionsDown(e, out)
	}
	report, reason, err := fetchSessions(ctx, addr)
	if err != nil {
		return err
	}
	if report == nil {
		out.Reason = reason
		return sessionsDown(e, out)
	}
	out.Running, out.Report = true, report
	if e.json {
		return printJSON(e.out, out)
	}
	writeSessions(e.out, addr, report)
	return nil
}

// sessionsDown says the web app could not be asked, in the output and in the exit code.
func sessionsDown(e *env, out sessionsOut) error {
	if e.json {
		if err := printJSON(e.out, out); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(e.out, "web app: not answering at %s\n%s\n", out.Address, out.Reason)
	}
	return quietFail("the web app did not answer: %s", out.Reason)
}

// fetchSessions asks GET /api/admin/sessions on the loopback listener. A nil report with a reason
// means the app is not there to ask; an error is anything else that went wrong.
func fetchSessions(ctx context.Context, addr string) (*web.SessionsReport, string, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/admin/sessions", nil)
	if err != nil {
		return nil, "", failed("web.listen %q is not an address: %v", addr, err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled) {
			return nil, fmt.Sprintf("nothing answered within %s; it is not running, or it is stuck (holocron status, holocron logs --component web)", sessionsTimeout), nil
		}
		return nil, "nothing is listening there: the web app is not running (start it with holocron up). While it is stopped nobody can be watching; this list lives in its memory and starts empty at each start", nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, "the loopback listener wants a token (CAMUI_TOKEN is set for the web app), which this command does not send; open the Dashboard or the Admin tab in a browser instead", nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		return nil, "something answers there but it does not know this route: it is not this version of the web app (restart it with holocron restart)", nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", failed("the web app answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	var report web.SessionsReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, "", failed("the web app's answer is not what this version expects: %v", err)
	}
	return &report, "", nil
}

func writeSessions(w io.Writer, addr string, r *web.SessionsReport) {
	fmt.Fprintf(w, "web app: running at %s. \"Online\" means a page polled in the last %d s; the peer addresses live in its memory only.\n", addr, r.ActiveSeconds)
	if !r.LoginRequired {
		fmt.Fprintln(w, "No HTTPS login listener (web.tlsListen is empty): there are no logins to list.")
	} else {
		fmt.Fprintf(w, "\nSIGNED IN (%d sessions)\n", len(r.SignedIn))
		if len(r.SignedIn) == 0 {
			fmt.Fprintln(w, "  nobody")
		} else {
			tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  USER\tROLE\tNOW\tWHERE\tADDRESS\tSIGNED IN\tLAST ACTIVITY")
			for _, s := range r.SignedIn {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.User, s.Role, nowText(s), orDash(s.Network), orDash(s.Peer), clock(s.Since), lastActive(r.Now, s))
			}
			tw.Flush()
		}
	}
	fmt.Fprintf(w, "\nWATCHING (%d)\n", len(r.Watching))
	if len(r.Watching) == 0 {
		fmt.Fprintln(w, "  nobody")
	} else {
		tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  USER\tCAMERA\tSTREAM\tSESSIONS\tSINCE\tWHERE\tADDRESS")
		for _, v := range r.Watching {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%s\t%s\t%s\n", v.User, v.Camera, v.Streams, v.Count, clock(v.Since), orDash(v.Network), orDash(v.Peer))
		}
		tw.Flush()
	}
	writeBandwidth(w, r.Bandwidth)
}

// nowText is the state column: watching (with the cameras), online or not online.
func nowText(s web.SignedInRow) string {
	switch {
	case len(s.Cameras) > 0:
		return "watching " + strings.Join(s.Cameras, ", ")
	case s.Online:
		return "online"
	}
	return "not online"
}

func clock(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

// lastActive is how long before now the login was last active. Without a poll seen by this run of the web
// app the time is the session file's, which is written at most every 10 minutes: marked with "~".
func lastActive(now time.Time, s web.SignedInRow) string {
	d := now.Sub(s.LastActivity).Round(time.Second)
	if d < 0 {
		d = 0
	}
	text := d.String() + " ago"
	switch {
	case d >= 48*time.Hour:
		text = fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d >= time.Hour:
		text = fmt.Sprintf("%dh%02dm ago", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		text = fmt.Sprintf("%dm%02ds ago", int(d.Minutes()), int(d.Seconds())%60)
	}
	if !s.Precise {
		text = "~" + text
	}
	return text
}

// writeBandwidth prints what the viewers receive, as their pages report it (rates are what arrived in
// the last few seconds; the totals are since the web app started and are lost at a restart).
func writeBandwidth(w io.Writer, b web.BandwidthReport) {
	fmt.Fprintf(w, "\nBANDWIDTH (video received by viewers, as their pages report it)\n")
	fmt.Fprintf(w, "  now: %s from %d page(s) reporting in the last %d s (home network %s, internet %s)\n",
		bitRate(b.CurrentBps), b.Viewers, b.WindowSeconds, bitRate(b.HomeBps), bitRate(b.InternetBps))
	fmt.Fprintf(w, "  since %s: %s in total (home network %s, internet %s)\n",
		clock(b.Since), byteSize(b.TotalBytes), byteSize(b.HomeBytes), byteSize(b.InternetBytes))
	if b.RejectedReport > 0 {
		fmt.Fprintf(w, "  %d implausible report(s) ignored\n", b.RejectedReport)
	}
}

// bitRate is a rate in bits per second as "1.8 Mb/s".
func bitRate(bps uint64) string {
	switch {
	case bps >= 1_000_000:
		return fmt.Sprintf("%.1f Mb/s", float64(bps)/1e6)
	case bps >= 1000:
		return fmt.Sprintf("%d kb/s", (bps+500)/1000)
	}
	return fmt.Sprintf("%d b/s", bps)
}

// byteSize is a byte count as "1.20 GB" (decimal units, like the rates).
func byteSize(n uint64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%d kB", (n+500)/1000)
	}
	return fmt.Sprintf("%d B", n)
}
