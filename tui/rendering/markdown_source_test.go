package rendering

import (
	"strings"
	"testing"

	"crude/tui/theme"
)

func TestMappedMarkdownKeepsLiteralCodeAndSourceWhitespace(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"```text\r\n\tfirst  \r\n\r\n\tsecond\r\n```", "\tfirst  \r\n\r\n\tsecond\r\n"},
		{"> ~~~~text\n> a\\*b &lt;tag&gt;\n> ```\n> ~~~~", "a\\*b &lt;tag&gt;\n```\n"},
		{"```text\nx\x1b_fake\x1b\\y\n```", "x\x1b_fake\x1b\\y\n"},
	} {
		source := tc.source
		p, err := MakeMdRenderer(80, theme.DefaultDark()).RenderMapped(source)
		if err != nil {
			t.Fatal(err)
		}
		parser := p.Parser()
		var code *CopyText
		for _, line := range strings.Split(p.ANSI, "\n") {
			for _, run := range parser.ParseLine(line) {
				if run.Copy.Text != nil && !run.Copy.Valid() {
					t.Fatalf("invalid source range: %+v", run.Copy)
				}
			}
			if value := parser.CopyAction(); value != nil {
				code = value
			}
		}
		if code == nil || code.Value != tc.want {
			t.Fatalf("block source=%+v want %q", code, tc.want)
		}
	}
}
func TestMappedMarkdownDoesNotTreatSourceControlsAsCopyMetadata(t *testing.T) {
	input := "hello &#27;_1&#27;\\ world\n\n```text\nx\n```"
	p, err := MakeMdRenderer(80, theme.DefaultDark()).RenderMapped(input)
	if err != nil {
		t.Fatal(err)
	}
	parser := p.Parser()
	actions := 0
	for _, line := range strings.Split(p.ANSI, "\n") {
		for _, run := range parser.ParseLine(line) {
			if strings.ContainsRune(run.Text, '\x1b') {
				t.Fatal("control reached visible text")
			}
		}
		if code := parser.CopyAction(); code != nil {
			actions++
			if code.Value != "x\n" {
				t.Fatalf("source forged code target %q", code.Value)
			}
		}
	}
	if actions != 1 {
		t.Fatalf("expected the one parsed code action, got %d", actions)
	}
}
