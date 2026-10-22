package chatprojection

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/types"
)

func largeGoPatch(name string, lines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "```diff\n--- a/%[1]s.go\n+++ b/%[1]s.go\n@@ -0,0 +1,%[2]d @@\n", name, lines)
	for i := range lines {
		fmt.Fprintf(&b, "+func %sHelper%d(value int) (string, error) { return fmt.Sprint(value), nil }\n", name, i)
	}
	return b.String() + "```\n"
}

func awaitDiffSyntax(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for DiffSyntaxBusy() {
		if time.Now().After(deadline) {
			t.Fatal("background diff lexing never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

func syntaxColored(rows []PaintLine) bool {
	for _, row := range rows {
		for _, span := range row.Spans {
			if span.FGSet && strings.Contains(span.Text, "Helper") {
				return true
			}
		}
	}
	return false
}

// History materialization renders file edits expanded on the UI goroutine.
// A large edit must not lex there: it paints its diff immediately without
// syntax colors, keeps exactly the same rows, and gains the colors once the
// background lexer finishes.
func TestLargeDiffLexesOffTheCallerAndKeepsGeometry(t *testing.T) {
	raw := strings.TrimSuffix(strings.TrimPrefix(largeGoPatch(t.Name()[4:24], 160), "```diff\n"), "```\n")
	first, pending := buildBandedDiffRows(raw, 100, 104, true)
	if !pending {
		t.Fatal("a large expanded diff was lexed on the calling goroutine")
	}
	if syntaxColored(first) {
		t.Fatal("pending rows painted syntax colors they could not have")
	}
	awaitDiffSyntax(t)
	second, pending := buildBandedDiffRows(raw, 100, 104, true)
	if pending || !syntaxColored(second) {
		t.Fatalf("finished lexing did not color the diff (pending=%v)", pending)
	}
	if len(first) != len(second) {
		t.Fatalf("colors changed the row count: %d → %d", len(first), len(second))
	}
	for i := range first {
		if first[i].PlainText() != second[i].PlainText() {
			t.Fatalf("row %d text changed with colors: %q → %q", i, first[i].PlainText(), second[i].PlainText())
		}
	}
}

// A small edit and a collapsed preview stay synchronous, so they never flash.
func TestSmallDiffStaysSynchronous(t *testing.T) {
	raw := "--- a/small.go\n+++ b/small.go\n@@ -1 +1 @@\n-func old() {}\n+func smallHelper() {}\n"
	rows, pending := buildBandedDiffRows(raw, 100, 104, true)
	if pending || !syntaxColored(rows) {
		t.Fatalf("small diff was not colored synchronously (pending=%v)", pending)
	}
}

// A pending edit row repaints like an animating row until its colors arrive,
// and the projection keeps frames coming meanwhile.
func TestPendingDiffRowRepaintsWhenColorsArrive(t *testing.T) {
	msg := types.ChatMessage{Role: types.RoleTool, ToolName: "apply_patch", ToolArgs: "pending.go", ToolStatus: "success", ToolPreviewPhase: "done",
		Content: "Success. Updated the following files:\nM pending.go\n" + largeGoPatch("Pending", 140)}
	render := func() messageLines {
		return renderToolMessageLines(msg, theme.DefaultDark(), 100, 104, 104, 0, 40, msg.ToolPreviewPhase, DisclosureExpanded)
	}
	first := render()
	if !first.frameSensitive {
		t.Fatal("a row waiting for colors is not repainted when they arrive")
	}
	awaitDiffSyntax(t)
	second := render()
	if second.frameSensitive {
		t.Fatal("a colored row keeps repainting every activity phase")
	}
	if !syntaxColored(second.native) || !reflect.DeepEqual(first.lines, second.lines) {
		t.Fatal("colored repaint changed text or lacks colors")
	}
}
