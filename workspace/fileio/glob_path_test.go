package fileio

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitGlobPath(t *testing.T) {
	cases := []struct{ in, base, pattern string }{
		{"workspace/lsp*.go", "workspace", "lsp*.go"},
		{"tui/**/*_test.go", "tui", "**/*_test.go"},
		{"*.go", ".", "*.go"},
		{"/abs/dir/x?.go", "/abs/dir", "x?.go"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct{ in, base, pattern string }{`C:\repo\lsp*.go`, `C:\repo`, "lsp*.go"},
			struct{ in, base, pattern string }{`C:\*.go`, `C:\`, "*.go"},
		)
	}
	for _, c := range cases {
		base, pattern, ok := SplitGlobPath(c.in)
		if !ok || base != filepath.FromSlash(c.base) || pattern != c.pattern {
			t.Errorf("SplitGlobPath(%q) = %q, %q, %t; want %q, %q", c.in, base, pattern, ok, filepath.FromSlash(c.base), c.pattern)
		}
	}
	if _, _, ok := SplitGlobPath("app/[id].tsx"); ok {
		t.Error("brackets are literal file-name characters, not a glob")
	}
}

func TestGrepFilesAcceptsGlobPath(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ws/lsp_manager.go", "needle\n")
	write("ws/lsp_types.go", "needle\n")
	write("ws/session.go", "needle\n")
	write("ws/sub/lsp_deep.go", "needle\n")
	exec := NewLocalExec()
	matches, err := exec.GrepFiles("needle", filepath.Join(dir, "ws", "lsp*.go"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range matches {
		got = append(got, filepath.Base(m.Path))
	}
	if strings.Join(got, ",") != "lsp_manager.go,lsp_types.go" {
		t.Fatalf("single-segment glob matched %v", got)
	}
	matches, err = exec.GrepFiles("needle", filepath.Join(dir, "ws", "**", "lsp*.go"), true, false)
	if err != nil || len(matches) != 3 {
		t.Fatalf("recursive glob matched %d (%v)", len(matches), err)
	}
	if _, err := exec.GrepFiles("needle", filepath.Join(dir, "missing"), true, false); err == nil {
		t.Fatal("a missing search path must be an error, not an empty result")
	}
}
