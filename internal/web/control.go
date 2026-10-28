package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Control actions
//
// Every state change goes through the Controller (controller.go). The web app
// starts nothing, stops nothing and kills nothing itself: the supervisor owns the
// process inventory and the ordering that a stop has to respect. Status is a
// file read - see api.go.
// ---------------------------------------------------------------------------

// controlTimeout bounds a start (holocron up --detach) or a stop, which gives the
// orderly path time before it stops whatever outlived it, so the budget has to
// be generous.
const controlTimeout = 180 * time.Second

// recordToggleTimeout bounds a record toggle, which rewrites cameras.json and
// returns.
const recordToggleTimeout = 60 * time.Second

// maxOutputLines caps what an action's console output can add to a response.
const maxOutputLines = 200

// ActionRequest is the body of POST /api/control.
type ActionRequest struct {
	Scope  string `json:"scope"`
	Action string `json:"action"`
}

// RecordRequest is the body of POST /api/cameras/{name}/record.
type RecordRequest struct {
	Enabled *bool `json:"enabled"`
}

// ActionResponse reports what was run and what it said. The output is scrubbed
// like every other response: an action's output or error text can carry a
// camera address.
type ActionResponse struct {
	Ok         bool     `json:"ok"`
	Action     string   `json:"action"`
	Scope      string   `json:"scope,omitempty"`
	Camera     string   `json:"camera,omitempty"`
	Command    string   `json:"command"`
	ExitCode   int      `json:"exitCode"`
	DurationMs int64    `json:"durationMs"`
	Output     []string `json:"output"`
	Error      string   `json:"error,omitempty"`
}

// ActionResult is what a control action reports.
type ActionResult struct {
	Command  string
	ExitCode int
	Output   []string
	Err      error
	Duration time.Duration
	// HTTP, when set, is the status the page gets instead of the 200/502 pair: a refusal or a
	// conflict of a controller that did not run anything.
	HTTP int
}

func (a *API) handleControl(w http.ResponseWriter, r *http.Request) {
	var request ActionRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}

	scope := strings.ToLower(strings.TrimSpace(request.Scope))
	action := strings.ToLower(strings.TrimSpace(request.Action))

	switch scope {
	case "all", "recorder", "dashboard":
	default:
		writeAPIError(w, http.StatusUnprocessableEntity, "scope must be all, recorder or dashboard")
		return
	}
	switch action {
	case "start", "stop":
	default:
		writeAPIError(w, http.StatusUnprocessableEntity, "action must be start or stop")
		return
	}
	setTarget(r, scope+"/"+action)

	// One action at a time. Two stops racing each other, or a start racing a
	// stop, is how a half-stopped system happens.
	if !a.actionMu.TryLock() {
		writeAPIError(w, http.StatusConflict, "another action is still running; wait for it to finish")
		return
	}
	defer a.actionMu.Unlock()

	ctlr := a.controller()
	var result ActionResult
	if action == "start" {
		result = ctlr.Start(actionCtx(r), scope)
	} else {
		result = ctlr.Stop(actionCtx(r), scope)
	}
	a.expireController()
	writeJSON(w, actionStatus(result), ActionResponse{
		Ok:         result.Err == nil && result.ExitCode == 0,
		Action:     action,
		Scope:      scope,
		Command:    result.Command,
		ExitCode:   result.ExitCode,
		DurationMs: result.Duration.Milliseconds(),
		Output:     result.Output,
		Error:      errorText(result),
	})
}

func (a *API) handleRecordToggle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "no camera name in the path")
		return
	}

	var request RecordRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if request.Enabled == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, `the body must be {"enabled": true} or {"enabled": false}`)
		return
	}

	// Resolved here rather than left to the script, which exits 1 and prints a
	// list of valid targets - useful at a prompt, not to a page.
	cameras, _, err := a.readCameras()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if findCamera(cameras, name) < 0 {
		writeAPIError(w, http.StatusNotFound, fmt.Sprintf("no camera named %q is in cameras.json", name))
		return
	}

	setTarget(r, name)
	if !a.actionMu.TryLock() {
		writeAPIError(w, http.StatusConflict, "another action is still running; wait for it to finish")
		return
	}
	defer a.actionMu.Unlock()

	result := a.controller().SetRecord(actionCtx(r), name, *request.Enabled)
	writeJSON(w, actionStatus(result), ActionResponse{
		Ok:         result.Err == nil && result.ExitCode == 0,
		Action:     map[bool]string{true: "enable", false: "disable"}[*request.Enabled],
		Camera:     name,
		Command:    result.Command,
		ExitCode:   result.ExitCode,
		DurationMs: result.Duration.Milliseconds(),
		Output:     result.Output,
		Error:      errorText(result),
	})
}

func actionStatus(result ActionResult) int {
	if result.HTTP != 0 {
		return result.HTTP
	}
	if result.Err != nil {
		return http.StatusBadGateway
	}
	if result.ExitCode != 0 {
		return http.StatusBadGateway
	}
	return http.StatusOK
}

func errorText(result ActionResult) string {
	if result.Err != nil {
		return ScrubText(result.Err.Error())
	}
	if result.ExitCode != 0 {
		return fmt.Sprintf("the script exited with code %d", result.ExitCode)
	}
	return ""
}

// scrubLines turns console output into bounded, redacted, valid-UTF-8 lines.
// A child's output is in the console codepage, so a byte sequence
// that is not UTF-8 reaches here and would otherwise break the JSON encoder.
func scrubLines(text string) []string {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, ScrubText(strings.ToValidUTF8(line, "")))
		if len(lines) == maxOutputLines {
			lines = append(lines, fmt.Sprintf("... output truncated at %d lines", maxOutputLines))
			break
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// Shared HTTP helpers
// ---------------------------------------------------------------------------

// maxRequestBody bounds a PUT. The camera list and the config subset are both
// small; anything larger is a mistake or an attack, not a settings change.
const maxRequestBody = 256 * 1024

// APIError is the shape every refusal takes, so the page has one place to look
// for something to show the Operator.
type APIError struct {
	Ok      bool         `json:"ok"`
	Error   string       `json:"error"`
	Invalid []FieldError `json:"invalid,omitempty"`
}

// FieldError names the field that was wrong and says what would be right.
type FieldError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeAPIError(w, http.StatusBadRequest, "the request body is not the JSON this endpoint expects: "+ScrubText(err.Error()))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, APIError{Error: message})
}

func writeFieldErrors(w http.ResponseWriter, message string, invalid []FieldError) {
	writeJSON(w, http.StatusUnprocessableEntity, APIError{Error: message, Invalid: invalid})
}
