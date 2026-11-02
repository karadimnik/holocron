//go:build darwin

package osx

import "syscall"

// ListProcs reads kern.proc.all (struct kinfo_proc of every process): name (p_comm) and parent pid.
// Read only. Not yet run on a Mac.
func ListProcs() ([]ProcEntry, error) {
	mib := []int32{ctlKern, kernProc, kernProcAll}
	for {
		size, err := sysctl(mib, nil)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size)
		n, err := sysctl(mib, buf)
		if err == syscall.ENOMEM {
			continue // the process table grew between the two calls
		}
		if err != nil {
			return nil, err
		}
		return macProcEntries(buf[:n])
	}
}
