package osx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// The macOS process structures that inspect_darwin.go and procs_darwin.go read through sysctl. The
// parsing carries no build tag so its tests run on every OS.

// struct kinfo_proc (<sys/sysctl.h>) as the kernel returns it to a 64-bit process: one layout on
// amd64 and arm64, little-endian. The offsets are those of golang.org/x/sys/unix.KinfoProc
// (cgo -godefs output of the macOS SDK), checked against that type by a compile-time assertion for
// darwin/amd64 and darwin/arm64 on 2026-11-02 (x/sys v0.49.0).
const (
	kinfoProcSize  = 648
	kinfoStartSec  = 0   // kp_proc.p_starttime.tv_sec, int64
	kinfoStartUsec = 8   // kp_proc.p_starttime.tv_usec, int32
	kinfoStat      = 36  // kp_proc.p_stat, char
	kinfoPid       = 40  // kp_proc.p_pid, int32
	kinfoComm      = 243 // kp_proc.p_comm, char[MAXCOMLEN+1]
	kinfoCommLen   = 17
	kinfoPpid      = 560 // kp_eproc.e_ppid, int32
	kinfoPgid      = 564 // kp_eproc.e_pgid, int32

	statZombie = 5 // SZOMB, <sys/proc.h>
)

// macProc is what is read of one kinfo_proc.
type macProc struct {
	pid, ppid, pgid int
	zombie          bool
	start           string // the ChildEntry.Start token: "<tv_sec>.<tv_usec, 6 digits>" of the start time stored at fork
	comm            string // p_comm: the executable's name, at most 16 bytes
}

func parseKinfoProc(b []byte) (macProc, error) {
	if len(b) != kinfoProcSize {
		return macProc{}, fmt.Errorf("kinfo_proc of %d bytes, want %d", len(b), kinfoProcSize)
	}
	le := binary.LittleEndian
	i32 := func(off int) int { return int(int32(le.Uint32(b[off:]))) }
	comm := b[kinfoComm : kinfoComm+kinfoCommLen]
	if n := bytes.IndexByte(comm, 0); n >= 0 {
		comm = comm[:n]
	}
	return macProc{
		pid:    i32(kinfoPid),
		ppid:   i32(kinfoPpid),
		pgid:   i32(kinfoPgid),
		zombie: b[kinfoStat] == statZombie,
		start:  fmt.Sprintf("%d.%06d", int64(le.Uint64(b[kinfoStartSec:])), i32(kinfoStartUsec)),
		comm:   string(comm),
	}, nil
}

// macProcOf reads the reply of kern.proc.pid for pid: an empty reply (no such process) and a zombie
// are ErrNotRunning.
func macProcOf(pid int, reply []byte) (macProc, error) {
	if len(reply) == 0 {
		return macProc{}, ErrNotRunning
	}
	p, err := parseKinfoProc(reply)
	switch {
	case err != nil:
		return macProc{}, err
	case p.pid != pid:
		return macProc{}, fmt.Errorf("kern.proc.pid %d answered pid %d", pid, p.pid)
	case p.zombie:
		return macProc{}, ErrNotRunning
	}
	return p, nil
}

// macProcEntries reads the reply of kern.proc.all (consecutive kinfo_proc) into the process list,
// zombies left out.
func macProcEntries(reply []byte) ([]ProcEntry, error) {
	if len(reply)%kinfoProcSize != 0 {
		return nil, fmt.Errorf("kern.proc.all answered %d bytes, not a multiple of %d", len(reply), kinfoProcSize)
	}
	var out []ProcEntry
	for off := 0; off < len(reply); off += kinfoProcSize {
		p, err := parseKinfoProc(reply[off : off+kinfoProcSize])
		if err != nil {
			return nil, err
		}
		if !p.zombie {
			out = append(out, ProcEntry{Pid: p.pid, PPid: p.ppid, Name: p.comm})
		}
	}
	return out, nil
}

// execPathOf reads the executable path from a kern.procargs2 reply: argc (int32), then the path the
// process was started with, NUL-terminated, then argv. The path is the one handed to execve, so it is
// relative when the process was started by a relative path; it never changes for the life of the
// process, which is what the identity check needs.
func execPathOf(reply []byte) (string, error) {
	if len(reply) < 4 {
		return "", fmt.Errorf("kern.procargs2 answered %d bytes", len(reply))
	}
	rest := reply[4:]
	n := bytes.IndexByte(rest, 0)
	if n <= 0 {
		return "", errors.New("kern.procargs2 holds no executable path")
	}
	return string(rest[:n]), nil
}
