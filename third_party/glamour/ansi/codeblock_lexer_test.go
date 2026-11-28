package ansi

import (
	"bytes"
	"testing"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/quick"
)

// The memoized lookup must pick what chroma picks, on the first call and
// from the table, including no lexer for an unknown or empty language.
func TestCodeLexerAgreesWithChroma(t *testing.T) {
	languages := []string{"", "go", "Go", "py", "python", "ts", "sh", "bash", "text", "diff", "json", "yaml", "Makefile", "Dockerfile", "rs", "c++", "unknown-language", "go.mod"}
	for round := range 2 {
		for _, language := range languages {
			want, got := lexers.Get(language), codeLexer(language)
			if want != got {
				t.Fatalf("round %d: language %q: chroma picks %v, memo %v", round, language, want, got)
			}
		}
	}
	sources := map[string]string{
		"":     "ok  \tcrude/tui/rendering\t3.307s\nPASS\n",
		"go":   "package main\n\nfunc main() {}\n",
		"html": "<!DOCTYPE html><html><body>x</body></html>\n",
		"none": "#!/bin/sh\necho hi\n",
	}
	for language, source := range sources {
		var want, got bytes.Buffer
		if err := quick.Highlight(&want, source, language, chromaFormatter, "monokai"); err != nil {
			t.Fatal(err)
		}
		if err := highlight(&got, source, language, chromaFormatter, "monokai"); err != nil {
			t.Fatal(err)
		}
		if want.String() != got.String() {
			t.Fatalf("language %q: output differs from quick.Highlight\nwant %q\ngot  %q", language, want.String(), got.String())
		}
	}
}
