package editor

import (
	"strings"
	"testing"
)

// The buffer holds Git's CRLF checkout while HEAD is LF in repository form.
// Neither the full projection nor the incremental remap may mark committed
// lines, and a real edit marks only its own line.
func TestGitGutterIgnoresCheckoutLineEndings(t *testing.T) {
	head := "# Title\n\none\ntwo\nthree\n"
	ed := contentIdentityEditor(strings.ReplaceAll(head, "\n", "\r\n"))
	blame := []GitBlameLine{}
	for line := 1; line <= 5; line++ {
		blame = append(blame, GitBlameLine{Line: line, Hash: "abc", ShortHash: "abc", Author: "Crude Test"})
	}
	// A stale source revision forces the editor's own full re-diff.
	ed.SetGitAnnotation(ed.Revision()+1, head, nil, blame, false)
	for line := 0; line < 5; line++ {
		if kind := ed.gitKindAt(line); kind != GitLineUnchanged {
			t.Fatalf("committed line %d marked %v", line, kind)
		}
		if got := gutterRailCharAt(t, ed, line); got != ' ' {
			t.Fatalf("committed line %d painted %q", line, got)
		}
	}
	ed.SetCursorPosition(3, 0)
	if got := ed.CursorBlameSummary(); got == "Not committed yet" || !strings.Contains(got, "Crude Test") {
		t.Fatalf("committed line blame = %q", got)
	}
	ed.InsertRune('x')
	for line := 0; line < 5; line++ {
		want := GitLineUnchanged
		if line == 3 {
			want = GitLineModified
		}
		if kind := ed.gitKindAt(line); kind != want {
			t.Fatalf("after edit line %d = %v, want %v", line, kind, want)
		}
	}
	if hunks := ed.GitHunks(); len(hunks) != 1 || strings.Join(hunks[0].NewLines, "") != "xtwo\r\n" || strings.Join(hunks[0].OldLines, "") != "two\n" {
		t.Fatalf("edit hunk = %#v", hunks)
	}
	ed.deleteBackward()
	if hunks := ed.GitHunks(); len(hunks) != 0 {
		t.Fatalf("inverse edit left hunks %#v", hunks)
	}
}
