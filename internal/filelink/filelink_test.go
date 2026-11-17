package filelink

import "testing"

func TestParseSourceReferences(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Reference
	}{
		{"src/main.go", Reference{Path: "src/main.go"}},
		{"src/main.go:12", Reference{Path: "src/main.go", Line: 12}},
		{"src/main.go:12:3", Reference{Path: "src/main.go", Line: 12, Column: 3}},
		{"main.go:12:3", Reference{Path: "main.go", Line: 12, Column: 3}},
		{"src/main.go#L12-L20", Reference{Path: "src/main.go", Line: 12}},
		{"src/main.go#L12C3", Reference{Path: "src/main.go", Line: 12, Column: 3}},
		{"src/a%20b.go#L2", Reference{Path: "src/a b.go", Line: 2}},
		{"../README.md#intro", Reference{Path: "../README.md"}},
		{`C:\repo\main.go:12:3`, Reference{Path: `C:\repo\main.go`, Line: 12, Column: 3}},
		{"C:/repo/main.go#L12", Reference{Path: "C:/repo/main.go", Line: 12}},
		{"/tmp/سلام.go:2", Reference{Path: "/tmp/سلام.go", Line: 2}},
		{"Makefile", Reference{Path: "Makefile"}},
	} {
		got, ok := Parse(tc.input)
		if !ok || got != tc.want {
			t.Errorf("%q = %+v, %v; want %+v", tc.input, got, ok, tc.want)
		}
	}
	for _, input := range []string{
		"", "#anchor", "https://example.test/a", "mailto:x@example.test", "javascript:alert(1)",
		"file:///etc/passwd", "command:open", "//server/share", `\\server\share`,
		"src/a.go?run=1", "src/a.go:0", "src/a.go:-1", "src/a%0ab.go", "src/\x1bgo",
		"src/a\u202ego", "src/*.go", "src/a%zz.go", " src/a.go ",
	} {
		if got, ok := Parse(input); ok {
			t.Errorf("unsafe/non-file %q accepted as %+v", input, got)
		}
	}
}

func TestCandidatesDoNotLinkCommandsVersionsOrGlobs(t *testing.T) {
	for _, value := range []string{"README.md", ".gitignore", "./go.mod", "src/main.go", "src/سلام.go:2", ".github/workflows/tui.yml", "src/a b.go", "src/Makefile", "./configure", "@types/node/index.d.ts", "app/(auth)/page.tsx", "src/a[b].go"} {
		if !Candidate(value) {
			t.Errorf("missed file %q", value)
		}
	}
	for _, value := range []string{"word", "v1.2.3", "a@b.com", "src/*.go", "a=b/c", "https://example.test/a", "and/or", "go test ./...", "go run ./src/main.go", "./app.py --check", "Ctrl+F/Ctrl+H"} {
		if Candidate(value) {
			t.Errorf("non-file candidate %q", value)
		}
	}
}

func BenchmarkParseSourceReference(b *testing.B) {
	for b.Loop() {
		Parse("tui/rendering/markdown.go:120:4")
	}
}
