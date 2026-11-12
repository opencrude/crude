package editor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/lspcompletion"
	"crude/snippet"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"github.com/stretchr/testify/require"
)

func completionIndentItem(t *testing.T, raw string) (item lspcompletion.Item) {
	t.Helper()
	item.Original = json.RawMessage(raw)
	return item
}

func TestCompletionIndentModeUsesFileAndKeepsAsIsLiteral(t *testing.T) {
	for _, tc := range []struct{ name, mode, want string }{
		{"as is", "1", "\tif {\n\tx()\n}\r\nrest"},
		{"adjust", "2", "\tif {\r\n\t    x()\r\n\t}\r\nrest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "fixture.txt", "\tpr\r\nrest")
			require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 4, TabWidth: 4}))
			e.SetCursorPosition(0, 6)
			raw := `{"label":"if","insertTextMode":` + tc.mode + `,"textEdit":{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"if {\n\tx()\n}"}}`
			item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
			require.NoError(t, err)
			require.NoError(t, e.ApplyCompletion(item))
			require.Equal(t, tc.want, e.Value())
			e.Undo()
			require.Equal(t, "\tpr\r\nrest", e.Value())
			e.Redo()
			require.Equal(t, tc.want, e.Value())
		})
	}
}

func TestCompletionAdjustedNestedSnippetKeepsMirrorsAndSelection(t *testing.T) {
	e := opsEditor(t, "fixture.txt", "  pr\nrest")
	require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 2}))
	e.SetCursorPosition(0, 4)
	raw := `{"label":"wrap","insertTextMode":2,"insertTextFormat":2,"textEdit":{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":4}},"newText":"wrap(\n${1:界},\n${2:${3:deep}} / $2\n)$0"}}`
	item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
	require.NoError(t, err)
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, "  wrap(\n  界,\n  deep / deep\n  )\nrest", e.Value())
	require.True(t, e.SnippetActive())
	require.Equal(t, "界", e.SelectedText())
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "deep", e.SelectedText())
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	require.Equal(t, "  wrap(\n  界,\n  X / X\n  )\nrest", e.Value())
}

func TestAdjustedCompletionUsesOnlyIndentationBeforeCursor(t *testing.T) {
	e := opsEditor(t, "fixture.txt", "    suffix")
	require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 4}))
	e.SetCursorPosition(0, 2)
	raw := `{"label":"x","insertTextMode":2,"textEdit":{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":2}},"newText":"X\nY"}}`
	item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
	require.NoError(t, err)
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, "  X\n  Y  suffix", e.Value())
	e.Undo()
	require.Equal(t, "    suffix", e.Value())
}
func TestAdjustedCompletionKeepsMultilineMirrorAndChoices(t *testing.T) {
	t.Run("multiline mirror", func(t *testing.T) {
		e := opsEditor(t, "fixture.txt", "  pr\n")
		e.SetCursorPosition(0, 4)
		raw := `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"${1:first\nsecond}\n$1$0"}`
		item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
		require.NoError(t, err)
		require.NoError(t, e.ApplyCompletion(item))
		require.Equal(t, "  first\n  second\n  first\n  second\n", e.Value())
		require.Equal(t, "first\n  second", e.SelectedText())
		e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
		require.Equal(t, "  X\n  X\n", e.Value())
	})
	t.Run("multiline choice", func(t *testing.T) {
		e := opsEditor(t, "fixture.txt", "  pr\n")
		e.SetCursorPosition(0, 4)
		raw := `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"${1|one,two\nnext|}$0"}`
		item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
		require.NoError(t, err)
		require.NoError(t, e.ApplyCompletion(item))
		choices, ok := e.SnippetChoiceOptions()
		require.True(t, ok)
		require.Equal(t, []string{"one", "two\n  next"}, choices.Choices)
		require.NoError(t, e.ApplySnippetChoice(choices, choices.Choices[1]))
		require.Equal(t, "  two\n  next\n", e.Value())
	})
}
func TestAdjustedCompletionBoundsAndOriginalIdentityAreAtomic(t *testing.T) {
	t.Run("single line after long indent", func(t *testing.T) {
		indent := strings.Repeat(" ", 70000)
		e := opsEditor(t, "fixture.txt", indent+"pr")
		e.SetCursorPosition(0, len(indent)+2)
		item, err := lspcompletion.Insertion(completionIndentItem(t, `{"label":"x","insertTextMode":2,"insertText":"x"}`))
		require.NoError(t, err)
		require.NoError(t, e.ApplyCompletion(item))
		require.Equal(t, indent+"x", e.Value())
	})
	t.Run("expansion limit", func(t *testing.T) {
		indent := strings.Repeat(" ", 8192)
		e := opsEditor(t, "fixture.txt", indent+"pr")
		e.SetCursorPosition(0, len(indent)+2)
		raw := fmt.Sprintf(`{"label":"x","insertTextMode":2,"insertText":%q}`, strings.Repeat("\n", 10))
		item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
		require.NoError(t, err)
		rev := e.Revision()
		require.Error(t, e.ApplyCompletion(item))
		require.Equal(t, indent+"pr", e.Value())
		require.Equal(t, rev, e.Revision())
		require.Empty(t, e.buf.changes)
	})
	t.Run("changed original syntax", func(t *testing.T) {
		e := opsEditor(t, "fixture.txt", "  pr")
		e.SetCursorPosition(0, 4)
		item, err := lspcompletion.Insertion(completionIndentItem(t, `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"${1:safe}\n$1"}`))
		require.NoError(t, err)
		item.SnippetSource = "${1:other}\n$1"
		rev := e.Revision()
		require.Error(t, e.ApplyCompletion(item))
		require.Equal(t, "  pr", e.Value())
		require.Equal(t, rev, e.Revision())
		require.Empty(t, e.buf.changes)
	})
}

func BenchmarkCompletionIndentation(b *testing.B) {
	for _, lines := range []int{4, 500} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			source := strings.Repeat("    value(界)\n", lines)
			style := indentStyle{size: 4, tabSize: 4}
			b.ReportAllocs()
			for range b.N {
				if _, err := reindentCompletion(source, []byte("    "), "\n", style); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var adjustedCompletionSink string

func TestCompletionIndentationDoesNotAllocatePerLine(t *testing.T) {
	source := "x\n" + strings.Repeat("\tvalue\n", 100)
	style := indentStyle{size: 2, tabSize: 2}
	expected := "x\n" + strings.Repeat("    value\n", 100) + "  "
	got, err := reindentCompletion(source, []byte("  "), "\n", style)
	require.NoError(t, err)
	require.Equal(t, expected, got)
	allocations := testing.AllocsPerRun(50, func() { adjustedCompletionSink, _ = reindentCompletion(source, []byte("  "), "\n", style) })
	if allocations > 24 {
		t.Fatalf("indentation allocated per line: %.0f", allocations)
	}
}

// Indentation can start inside a placeholder rather than in surrounding text.
// The rendered result and the selected default must use the same file policy.
func TestAdjustedCompletionNormalizesPlaceholderLeadingIndent(t *testing.T) {
	e := opsEditor(t, "fixture.txt", "  pr\n")
	require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 2}))
	e.SetCursorPosition(0, 4)
	raw := `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"begin\n${1:\tword}$0"}`
	item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
	require.NoError(t, err)
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, "  begin\n    word\n", e.Value())
	require.Equal(t, "  word", e.SelectedText())
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	require.Equal(t, "  begin\n  X\n", e.Value())
	e.Undo()
	require.Equal(t, "  begin\n    word\n", e.Value())
}

func TestAdjustedCompletionPlaceholderIndentKeepsLinkedValues(t *testing.T) {
	for _, tc := range []struct{ name, body, want, selected, after string }{
		{"continuation mirrors", "begin\n${1:\tword}\n$1$0", "  begin\n    word\n    word\n", "  word", "  begin\n  X\n  X\n"},
		{"mixed positions", "${1:\tword}\n$1$0", "  \tword\n  \tword\n", "\tword", "  X\n  X\n"},
		{"nested placeholder", "begin\n${1:${2:\tword}}$0", "  begin\n    word\n", "  word", "  begin\n  X\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "fixture.txt", "  pr\n")
			require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 2}))
			e.SetCursorPosition(0, 4)
			raw := fmt.Sprintf(`{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":%q}`, tc.body)
			item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
			require.NoError(t, err)
			require.NoError(t, e.ApplyCompletion(item))
			require.Equal(t, tc.want, e.Value())
			require.Equal(t, tc.selected, e.SelectedText())
			e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
			require.Equal(t, tc.after, e.Value())
			e.Undo()
			require.Equal(t, tc.want, e.Value())
		})
	}
}

func TestAdjustedCompletionNormalizesChoiceBeginningAtContinuation(t *testing.T) {
	e := opsEditor(t, "fixture.txt", "  pr\n")
	require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 2}))
	e.SetCursorPosition(0, 4)
	raw := `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"begin\n${1|\tword,\tother|}$0"}`
	item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
	require.NoError(t, err)
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, "  begin\n    word\n", e.Value())
	choices, ok := e.SnippetChoiceOptions()
	require.True(t, ok)
	require.Equal(t, []string{"  word", "  other"}, choices.Choices)
	require.NoError(t, e.ApplySnippetChoice(choices, choices.Choices[1]))
	require.Equal(t, "  begin\n    other\n", e.Value())
}

// The provider is allowed many adjacent whitespace stops after a long indent.
// This isolates coordinate discovery; no editor or worker is timed.
func BenchmarkCompletionLeadingStopsDense(b *testing.B) {
	var source strings.Builder
	source.WriteString("begin\n")
	source.WriteString(strings.Repeat(" ", 60000))
	for i := 1; i <= 255; i++ {
		fmt.Fprintf(&source, "${%d: }", i)
	}
	source.WriteString("$0")
	text, plan, err := snippet.Parse(source.String())
	if err != nil {
		b.Fatal(err)
	}
	style := indentStyle{size: 2, tabSize: 2}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		completionLeadingStops(text, plan, []byte("  "), style)
	}
}

func TestAdjustedCompletionUsesTabsInsideContinuationPlaceholder(t *testing.T) {
	e := opsEditor(t, "fixture.txt", " \tpr\n")
	require.NoError(t, e.SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 4, TabWidth: 4}))
	e.SetCursorPosition(0, 6)
	raw := `{"label":"x","insertTextMode":2,"insertTextFormat":2,"insertText":"begin\n${1:    word}$0"}`
	item, err := lspcompletion.Insertion(completionIndentItem(t, raw))
	require.NoError(t, err)
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, " \tbegin\n \t\tword\n", e.Value())
	require.Equal(t, "\tword", e.SelectedText())
	e.HandleEvent(tea.KeyPressMsg{Code: 'X', Text: "X"})
	require.Equal(t, " \tbegin\n \tX\n", e.Value())
	e.Undo()
	require.Equal(t, " \tbegin\n \t\tword\n", e.Value())
}

// Exercise the actual editor mutation, optional grid paint and Undo over dense
// source. Parser-only measurements cannot establish the editing path's cost.
func BenchmarkAdjustedCompletionDense(b *testing.B) {
	for _, adjust := range []bool{false, true} {
		for _, paint := range []bool{false, true} {
			b.Run(fmt.Sprintf("adjust%t/paint%t", adjust, paint), func(b *testing.B) {
				e := NewPreview(theme.DefaultDark(), "dense.txt", "\tpr\n"+strings.Repeat("unchanged source line\n", 20000), 1)
				b.Cleanup(e.Close)
				e.readOnly = false
				require.NoError(b, e.SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 4, TabWidth: 4}))
				source := "if (${1:界}) {\n${2:    use($1);}\n}$0"
				item := numericCompletion(b, source)
				item.AdjustIndentation = adjust
				if adjust {
					item.SnippetSource = source
				}
				surf := surface.NewSurface("adjusted-completion", 120, 40, cell.TransparentBlank())
				e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
				settleSyntax(b, e)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					e.SetCursorPosition(0, 6)
					if err := e.ApplyCompletion(item); err != nil {
						b.Fatal(err)
					}
					if paint {
						e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
					}
					e.Undo()
					if e.LineText(0) != "\tpr" {
						b.Fatal("completion Undo changed source")
					}
				}
			})
		}
	}
}
