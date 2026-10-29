//go:build windows

package web

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strings"
)

// restrictToOwner removes every inherited access entry from a file or directory
// and grants full control to the current user alone, by SID. It fails closed:
// a private key must never be left readable by other accounts.
func restrictToOwner(path string, isDir bool) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("find the current user: %w", err)
	}
	grant := "*" + current.Uid + ":F"
	if isDir {
		grant = "*" + current.Uid + ":(OI)(CI)F"
	}
	out, err := exec.Command("icacls.exe", path, "/inheritance:r", "/grant:r", grant).CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls could not restrict %s: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// keyProtection describes who may use a key file (its access entries as icacls lists them) and
// whether exactly one, not inherited, entry is left: what restrictToOwner sets.
func keyProtection(path string) (string, bool, error) {
	if _, err := os.Stat(path); err != nil {
		return "", false, err
	}
	out, err := exec.Command("icacls.exe", path).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("icacls could not read %s: %v: %s", path, err, strings.TrimSpace(string(out)))
	}
	var entries []string
	for i, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		if i == 0 {
			line = strings.TrimPrefix(line, path)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // the summary line follows the blank one
		}
		entries = append(entries, line)
	}
	inherited := slices.ContainsFunc(entries, func(e string) bool { return strings.Contains(e, "(I)") })
	return strings.Join(entries, ", "), len(entries) == 1 && !inherited, nil
}
