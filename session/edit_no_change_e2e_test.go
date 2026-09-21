package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A patch that resolves to the bytes a file already holds must fail. Crude's
// patch matching is exact → whitespace-normalised → re-indented, so an edit can
// end up changing nothing; reporting success would tell the model the edit
// happened and hand the chat projection a "patch" body that is only the result's
// summary sentence. Claude Code's Edit tool ships the same contract ("No changes
// to make: old_string and new_string are exactly the same.").
func TestEditFileNoChangeIsFailure(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		oldString string
		newString string
		reason    string
	}{
		{
			name:      "identical arguments",
			content:   "alpha\nbeta\ngamma\n",
			oldString: "beta",
			newString: "beta",
			reason:    "old_string and new_string are exactly the same",
		},
		{
			name:      "whitespace-normalised match re-indents the replacement back",
			content:   "func f() {\n\t\tfoo()\n}\n",
			oldString: "  foo()",
			newString: "foo()",
			reason:    "byte-identical",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, "target.go")
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			sess, err := NewWithSessionDir("edit-no-change", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			sess.SetWorkDir(project)

			result, err := sess.ExecuteToolForTest(context.Background(), "edit_file", map[string]any{
				"path": "target.go", "old_string": c.oldString, "new_string": c.newString,
			})
			// The session pipeline surfaces error-prefixed tool content as an
			// error as well; both carry the same reason line.
			outcome := result.Content
			if err != nil {
				outcome = err.Error()
			}
			if !strings.HasPrefix(outcome, "Error: ") {
				t.Fatalf("no-change patch reported success: %q", outcome)
			}
			if !strings.Contains(outcome, "no changes to make:") || !strings.Contains(outcome, c.reason) {
				t.Fatalf("reason does not name the no-change case (%q): %q", c.reason, outcome)
			}
			if strings.Contains(outcome, "Replaced") {
				t.Fatalf("no-change result still claims a replacement: %q", outcome)
			}
			if result.Added != 0 || result.Removed != 0 {
				t.Fatalf("no-change result counted lines: +%d -%d", result.Added, result.Removed)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != c.content {
				t.Fatalf("file was not left byte-identical: err=%v content=%q", readErr, got)
			}
		})
	}
}

// The control case: a patch that does change bytes still reports the
// replacement and carries a fenced diff for the patch card.
func TestEditFileRealChangeStillReportsReplacement(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "target.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := NewWithSessionDir("edit-change", testConfig(), t.TempDir(), filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetWorkDir(project)

	result, err := sess.ExecuteToolForTest(context.Background(), "edit_file", map[string]any{
		"path": "target.go", "old_string": "beta", "new_string": "delta",
	})
	if err != nil {
		t.Fatalf("tool call error: %v", err)
	}
	if !strings.HasPrefix(result.Content, "Replaced 1 occurrence in ") {
		t.Fatalf("successful patch lost its summary: %q", result.Content)
	}
	if !strings.Contains(result.Content, "```diff") {
		t.Fatalf("successful patch has no fenced diff: %q", result.Content)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "alpha\ndelta\ngamma\n" {
		t.Fatalf("patch did not apply: err=%v content=%q", readErr, got)
	}
}
