//go:build unix && !linux && !darwin

package osx

import (
	"errors"
	"syscall"
)

// OSInspector on a Unix other than Linux and macOS: the start time and image path are not read, so
// nothing can be verified and the reap leaves every entry alone (logged). Compiled only.
type OSInspector struct{}

var errNoInspect = errors.New("process inspection is not implemented on this OS")

func (OSInspector) Inspect(pid int) (ProcInfo, error) {
	if pid <= 0 || syscall.Kill(pid, 0) == syscall.ESRCH {
		return ProcInfo{}, ErrNotRunning
	}
	return ProcInfo{}, errNoInspect
}

func (OSInspector) Stop(ChildEntry) error { return errNoInspect }
