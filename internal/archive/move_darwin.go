package archive

import (
	"io/fs"
	"syscall"
	"time"
)

// renameNoReplace renames src to dst on one volume and fails when dst exists (fs.ErrExist):
// linkThenUnlink. macOS has an atomic renamex_np(RENAME_EXCL), but package syscall does not
// expose it and reaching it without cgo or golang.org/x/sys would mean libSystem trampolines in
// assembly per architecture; the link protocol already gives one winner.
func renameNoReplace(src, dst string) error { return linkThenUnlink(src, dst) }

// statusChanged is the last status change of a file (ctime), which a hard link sets.
func statusChanged(fi fs.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Unix())
	}
	return time.Time{}
}
