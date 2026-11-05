package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"holocron/internal/web"
)

func sessionsServer(t *testing.T, report web.SessionsReport) (*httptest.Server, *int) {
	t.Helper()
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		*hits++
		if req.URL.Path != "/api/admin/sessions" || req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_ = json.NewEncoder(w).Encode(report)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

var sessionsNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func sampleReport() web.SessionsReport {
	return web.SessionsReport{
		Ok: true, Now: sessionsNow, ActiveSeconds: 60, LoginRequired: true,
		SignedIn: []web.SignedInRow{
			{LoginID: "aaaaaaaa", User: "anna", Role: "admin", Since: sessionsNow.Add(-3 * time.Hour), LastActivity: sessionsNow.Add(-4 * time.Second), Precise: true, Online: true, State: "watching", Cameras: []string{"Front Door"}, Peer: "192.168.1.50", Network: "lan"},
			{LoginID: "bbbbbbbb", User: "bob", Role: "viewer", Since: sessionsNow.Add(-72 * time.Hour), LastActivity: sessionsNow.Add(-3 * time.Hour), Online: false, State: "not-online", Cameras: []string{}},
		},
		Watching: []web.WatchRow{
			{LoginID: "aaaaaaaa", User: "anna", Camera: "Front Door", Streams: "main, sub", Count: 2, Since: sessionsNow.Add(-20 * time.Minute), Peer: "192.168.1.50", Network: "lan"},
			{User: "this PC (no login)", ThisPC: true, Camera: "Garage", Streams: "sub", Count: 1, Since: sessionsNow.Add(-5 * time.Minute), Peer: "127.0.0.1", Network: "local"},
		},
	}
}

func TestUserSessionsAsksTheRunningWebApp(t *testing.T) {
	srv, hits := sessionsServer(t, sampleReport())
	r := newRig(t)
	r.listenOn(strings.TrimPrefix(srv.URL, "http://"), "")
	out, errw, code := r.run("user", "sessions")
	if code != 0 || errw != "" || *hits != 1 {
		t.Fatalf("user sessions: %d %q %q (%d calls)", code, out, errw, *hits)
	}
	for _, want := range []string{
		"web app: running at " + strings.TrimPrefix(srv.URL, "http://"),
		"SIGNED IN (2 sessions)", "WATCHING (2)",
		"anna", "watching Front Door", "192.168.1.50", "4s ago",
		"bob", "viewer", "not online", "~3h00m ago",
		"main, sub", "this PC (no login)", "local", "Garage",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	// the CLI wrote nothing of its own to the auth folder and opened no credential file
	if hits := r.leaks("camera_creds"); len(hits) != 0 {
		t.Errorf("unexpected files: %v", hits)
	}

	j, _, code := r.run("--json", "user", "sessions")
	var got sessionsOut
	if err := json.Unmarshal([]byte(j), &got); err != nil || code != 0 || !got.Running || got.Report == nil || len(got.Report.SignedIn) != 2 || got.Report.Watching[1].User != "this PC (no login)" {
		t.Errorf("--json: %v %d %q", err, code, j)
	}
}

func TestUserSessionsSaysWhenTheAppIsNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	r := newRig(t)
	r.listenOn(addr, "")
	out, errw, code := r.run("user", "sessions")
	if code != 1 || !strings.Contains(out, "web app: not answering at "+addr) || !strings.Contains(out, "not running") || !strings.Contains(out, "nobody can be watching") {
		t.Errorf("stopped app: %d %q %q", code, out, errw)
	}
	j, _, code := r.run("--json", "user", "sessions")
	var got sessionsOut
	if err := json.Unmarshal([]byte(j), &got); err != nil || code != 1 || got.Running || got.Report != nil || got.Reason == "" || got.Address != addr {
		t.Errorf("--json stopped app: %v %d %q", err, code, j)
	}
}

func TestUserSessionsOtherAnswers(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
		code   int
	}{
		{http.StatusUnauthorized, "wants a token", 1},
		{http.StatusNotFound, "does not know this route", 1},
		{http.StatusInternalServerError, "answered HTTP 500", 1},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { http.Error(w, "x", tc.status) }))
		r := newRig(t)
		r.listenOn(strings.TrimPrefix(srv.URL, "http://"), "")
		out, errw, code := r.run("user", "sessions")
		srv.Close()
		if code != tc.code || !strings.Contains(out+errw, tc.want) {
			t.Errorf("HTTP %d: %d %q %q, want %q", tc.status, code, out, errw, tc.want)
		}
	}
}

func TestUserSessionsWithoutLoginListener(t *testing.T) {
	rep := sampleReport()
	rep.LoginRequired, rep.SignedIn = false, []web.SignedInRow{}
	srv, _ := sessionsServer(t, rep)
	r := newRig(t)
	r.listenOn(strings.TrimPrefix(srv.URL, "http://"), "")
	out, _, code := r.run("user", "sessions")
	if code != 0 || !strings.Contains(out, "there are no logins to list") || strings.Contains(out, "SIGNED IN") {
		t.Errorf("%d %q", code, out)
	}
}

func TestUserSessionsRefusesArguments(t *testing.T) {
	r := newRig(t)
	if _, _, code := r.run("user", "sessions", "anna"); code != 2 {
		t.Errorf("an argument: exit %d, want 2", code)
	}
}

func TestUserSessionsShowsTheBandwidth(t *testing.T) {
	rep := sampleReport()
	rep.Bandwidth = web.BandwidthReport{CurrentBps: 2_600_000, HomeBps: 1_800_000, InternetBps: 800_000, Viewers: 3, WindowSeconds: 15,
		TotalBytes: 1_500_000_000, HomeBytes: 1_200_000_000, InternetBytes: 300_000_000, Since: sessionsNow.Add(-2 * time.Hour), RejectedReport: 2}
	srv, _ := sessionsServer(t, rep)
	r := newRig(t)
	r.listenOn(strings.TrimPrefix(srv.URL, "http://"), "")
	out, errw, code := r.run("user", "sessions")
	if code != 0 || errw != "" {
		t.Fatalf("user sessions: %d %q %q", code, out, errw)
	}
	for _, want := range []string{"BANDWIDTH", "now: 2.6 Mb/s from 3 page(s)", "home network 1.8 Mb/s, internet 800 kb/s",
		"1.50 GB in total (home network 1.20 GB, internet 300.0 MB)", "2 implausible report(s) ignored"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	j, _, _ := r.run("--json", "user", "sessions")
	var got sessionsOut
	if err := json.Unmarshal([]byte(j), &got); err != nil || got.Report == nil || got.Report.Bandwidth.CurrentBps != 2_600_000 || got.Report.Bandwidth.InternetBytes != 300_000_000 {
		t.Errorf("--json: %v %q", err, j)
	}
}
