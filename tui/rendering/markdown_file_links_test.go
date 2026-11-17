package rendering

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
)

func TestMarkdownFileReferencesAreLinksWithoutChangingTheirText(t *testing.T) {
	for _, width := range []int{80, 160} {
		for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
			md := MakeMdRenderer(width, th)
			for _, input := range []string{
				"See tui/rendering/markdown.go:32:2.",
				"See `tui/rendering/markdown.go:32:2`.",
				"**See tui/rendering/markdown.go:32:2**.",
				"- See tui/rendering/markdown.go:32:2.",
				"| File |\n|---|\n| `tui/rendering/markdown.go:32:2` |",
			} {
				projection, err := md.RenderMapped(input)
				if err != nil {
					t.Fatal(err)
				}
				parser := projection.Parser()
				var linked, copied strings.Builder
				for _, line := range strings.Split(projection.ANSI, "\n") {
					for _, run := range parser.ParseLine(line) {
						if run.Link.Target() == "tui/rendering/markdown.go:32:2" {
							linked.WriteString(run.Text)
							if run.Attr&cell.AttrUnderline == 0 {
								t.Fatalf("file link does not use link styling: %+v", run)
							}
							if run.Copy.Valid() {
								copied.WriteString(run.Copy.Text.Value[run.Copy.Start:run.Copy.End])
							}
						}
					}
				}
				if linked.String() != "tui/rendering/markdown.go:32:2" || copied.String() != linked.String() {
					t.Fatalf("width=%d input=%q linked=%q copied=%q", width, input, linked.String(), copied.String())
				}
			}
		}
	}
}

func TestMarkdownFileLinksKeepExplicitDestinationsAndExcludeCodeBlocks(t *testing.T) {
	md := MakeMdRenderer(120, theme.DefaultDark())
	raw, err := md.Render("[source](src/main.go#L12)\n\n`README.md`\n\n```text\nsrc/not-a-link.go\n```\n\nnot/a*glob.go and v1.2.3 and https://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, run := range ParseAnsiRuns(raw) {
		if target := run.Link.Target(); target != "" {
			seen[target] = true
		}
	}
	for _, target := range []string{"src/main.go#L12", "README.md", "https://example.test/path"} {
		if !seen[target] {
			t.Errorf("missing destination %q: %v", target, seen)
		}
	}
	for _, target := range []string{"src/not-a-link.go", "not/a*glob.go", "v1.2.3"} {
		if seen[target] {
			t.Errorf("non-file became a link: %q", target)
		}
	}
}

func TestMarkdownFileReferencesKeepRouteAndPackagePathSyntax(t *testing.T) {
	md := MakeMdRenderer(120, theme.DefaultDark())
	for _, path := range []string{"app/(auth)/page.tsx", "@types/node/index.d.ts", "src/a[b].go", `src\main.go:12:3`} {
		for _, input := range []string{"See `" + path + "`.", "See (" + path + ")."} {
			raw, err := md.Render(input)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, run := range ParseAnsiRuns(raw) {
				if run.Link.File() {
					if run.Link.Target() != path {
						t.Fatalf("%q linked a different file: %q", input, run.Link.Target())
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("%q has no link to %q", input, path)
			}
		}
	}
}
