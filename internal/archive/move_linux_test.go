package archive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// On Linux the rename is renameat2(RENAME_NOREPLACE) itself, not the link fallback: it moves a
// file, and refuses an existing destination with fs.ErrExist, leaving both files as they were.
func TestRenameat2NoReplace(t *testing.T) {
	if sysRenameat2 == 0 {
		t.Skip("no renameat2 call number for this architecture: the link protocol is used")
	}
	dir := t.TempDir()
	src, dst, taken := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	for _, f := range []string{src, taken} {
		if err := os.WriteFile(f, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := renameat2NoReplace(src, dst)
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS) {
		t.Skip("this kernel or filesystem lacks RENAME_NOREPLACE:", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the source is still there: %v", err)
	}
	if err := renameat2NoReplace(dst, taken); !errors.Is(err, fs.ErrExist) {
		t.Errorf("onto an existing name: %v, want fs.ErrExist", err)
	}
	for f, want := range map[string]string{dst: src, taken: taken} {
		if b, err := os.ReadFile(f); err != nil || string(b) != want {
			t.Errorf("%s holds %q (%v), want %q", f, b, err, want)
		}
	}
}
