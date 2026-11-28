package rendering

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
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

// MatchLexer replays chroma's walk over only the patterns that can match a
// name. It must agree with lexers.Match for a name derived from every
// registered pattern, with and without backup suffixes, and for names that
// match nothing. lexers.Match takes about 2 ms a name, so the comparison runs
// on every core.
func TestMatchLexerAgreesWithChromaForEveryPattern(t *testing.T) {
	names := []string{"workspace", "tui", "LICENSE", "README", "AGENTS", "x", ".git", "~", ".in", "a.unknownext.orig"}
	for _, lexer := range lexers.GlobalLexerRegistry.Lexers {
		config := lexer.Config()
		for _, glob := range append(append([]string(nil), config.Filenames...), config.AliasFilenames...) {
			name := sampleName(glob)
			names = append(names, name, name+"~", name+".in", "dir"+name)
		}
	}
	mismatches := make([]string, len(names))
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	for worker := range workers {
		wg.Go(func() {
			for i := worker; i < len(names); i += workers {
				got, want := MatchLexer("/repo/dir/"+names[i]), lexers.Match(names[i])
				if got != want {
					mismatches[i] = fmt.Sprintf("%q: index %v, chroma %v", names[i], got, want)
				}
			}
		})
	}
	wg.Wait()
	for _, mismatch := range mismatches {
		if mismatch != "" {
			t.Error(mismatch)
		}
	}
}

// sampleName turns a pattern into a name it matches: "*" and "?" become
// letters and a character class its first member.
func sampleName(glob string) string {
	var name strings.Builder
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			name.WriteString("x")
		case '?':
			name.WriteString("q")
		case '[':
			end := strings.IndexByte(glob[i:], ']')
			name.WriteByte(glob[i+1])
			i += end
		default:
			name.WriteByte(glob[i])
		}
	}
	return name.String()
}

// A cold transcript names many files without an extension (folders,
// "Makefile"). Each used to glob every registered pattern on the UI
// goroutine.
func BenchmarkMatchLexerDistinctNamesWithoutExtension(b *testing.B) {
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		MatchLexer(fmt.Sprintf("/repo/folder%d", i))
	}
}

func BenchmarkMatchLexerDistinctNames(b *testing.B) {
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		MatchLexer("/repo/file" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".go")
	}
}
