package archive

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// renameNoReplace renames src to dst on one volume and fails when dst exists (fs.ErrExist):
// renameat2(RENAME_NOREPLACE), one call that checks and renames atomically, so of movers racing
// for one file exactly one succeeds and no file is ever under both names. Where the kernel or the
// filesystem lacks the flag (EINVAL, ENOSYS: a kernel before 3.15, some network filesystems) and
// on an architecture whose call number is not listed here, it is linkThenUnlink.
func renameNoReplace(src, dst string) error {
	if sysRenameat2 != 0 {
		err := renameat2NoReplace(src, dst)
		if !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOSYS) {
			return err
		}
	}
	return linkThenUnlink(src, dst)
}

// sysRenameat2 is the renameat2 call number of this architecture (package syscall lacks it on
// amd64); 0 when not listed.
var sysRenameat2 = map[string]uintptr{"amd64": 316, "arm64": 276}[runtime.GOARCH]

const (
	atFDCWD             = -100 // AT_FDCWD: paths relative to the working folder (package syscall keeps it private)
	renameNoReplaceFlag = 0x1  // RENAME_NOREPLACE
)

func renameat2NoReplace(src, dst string) error {
	from, err := syscall.BytePtrFromString(src)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(dst)
	if err != nil {
		return err
	}
	cwd := atFDCWD
	_, _, e := syscall.Syscall6(sysRenameat2, uintptr(cwd), uintptr(unsafe.Pointer(from)), uintptr(cwd), uintptr(unsafe.Pointer(to)), renameNoReplaceFlag, 0)
	if e != 0 {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: e}
	}
	return nil
}

// statusChanged is the last status change of a file (ctime), which a hard link sets.
func statusChanged(fi fs.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Unix())
	}
	return time.Time{}
}
