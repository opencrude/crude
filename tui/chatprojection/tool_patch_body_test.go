package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// The patch gutter must only ever paint patch rows. A result that carries no
// diff: an edit that changed nothing, whose body is only "Replaced 1
// occurrence in <path>", used to be handed to the lenient unified-diff parser,
// which turned that prose into a fake context row with line number 1.
func TestProseToolBodyNeverRendersAsPatch(t *testing.T) {
	msg := types.ChatMessage{
		Role: types.RoleTool, ToolName: "edit_file", ToolStatus: "success",
		ToolPreviewPhase: types.ToolPreviewDone, ToolArgs: "tui/tools/diff.go",
		Content: "Replaced 1 occurrence in tui/tools/diff.go\n",
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 60, 62, 64, 0, 20, "done", DisclosureExpanded)
	body := false
	for _, line := range ml.lines {
		if strings.HasPrefix(line, "│ 1 ") {
			t.Fatalf("prose body fabricated a patch row: %q", line)
		}
		if strings.HasPrefix(line, "│   Replaced 1 occurrence in tui/tools/diff.go") {
			body = true
		}
	}
	if !body {
		t.Fatalf("prose body missing from the plain rows: %#v", ml.lines)
	}
}

// A no-change edit is a failure: one compact row, red failure dot, the reason on
// the title line, and never the model-facing hint.
func TestNoChangeEditFailureRendersOneRedRow(t *testing.T) {
	msg := types.ChatMessage{
		Role: types.RoleTool, ToolName: "edit_file", ToolStatus: types.ToolStatusError,
		ToolPreviewPhase: types.ToolPreviewError, ToolArgs: "tools/dispatcher.go",
		Content: "Error: no changes to make: old_string and new_string are exactly the same\n" +
			"Hint: match the file's current bytes exactly — read_file the section first.",
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 96, 98, 100, 0, 20, "error", DisclosureCollapsed)
	if len(ml.lines) > 2 {
		t.Fatalf("failure header should only wrap to terminal width, got %#v", ml.lines)
	}
	row := ml.lines[0]
	if strings.Contains(row, "Hint:") || strings.Contains(row, "Error:") {
		t.Fatalf("failure row leaked payload text: %q", row)
	}
	pl := ml.native[0]
	if len(pl.Spans) < 2 || pl.Spans[1].Role != ui.ColorError {
		t.Fatalf("failure row did not take the red contract dot: %+v", pl.Spans)
	}

	// With room to spare the whole reason rides the title, unabbreviated.
	wide := renderToolMessageLines(msg, theme.DefaultDark(), 200, 200, 200, 0, 20, "error", DisclosureCollapsed)
	if want := "│ × Edit File failed tools/dispatcher.go — no changes to make: old_string and new_string are exactly the same ▸"; wide.lines[0] != want {
		t.Fatalf("wide failure row = %q, want %q", wide.lines[0], want)
	}
}

// The gate must not cost real patches their card: a result with a fenced diff
// still renders through the banded diff rows.
func TestRealPatchBodyStillRendersDiffRows(t *testing.T) {
	msg := types.ChatMessage{
		Role: types.RoleTool, ToolName: "edit_file", ToolStatus: "success",
		ToolPreviewPhase: types.ToolPreviewDone, ToolArgs: "x.go",
		Content: "Replaced 1 occurrence in x.go\n\n```diff\n--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,2 @@\n a\n+b\n```\n",
	}
	ml := renderToolMessageLines(msg, theme.DefaultDark(), 60, 62, 64, 0, 20, "done", DisclosureExpanded)
	added := false
	for _, line := range ml.lines {
		if strings.HasPrefix(line, "│ 2 + b") {
			added = true
		}
		if strings.Contains(line, "```") {
			t.Fatalf("diff fence leaked into the card: %q", line)
		}
	}
	if !added {
		t.Fatalf("patch body lost its diff rows: %#v", ml.lines)
	}
}

// Evidence tools never take the patch card, even when their body carries
// diff-like text: a read of a fixture that embeds "```diff" inside a Go string
// literal once turned into fabricated patch rows (gutter 1, 2, 3 …) in compact
// form. Per contract their compact form is exactly one title line; expanding
// reveals the text through the plain body rows.
func TestEvidenceBodiesNeverTakePatchCard(t *testing.T) {
	content := "Lines 22–66 of tui/viewmodel/projection_parity_test.go (114 total):\n\n" +
		"  22 func TestLiveAndHydratedToolRowsAreIdentical(t *testing.T) {\n" +
		"  52     content: \"Replaced 1 occurrence in tools/dispatcher.go\\n\\n```diff\\n--- a/tools/dispatcher.go\\n+++ b/tools/dispatcher.go\",\n" +
		"  66 }\n"
	cases := []struct{ tool, title string }{
		{tool: "read_file", title: "│ ● Read tui/viewmodel/projection_parity_test.go — L22–66 (of 114) ▸"},
		{tool: "execute_command", title: "│ ● Ran "},
	}
	for _, c := range cases {
		msg := types.ChatMessage{
			Role: types.RoleTool, ToolName: c.tool, ToolStatus: "success",
			ToolPreviewPhase:  types.ToolPreviewDone,
			ToolArgs:          "tui/viewmodel/projection_parity_test.go",
			ToolProgressLabel: "tui/viewmodel/projection_parity_test.go line 22-66",
			Content:           content,
		}
		collapsed := renderToolMessageLines(msg, theme.DefaultDark(), 100, 102, 104, 0, 20, "done", DisclosureCollapsed)
		if len(collapsed.lines) != 1 {
			t.Fatalf("%s compact form must stay one line, got %#v", c.tool, collapsed.lines)
		}
		if !strings.HasPrefix(collapsed.lines[0], c.title) {
			t.Fatalf("%s compact row = %q, want prefix %q", c.tool, collapsed.lines[0], c.title)
		}
		for _, disclosure := range []DisclosureState{DisclosureCollapsed, DisclosureExpanded} {
			ml := renderToolMessageLines(msg, theme.DefaultDark(), 100, 102, 104, 0, 20, "done", disclosure)
			for i, line := range ml.lines {
				if patchGutterRow(line) {
					t.Fatalf("%s fabricated a patch row at %d: %q", c.tool, i, line)
				}
			}
		}
		expanded := renderToolMessageLines(msg, theme.DefaultDark(), 100, 102, 104, 0, 20, "done", DisclosureExpanded)
		body := false
		for _, line := range expanded.lines[1:] {
			if strings.Contains(line, "Lines 22–66 of") {
				body = true
			}
		}
		if !body {
			t.Fatalf("%s expanded body missing: %#v", c.tool, expanded.lines)
		}
	}
}

// patchGutterRow matches the numbered gutter the lenient diff parser fabricates
// for prose ("│ 1   …"), never the plain body gutter ("│   …").
func patchGutterRow(line string) bool {
	rest := strings.TrimPrefix(line, "│ ")
	if rest == line || rest == "" {
		return false
	}
	return rest[0] >= '0' && rest[0] <= '9'
}

// A wider gap between change groups still collapses to the marker, and a small
// one stays visible, the rule the patch card inherits from the trimmed rows.
func TestBandedDiffOnlyElidesWideGaps(t *testing.T) {
	raw := func(midLines int) string {
		var sb strings.Builder
		sb.WriteString("```diff\n--- a/f.go\n+++ b/f.go\n@@ -1,40 +1,40 @@\n ctx-1\n ctx-2\n-old-a\n+new-a\n")
		for i := 1; i <= midLines; i++ {
			sb.WriteString(" ctx-mid\n")
		}
		sb.WriteString("-old-b\n+new-b\n ctx-tail\n```\n")
		return sb.String()
	}
	small := BuildBandedDiffRows(raw(6), 60, 64, true)
	if hasEllipsisRow(small) {
		t.Fatalf("small gap collapsed to a marker: %#v", small)
	}
	wide := BuildBandedDiffRows(raw(40), 60, 64, true)
	if !hasEllipsisRow(wide) {
		t.Fatalf("wide gap did not collapse: %d rows", len(wide))
	}
}

func hasEllipsisRow(rows []PaintLine) bool {
	for _, pl := range rows {
		if strings.Contains(pl.PlainText(), "···") {
			return true
		}
	}
	return false
}
