//go:build darwin

package osx

import (
	"fmt"
	"math"
	"syscall"
	"unsafe"
)

// OSInspector reads the kernel's process table through sysctl: kern.proc.pid (struct kinfo_proc)
// for the start time, state and process group, kern.procargs2 for the executable path (macproc.go
// parses both). Start token <tv_sec>.<tv_usec> of the start time stored at fork. A zombie is not
// running. macOS has no pidfd: Stop checks the identity again right before the kill.
// Cross-compiled for darwin/amd64 and darwin/arm64 and the parsing unit-tested on Linux; not yet run
// on a Mac.
type OSInspector struct{}

// The sysctl names (<sys/sysctl.h>).
const (
	ctlKern       = 1
	kernProc      = 14
	kernProcAll   = 0
	kernProcPid   = 1
	kernProcArgs2 = 49
)

// sysctl is the __sysctl system call with a numeric name: the standard library offers sysctl only
// by a string name, which cannot carry a pid, and golang.org/x/sys is not a dependency of this
// module. A nil buf asks for the size. It returns the number of bytes written (or needed).
func sysctl(mib []int32, buf []byte) (int, error) {
	n := uintptr(len(buf))
	var p unsafe.Pointer
	if len(buf) > 0 {
		p = unsafe.Pointer(&buf[0])
	}
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(p), uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func (OSInspector) Inspect(pid int) (ProcInfo, error) {
	if pid <= 0 || pid > math.MaxInt32 {
		return ProcInfo{}, ErrNotRunning
	}
	buf := make([]byte, kinfoProcSize)
	n, err := sysctl([]int32{ctlKern, kernProc, kernProcPid, int32(pid)}, buf)
	if err == syscall.ESRCH {
		return ProcInfo{}, ErrNotRunning
	}
	if err != nil {
		return ProcInfo{}, fmt.Errorf("kern.proc.pid %d: %w", pid, err)
	}
	p, err := macProcOf(pid, buf[:n])
	if err != nil {
		return ProcInfo{}, err
	}
	img, err := execPath(pid)
	if err != nil {
		return ProcInfo{}, err
	}
	return ProcInfo{Start: p.start, Image: img, Group: p.pgid}, nil
}

// execPath reads kern.procargs2, which fails with EINVAL for another user's process.
func execPath(pid int) (string, error) {
	argmax, err := syscall.SysctlUint32("kern.argmax")
	if err != nil {
		return "", fmt.Errorf("kern.argmax: %w", err)
	}
	buf := make([]byte, argmax)
	n, err := sysctl([]int32{ctlKern, kernProcArgs2, int32(pid)}, buf)
	if err != nil {
		return "", fmt.Errorf("kern.procargs2 %d: %w", pid, err)
	}
	return execPathOf(buf[:n])
}

// Stop checks the start token and image again, then kills the group (when recorded) or the process.
// Without a pidfd a window remains between the check and the kill, in which the pid would have to
// be freed and handed out again (macOS hands out pids in sequence).
func (in OSInspector) Stop(e ChildEntry) error {
	info, err := in.Inspect(e.Pid)
	if err != nil || info.Start != e.Start || info.Image != e.Image {
		return fmt.Errorf("pid %d changed before the kill", e.Pid)
	}
	if e.Group > 0 {
		return syscall.Kill(-e.Group, syscall.SIGKILL)
	}
	return syscall.Kill(e.Pid, syscall.SIGKILL)
}
