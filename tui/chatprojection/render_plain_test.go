package chatprojection

import (
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestPlainAlphabeticBodyAvoidsMarkdownWithoutChangingRichMarkup(test *testing.T) {
	renderer := rendering.MakeMdRenderer(80, theme.DefaultDark())
	for _, text := range []string{"Plain body", "animated stable body"} {
		if _, markdown := renderMarkdown(text, renderer); markdown {
			test.Fatal("plain words still allocate a Markdown render")
		}
	}
	for _, text := range []string{"# Heading", "**bold**", "`code`", "one\ntwo", "https://example.test", "    indented code"} {
		if _, markdown := renderMarkdown(text, renderer); !markdown {
			test.Fatalf("rich semantics skipped for %q", text)
		}
	}
}
