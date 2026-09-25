package viewmodel

import (
	"strings"
	"testing"
)

func TestNativePatchLiveAndReopenedProjection(t *testing.T) {
	for _, c := range []projectionCase{
		{name: "multi-file patch", tool: "apply_patch", rawArgs: "a.go, b.go", pathLabel: "a.go, b.go", added: 2, removed: 1, content: "Success. Updated the following files:\nM a.go\nA b.go\n\n```diff\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n--- a/b.go\n+++ b/b.go\n@@ -0,0 +1 @@\n+second\n```\n"},
		{name: "partial failure", tool: "apply_patch", rawArgs: "a.go, missing", isError: true, added: 1, content: "Error: missing file\nEarlier operations committed and remain undoable:\nA a.go\n\n```diff\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+kept\n```\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, expanded := range []bool{false, true} {
				live := renderLive(t, "patch-1", c, expanded)
				hydrated := renderHydrated(t, "patch-1", c, expanded)
				if len(live) != 1 || len(hydrated) != 1 {
					t.Fatal("patch hidden in activity")
				}
				assertSameRows(t, "patch", live[0], hydrated[0])
			}
		})
	}
}

func TestNativePatchNoOpRemainsVisibleWithoutFakeDiff(t *testing.T) {
	c := projectionCase{tool: "apply_patch", rawArgs: "a.go", content: "Success. Updated the following files:\nM a.go\n"}
	rows := renderLive(t, "patch-noop", c, true)
	if len(rows) != 1 {
		t.Fatal("no-op disappeared")
	}
	// It must retain its actual upstream success rather than edit_file's error.
	for _, line := range rows[0].Lines {
		if strings.Contains(line, "failed") {
			t.Fatal(line)
		}
	}
}
