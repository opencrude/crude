package fileio

import (
	"os"
	"path/filepath"
	"testing"
)

// Models write PCRE-style `—`; RE2 rejected it with "invalid escape
// sequence: `\u`" and the search failed.
func TestGrepFilesAcceptsUnicodeEscapes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("// one — two\nplain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := &LocalExec{}
	for _, pattern := range []string{`—`, `\U00002014`, `\x{2014}`} {
		matches, err := ex.GrepFiles(pattern, dir, true, false)
		if err != nil {
			t.Fatalf("GrepFiles(%q): %v", pattern, err)
		}
		if len(matches) != 1 || matches[0].LineNum != 1 {
			t.Fatalf("GrepFiles(%q) = %+v, want line 1", pattern, matches)
		}
	}
	if got := normalizeUnicodeEscapes(`\\u2014`); got != `\\u2014` {
		t.Fatalf("escaped backslash rewritten: %q", got)
	}
}
