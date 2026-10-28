//go:build !windows

package archive

import "testing"

// The link protocol, the rename where no atomic no-replace rename is at hand (macOS, a Linux
// filesystem without RENAME_NOREPLACE): exactly one racer's link succeeds, and only it succeeds.
func TestLinkThenUnlinkHasOneWinner(t *testing.T) { oneWinner(t, linkThenUnlink) }
