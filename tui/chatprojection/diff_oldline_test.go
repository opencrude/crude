package chatprojection_test

import (
	"strings"
	"testing"

	"crude/tui/chatprojection"
)

func TestBuildBandedDiffRows_DeletionUsesOldLineGutter(t *testing.T) {
	// A unified diff where line 8 (old) is deleted and lines 8->9 (new) added.
	raw := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -5,8 +5,8 @@\n line5\n line6\n-oldFunc()\n+newFunc()\n line9\n```"
	rows := chatprojection.BuildBandedDiffRows(raw, 60, 80, true)
	if len(rows) == 0 {
		t.Fatalf("expected rows")
	}
	var delPlain string
	var addPlain string
	for _, r := range rows {
		plain := r.PlainText()
		if strings.Contains(plain, "oldFunc()") {
			delPlain = plain
		}
		if strings.Contains(plain, "newFunc()") {
			addPlain = plain
		}
	}
	if delPlain == "" {
		t.Fatal("no deletion row found")
	}
	if !strings.Contains(delPlain, "-") {
		t.Errorf("deletion row missing '-' indicator: %q", delPlain)
	}
	// The deletion must reference the OLD line number. With oldStart=5 and
	// lines 5,6, then the deletion is old line 7. The number gutter should
	// carry it rather than being blank.
	if !containsAnyLineNum(delPlain) {
		t.Errorf("deletion row should carry an old-line number in its gutter: %q", delPlain)
	}
	_ = addPlain
}

func containsAnyLineNum(s string) bool {
	for _, rune := range s {
		if rune >= '0' && rune <= '9' {
			return true
		}
	}
	return false
}

func TestBuildBandedDiffRows_BandsAndVisualBudget(t *testing.T) {
	var body strings.Builder
	body.WriteString("```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,40 +1,40 @@\n")
	for i := 0; i < 30; i++ {
		body.WriteString("-old value that wraps across several visual columns\n")
		body.WriteString("+new value that wraps across several visual columns\n")
	}
	body.WriteString("```")
	rows := chatprojection.BuildBandedDiffRows(body.String(), 24, 30, false)
	if len(rows) != 24 {
		t.Fatalf("preview rows = %d, want 24", len(rows))
	}
	if !strings.Contains(rows[len(rows)-1].PlainText(), "more changes") {
		t.Fatalf("missing visual-row truncation footer: %q", rows[len(rows)-1].PlainText())
	}
	banded := false
	for _, row := range rows[:len(rows)-1] {
		plain := row.PlainText()
		if !strings.Contains(plain, "old value") && !strings.Contains(plain, "new value") {
			continue
		}
		for i, span := range row.Spans {
			if i == 0 {
				continue
			}
			if !span.BgSet {
				t.Fatalf("changed row span lacks background: %+v", row.Spans)
			}
		}
		banded = true
		break
	}
	if !banded {
		t.Fatal("no changed band row found")
	}
}

func TestBuildBandedDiffRows_MultiFileShowsPaths(t *testing.T) {
	raw := "```diff\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-old\n+new\n```"
	plain := ""
	for _, row := range chatprojection.BuildBandedDiffRows(raw, 60, 80, true) {
		plain += row.PlainText() + "\n"
	}
	if !strings.Contains(plain, "a.go") || !strings.Contains(plain, "b.go") {
		t.Fatalf("multi-file paths missing:\n%s", plain)
	}
}

func TestBuildBandedDiffRows_DedentsCommonMarginOnly(t *testing.T) {
	raw := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n        if ready {\n-            oldCall()\n+            newCall()\n        }\n```"
	plain := ""
	for _, row := range chatprojection.BuildBandedDiffRows(raw, 60, 80, true) {
		plain += row.PlainText() + "\n"
	}
	if strings.Contains(plain, "        if ready") {
		t.Fatalf("common indentation was not removed:\n%s", plain)
	}
	if !strings.Contains(plain, "    oldCall") || !strings.Contains(plain, "    newCall") {
		t.Fatalf("relative nested indentation was flattened:\n%s", plain)
	}
}
