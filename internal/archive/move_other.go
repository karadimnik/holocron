//go:build !windows

package archive

import (
	"errors"
	"io/fs"
	"os"
)

// linkThenUnlink renames src to dst on one volume and fails when dst exists (fs.ErrExist): a hard
// link to dst, then the unlink of src. os.Rename is not used because it replaces an existing dst.
// The link decides the move: link(2) never replaces and creates a name atomically, so of movers
// racing for one file exactly one link succeeds, and that caller alone has moved the file. Its
// unlink cannot undo that: should src be gone already (a run finished what looked like an
// interrupted move, see Layout.rename), the move still stands. Interrupted between the two, both
// names hold the one file until a later run removes src (MoveReport.Recovered).
func linkThenUnlink(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// isBusy: only Windows refuses a rename because another process holds the file.
func isBusy(error) bool { return false }
