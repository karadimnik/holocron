package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// AGENTS.md rule 6: no em dash in any text, so none in a page, a script or a style sheet the browser gets.
func TestStaticAssetsHaveNoEmDash(t *testing.T) {
	forms := []string{"\u2014", "&mdash;", "&#8212;", "&#x2014;", `\u2014`}
	err := fs.WalkDir(staticFiles, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFiles.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(b))
		for _, form := range forms {
			if n := strings.Count(lower, strings.ToLower(form)); n > 0 {
				t.Errorf("%s holds %q %d time(s); use a plain -", path, form, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A camera's second line ("No footage since ...", "relay · 3 min ago · 6.4 GB") wraps rather than being cut
// with an ellipsis, in the table and in the phone's compact rows; the System panel's descriptions keep no
// paragraph margin on top of the form's grid gap.
func TestCameraLinesWrapAndSystemPanelSpacing(t *testing.T) {
	b, err := staticFiles.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, sel := range []string{`.err`, `.crow .sb`} {
		rule := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\{([^}]*)\}`).FindStringSubmatch(css)
		if rule == nil {
			t.Errorf("style.css has no rule %s", sel)
			continue
		}
		for _, clip := range []string{"ellipsis", "nowrap", "overflow:hidden", "max-width"} {
			if strings.Contains(rule[1], clip) {
				t.Errorf("%s holds %s: the line is cut instead of wrapped", sel, clip)
			}
		}
	}
	if !strings.Contains(css, "\n#aSystemPanel .form>p{margin:0}\n") {
		t.Error("style.css lost the rule that removes the paragraph margin in Admin > System")
	}
}
