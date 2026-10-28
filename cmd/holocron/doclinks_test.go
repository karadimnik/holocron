package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Link guard for the documents: every relative link in a Markdown document names a file that
// exists, and every #anchor - file.md#anchor or in-page - is a heading of its target by GitHub's
// rules. Moving text between files is what breaks anchors, and nothing else notices. This is the Go
// port of the PowerShell check `doclinks` (Invoke-CamCheckDocLinks and Find-CamDocLinkProblems in
// tests/), with the same rules; the PowerShell check walked only README.md and docs/*.md, this one
// walks every document.

var (
	docFence   = regexp.MustCompile("^\\s{0,3}```")
	docCode    = regexp.MustCompile("`[^`]*`")
	docLink    = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	docScheme  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	docHeading = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)(?:\s+#+)?\s*$`)
	docInline  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	docPunct   = regexp.MustCompile(`[^\p{L}\p{Mn}\p{Nd}\p{Pc}\- ]`)
)

// docAnchors returns the anchors GitHub gives the headings of one markdown text: lower case,
// punctuation dropped, spaces to hyphens, and -1, -2 on repeats. A line starting with # inside a
// fenced block is code, not a heading.
func docAnchors(text string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if docFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := docHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		words := docInline.ReplaceAllString(m[1], "${1}")
		slug := strings.ReplaceAll(docPunct.ReplaceAllString(strings.ToLower(words), ""), " ", "-")
		if n, ok := seen[slug]; ok {
			seen[slug] = n + 1
			anchors[fmt.Sprintf("%s-%d", slug, n+1)] = true
		} else {
			seen[slug] = 0
			anchors[slug] = true
		}
	}
	return anchors
}

// docProse returns text with every fenced line and every inline `code` span blanked, keeping the
// line count so a match can be given its line number. Links inside code are not links.
func docProse(text string) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		switch {
		case docFence.MatchString(line):
			inFence = !inFence
			lines[i] = ""
		case inFence:
			lines[i] = ""
		default:
			lines[i] = docCode.ReplaceAllString(line, "")
		}
	}
	return strings.Join(lines, "\n")
}

type docLinkResult struct {
	files, links, anchors int
	problems              []string
}

// checkDocLinks checks every relative link of the documents rels (slash paths below root). URLs
// with a scheme and protocol-relative links are not checked; a link to an existing directory is
// accepted as long as it carries no anchor.
func checkDocLinks(root string, rels []string) docLinkResult {
	var res docLinkResult
	anchorCache := map[string]map[string]bool{}
	read := func(path string) (string, error) {
		b, err := os.ReadFile(path)
		return strings.ReplaceAll(string(b), "\r\n", "\n"), err
	}
	for _, rel := range rels {
		path := filepath.Join(root, filepath.FromSlash(rel))
		text, err := read(path)
		if err != nil {
			res.problems = append(res.problems, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		res.files++
		prose := docProse(text)
		for _, loc := range docLink.FindAllStringSubmatchIndex(prose, -1) {
			target := prose[loc[2]:loc[3]]
			if docScheme.MatchString(target) || strings.HasPrefix(target, "//") {
				continue
			}
			res.links++
			where := fmt.Sprintf("%s:%d: '%s'", rel, 1+strings.Count(prose[:loc[0]], "\n"), target)
			file, anchor, _ := strings.Cut(target, "#")
			resolved := path
			if file != "" {
				resolved = filepath.Join(filepath.Dir(path), filepath.FromSlash(file))
				if _, err := os.Stat(resolved); err != nil {
					res.problems = append(res.problems, where+" - no such file")
					continue
				}
			}
			if anchor == "" {
				continue
			}
			res.anchors++
			set, ok := anchorCache[resolved]
			if !ok {
				body := text
				if resolved != path {
					if body, err = read(resolved); err != nil {
						res.problems = append(res.problems, where+" - cannot read the target: "+err.Error())
						continue
					}
				}
				set = docAnchors(body)
				anchorCache[resolved] = set
			}
			if !set[anchor] {
				name, _ := filepath.Rel(root, resolved)
				res.problems = append(res.problems, fmt.Sprintf("%s - %s has no heading with anchor #%s", where, filepath.ToSlash(name), anchor))
			}
		}
	}
	sort.Strings(res.problems)
	return res
}

// docLinkFiles is the document set: every Markdown file in the repository, and llms.txt, which is
// Markdown by convention. Folders whose name starts with a dot are tooling, not documents.
func docLinkFiles(t testing.TB, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			if rel != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
		case strings.HasSuffix(strings.ToLower(rel), ".md") || rel == "llms.txt":
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// repoRoot is the nearest folder at or above the working directory that holds go.mod.
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			t.Fatalf("no go.mod at or above %s", wd)
		}
	}
}

func TestDocLinksResolve(t *testing.T) {
	root := repoRoot(t)
	files := docLinkFiles(t, root)
	res := checkDocLinks(root, files)
	t.Logf("%d document(s), %d relative link(s) checked, %d of them with an anchor", res.files, res.links, res.anchors)
	if res.files != len(files) || res.links == 0 || res.anchors == 0 {
		t.Errorf("the check saw %d of %d documents, %d links and %d anchors: it is not looking at the documents", res.files, len(files), res.links, res.anchors)
	}
	for _, p := range res.problems {
		t.Error(p)
	}
}

func writeDocs(t *testing.T, root string, docs map[string]string) []string {
	t.Helper()
	var rels []string
	for rel, text := range docs {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	return rels
}

// Positive control: every kind of good link is accepted, and the counts are exact.
func TestDocLinksAcceptGoodLinks(t *testing.T) {
	root := t.TempDir()
	rels := writeDocs(t, root, map[string]string{
		"README.md": "# Title\n\n## Same, Name\n\n## Same, Name\n\n" +
			"[a](docs/a.md) [b](docs/a.md#deep-section) [self](#title) [dup](#same-name-1) [cross](docs/a.md#deep-section-1)\n" +
			"![img](docs/pic.png \"caption\") [url](https://example.com/x#y) [mail](mailto:a@b.c) [proto](//cdn/x)\n" +
			"[dir](docs) [empty](docs/a.md#) [text](docs/a.md#link-text-here)\n" +
			"`[in code](nowhere.md)`\n\n```\n[fenced](nowhere.md#nothing)\n```\n",
		"docs/a.md":    "# A\r\n\r\n## Deep Section\r\n\r\n## Deep Section\r\n\r\n## Link [text](a.md) here ##\r\n\r\n[up](../README.md#same-name) [up2](../README.md)\r\n",
		"docs/pic.png": "x",
	})
	res := checkDocLinks(root, rels[:2])
	if len(res.problems) != 0 {
		t.Errorf("good links reported: %q", res.problems)
	}
	if res.files != 2 || res.links != 12 || res.anchors != 6 {
		t.Errorf("files/links/anchors = %d/%d/%d, want 2/12/6", res.files, res.links, res.anchors)
	}
}

// Negative control: a missing file, a broken in-page anchor, a broken cross-file anchor and an
// anchor that only exists inside a fenced block are all flagged with file:line, the link and the
// reason - the check is worth nothing otherwise.
func TestDocLinksFlagBrokenLinks(t *testing.T) {
	root := t.TempDir()
	rels := writeDocs(t, root, map[string]string{
		"README.md": "# Title\n\n```\n# Fenced\n```\n" +
			"[gone](docs/no-such-file.md) and [in-page](#no-such-heading)\n" +
			"[cross](docs/a.md#no-such-heading) [ok](docs/a.md#a) [fenced](#fenced)\n",
		"docs/a.md": "# A\n\n[up](../NOPE.md) [ok](../README.md#title)\n",
	})
	res := checkDocLinks(root, rels)
	want := []string{
		"README.md:6: '#no-such-heading' - README.md has no heading with anchor #no-such-heading",
		"README.md:6: 'docs/no-such-file.md' - no such file",
		"README.md:7: '#fenced' - README.md has no heading with anchor #fenced",
		"README.md:7: 'docs/a.md#no-such-heading' - docs/a.md has no heading with anchor #no-such-heading",
		"docs/a.md:3: '../NOPE.md' - no such file",
	}
	if strings.Join(res.problems, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(res.problems, "\n"), strings.Join(want, "\n"))
	}
	if res.files != 2 || res.links != 7 || res.anchors != 5 {
		t.Errorf("files/links/anchors = %d/%d/%d, want 2/7/5", res.files, res.links, res.anchors)
	}
}
