package tools

import (
	"path/filepath"
	"testing"
)

func TestDisplayPathIsWorkspaceRelative(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	cases := map[string]string{
		filepath.Join(root, "workspace", "lsp.go"):    "workspace/lsp.go",
		filepath.ToSlash(filepath.Join(root, "a.go")): "a.go",
		root: ".",
		filepath.Join(filepath.Dir(root), "other", "x.go"): filepath.Join(filepath.Dir(root), "other", "x.go"),
	}
	for in, want := range cases {
		if got := displayPath(root, in); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := displayPath("", "/abs/x.go"); got != "/abs/x.go" {
		t.Errorf("no root must leave the path unchanged, got %q", got)
	}
}
