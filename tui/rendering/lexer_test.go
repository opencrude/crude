package rendering

import (
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
)

// MatchLexer must agree with chroma's exhaustive matcher, including names
// decided by full-name patterns and backup suffixes.
func TestMatchLexerAgreesWithChroma(t *testing.T) {
	names := []string{"main.go", "other.go", "go.mod", "go.sum", "Makefile", "x.mk", "Dockerfile", "Dockerfile.dev",
		"index.d.ts", "app.ts", "a.tsx", "CMakeLists.txt", "notes.txt", "README.md", "config.yaml", "x.yml",
		"main.go.orig", "file.c", "file.h", "file.hpp", "script.sh", ".bashrc", "PKGBUILD", "noext", "a.unknownext",
		"Cargo.toml", "x.toml", "page.html", "style.css", "q.sql", "q.SQL", "main.py", "setup.cfg", "file.bak"}
	for _, name := range names {
		for range 2 { // second pass exercises the caches
			got, want := MatchLexer("/repo/dir/"+name), lexers.Match(name)
			if (got == nil) != (want == nil) || got != nil && got.Config().Name != want.Config().Name {
				t.Fatalf("%s: got %v want %v", name, got, want)
			}
		}
	}
}

func BenchmarkMatchLexerDistinctNames(b *testing.B) {
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		MatchLexer("/repo/file" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".go")
	}
}
