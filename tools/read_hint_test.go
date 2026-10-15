package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileHintNamesNearbyCandidates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lsp_manager.go", "lsp_types.go", "session.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(dir, "lsp.go")
	hint := readFileHint(fmt.Errorf("file not found: %s: %w", missing, fs.ErrNotExist), missing)
	if !strings.Contains(hint, "lsp_manager.go") || strings.Index(hint, "lsp_manager.go") > strings.Index(hint, "session.go") {
		t.Fatalf("closest candidates not listed first: %s", hint)
	}
	deep := filepath.Join(dir, "nope", "deeper", "x.go")
	if hint := readFileHint(fmt.Errorf("missing: %w", fs.ErrNotExist), deep); !strings.Contains(hint, "session.go") {
		t.Fatalf("missing directories should fall back to the nearest existing one: %s", hint)
	}
}
