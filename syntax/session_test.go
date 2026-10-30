package syntax

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func newTestSession(t testing.TB, language LanguageID) *Session {
	t.Helper()
	s, err := New(language)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func parseTest(t testing.TB, s *Session, revision uint64, source string) Result {
	t.Helper()
	result, err := s.Parse(context.Background(), revision, []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	checkRanges(t, source, result)
	return result
}

func checkRanges(t testing.TB, source string, result Result) {
	t.Helper()
	var end uint32
	for _, span := range result.Spans {
		if span.Start != end || span.End <= span.Start || span.End > uint32(len(source)) {
			t.Fatalf("incomplete/overlapping/out-of-range source partition: %+v after %d", span, end)
		}
		for _, offset := range []uint32{span.Start, span.End} {
			if int(offset) < len(source) && !utf8.RuneStart(source[offset]) {
				t.Fatalf("span splits UTF-8 at %d: %+v", offset, span)
			}
		}
		end = span.End
	}
	if int(end) != len(source) {
		t.Fatalf("source partition ends at %d, want %d", end, len(source))
	}
	for _, problem := range result.Problems {
		if problem.Start > problem.End || problem.End > uint32(len(source)) {
			t.Fatalf("problem outside source: %+v", problem)
		}
	}
}

func requireKind(t testing.TB, source string, result Result, needle string, kind Kind) {
	t.Helper()
	offset := strings.Index(source, needle)
	if offset < 0 {
		t.Fatalf("test has no %q", needle)
	}
	for _, span := range result.Spans {
		if span.Start <= uint32(offset) && span.End >= uint32(offset+len(needle)) && span.Kind == kind {
			return
		}
	}
	t.Fatalf("%q at %d not highlighted as %v: %+v", needle, offset, kind, result.Spans)
}

func TestBundledSyntaxCorpus(t *testing.T) {
	for _, tc := range []struct {
		language LanguageID
		source   string
		checks   map[string]Kind
	}{
		{Go, "package main\n// actual comment\nfunc answer() int { value := \"界 é\"; _ = value; return 42 }\n",
			map[string]Kind{"package": Keyword, "// actual comment": Comment, "answer": Function, "int": Type, "\"界 é\"": String, "return": Keyword, "42": Number}},
		{Python, "def answer():\n    \"\"\"documentation, not code\"\"\"\n    return 42\n# actual comment\n",
			map[string]Kind{"def": Keyword, "answer": Function, "documentation, not code": String, "return": Keyword, "42": Number, "# actual comment": Comment}},
		{JavaScript, "const answer = (value) => `界 ${value + 42}`; // actual comment\n",
			map[string]Kind{"const": Keyword, "answer": Function, "界 ": String, "+": Operator, "42": Number, "// actual comment": Comment}},
		{TypeScript, "const id = async <T>(v: T): Promise<T> => v; // actual comment\n",
			map[string]Kind{"const": Keyword, "id": Function, "async": Keyword, "Promise": Type, "// actual comment": Comment}},
		{TSX, "interface Props { title: string }\nconst view = (p: Props) => <div title={p.title}>Hello</div>;\n",
			map[string]Kind{"interface": Keyword, "Props": Type, "const": Keyword, "view": Function}},
	} {
		t.Run(string(tc.language), func(t *testing.T) {
			result := parseTest(t, newTestSession(t, tc.language), 1, tc.source)
			if result.HasError {
				t.Fatalf("valid source has syntax errors: %+v", result.Problems)
			}
			for needle, kind := range tc.checks {
				requireKind(t, tc.source, result, needle, kind)
			}
			if result.Identity.Language != tc.language || result.Identity.Grammar == "" || result.Identity.QueryDigest == [32]byte{} {
				t.Fatalf("missing syntax identity: %+v", result.Identity)
			}
		})
	}
}

func TestTypeScriptGenericAsyncArrowStructure(t *testing.T) {
	s := newTestSession(t, TypeScript)
	parseTest(t, s, 1, "const id = async <T>(v: T): Promise<T> => v;")
	declaration := s.tree.RootNode().NamedChild(0).NamedChild(0)
	value := declaration.ChildByFieldName("value")
	if value == nil || value.Kind() != "arrow_function" {
		t.Fatalf("valid generic async arrow has wrong AST: %s", s.tree.RootNode().ToSexp())
	}
}

func TestSyntaxLongContextEditsAndUndo(t *testing.T) {
	source := "\"\"\"\r\n" + strings.Repeat("# documentation 界\r\n", 20_000) + "\"\"\"\r\ndef after():\r\n    return 7\r\n"
	s := newTestSession(t, Python)
	first := parseTest(t, s, 1, source)
	requireKind(t, source, first, "# documentation 界", String)
	requireKind(t, source, first, "return", Keyword)
	// Delete an offscreen opener while leaving distant visible bytes identical.
	edited := source[3:]
	second := parseTest(t, s, 2, edited)
	requireKind(t, edited, second, "# documentation 界", Comment)
	restored := parseTest(t, s, 3, source)
	if !reflect.DeepEqual(restored.Spans, first.Spans) {
		t.Fatal("undo did not restore document-wide lexical context")
	}
}

func TestIncrementalMatchesFreshAcrossEdits(t *testing.T) {
	for language, initial := range map[LanguageID]string{
		Go:         "package main\r\nfunc answer() string { return \"界 é\" }\r\n",
		Python:     "def answer():\n    return f\"界 {value + 42}\"\n",
		JavaScript: "const answer = value => `界 ${value + 42}`;\n",
		TypeScript: "const id = async <T>(v: T): Promise<T> => v;\n",
		TSX:        "const view = (x: string) => <div>{x}</div>;\n",
	} {
		t.Run(string(language), func(t *testing.T) {
			s := newTestSession(t, language)
			source := initial
			parseTest(t, s, 1, source)
			rng := rand.New(rand.NewPCG(17, 23))
			insertions := []string{"", "\r\n", "界", "🙂", "é", "\"\"\"", "/*", "*/", "${", "return", " "}
			for revision := uint64(2); revision < 75; revision++ {
				runes := []rune(source)
				start := rng.IntN(len(runes) + 1)
				end := start + rng.IntN(len(runes)-start+1)
				source = string(runes[:start]) + insertions[rng.IntN(len(insertions))] + string(runes[end:])
				got := parseTest(t, s, revision, source)
				fresh := newTestSession(t, language)
				want := parseTest(t, fresh, 1, source)
				// The caller closes unused native state immediately, not just
				// when the outer test eventually finishes.
				fresh.Close()
				if !reflect.DeepEqual(got.Spans, want.Spans) || !reflect.DeepEqual(got.Problems, want.Problems) || got.HasError != want.HasError {
					t.Fatalf("revision %d differs from fresh analysis of %q", revision, source)
				}
			}
		})
	}
}

func TestSyntaxFailureDoesNotReplaceAcceptedState(t *testing.T) {
	s := newTestSession(t, Python)
	source := "# original\nanswer = 42\n"
	parseTest(t, s, 1, source)
	tree := s.tree
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		revision uint64
		source   []byte
		want     error
	}{
		{"cancel", ctx, 2, []byte("# replacement"), context.Canceled},
		{"stale", context.Background(), 1, []byte("# replacement"), ErrRevision},
		{"zero", context.Background(), 0, nil, ErrRevision},
		{"invalid-utf8", context.Background(), 2, []byte{0xff}, ErrInvalidText},
		{"oversized", context.Background(), 2, make([]byte, MaxDocumentBytes+1), ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Parse(tc.ctx, tc.revision, tc.source); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if s.tree != tree || s.revision != 1 || string(s.text) != source {
				t.Fatal("failed analysis replaced the accepted base")
			}
		})
	}
	parseTest(t, s, 2, "# recovered\nanswer = 43\n")
	s.Close()
	s.Close()
	if _, err := s.Parse(context.Background(), 3, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("parse after close: %v", err)
	}
}

func TestSyntaxCancellationDuringWorkKeepsBase(t *testing.T) {
	s := newTestSession(t, JavaScript)
	parseTest(t, s, 1, "const before = 1;\n")
	tree := s.tree
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := s.Parse(ctx, 2, []byte(strings.Repeat("const value = [1,2,3,4,5];\n", 200_000)))
	if !errors.Is(err, context.DeadlineExceeded) || s.tree != tree || s.revision != 1 {
		t.Fatalf("cancellation published or corrupted native base: %v revision=%d", err, s.revision)
	}
	got := parseTest(t, s, 2, "const after = 42;\n")
	requireKind(t, "const after = 42;\n", got, "42", Number)
}

func TestSyntaxInputAndResultOwnership(t *testing.T) {
	s := newTestSession(t, Go)
	source := []byte("package main\nvar answer = 42\n")
	result, err := s.Parse(context.Background(), 1, source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'X'
	result.Spans[0].Kind = Comment
	second := parseTest(t, s, 2, "package main\nvar answer = 43\n")
	requireKind(t, "package main\nvar answer = 43\n", second, "package", Keyword)
}

func TestSyntaxUnsupportedLanguagesDoNotPretendCoverage(t *testing.T) {
	for _, path := range []string{"sample.cr", "build.zig", "readme.md", "script"} {
		if _, ok := LanguageForPath(path); ok {
			t.Fatalf("ungated file %q declared supported", path)
		}
	}
	if _, err := New("crystal"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported grammar: %v", err)
	}
	for path, want := range map[string]LanguageID{"MAIN.GO": Go, "a.pyi": Python, "a.jsx": JavaScript, "a.mts": TypeScript, "a.tsx": TSX} {
		if got, ok := LanguageForPath(path); !ok || got != want {
			t.Fatalf("%q: %q %v, want %q", path, got, ok, want)
		}
	}
}

func TestSyntaxRealProjectSources(t *testing.T) {
	for _, path := range []string{"../tui/editor/editor.go", "../site/src/App.tsx"} {
		t.Run(path, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			language, ok := LanguageForPath(path)
			if !ok {
				t.Fatal("fixture has no grammar")
			}
			result := parseTest(t, newTestSession(t, language), 1, string(source))
			if result.HasError {
				t.Fatalf("real project source has parse errors: %+v", result.Problems)
			}
		})
	}
}

func BenchmarkNativeSyntaxPreparation(b *testing.B) {
	for _, lines := range []int{200, 20_000} {
		source := []byte("\"\"\"\n" + strings.Repeat("documentation\n", lines) + "\"\"\"\ndef after():\n    return 7\n")
		b.Run(fmt.Sprintf("Python_%d", lines), func(b *testing.B) {
			s := newTestSession(b, Python)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				// A fresh source tree, but precompiled immutable grammar/query.
				s.tree.Close()
				s.tree = nil
				if _, err := s.Parse(context.Background(), uint64(i+1), source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNativeSyntaxIncremental(b *testing.B) {
	a := "\"\"\"\n" + strings.Repeat("documentation\n", 20_000) + "\"\"\"\ndef after():\n    return 7\n"
	x := []byte(strings.Replace(a, "documentation", "Documentation", 1))
	y := []byte(a)
	s := newTestSession(b, Python)
	parseTest(b, s, 1, a)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		source := x
		if i%2 != 0 {
			source = y
		}
		if _, err := s.Parse(context.Background(), uint64(i+2), source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNativeSyntaxColdSession(b *testing.B) {
	source := []byte("const id = async <T>(v: T): Promise<T> => v;\n")
	b.ReportAllocs()
	for b.Loop() {
		s, err := New(TypeScript)
		if err != nil {
			b.Fatal(err)
		}
		_, err = s.Parse(context.Background(), 1, source)
		s.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNativeSyntaxRealSources(b *testing.B) {
	for _, path := range []string{"../tui/editor/editor.go", "../site/src/App.tsx"} {
		source, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		language, _ := LanguageForPath(path)
		b.Run(string(language), func(b *testing.B) {
			s := newTestSession(b, language)
			b.SetBytes(int64(len(source)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				s.tree.Close()
				s.tree = nil
				if _, err := s.Parse(context.Background(), uint64(i+1), source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
