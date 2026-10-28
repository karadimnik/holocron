// Package golden holds the tests that load the golden files under
// testdata/golden (frozen outputs of the PowerShell product; provenance in
// testdata/golden/README.md) and check they parse and are not empty.
// Later packages (ffmpeg, relay, archive, clock) compare their own output to
// the same files.
package golden
