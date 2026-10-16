package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/prompt"
	"crude/types"
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

// Every tool hint is separated by the one contract presentation strips, so
// the model keeps the hint and no transcript row ever shows it.
func TestToolHintsUseThePresentationSeparator(t *testing.T) {
	if prompt.ToolHintSeparator != types.ToolHintSeparator {
		t.Fatalf("prompt separator %q differs from presentation separator %q", prompt.ToolHintSeparator, types.ToolHintSeparator)
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.go")
	failure := readFileHint(fmt.Errorf("file not found: %s: %w", missing, fs.ErrNotExist), missing)
	if !strings.Contains(failure, types.ToolHintSeparator) {
		t.Fatalf("read failure hint is not separated: %q", failure)
	}
	if shown := types.StripToolHint(failure); strings.Contains(shown, "file not found;") || strings.Contains(shown, "Hint: ") || !strings.Contains(shown, "absent.go") {
		t.Fatalf("stripped failure = %q", shown)
	}
	if edit := noChangeEditResult("a", "a").Content; types.StripToolHint(edit) != "Error: no changes to make: old_string and new_string are exactly the same" {
		t.Fatalf("no-change edit hint not separated: %q", edit)
	}
}
