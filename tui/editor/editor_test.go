package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/stretchr/testify/assert"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/tui/theme"
)

// chroma style tests

func TestBuildChromaStyle_KeyTokensMapped(t *testing.T) {
	th := theme.DefaultDark()
	s := buildChromaStyle(th)
	entry := s.Get(chroma.Keyword)
	assert.True(t, entry.Colour.IsSet(), "Keyword should have a colour set")
}

func TestBuildChromaStyle_MarkdownTokensMapped(t *testing.T) {
	th := theme.DefaultDark()
	s := buildChromaStyle(th)
	tests := []struct {
		name  string
		ttype chroma.TokenType
	}{
		{"GenericHeading", chroma.GenericHeading},
		{"GenericSubheading", chroma.GenericSubheading},
		{"GenericEmph", chroma.GenericEmph},
		{"GenericStrong", chroma.GenericStrong},
		{"GenericDeleted", chroma.GenericDeleted},
		{"NameTag", chroma.NameTag},
		{"NameAttribute", chroma.NameAttribute},
		{"NameEntity", chroma.NameEntity},
		{"LiteralStringBacktick", chroma.LiteralStringBacktick},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := s.Get(tt.ttype)
			assert.True(t, entry.Colour.IsSet(), "%s should have a colour set", tt.name)
		})
	}
}

func TestBuildChromaStyle_AllSubtypesMapped(t *testing.T) {
	th := theme.DefaultDark()
	s := buildChromaStyle(th)
	allTypes := []chroma.TokenType{
		chroma.Keyword, chroma.KeywordType, chroma.KeywordDeclaration, chroma.NameFunction,
		chroma.NameDecorator, chroma.NameClass, chroma.NameBuiltin, chroma.NameVariable,
		chroma.NameConstant, chroma.String,
		chroma.StringDouble, chroma.StringSingle, chroma.Number, chroma.NumberInteger,
		chroma.NumberFloat, chroma.Comment, chroma.CommentSingle, chroma.CommentSpecial,
		chroma.Operator, chroma.Punctuation,
	}
	for _, tt := range allTypes {
		entry := s.Get(tt)
		assert.True(t, entry.Colour.IsSet(), "token type %s should have a colour set", tt)
	}
}

func TestBuildChromaStyle_StyleReuse(t *testing.T) {
	th := theme.DefaultDark()
	s1 := buildChromaStyle(th)
	s2 := buildChromaStyle(th)
	assert.Equal(t, s1.Get(chroma.Keyword).Colour.String(), s2.Get(chroma.Keyword).Colour.String())
}

// chroma token color tests

func TestChromaTokenColor_Hit(t *testing.T) {
	e := editorWithTheme()
	e.chromaStyle = buildChromaStyle(e.th)
	clr := e.chromaTokenColor(chroma.Keyword)
	assert.NotEmpty(t, clr, "Keyword should return a colour")
	assert.True(t, strings.HasPrefix(clr, "#"))
}

func TestChromaTokenColor_Miss(t *testing.T) {
	e := editorWithTheme()
	e.chromaStyle = buildChromaStyle(e.th)
	clr := e.chromaTokenColor(chroma.Text)
	assert.Empty(t, clr, "Text token should return empty")
}

func TestChromaTokenColor_DifferentTokens(t *testing.T) {
	e := editorWithTheme()
	e.chromaStyle = buildChromaStyle(e.th)
	kw := e.chromaTokenColor(chroma.Keyword)
	cm := e.chromaTokenColor(chroma.Comment)
	assert.NotEqual(t, kw, cm, "Keyword and Comment should have different colours")
}

// buildLineColorMap tests       ──

func editorWithTheme() *Editor {
	e := New(theme.DefaultDark())
	e.open = true
	e.initChroma()
	return e
}

func TestBuildLineColorMap_EmptyLine(t *testing.T) {
	e := editorWithTheme()
	cm := e.buildLineColorMap("", 0)
	assert.Nil(t, cm)
}

func TestBuildLineColorMap_PlainText(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	cm := e.buildLineColorMap("hello world", 11)
	assert.NotNil(t, cm)
	assert.Equal(t, 11, len(cm))
	// All entries should be empty (plain text has no tokens with colour)
}

func TestBuildLineColorMap_GoComment(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	cm := e.buildLineColorMap("// this is a comment", 20)
	assert.NotNil(t, cm)
	hasColor := false
	for _, c := range cm {
		if c != "" {
			hasColor = true
			break
		}
	}
	assert.True(t, hasColor, "comment line should have at least one coloured cell")
}

func TestBuildLineColorMap_GoKeyword(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	cm := e.buildLineColorMap("func main() {", 13)
	assert.NotNil(t, cm)
	// "func" should be colored as a keyword
	assert.NotEmpty(t, cm[0], "first char of 'func' should be coloured")
}

func TestBuildLineColorMap_CacheHit(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	cm1 := e.buildLineColorMap("var x = 1", 9)
	assert.NotNil(t, cm1)
	cm2 := e.buildLineColorMap("var x = 1", 9)
	assert.Equal(t, cm1, cm2, "cached result should match")
}

func TestBuildLineColorMap_CacheMiss(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	// "var x = 1" -> "var" keyword, rest text; "func foo()" -> "func" keyword, "foo" NameFunction
	cm1 := e.buildLineColorMap("var x = 1", 9)
	cm2 := e.buildLineColorMap("func foo()", 9)
	if len(cm1) == len(cm2) {
		allEqual := true
		for i := range cm1 {
			if cm1[i] != cm2[i] {
				allEqual = false
				break
			}
		}
		assert.False(t, allEqual, "different token patterns should produce different colour maps")
	}
}

func TestBuildLineColorMap_NilLexer(t *testing.T) {
	e := editorWithTheme()
	e.chromaLexer = nil
	cm := e.buildLineColorMap("hello", 5)
	assert.Nil(t, cm)
}

// ── buildLineColorMapWithRunes tests ─

func TestBuildLineColorMapWithRunes(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	line := "var x = 1"
	runes := []rune(line)
	widths := make([]int, len(runes))
	for i, r := range runes {
		widths[i] = runeVisualWidth(r)
	}
	cm := e.buildLineColorMapWithRunes(line, len(runes), runes, widths)
	assert.NotNil(t, cm)
	assert.Equal(t, len(runes), len(cm))
}

func TestBuildLineColorMapWithRunes_MatchesStandard(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "test.go"
	e.initChroma()
	line := "func main() { return 42; }"
	runes := []rune(line)
	widths := make([]int, len(runes))
	visW := 0
	for i, r := range runes {
		widths[i] = runeVisualWidth(r)
		visW += widths[i]
	}
	cm1 := e.buildLineColorMap(line, visW)
	cm2 := e.buildLineColorMapWithRunes(line, visW, runes, widths)
	assert.Equal(t, cm2, cm1, "both colour map builders should produce identical results")
}

func TestBuildLineColorMapWithRunes_Empty(t *testing.T) {
	e := editorWithTheme()
	cm := e.buildLineColorMapWithRunes("", 0, nil, nil)
	assert.Nil(t, cm)
}

// ── Markdown-specific chroma tests

func TestBuildLineColorMap_MarkdownHeading(t *testing.T) {
	e := New(theme.DefaultDark())
	e.filePath = "test.md"
	e.open = true
	e.initChroma()
	// chroma v2.20.0 markdown lexer does NOT produce heading tokens for
	// single-line input; verify it at least returns a non-nil map.
	cm := e.buildLineColorMap("# Hello World", 13)
	assert.NotNil(t, cm)
}

func TestBuildLineColorMap_MarkdownCode(t *testing.T) {
	e := New(theme.DefaultDark())
	e.filePath = "test.md"
	e.open = true
	e.initChroma()
	cm := e.buildLineColorMap("`code` here", 11)
	assert.NotNil(t, cm)
	hasColor := false
	for _, c := range cm {
		if c != "" {
			hasColor = true
			break
		}
	}
	assert.True(t, hasColor, "markdown inline code should have coloured cells")
}

func TestBuildLineColorMap_MarkdownBold(t *testing.T) {
	e := New(theme.DefaultDark())
	e.filePath = "test.md"
	e.open = true
	e.initChroma()
	cm := e.buildLineColorMap("**bold** text", 12)
	assert.NotNil(t, cm)
}

// ── diagnostics tests ─

func TestSetDiagnostics_StoresAndCounts(t *testing.T) {
	e := editorWithTheme()
	diags := []LSPDiagnostic{
		{Line: 0, Col: 0, EndCol: 5, Severity: DiagError, Message: "err1"},
		{Line: 1, Col: 2, EndCol: 4, Severity: DiagWarning, Message: "warn1"},
		{Line: 2, Col: 0, EndCol: 1, Severity: DiagInfo, Message: "info1"},
		{Line: 0, Col: 10, EndCol: 15, Severity: DiagHint, Message: "hint1"},
	}
	e.SetDiagnostics(diags)
	errs, warns, hints := e.DiagnosticCounts()
	assert.Equal(t, 1, errs)
	assert.Equal(t, 1, warns)
	assert.Equal(t, 2, hints) // DiagInfo + DiagHint count as hints
}

func TestSetDiagnostics_Empty(t *testing.T) {
	e := editorWithTheme()
	e.SetDiagnostics(nil)
	errs, warns, hints := e.DiagnosticCounts()
	assert.Equal(t, 0, errs)
	assert.Equal(t, 0, warns)
	assert.Equal(t, 0, hints)
}

func TestSetDiagnostics_MultipleSameLine(t *testing.T) {
	e := editorWithTheme()
	diags := []LSPDiagnostic{
		{Line: 0, Col: 0, EndCol: 3, Severity: DiagError, Message: "e1"},
		{Line: 0, Col: 5, EndCol: 8, Severity: DiagError, Message: "e2"},
	}
	e.SetDiagnostics(diags)
	errs, warns, hints := e.DiagnosticCounts()
	assert.Equal(t, 2, errs)
	assert.Equal(t, 0, warns)
	assert.Equal(t, 0, hints)
}

func TestSetDiagnostics_ReplacesPrevious(t *testing.T) {
	e := editorWithTheme()
	e.SetDiagnostics([]LSPDiagnostic{{Severity: DiagError, Message: "old"}})
	e.SetDiagnostics([]LSPDiagnostic{{Severity: DiagWarning, Message: "new"}})
	errs, warns, _ := e.DiagnosticCounts()
	assert.Equal(t, 0, errs, "old errors should be replaced")
	assert.Equal(t, 1, warns, "new warning should be counted")
}

func TestSetDiagnostics_ThreadSafety(t *testing.T) {
	e := editorWithTheme()
	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			e.SetDiagnostics([]LSPDiagnostic{
				{Line: 0, Col: 0, EndCol: 1, Severity: DiagError, Message: "race"},
			})
		}
		done <- true
	}()
	for i := 0; i < 100; i++ {
		e.DiagnosticCounts()
		e.lineDiagnostics(0)
	}
	<-done
}

// ── lineDiagnostics tests

func TestLineDiagnostics_ByLine(t *testing.T) {
	e := editorWithTheme()
	_, err := e.buf.Insert(Position{}, []byte("abcde\nabc\n"))
	assert.NoError(t, err)
	diags := []LSPDiagnostic{
		{Line: 0, Col: 0, EndCol: 5, Severity: DiagError, Message: "e"},
		{Line: 1, Col: 0, EndCol: 3, Severity: DiagWarning, Message: "w"},
	}
	e.SetDiagnostics(diags)
	line0 := e.lineDiagnostics(0)
	assert.Len(t, line0, 1)
	assert.Equal(t, DiagError, line0[0].severity)
	assert.Equal(t, 0, line0[0].startCol)
	assert.Equal(t, 5, line0[0].endCol)

	line1 := e.lineDiagnostics(1)
	assert.Len(t, line1, 1)
	assert.Equal(t, DiagWarning, line1[0].severity)
	assert.Equal(t, 0, line1[0].startCol)
	assert.Equal(t, 3, line1[0].endCol)

	line2 := e.lineDiagnostics(2)
	assert.Nil(t, line2, "line without diagnostics should return nil")
}

func TestLineDiagnostics_Span(t *testing.T) {
	e := editorWithTheme()
	_, err := e.buf.Insert(Position{}, []byte("abcdefgh"))
	assert.NoError(t, err)
	e.SetDiagnostics([]LSPDiagnostic{
		{Line: 0, Col: 3, EndCol: 7, Severity: DiagError},
	})
	spans := e.lineDiagnostics(0)
	assert.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, 3, s.startCol)
	assert.Equal(t, 7, s.endCol)
}

func TestLineDiagnostics_TabConversion(t *testing.T) {
	e := editorWithTheme()
	_, err := e.buf.Insert(Position{}, []byte("\t\tfoo\nbar"))
	assert.NoError(t, err)
	e.SetDiagnostics([]LSPDiagnostic{
		{Line: 0, Col: 2, EndCol: 5, Severity: DiagError, Message: "on-foo"},
		{Line: 1, Col: 1, EndCol: 4, Severity: DiagWarning, Message: "on-bar"},
	})
	spans := e.lineDiagnostics(0)
	assert.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, 8, s.startCol, "two tabs before 'foo' → visual col 8")
	assert.Equal(t, 11, s.endCol, "visual end: foo spans cols 8-10 → endCol 11")

	spans = e.lineDiagnostics(1)
	assert.Len(t, spans, 1)
	s = spans[0]
	assert.Equal(t, 1, s.startCol, "bar starts at col 1")
	assert.Equal(t, 3, s.endCol, "bar is 3 wide; EndCol=4 snapped to visual width 3")
}

// ── diagSeverityAtCol tests ─

func TestDiagSeverityAtCol_Inside(t *testing.T) {
	spans := []diagSpan{{startCol: 2, endCol: 5, severity: DiagError}}
	assert.Equal(t, DiagError, diagSeverityAtCol(spans, 2))
	assert.Equal(t, DiagError, diagSeverityAtCol(spans, 3))
	assert.Equal(t, DiagError, diagSeverityAtCol(spans, 4))
}

func TestDiagSeverityAtCol_Outside(t *testing.T) {
	spans := []diagSpan{{startCol: 2, endCol: 5, severity: DiagError}}
	assert.Equal(t, 0, diagSeverityAtCol(spans, 1))
	assert.Equal(t, 0, diagSeverityAtCol(spans, 5))
	assert.Equal(t, 0, diagSeverityAtCol(spans, 6))
}

func TestDiagSeverityAtCol_Empty(t *testing.T) {
	assert.Equal(t, 0, diagSeverityAtCol(nil, 0))
	assert.Equal(t, 0, diagSeverityAtCol([]diagSpan{}, 0))
}

func TestDiagSeverityAtCol_MultipleSpans(t *testing.T) {
	spans := []diagSpan{
		{startCol: 0, endCol: 3, severity: DiagWarning},
		{startCol: 5, endCol: 8, severity: DiagError},
	}
	assert.Equal(t, DiagWarning, diagSeverityAtCol(spans, 1))
	assert.Equal(t, DiagError, diagSeverityAtCol(spans, 6))
	assert.Equal(t, 0, diagSeverityAtCol(spans, 4))
}

// ── utility function tests ──────────────────────────────────────

func TestRuneVisualWidth(t *testing.T) {
	tests := []struct {
		r   rune
		exp int
	}{
		{'a', 1},
		{' ', 1},
		{'\t', tabWidth},
		{'文', 2},
		{'😀', 2},
		{'\n', 0},
	}
	for _, tt := range tests {
		got := runeVisualWidth(tt.r)
		assert.Equal(t, tt.exp, got, "runeVisualWidth(%c) should be %d", tt.r, tt.exp)
	}
}

func TestLineVisualWidthFromRunes(t *testing.T) {
	tests := []struct {
		name  string
		runes []rune
		exp   int
	}{
		{"empty", []rune{}, 0},
		{"ascii", []rune("hello"), 5},
		{"mixed", []rune("a文😀"), 1 + 2 + 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.exp, lineVisualWidthFromRunes(tt.runes))
		})
	}
}

func TestColToRuneIdx(t *testing.T) {
	runes := []rune("ab文c")
	// a=col0, b=col1, 文=col2(col2-3), c=col4
	assert.Equal(t, 0, colToRuneIdx(runes, 0))
	assert.Equal(t, 1, colToRuneIdx(runes, 1))
	assert.Equal(t, 2, colToRuneIdx(runes, 2))
	assert.Equal(t, 2, colToRuneIdx(runes, 3))
	assert.Equal(t, 3, colToRuneIdx(runes, 4))
	assert.Equal(t, 4, colToRuneIdx(runes, 10))
}

func TestColToRuneIdx_Empty(t *testing.T) {
	assert.Equal(t, 0, colToRuneIdx(nil, 0))
	assert.Equal(t, 0, colToRuneIdx([]rune{}, 5))
}

func TestRunesToVisualCol(t *testing.T) {
	runes := []rune("ab文c")
	assert.Equal(t, 0, runesToVisualCol(runes, 0))
	assert.Equal(t, 1, runesToVisualCol(runes, 1))
	assert.Equal(t, 2, runesToVisualCol(runes, 2))
	assert.Equal(t, 4, runesToVisualCol(runes, 3))
	// Past end returns total width
	assert.Equal(t, 5, runesToVisualCol(runes, 10))
}

func TestRunesToVisualCol_Empty(t *testing.T) {
	assert.Equal(t, 0, runesToVisualCol(nil, 0))
	assert.Equal(t, 0, runesToVisualCol([]rune{}, 5))
}

func TestIsWordChar(t *testing.T) {
	assert.True(t, isWordChar('a'))
	assert.True(t, isWordChar('Z'))
	assert.True(t, isWordChar('_'))
	assert.True(t, isWordChar('é'))
	assert.True(t, isWordChar('日'))
	assert.True(t, isWordChar('3'))
	assert.False(t, isWordChar(' '))
	assert.False(t, isWordChar('('))
	assert.False(t, isWordChar('.'))
}

// ── LSPIntegration tests ───────────────────────────────────────────

func TestLSPDiagnostic_SeverityConstants(t *testing.T) {
	assert.Equal(t, 1, DiagError)
	assert.Equal(t, 2, DiagWarning)
	assert.Equal(t, 3, DiagInfo)
	assert.Equal(t, 4, DiagHint)
}

func TestLSPIntegration_New(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	assert.NotNil(t, l)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_UpdateDiagnostics(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	// Open a file so that diagnostics for it are accepted.
	l.mu.Lock()
	l.openFiles["/tmp/test.go"] = &openFileState{uri: "file:///tmp/test.go"}
	l.mu.Unlock()

	var got []LSPDiagnostic
	l.OnDiagnostics(func(fp string, diags []LSPDiagnostic) {
		got = diags
	})

	// Simulate updateDiagnostics via callback.
	l.mu.Lock()
	cb := l.onDiagnostics
	l.mu.Unlock()
	if cb != nil {
		cb("/tmp/test.go", []LSPDiagnostic{
			{Line: 0, Col: 0, EndCol: 5, Severity: DiagError, Message: "test error"},
		})
	}

	assert.Len(t, got, 1)
	assert.Equal(t, "test error", got[0].Message)
	assert.Equal(t, DiagError, got[0].Severity)
}

func TestLSPIntegration_OnDiagnosticsCallback(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/test")
	called := false
	l.OnDiagnostics(func(fp string, diags []LSPDiagnostic) {
		called = true
		assert.Len(t, diags, 1)
		assert.Equal(t, DiagWarning, diags[0].Severity)
	})

	l.mu.Lock()
	cb := l.onDiagnostics
	l.mu.Unlock()
	if cb != nil {
		cb("/tmp/test.go", []LSPDiagnostic{
			{Severity: DiagWarning},
		})
	}

	assert.True(t, called, "onDiagnostics callback should have been called")
}

func TestLSPIntegration_URIToPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space", "main.go")
	uri := protocol.URIFromPath(path)
	l := NewLSPIntegration("gopls", nil, string(protocol.URIFromPath(filepath.Dir(path))))

	assert.Equal(t, filepath.Clean(path), l.uriToPath(uri))
}

// ── lspServerConfig tests ──────────────────────────────────────────

func TestLSPServers_Go(t *testing.T) {
	cfg, ok := lspServers[".go"]
	assert.True(t, ok)
	assert.Equal(t, "gopls", cfg.Cmd)
}

func TestLSPServers_Markdown(t *testing.T) {
	_, ok := lspServers[".md"]
	assert.False(t, ok, "markdown should not have an LSP server entry")
}

func TestLSPServers_Rust(t *testing.T) {
	cfg, ok := lspServers[".rs"]
	assert.True(t, ok)
	assert.Equal(t, "rust-analyzer", cfg.Cmd)
}

func TestLSPServers_Python(t *testing.T) {
	cfg, ok := lspServers[".py"]
	assert.True(t, ok)
	assert.Equal(t, "pyright-langserver", cfg.Cmd)
}

func TestLSPServers_Ruby(t *testing.T) {
	_, ok := lspServers[".rb"]
	assert.False(t, ok)
}

func TestLSPServers_Unknown(t *testing.T) {
	_, ok := lspServers[".xyz"]
	assert.False(t, ok)
}

func TestLSPServers_C(t *testing.T) {
	for _, ext := range []string{".c", ".h", ".cpp", ".hpp", ".cc", ".hh", ".cxx", ".hxx", ".c++", ".h++"} {
		cfg, ok := lspServers[ext]
		assert.True(t, ok, ext)
		assert.Equal(t, "clangd", cfg.Cmd, ext)
	}
}

func TestLSPServers_CSharp(t *testing.T) {
	cfg, ok := lspServers[".cs"]
	assert.True(t, ok)
	assert.Equal(t, "omnisharp", cfg.Cmd)
}

func TestLSPServers_Zig(t *testing.T) {
	cfg, ok := lspServers[".zig"]
	assert.True(t, ok)
	assert.Equal(t, "zls", cfg.Cmd)
}

func TestLSPServers_Odin(t *testing.T) {
	cfg, ok := lspServers[".odin"]
	assert.True(t, ok)
	assert.Equal(t, "ols", cfg.Cmd)
}

func TestLSPServers_Lua(t *testing.T) {
	cfg, ok := lspServers[".lua"]
	assert.True(t, ok)
	assert.Equal(t, "lua-language-server", cfg.Cmd)
}

// ── Editor lifecycle tests ─────────────────────────────────────────

func TestEditorNew(t *testing.T) {
	th := theme.DefaultDark()
	e := New(th)
	assert.NotNil(t, e)
	assert.NotNil(t, e.buf)
	assert.False(t, e.open)
	assert.False(t, e.Modified())
}

func TestEditorOpenClose(t *testing.T) {
	e := editorWithTheme()
	e.filePath = "/tmp/test.go"
	e.title = "test.go"
	e.buffer.Insert(Position{}, []byte("package main\nfunc main() {\n}\n"))
	e.open = true
	e.initChroma()
	assert.True(t, e.IsOpen())
	assert.Equal(t, "test.go", e.BaseTitle())
	assert.True(t, e.Modified())
	assert.Equal(t, 4, e.LineCount())

	e.Close()
	assert.False(t, e.IsOpen())
}

func TestEditorCursor(t *testing.T) {
	e := editorWithTheme()
	assert.Equal(t, 0, e.CursorLine())
	assert.Equal(t, 0, e.CursorCol())
}

// ── Theme integration tests ─────────────────────────────────────────

func TestDefaultDarkTheme_HasCodeColors(t *testing.T) {
	th := theme.DefaultDark()
	assert.NotNil(t, th.Colors.Code.Keyword)
	assert.NotNil(t, th.Colors.Code.Function)
	assert.NotNil(t, th.Colors.Code.String)
	assert.NotNil(t, th.Colors.Code.Number)
	assert.NotNil(t, th.Colors.Code.Comment)
	assert.NotNil(t, th.Colors.Error)
	assert.NotNil(t, th.Colors.Warning)
	assert.NotNil(t, th.Colors.Success)
}

func TestBuildLineColorMap_MultipleFileTypes(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		line     string
	}{
		{"go", "test.go", "func main() { return 42 }"},
		{"py", "test.py", "def hello():"},
		{"js", "test.js", "function hello() {"},
		{"rs", "test.rs", "fn main() -> () {"},
		{"ts", "test.ts", "const x: number = 1;"},
		{"css", "test.css", ".class { color: red; }"},
		{"json", "test.json", `{"key": "value"}`},
		{"md", "test.md", "# Heading"},
		{"go_comment", "test.go", "// comment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(theme.DefaultDark())
			e.filePath = tt.filePath
			e.open = true
			e.initChroma()
			cm := e.buildLineColorMap(tt.line, len([]rune(tt.line)))
			assert.NotNil(t, cm)
		})
	}
}

func TestEditorDiagnosticPipeline(t *testing.T) {
	e := editorWithTheme()
	_, err := e.buf.Insert(Position{}, []byte("abcdefghijklmnopqrst\nabcdefgh\nabcde\n"))
	assert.NoError(t, err)
	diags := []LSPDiagnostic{
		{Line: 0, Col: 0, EndCol: 10, Severity: DiagError, Message: "syntax error"},
		{Line: 0, Col: 15, EndCol: 20, Severity: DiagHint, Message: "hint"},
		{Line: 1, Col: 3, EndCol: 8, Severity: DiagWarning, Message: "unused variable"},
		{Line: 2, Col: 0, EndCol: 5, Severity: DiagInfo, Message: "info"},
	}
	e.SetDiagnostics(diags)

	errs, warns, hints := e.DiagnosticCounts()
	assert.Equal(t, 1, errs)
	assert.Equal(t, 1, warns)
	assert.Equal(t, 2, hints) // DiagInfo + DiagHint

	line0 := e.lineDiagnostics(0)
	assert.Len(t, line0, 2)
	assert.Equal(t, DiagError, diagSeverityAtCol(line0, 0))
	assert.Equal(t, 0, diagSeverityAtCol(line0, 10))
	assert.Equal(t, DiagHint, diagSeverityAtCol(line0, 15))

	line1 := e.lineDiagnostics(1)
	assert.Len(t, line1, 1)
	assert.Equal(t, DiagWarning, line1[0].severity)

	line2 := e.lineDiagnostics(2)
	assert.Len(t, line2, 1)
	assert.Equal(t, DiagInfo, line2[0].severity)

	// No diagnostics for this line
	assert.Nil(t, e.lineDiagnostics(5))
}

func TestEditorDiagnosticPipeline_Replace(t *testing.T) {
	e := editorWithTheme()

	e.SetDiagnostics([]LSPDiagnostic{{Line: 0, Col: 0, EndCol: 5, Severity: DiagError}})
	errs, _, _ := e.DiagnosticCounts()
	assert.Equal(t, 1, errs)

	e.SetDiagnostics(nil)
	errs, _, _ = e.DiagnosticCounts()
	assert.Equal(t, 0, errs)

	e.SetDiagnostics([]LSPDiagnostic{{Line: 0, Col: 0, EndCol: 5, Severity: DiagWarning}})
	_, warns, _ := e.DiagnosticCounts()
	assert.Equal(t, 1, warns)
}

// ── LSPIntegration start-failure tests ────────────────────────────

func TestLSPIntegration_StartFailsGracefully(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	ctx := t.Context()
	err := l.Start(ctx)
	assert.Error(t, err)
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_StartNotCalled(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/tmp")
	ctx := t.Context()
	l.OpenFile(ctx, "/tmp/test.go", "go", "package main")
	l.UpdateFile(ctx, "/tmp/test.go", "package main\nfunc main() {}")
	l.CloseFile(ctx, "/tmp/test.go")
	diags := l.Diagnostics()
	assert.Empty(t, diags)
}

func TestLSPIntegration_DoubleStart(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	ctx := t.Context()
	err1 := l.Start(ctx)
	assert.Error(t, err1)
	err2 := l.Start(ctx)
	assert.Error(t, err2)
}

func TestLSPIntegration_StopTwice(t *testing.T) {
	l := NewLSPIntegration("nonexistent-lsp-server-12345", nil, "/tmp")
	l.Stop()
	l.Stop()
}

func TestLSPIntegration_RequestCompletionsWithoutStart(t *testing.T) {
	l := NewLSPIntegration("gopls", nil, "/tmp")
	ctx := t.Context()
	items := l.RequestCompletions(ctx, "/tmp/test.go", 0, 0)
	assert.Nil(t, items)
}

// ── CJK / wide-character width tests ──────────────────────────────

func TestRuneVisualWidth_CJK(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		exp  int
	}{
		{"hiragana", 'あ', 2},
		{"kanji", '文', 2},
		{"hanzi", '中', 2},
		{"korean", '한', 2},
		{"emoji_wide", '😀', 2},
		{"fullwidth_digit", '１', 2},
		{"ascii", 'a', 1},
		{"space", ' ', 1},
		{"tab", '\t', tabWidth},
		{"newline", '\n', 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runeVisualWidth(tt.r)
			assert.Equal(t, tt.exp, got, "runeVisualWidth(%c U+%04x)", tt.r, tt.r)
		})
	}
}

func TestColToRuneIdx_CJK(t *testing.T) {
	runes := []rune("a文b😀c")
	layout := []struct {
		col int
		ri  int
	}{
		{0, 0},
		{1, 1},
		{2, 1},
		{3, 2},
		{4, 3},
		{5, 3},
		{6, 4},
		{7, 5},
	}
	for _, l := range layout {
		got := colToRuneIdx(runes, l.col)
		assert.Equal(t, l.ri, got, "colToRuneIdx(col=%d)", l.col)
	}
}

func TestRunesToVisualCol_CJK(t *testing.T) {
	runes := []rune("a文b😀c")
	layout := []struct {
		ri  int
		col int
	}{
		{0, 0},
		{1, 1},
		{2, 3},
		{3, 4},
		{4, 6},
		{5, 7},
	}
	for _, l := range layout {
		got := runesToVisualCol(runes, l.ri)
		assert.Equal(t, l.col, got, "runesToVisualCol(ri=%d)", l.ri)
	}
}

func TestLineVisualWidthFromRunes_CJK(t *testing.T) {
	tests := []struct {
		name string
		s    string
		exp  int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"cjk", "中文测试", 8},
		{"mixed", "a文😀", 1 + 2 + 2},
		{"tab", "\t\t", tabWidth * 2},
		// ab=2 + 文=2 + 😀=2 = 6; tab at col 6 advances 2 to stop 8.
		{"all", "ab文😀\t", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineVisualWidthFromRunes([]rune(tt.s))
			assert.Equal(t, tt.exp, got)
		})
	}
}
