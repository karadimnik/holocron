package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The camera list as the supervisor reads it: a missing file stops holocron up (exit 21), so
// doctor must not say ok then.
func TestDoctorSetupCameraList(t *testing.T) {
	const addFix = "holocron cam add --name N --ip HOST --record"
	cases := []struct {
		name, content      string // content "-": no file
		level, detail, fix string
		exit               int
		verdictPrefix      string
		cameras            any // data.cameras in --json, nil when absent
	}{
		{"missing", "-", lvFail, "no camera list: %s does not exist", addFix, 1, "BROKEN", nil},
		{"invalid", `[{"Name":"Front","IP":"192.0.2.10","Record":"yes"}]`, lvFail, "%s: ", "holocron config validate, then correct %s", 1, "BROKEN", nil},
		{"empty", `[]`, lvWarn, "the camera list is empty: %s", addFix, 0, "DEGRADED", float64(0)},
		{"two cameras", rigCameras, lvOK, "2 camera(s) in %s", "", 0, "", float64(2)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.doctorTools(nil)
			if c.content == "-" {
				os.Remove(r.cameras())
			} else {
				os.WriteFile(r.cameras(), []byte(c.content), 0o644)
			}
			path := r.cameras()
			detail, fix := strings.ReplaceAll(c.detail, "%s", path), strings.ReplaceAll(c.fix, "%s", path)

			out, _, code := r.run("doctor", "--json", "--group", "setup")
			var rep doctorReport
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var got *check
			for i, ch := range rep.Checks {
				if ch.ID == "setup.cameras" {
					got = &rep.Checks[i]
				}
			}
			if got == nil || rep.Checks[1].ID != "setup.cameras" {
				t.Fatalf("setup.cameras is not the second check: %+v", rep.Checks)
			}
			if got.Level != c.level || got.Title != "camera list" || !strings.HasPrefix(got.Detail, detail) || got.Fix != fix {
				t.Errorf("check %+v", *got)
			}
			if c.name == "invalid" && !strings.Contains(got.Detail, "cameras[0].Record") {
				t.Errorf("not the config validate message: %q", got.Detail)
			}
			var cameras any
			if m, ok := got.Data.(map[string]any); ok {
				cameras = m["cameras"]
			}
			if cameras != c.cameras {
				t.Errorf("data %+v, want cameras %v", got.Data, c.cameras)
			}
			if code != c.exit || (c.verdictPrefix != "" && !strings.HasPrefix(rep.Summary.Verdict, c.verdictPrefix)) {
				t.Errorf("exit %d, verdict %s", code, rep.Summary.Verdict)
			}

			text, _, _ := r.run("doctor", "--group", "setup")
			if want := c.level + strings.Repeat(" ", 5-len(c.level)) + "camera list    " + detail; !strings.Contains(text, want) {
				t.Errorf("text output lacks %q:\n%s", want, text)
			}
		})
	}
}

// The detail of an invalid list is the one holocron config validate prints for the file.
func TestDoctorCameraListSaysWhatConfigValidateSays(t *testing.T) {
	r := newRig(t)
	r.doctorTools(nil)
	os.WriteFile(r.cameras(), []byte(`[{"Name":"Front","IP":"not a host!"}]`), 0o644)
	val, _, _ := r.run("config", "validate")
	var want string
	for _, l := range strings.Split(val, "\n") {
		if strings.HasPrefix(l, "FAIL "+r.cameras()) {
			want = strings.TrimPrefix(l, "FAIL ")
		}
	}
	out, _, _ := r.run("doctor", "--group", "setup")
	if want == "" || !strings.Contains(out, "fail camera list    "+want+"\n") {
		t.Errorf("config validate said %q; doctor:\n%s", want, out)
	}
}
