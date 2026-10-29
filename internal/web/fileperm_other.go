//go:build !windows

package web

import (
	"fmt"
	"os"
)

// restrictToOwner makes a file (0600) or directory (0700) accessible to its owner only.
func restrictToOwner(path string, isDir bool) error {
	if isDir {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

// keyProtection describes who may read a key file (its mode) and whether only its owner may.
func keyProtection(path string) (string, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	perm := fi.Mode().Perm()
	return fmt.Sprintf("mode %04o", perm), perm&0o077 == 0, nil
}
