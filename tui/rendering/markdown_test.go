package rendering

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestMarkdownKeepsEstablishedVisualHierarchy(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		t.Run(th.Name, func(t *testing.T) {
			md := MakeMdRenderer(110, th)
			raw, err := md.Render("# MainTitle\n\n## SectionTitle\n\nBody text with `literal` and [Reference](https://example.test/guide).")
			if err != nil {
				t.Fatal(err)
			}
			var title, section, code, label, href AnsiRun
			for _, run := range ParseAnsiRuns(raw) {
				switch {
				case strings.Contains(run.Text, "MainTitle"):
					title = run
				case strings.Contains(run.Text, "SectionTitle"):
					section = run
				case strings.Contains(run.Text, "literal"):
					code = run
				case strings.Contains(run.Text, "Reference"):
					label = run
				case strings.Contains(run.Text, "https://example.test/guide") && run.Attr&cell.AttrUnderline != 0:
					href = run
				}
			}
			if title.Text == "" || title.BG.IsDefault() || title.Attr&cell.AttrBold == 0 {
				t.Errorf("level-one heading lost its established bold, filled treatment: %+v", title)
			}
			if code.Text == "" || code.BG.IsDefault() || code.FG.Equal(section.FG) {
				t.Errorf("inline code and headings lost their distinct treatments: code=%+v heading=%+v", code, section)
			}
			if label.Text == "" || href.Text == "" || label.FG.Equal(href.FG) {
				t.Errorf("link label and destination collapsed to the same ink: label=%+v href=%+v", label, href)
			}
		})
	}
}

func TestANSIControlsDoNotBecomePaintedText(t *testing.T) {
	for _, wrapper := range []struct{ name, start, end string }{
		{"OSC-ST", "\x1b]8;id=7;https://example.test/hidden\x1b\\", "\x1b]8;;\x1b\\"},
		{"OSC-BEL", "\x1b]8;;https://example.test/hidden\a", "\x1b]8;;\a"},
		{"OSC-C1", "\x9d8;;https://example.test/hidden\x9c", "\x9d8;;\x9c"},
	} {
		t.Run(wrapper.name, func(t *testing.T) {
			input := "before " + wrapper.start + "\x1b[1;4mLink\x1b[0m" + wrapper.end + " after"
			var text strings.Builder
			for _, run := range ParseAnsiRuns(input) {
				text.WriteString(run.Text)
			}
			if text.String() != "before Link after" {
				t.Fatalf("hyperlink metadata leaked into text: %q", text.String())
			}
			buf := surface.NewCellBuffer(80, 1)
			RenderToBufferAt(buf, 0, 0, input)
			var painted strings.Builder
			for x := 0; x < 17; x++ {
				painted.WriteRune(buf.Get(x, 0).Char)
			}
			if painted.String() != "before Link after" {
				t.Fatalf("surface parser leaked metadata or shifted text: %q", painted.String())
			}
		})
	}
}
