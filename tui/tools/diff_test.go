package tools

import (
	"strconv"
	"strings"
	"testing"

	"crude/tui/theme"
)

func TestParseUnifiedDiff(t *testing.T) {
	raw := "Replaced 1 occurrence in src/main.go\n\n```diff\n--- a/src/main.go\n+++ b/src/main.go\n@@ -8,7 +8,8 @@\n func process() {\n-    return errors.New(\"not found\")\n+    result, err := find()\n+    if err != nil {\n+        return err\n+    }\n     return nil\n }\n```"

	files := parseUnifiedDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "src/main.go" {
		t.Errorf("expected path 'src/main.go', got '%s'", f.Path)
	}
	if f.DelCount != 1 {
		t.Errorf("expected 1 deletion, got %d", f.DelCount)
	}
	if f.AddCount != 4 {
		t.Errorf("expected 4 additions, got %d", f.AddCount)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.StartLine != 8 {
		t.Errorf("expected start line 8, got %d", h.StartLine)
	}
	if len(h.Lines) != 8 {
		t.Fatalf("expected 8 lines, got %d", len(h.Lines))
	}

	// Verify line types
	// Line 0: context " func process() {"
	if h.Lines[0].Type != lineContext {
		t.Errorf("line 0: expected context, got %d", h.Lines[0].Type)
	}
	// Line 1: delete
	if h.Lines[1].Type != lineDelete {
		t.Errorf("line 1: expected delete, got %d", h.Lines[1].Type)
	}
	// Lines 2-4: add
	for i := 2; i <= 4; i++ {
		if h.Lines[i].Type != lineAdd {
			t.Errorf("line %d: expected add, got %d", i, h.Lines[i].Type)
		}
	}

	// Verify line numbers
	if h.Lines[0].NewLineNum != 8 {
		t.Errorf("line 0: expected lineNum 8, got %d", h.Lines[0].NewLineNum)
	}
	if h.Lines[1].NewLineNum != 0 {
		t.Errorf("line 1 (delete): expected lineNum 0, got %d", h.Lines[1].NewLineNum)
	}
	if h.Lines[2].NewLineNum != 9 {
		t.Errorf("line 2 (add): expected lineNum 9, got %d", h.Lines[2].NewLineNum)
	}
}

func TestRenderDiffNoRawHeaders(t *testing.T) {
	th := theme.DefaultDark()
	raw := "Written 10 lines to test.go\n\n```diff\n--- a/test.go\n+++ b/test.go\n@@ -1,3 +1,3 @@\n package main\n-func old()\n+func new()\n```"

	result := RenderDiff(raw, 0, 80, th)

	// Must NOT contain raw diff headers
	if strings.Contains(result, "+++") {
		t.Error("RenderDiff output contains '+++' header")
	}
	if strings.Contains(result, "---") {
		t.Error("RenderDiff output contains '---' header")
	}
	if strings.Contains(result, "@@") {
		t.Error("RenderDiff output contains '@@' hunk header")
	}
	if strings.Contains(result, "```diff") {
		t.Error("RenderDiff output contains markdown fence")
	}

	// Must contain the file path
	if !strings.Contains(result, "test.go") {
		t.Error("RenderDiff output missing file path")
	}

	// Must contain change stats
	if !strings.Contains(result, "−1") {
		t.Error("RenderDiff output missing deletion count")
	}
	if !strings.Contains(result, "+1") {
		t.Error("RenderDiff output missing addition count")
	}

	// Must contain the actual change content
	if !strings.Contains(result, "func old()") {
		t.Error("RenderDiff output missing deleted line content")
	}
	if !strings.Contains(result, "func new()") {
		t.Error("RenderDiff output missing added line content")
	}

	// Must contain the accent marker
	if !strings.Contains(result, "▌") {
		t.Error("RenderDiff output missing accent marker")
	}

	// Must contain line number separator
	if !strings.Contains(result, "│") {
		t.Error("RenderDiff output missing gutter separator")
	}

	// Must contain the delete indicator
	if !strings.Contains(result, "─") {
		t.Error("RenderDiff output missing delete indicator")
	}
}

func TestRenderDiffWithContext(t *testing.T) {
	th := theme.DefaultDark()
	raw := "```diff\n--- a/app.go\n+++ b/app.go\n@@ -5,5 +5,5 @@\n import \"fmt\"\n \n func main() {\n-    fmt.Println(\"old\")\n+    fmt.Println(\"new\")\n }\n```"

	result := RenderDiff(raw, 0, 80, th)

	// Context lines should be present
	if !strings.Contains(result, "import") {
		t.Error("RenderDiff output missing context line 'import'")
	}
	if !strings.Contains(result, "func main()") {
		t.Error("RenderDiff output missing context line 'func main()'")
	}
}

func TestRenderDiffCompactMode(t *testing.T) {
	th := theme.DefaultDark()

	// Create a diff with two change groups separated by context so compact mode
	// inserts an ellipsis between them.
	lines := []string{"```diff", "--- a/big.go", "+++ b/big.go", "@@ -1,20 +1,20 @@"}
	for i := 1; i <= 3; i++ {
		lines = append(lines, " // context line "+strings.Repeat("x", i))
	}
	lines = append(lines, "-    old code here")
	lines = append(lines, "+    new code here")
	for i := 5; i <= 14; i++ {
		lines = append(lines, " // middle context "+strings.Repeat("m", i))
	}
	lines = append(lines, "-    second old code")
	lines = append(lines, "+    second new code")
	for i := 14; i <= 20; i++ {
		lines = append(lines, " // trailing context "+strings.Repeat("y", i))
	}
	lines = append(lines, "```")
	raw := strings.Join(lines, "\n")

	// With maxLines=8, compact mode should trim context between the two changes
	result := RenderDiff(raw, 8, 80, th)

	// Should contain ellipsis marker between the two change groups
	if !strings.Contains(result, "···") {
		t.Error("RenderDiff compact mode missing ellipsis marker between change groups")
	}

	// Should NOT start with an ellipsis (leading ellipsis is redundant)
	resultLines := strings.Split(result, "\n")
	for _, line := range resultLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if strings.Contains(trimmed, "···") {
				t.Error("RenderDiff compact mode should not have leading ellipsis as first visible line")
			}
			break
		}
	}
}

func TestParseGitDiffHeaders(t *testing.T) {
	// Git diff output with diff --git and index headers
	raw := "diff --git a/agent/agent.go b/agent/agent.go\n" +
		"index 4d2d242..8a3e2dd 100644\n" +
		"--- a/agent/agent.go\n" +
		"+++ b/agent/agent.go\n" +
		"@@ -693,6 +693,9 @@ func getStr(params map[string]any, key string) string {\n" +
		" \tif s, ok := v.(string); ok {\n" +
		" \t\treturn s\n" +
		" \t}\n" +
		"+\tif v == nil {\n" +
		"+\t\treturn \"\"\n" +
		"+\t}\n" +
		" \treturn fmt.Sprintf(\"%v\", v)\n" +
		" }\n"

	files := parseUnifiedDiff(raw)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "agent/agent.go" {
		t.Errorf("expected path 'agent/agent.go', got '%s'", f.Path)
	}
	if f.DelCount != 0 {
		t.Errorf("expected 0 deletions, got %d", f.DelCount)
	}
	if f.AddCount != 3 {
		t.Errorf("expected 3 additions, got %d", f.AddCount)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.StartLine != 693 {
		t.Errorf("expected start line 693, got %d", h.StartLine)
	}

	// Verify the diff --git and index lines are NOT present as content
	for _, line := range h.Lines {
		if strings.Contains(line.Content, "diff --git") {
			t.Error("git diff --git header leaked into content lines")
		}
		if strings.Contains(line.Content, "index ") {
			t.Error("git index header leaked into content lines")
		}
	}
}

func TestIsDiffContent(t *testing.T) {
	tests := []struct {
		input  string
		expect bool
	}{
		{"```diff\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+new\n```", true},
		{"Just some regular text", false},
		{"--- a/file\n+++ b/file\n@@ stuff", true},
		{"Error: something went wrong", false},
	}
	for _, tt := range tests {
		got := IsDiffContent(tt.input)
		if got != tt.expect {
			t.Errorf("IsDiffContent(%q) = %v, want %v", tt.input, got, tt.expect)
		}
	}
}

// gapDiff builds a compact-mode diff whose two change groups are separated by
// exactly `gap` hidden context lines. With CompactContextLines of context kept
// around each change, the change rows sit at 2/3 and 6+gap/7+gap, which leaves
// rows 6…5+gap — exactly `gap` lines — uncovered between them.
func gapDiff(gap int) string {
	lines := []string{
		"--- a/f.go", "+++ b/f.go", "@@ -1,40 +1,40 @@",
		" ctx-head-1", " ctx-head-2",
		"-old-a", "+new-a",
	}
	for i := 1; i <= gap+4; i++ {
		lines = append(lines, " ctx-mid-"+strconv.Itoa(i))
	}
	lines = append(lines, "-old-b", "+new-b", " ctx-tail-1", " ctx-tail-2")
	return strings.Join(lines, "\n")
}

func trimGapDiff(t *testing.T, gap int) []DiffRow {
	t.Helper()
	files := ParseUnifiedDiffStruct(gapDiff(gap))
	if len(files) != 1 {
		t.Fatalf("gap %d: parsed %d files", gap, len(files))
	}
	return TrimDiffContext(BuildDiffRows(files[0]), CompactContextLines)
}

// The "···" marker must earn its row: hiding one or two untouched lines loses
// context for nothing, so only gaps wider than four lines collapse. ctx-mid-3 is
// the first line of the uncovered run at every gap size.
func TestTrimDiffContextKeepsGapsOfFourOrFewer(t *testing.T) {
	for gap := 1; gap <= 8; gap++ {
		rows := trimGapDiff(t, gap)
		ellipses := 0
		seen := map[string]bool{}
		for _, r := range rows {
			if r.Ellipsis {
				ellipses++
				continue
			}
			seen[r.Content] = true
		}
		if gap <= 4 {
			if ellipses != 0 {
				t.Fatalf("gap %d: elided with %d marker row(s)", gap, ellipses)
			}
			if !seen["ctx-mid-3"] {
				t.Fatalf("gap %d: hidden context was dropped instead of kept: %+v", gap, rows)
			}
		} else {
			if ellipses != 1 {
				t.Fatalf("gap %d: got %d marker row(s), want exactly 1", gap, ellipses)
			}
			if seen["ctx-mid-3"] {
				t.Fatalf("gap %d: collapsed gap leaked its context: %+v", gap, rows)
			}
		}
	}
}

// Both end states keep the surrounding change groups and their context.
func TestTrimDiffContextAlwaysKeepsChangesAndAdjacentContext(t *testing.T) {
	for _, gap := range []int{2, 6} {
		rows := trimGapDiff(t, gap)
		seen := map[string]bool{}
		for _, r := range rows {
			seen[r.Content] = true
		}
		for _, want := range []string{"old-a", "new-a", "old-b", "new-b", "ctx-head-1", "ctx-tail-2"} {
			if !seen[want] {
				t.Fatalf("gap %d: %q missing from trimmed rows: %+v", gap, want, rows)
			}
		}
	}
}
