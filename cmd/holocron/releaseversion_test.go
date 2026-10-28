package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Staleness guard for the download examples: README.md ships inside every release archive and pins
// a version in its "Download a release" section, so a release whose README still names the
// previous version publishes a stale download recipe. The pin must equal the newest released
// version in CHANGELOG.md, which gains its section in the same commit that must bump the README.

var (
	changelogVersion = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - \d{4}-\d{2}-\d{2}(.*)$`)
	readmeVersion    = regexp.MustCompile(`\d+\.\d+\.\d+`)
)

const downloadHeading = "### Download a release"

// latestReleased is the version of the first changelog heading "## [X.Y.Z] - YYYY-MM-DD", skipping
// [Unreleased], pre-releases (their headings carry a suffix) and a version marked [YANKED].
func latestReleased(changelog string) string {
	for _, m := range changelogVersion.FindAllStringSubmatch(changelog, -1) {
		if !strings.Contains(m[2], "[YANKED]") {
			return m[1]
		}
	}
	return ""
}

// downloadSection is the README text after the download heading up to the next heading of any
// level; a line starting with # inside a fenced block is a shell comment, not a heading.
func downloadSection(readme string) string {
	var b strings.Builder
	in, inFence := false, false
	for _, line := range strings.Split(readme, "\n") {
		if docFence.MatchString(line) {
			inFence = !inFence
		} else if !inFence && docHeading.MatchString(line) {
			if in {
				break
			}
			in = strings.TrimSpace(line) == downloadHeading
			continue
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// releaseVersionProblems returns one message per way the README's download section disagrees with
// the newest released version of the changelog.
func releaseVersionProblems(readme, changelog string) []string {
	const fix = "update every version in the \"Download a release\" section of README.md to it, in the same commit that adds the CHANGELOG.md section (docs/releasing.md, release checklist)"
	want := latestReleased(changelog)
	if want == "" {
		return []string{"CHANGELOG.md has no released version heading \"## [X.Y.Z] - YYYY-MM-DD\""}
	}
	section := downloadSection(readme)
	if section == "" {
		return []string{"README.md has no section \"" + downloadHeading + "\" (this guard reads the pinned version there)"}
	}
	pins := readmeVersion.FindAllString(section, -1)
	if len(pins) == 0 {
		return []string{"README.md: the \"" + downloadHeading + "\" section pins no version (this guard expects VERSION=X.Y.Z and $Version = \"X.Y.Z\")"}
	}
	var problems []string
	for _, v := range pins {
		if v != want {
			problems = append(problems, "README.md download section pins "+v+" but the newest released version in CHANGELOG.md is "+want+": "+fix)
		}
	}
	return problems
}

func TestReadmePinsReleasedVersion(t *testing.T) {
	root := repoRoot(t)
	check := func(dir string) []string {
		t.Helper()
		read := func(name string) string {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			return strings.ReplaceAll(string(b), "\r\n", "\n")
		}
		return releaseVersionProblems(read("README.md"), read("CHANGELOG.md"))
	}
	for _, p := range check(root) {
		t.Error(p)
	}

	// controls on temporary copies: the checker accepts a README that matches the changelog and
	// flags one that a newer changelog section has left behind
	readme := "# R\n\n### Download a release\n\n(tag `v1.2.3`)\n\n```bash\n# Linux and macOS\nVERSION=1.2.3\n```\n\n```powershell\n$Version = \"1.2.3\"\n```\n\n### Build from source\n\nGo 1.27, MediaMTX v1.21.2\n"
	write := func(changelog string) string {
		dir := t.TempDir()
		for name, text := range map[string]string{"README.md": readme, "CHANGELOG.md": changelog} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	released := "# Changelog\n\n## [Unreleased]\n\n## [1.2.3] - 2026-01-02\n\n## [1.2.2] - 2026-01-01\n"
	if got := check(write(released)); len(got) != 0 {
		t.Errorf("positive control: a README pinning the newest release is flagged: %v", got)
	}
	skipped := "## [Unreleased]\n\n## [1.3.0-rc.1] - 2026-01-04\n\n## [1.2.4] - 2026-01-03 [YANKED]\n\n## [1.2.3] - 2026-01-02\n"
	if got := check(write(skipped)); len(got) != 0 {
		t.Errorf("positive control: a pre-release or a yanked version is taken as the newest release: %v", got)
	}
	newer := "## [Unreleased]\n\n## [1.3.0] - 2026-02-01\n\n## [1.2.3] - 2026-01-02\n"
	got := check(write(newer))
	if len(got) != 3 || !strings.Contains(got[0], "pins 1.2.3") || !strings.Contains(got[0], "is 1.3.0") ||
		!strings.Contains(got[0], "README.md") || !strings.Contains(got[0], "CHANGELOG.md") {
		t.Errorf("negative control: a README left at 1.2.3 after CHANGELOG.md gained 1.3.0 is not flagged once per pin: %v", got)
	}
	if got := releaseVersionProblems("# R\n", released); len(got) != 1 {
		t.Errorf("negative control: a README without the download section is not flagged: %v", got)
	}
}
