package chatprojection_test

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/tools"
	"crude/tui/ui"
)

func TestBuildDiffRowsEmpty(t *testing.T) {
	// The unified-diff parser is lenient: empty/plain input yields a single
	// context row (matching the former ANSI renderer). What matters is that
	// every row is native, never PreferANSI.
	for _, in := range []string{"", "just plain text"} {
		rows := chatprojection.BuildDiffRows(in, 0, 80)
		if len(rows) == 0 {
			t.Fatalf("input %q yielded no rows", in)
		}
		for i, r := range rows {
			if r.PreferANSI() {
				t.Fatalf("input %q row %d PreferANSI", in, i)
			}
		}
	}
}

func TestBuildDiffRowsRoles(t *testing.T) {
	raw := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old()\n+func new()\n```"
	rows := chatprojection.BuildDiffRows(raw, 0, 80)
	if len(rows) < 3 {
		t.Fatalf("expected at least 3 rows, got %d", len(rows))
	}
	// Every row must be native (never PreferANSI).
	for i, r := range rows {
		if r.PreferANSI() {
			t.Fatalf("row %d PreferANSI — diff must be fully native", i)
		}
	}
	// Find the addition and deletion rows by their content.
	var addRow, delRow, ctxRow *chatprojection.PaintLine
	for i := range rows {
		plain := rows[i].PlainText()
		if strings.Contains(plain, "func new()") {
			r := rows[i]
			addRow = &r
		}
		if strings.Contains(plain, "func old()") {
			r := rows[i]
			delRow = &r
		}
		if strings.Contains(plain, "package main") {
			r := rows[i]
			ctxRow = &r
		}
	}
	if addRow == nil {
		t.Fatalf("missing addition row in %#v", rows)
	}
	if delRow == nil {
		t.Fatalf("missing deletion row in %#v", rows)
	}
	if ctxRow == nil {
		t.Fatalf("missing context row in %#v", rows)
	}
	if role, ok := spanRoleByText(*addRow, "+ "); !ok || role != ui.ColorDiffAddition {
		t.Fatalf("addition indicator role=%v want ColorDiffAddition", role)
	}
	if role, ok := contentRole(*addRow); !ok || role != ui.ColorDiffAddition {
		t.Fatalf("addition content role=%v want ColorDiffAddition", role)
	}
	if role, ok := contentRole(*delRow); !ok || role != ui.ColorDiffDeletion {
		t.Fatalf("deletion content role=%v want ColorDiffDeletion", role)
	}
	if role, ok := contentRole(*ctxRow); !ok || role != ui.ColorDiffContext {
		t.Fatalf("context content role=%v want ColorDiffContext", role)
	}
}

// spanRoleByText returns the role of the span whose text equals want.
func spanRoleByText(pl chatprojection.PaintLine, want string) (ui.SemanticColor, bool) {
	for _, s := range pl.Spans {
		if s.Text == want {
			return s.Role, true
		}
	}
	return 0, false
}

// contentRole returns the role of the last (content) span.
func contentRole(pl chatprojection.PaintLine) (ui.SemanticColor, bool) {
	if len(pl.Spans) == 0 {
		return 0, false
	}
	return pl.Spans[len(pl.Spans)-1].Role, true
}

func TestBuildDiffRowsTruncationIndicator(t *testing.T) {
	// Build a diff with many lines so maxLines triggers truncation.
	lines := []string{"```diff", "--- a/big.go", "+++ b/big.go", "@@ -1,20 +1,20 @@"}
	for i := 1; i <= 3; i++ {
		lines = append(lines, " // context line "+strings.Repeat("x", i))
	}
	lines = append(lines, "-    old code here")
	lines = append(lines, "+    new code here")
	for i := 5; i <= 12; i++ {
		lines = append(lines, " // middle context "+strings.Repeat("m", i))
	}
	lines = append(lines, "-    second old code")
	lines = append(lines, "+    second new code")
	for i := 14; i <= 20; i++ {
		lines = append(lines, " // trailing context "+strings.Repeat("y", i))
	}
	lines = append(lines, "```")
	raw := strings.Join(lines, "\n")

	rows := chatprojection.BuildDiffRows(raw, 10, 80)
	found := false
	for _, r := range rows {
		if strings.Contains(r.PlainText(), "more changes not shown") {
			found = true
			if r.PreferANSI() {
				t.Fatal("truncation row PreferANSI")
			}
		}
	}
	if !found {
		t.Fatalf("missing truncation indicator in %d rows", len(rows))
	}
	// Compact mode should insert an ellipsis row.
	foundEllipsis := false
	for _, r := range rows {
		if strings.Contains(r.PlainText(), "···") {
			foundEllipsis = true
		}
	}
	if !foundEllipsis {
		t.Fatalf("missing ellipsis marker in compact mode")
	}
}

// Painted-cell test: render a diff body into a cell buffer and assert
// addition lines paint the addition colour and deletion lines the deletion colour.
func TestBuildDiffRowsPaintedCells(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	addCell := tokens.Color(ui.ColorDiffAddition)
	delCell := tokens.Color(ui.ColorDiffDeletion)
	if addCell.IsDefault() || delCell.IsDefault() {
		t.Fatal("diff roles unresolved")
	}

	raw := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n-func old()\n+func new()\n```"
	rows := chatprojection.BuildDiffRows(raw, 0, 80)
	if len(rows) < 3 {
		t.Fatalf("expected >=3 rows, got %d", len(rows))
	}

	buf := surface.NewCellBuffer(80, len(rows))
	for y, pl := range rows {
		chatprojection.PaintLineAt(buf, 0, y, 80, pl, tokens, 0, 0, false)
	}

	// Find which row has the addition content "func new()" and which has
	// "func old()", then assert the content cells carry the right FG.
	addY, delY := -1, -1
	for y, pl := range rows {
		plain := pl.PlainText()
		if strings.Contains(plain, "func new()") {
			addY = y
		}
		if strings.Contains(plain, "func old()") {
			delY = y
		}
	}
	if addY < 0 || delY < 0 {
		t.Fatalf("addY=%d delY=%d rows=%#v", addY, delY, rows)
	}

	// Scan the addition row for a cell whose FG equals the addition colour.
	if !rowHasFG(buf, addY, addCell) {
		t.Fatalf("addition row %d has no cell with addition FG", addY)
	}
	if !rowHasFG(buf, delY, delCell) {
		t.Fatalf("deletion row %d has no cell with deletion FG", delY)
	}
	// Ensure no escape codes leak into cells.
	for y := range rows {
		for x := 0; x < 80; x++ {
			c := buf.Get(x, y)
			if strings.ContainsRune(string(c.Char), '\x1b') {
				t.Fatalf("escape at (%d,%d)", x, y)
			}
		}
	}
}

func rowHasFG(buf *surface.CellBuffer, y int, want cell.Color) bool {
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.FG.Equal(want) {
			return true
		}
	}
	return false
}

func TestDiffStructureParserExported(t *testing.T) {
	files := tools.ParseUnifiedDiffStruct("```diff\n--- a/f.go\n+++ b/f.go\n@@ -1,2 +1,2 @@\n-old\n+new\n```")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "f.go" {
		t.Fatalf("path=%q", files[0].Path)
	}
	rows := tools.BuildDiffRows(files[0])
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Kind != tools.DiffLineDeletion || rows[1].Kind != tools.DiffLineAddition {
		t.Fatalf("kinds=%v %v", rows[0].Kind, rows[1].Kind)
	}
}

func TestBandedGoDiffPreservesIndentationAndSyntaxColor(t *testing.T) {
	raw := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-        if old { return }\n+        if ready { return }\n```"
	rows := chatprojection.BuildBandedDiffRows(raw, 60, 80, true)
	foundIndented, foundSyntax := false, false
	for _, row := range rows {
		if strings.Contains(row.PlainText(), "        if ready") {
			foundIndented = true
			for _, span := range row.Spans {
				foundSyntax = foundSyntax || span.FGSet
			}
		}
	}
	if !foundIndented || !foundSyntax {
		t.Fatalf("indented syntax projection missing: indented=%v syntax=%v rows=%#v", foundIndented, foundSyntax, rows)
	}
}
