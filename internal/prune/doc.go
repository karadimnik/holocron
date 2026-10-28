// Package prune is the archive pruner: a pure planner (Plan) that decides which segments may be
// deleted, and a guarded executor (Executor) that deletes them one by one, re-checking every file
// just before it goes. It ports Invoke-CamPrune (cam.recorder.ps1 of the former PowerShell system, original repository windows-terminal-IPCCTV-dashboard) and its gates; see README.md.
package prune
