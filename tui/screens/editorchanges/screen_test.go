package editorchanges

import (
	"strings"
	"testing"

	"crude/tui/editor"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestScreenProjectsExactHunkAsReadOnlyDiff(t *testing.T) {
	screen := New(theme.DefaultDark(), editor.GitHunk{
		OldStart: 4, NewStart: 4,
		OldLines: []string{"old\n", "gone"}, NewLines: []string{"new\n"},
	}, "HEAD")
	t.Cleanup(screen.Close)
	text := screen.preview.Value()
	for _, want := range []string{"@@ HEAD 5,2 -> Buffer 5,1 @@", "-     5  old", "-     6  gone", "+     5  new", "\\ No newline at end of file"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview missing %q:\n%s", want, text)
		}
	}
	if !screen.preview.ReadOnly() {
		t.Fatal("change preview is editable")
	}
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "changes.next"})
	if !result.Handled || result.Cmd == nil {
		t.Fatal("next change action was not routed")
	}
}
