package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/insight/catalog"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmartEnterUsesLanguageConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, path, before, after, want string }{
		{"off-side block", "sample.py", "if ready:", "", "if ready:\n    "},
		{"not every colon", "sample.py", "value = {'key':", "", "value = {'key':\n"},
		{"comment continuation", "sample.go", "// hello ", "world", "// hello \n// world"},
		{"doc comment pair", "sample.js", "/**", " */", "/**\n * \n */"},
		{"doc comment leader", "sample.ts", " * words", "", " * words\n * "},
		{"after doc comment", "sample.js", " */", "", " */\n"},
		{"bracket not at end", "sample.js", "call(value,", "", "call(value,\n    "},
		{"line comment bracket", "sample.go", "// {", "", "// {\n"},
		{"string bracket", "sample.go", "var s = `text {", " more`", "var s = `text {\n more`"},
		{"nested string", "sample.py", "text = \"\"\"if ready:", "\"\"\"", "text = \"\"\"if ready:\n\"\"\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.before + tc.after
			// The continued comment has an established preceding leader.
			prefix := ""
			if tc.name == "doc comment leader" {
				prefix = "/**\n"
			}
			e := opsEditor(t, tc.path, prefix+text)
			t.Cleanup(func() { closeSyntax(e) })
			settleSyntax(t, e)
			line := 0
			if prefix != "" {
				line = 1
			}
			e.SetCursorPosition(line, LineVisualWidth(tc.before, e.TabWidth()))
			press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, prefix+tc.want, e.Value())
			e.Undo()
			require.Equal(t, prefix+text, e.Value())
			e.Redo()
			require.Equal(t, prefix+tc.want, e.Value())
		})
	}
}

func TestTypedClosingBracketDedentsInOneUndo(t *testing.T) {
	for _, tc := range []struct {
		path, text, want string
		closer           rune
	}{
		{"sample.js", "if (ready) {\n    work();\n    ", "if (ready) {\n    work();\n}", '}'},
		{"sample.js", "call(\n    value,\n    ", "call(\n    value,\n)", ')'},
		{"sample.py", "items = [\n  value,\n  ", "items = [\n  value,\n]", ']'},
		{"sample.cpp", "items = [\n  value,\n  ", "items = [\n  value,\n]", ']'},
		{"sample.go", "if ready {\n\twork()\n\t", "if ready {\n\twork()\n}", '}'},
		{"sample.js", "if (ready) {\r\n    work();\r\n    ", "if (ready) {\r\n    work();\r\n}", '}'},
		{"sample.js", "if (ready) {\n    work();\n    }", "if (ready) {\n    work();\n    }", '}'}, // already a decrease: respect intentional indent/type-over
	} {
		t.Run(tc.path+string(tc.closer), func(t *testing.T) {
			e := opsEditor(t, tc.path, tc.text)
			t.Cleanup(func() { closeSyntax(e) })
			settleSyntax(t, e)
			line := e.LineCount() - 1
			e.SetCursorPosition(line, LineVisualWidth(leadingWhitespace(e.LineText(line)), e.TabWidth()))
			typeChar(e, tc.closer)
			require.Equal(t, tc.want, e.Value())
			if tc.want != tc.text {
				e.Undo()
				require.Equal(t, tc.text, e.Value())
				e.Redo()
				require.Equal(t, tc.want, e.Value())
			}
		})
	}
}

// A newly supplied language must use the same input consumer. None of these
// tokens or actions is known to production Go code.
func TestSmartIndentUsesNewCatalogLanguageAndRuleOrder(t *testing.T) {
	dir := t.TempDir()
	rules := `{"format":1,"languages":[{"id":"fixture-indent","extensions":[".indent-fixture"],"lsp_language_id":"fixture-indent","editing":{"brackets":[["BEGIN","END"]],"on_enter_rules":[{"before_text":{"pattern":"^BEGIN SPECIAL$","flags":"i"},"after_text":{"pattern":"^END$"},"previous_line_text":{"pattern":"^context$"},"action":{"indent":"indentOutdent","append_text":"\t"}},{"before_text":{"pattern":"^begin(?! ignored)","flags":"i"},"action":{"indent":"indent","append_text":"leader "}}],"indentation_rules":{"increase_indent_pattern":{"pattern":"^BEGIN$"},"decrease_indent_pattern":{"pattern":"^\\s*END$"},"indent_next_line_pattern":{"pattern":"^once$"},"un_indented_line_pattern":{"pattern":"^ignore$"}}}}]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rules.json"), []byte(rules), 0600))
	require.Empty(t, catalog.UsePacks(dir))
	t.Cleanup(func() { catalog.UsePacks(t.TempDir()) })
	for _, tc := range []struct{ before, after, want string }{
		{"context\nBEGIN SPECIAL", "END", "context\nBEGIN SPECIAL\n  \nEND"},
		{"context\nbegin body", "", "context\nbegin body\n  leader "},
		{"begin ignored", "", "begin ignored\n"},
		{"NOTBEGIN", "", "NOTBEGIN\n"},
		{"once", "", "once\n  "},
		{"once\n  body", "", "once\n  body\n"},
		{"BEGIN\nignore", "", "BEGIN\nignore\n  "},
	} {
		e := opsEditor(t, "sample.indent-fixture", tc.before+tc.after)
		require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 8}))
		t.Cleanup(func() { closeSyntax(e) })
		settleSyntax(t, e)
		line := strings.Count(tc.before, "\n")
		before := tc.before[strings.LastIndex(tc.before, "\n")+1:]
		e.SetCursorPosition(line, LineVisualWidth(before, e.TabWidth()))
		press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, tc.want, e.Value())
		e.Undo()
		require.Equal(t, tc.before+tc.after, e.Value())
	}
}

func TestSmartIndentMultipleCaretsAndLinkedViewKeepAtomicHistory(t *testing.T) {
	for _, tc := range []struct {
		source, want string
		key          tea.KeyPressMsg
	}{
		{"if one:\nif two:", "if one:\n    \nif two:\n    ", tea.KeyPressMsg{Code: tea.KeyEnter}},
		{"if (one) {\n    call();\n    \n}\nif (two) {\n    work();\n    ", "if (one) {\n    call();\n}\n}\nif (two) {\n    work();\n}", tea.KeyPressMsg{Code: '}', Text: "}"}},
	} {
		path := "sample.py"
		if tc.key.Code == '}' {
			path = "sample.js"
		}
		e := opsEditor(t, path, tc.source)
		t.Cleanup(func() { closeSyntax(e) })
		settleSyntax(t, e)
		peer := e.NewLinkedView()
		t.Cleanup(peer.Close)
		e.SetCursorPosition(0, 7)
		if tc.key.Code == tea.KeyEnter {
			e.HandleEvent(ActionMsg{ID: "select.caretBelow"})
		} else {
			e.SetCursorPosition(2, 4)
			paintEditor(e, 80, 24)
			e.HandleEvent(tea.MouseClickMsg{X: e.GutterWidth() + 4, Y: 6, Button: tea.MouseLeft, Mod: tea.ModAlt})
			e.HandleEvent(tea.MouseReleaseMsg{X: e.GutterWidth() + 4, Y: 6, Button: tea.MouseLeft})
		}
		require.Equal(t, 2, e.CaretCount())
		original := e.SourceSelections()
		press(e, tc.key)
		require.Equal(t, tc.want, e.Value())
		require.Equal(t, tc.want, peer.Value())
		e.Undo()
		require.Equal(t, tc.source, peer.Value())
		require.Equal(t, original, e.SourceSelections())
		e.Redo()
		require.Equal(t, tc.want, peer.Value())
	}
}

func TestSmartIndentNeverUsesStaleSyntaxOrRewritesPaste(t *testing.T) {
	const source = "if ready:\n    value = 1"
	e := opsEditor(t, "sample.py", source)
	t.Cleanup(func() { closeSyntax(e) })
	settleSyntax(t, e)
	// Keep a completed old result, edit its source, then deliver it late.
	e.invalidateSyntax()
	cmd := e.SyntaxCommand()
	require.NotNil(t, cmd)
	old := cmd().(SyntaxReadyMsg)
	e.SetCursorPosition(0, 3)
	e.HandleEvent(tea.PasteMsg{Content: "other_"})
	old.Apply()
	e.StopSyntax()
	e.SetCursorPosition(0, 15)
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "if other_ready:\n\n    value = 1", e.Value())
	e.Undo()
	require.Equal(t, "if other_ready:\n    value = 1", e.Value())
	// Paste is literal and never an on-type indentation event.
	e.ReplaceDocument("if (ready) {\n    work();\n    ")
	e.SetCursorPosition(2, 4)
	e.HandleEvent(tea.PasteMsg{Content: "}"})
	require.Equal(t, "if (ready) {\n    work();\n    }", e.Value())
}

func TestSmartIndentPreservesStringCommentAndReadOnlySource(t *testing.T) {
	for _, source := range []string{"/*\n    \n*/", "const text = `\n    \n`;"} {
		e := opsEditor(t, "source.js", source)
		t.Cleanup(func() { closeSyntax(e) })
		settleSyntax(t, e)
		e.SetCursorPosition(1, 4)
		typeChar(e, '}')
		require.Equal(t, strings.Replace(source, "    \n", "    }\n", 1), e.Value())
		e.Undo()
		require.Equal(t, source, e.Value())
		e.readOnly = true
		press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
		typeChar(e, '}')
		require.Equal(t, source, e.Value())
	}
}
