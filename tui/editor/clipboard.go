package editor

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/workspace/document"
)

// Clipboard commands are a port of VS Code's editor clipboard with its default
// editor.emptySelectionClipboard (src/vs/editor/contrib/clipboard/browser/
// clipboard.ts, common/viewModel/viewModelImpl.ts getPlainTextToCopy,
// common/cursor/cursorDeleteOperations.ts cut and cursorTypeEditOperations.ts
// _simplePaste): with no selection, Copy and Cut take the caret's whole line,
// and pasting that line inserts it above the caret's line.

// LineClipboard is VS Code's in-memory clipboard metadata. The shell shares
// one across its editors and clears it when another control copies text.
type LineClipboard struct {
	line string
}

func (c *LineClipboard) Clear() { c.line = "" }

func (c *LineClipboard) remember(text string, linewise bool) {
	c.Clear()
	if linewise {
		c.line = ui.PastedLineBreaks(text)
	}
}

func (c *LineClipboard) matches(text string) bool {
	if c.line != "" && c.line == ui.PastedLineBreaks(text) {
		return true
	}
	c.Clear()
	return false
}

// SetClipboard binds this editor to its shell's clipboard owner.
func (e *Editor) SetClipboard(lines *LineClipboard, copy func(string) tea.Cmd) {
	e.lineClipboard, e.clipboardCopy = lines, copy
}

func (e *Editor) clipboardLines() *LineClipboard {
	if e.lineClipboard == nil {
		e.lineClipboard = &LineClipboard{}
	}
	return e.lineClipboard
}

// copySpans returns the sorted carets and selections the clipboard reads.
func (e *Editor) copySpans() []selectionSpan {
	e.normalizeCarets()
	e.buf.ensureCache()
	spans := slices.Clone(e.selectionSpans())
	slices.SortFunc(spans, func(a, b selectionSpan) int {
		as, _ := a.bounds()
		bs, _ := b.bounds()
		return as - bs
	})
	return spans
}

// copyText is getPlainTextToCopy and getDataToCopy: selected text, or the
// whole caret line with its terminator when a caret has no selection.
func (e *Editor) copyText() (string, bool) {
	if e == nil || e.buf == nil {
		return "", false
	}
	spans := e.copySpans()
	empty := false
	for _, s := range spans {
		a, b := s.bounds()
		empty = empty || a == b
	}
	var parts []string
	if empty {
		previous := -1
		for _, s := range spans {
			a, b := s.bounds()
			line := e.buf.positionAtOffset(a).Line
			if a == b {
				if line != previous {
					parts = append(parts, e.LineText(line)+e.lineEnding())
				}
			} else {
				parts = append(parts, string(e.buf.cachedBytes[a:b]))
			}
			previous = line
		}
	} else {
		for _, s := range spans {
			a, b := s.bounds()
			parts = append(parts, string(e.buf.cachedBytes[a:b]))
		}
	}
	// getDataToCopy joins multicursor text with the document's line break.
	return strings.Join(parts, e.lineEnding()), empty && len(spans) == 1
}

func (e *Editor) writeClipboard(text string, linewise bool) tea.Cmd {
	e.clipboardLines().remember(text, linewise)
	if e.clipboardCopy != nil {
		return e.clipboardCopy(text)
	}
	return rendering.CopyToClipboardCmd(text)
}

// CopyCommand copies the selection, or the caret line with its terminator.
func (e *Editor) CopyCommand() tea.Cmd { return e.writeClipboard(e.copyText()) }

// cutCmd is VS Code's cut: CopyText, then DeleteOperations.cut as one undo step.
func (e *Editor) cutCmd() tea.Cmd {
	text, linewise := e.copyText()
	if text == "" || e.readOnly {
		return nil
	}
	spans := e.copySpans()
	var edits []document.Edit
	var last *document.Edit
	lines := e.LineCount()
	for _, s := range spans {
		a, b := s.bounds()
		if a != b {
			edits = append(edits, document.Edit{Start: a, End: b})
			last = &edits[len(edits)-1]
			continue
		}
		line := e.buf.positionAtOffset(a).Line
		var start, end int
		switch {
		case line < lines-1:
			// Cutting a line in the middle of the model.
			start, end = e.buf.positionToOffset(Position{Line: line}), e.buf.positionToOffset(Position{Line: line + 1})
		case line > 0 && (last == nil || e.buf.positionAtOffset(last.End).Line != line):
			// Cutting the last line: take the previous line break too.
			start = e.buf.positionToOffset(Position{Line: line - 1, Col: e.lineVisualWidth(line - 1)})
			end = e.buf.positionToOffset(Position{Line: line, Col: e.lineVisualWidth(line)})
		default:
			// Cutting the single line that the model contains.
			start = e.buf.positionToOffset(Position{Line: line})
			end = e.buf.positionToOffset(Position{Line: line, Col: e.lineVisualWidth(line)})
		}
		edits = append(edits, document.Edit{Start: start, End: end})
		last = &edits[len(edits)-1]
	}
	merged := edits[:0]
	for _, edit := range edits {
		if edit.Start == edit.End {
			continue
		}
		if n := len(merged); n > 0 && edit.Start <= merged[n-1].End {
			merged[n-1].End = max(merged[n-1].End, edit.End)
			continue
		}
		merged = append(merged, edit)
	}
	if len(merged) == 0 {
		return e.writeClipboard(text, linewise)
	}
	e.CancelSnippet()
	e.DismissGhostText()
	e.BreakUndoGroup()
	// ReplaceCommand.computeCursorState: each caret lands where its deleted
	// range started.
	err := e.applyMappedCaretEdits(merged, spans, func(s selectionSpan) SourceSelection {
		a, _ := s.bounds()
		for _, edit := range merged {
			if a >= edit.Start && a <= edit.End {
				a = edit.Start
				break
			}
		}
		off := remapLinkedOffset(a, merged)
		return SourceSelection{off, off}
	})
	e.BreakUndoGroup()
	if err != nil {
		return selectionNotice(err)
	}
	return e.writeClipboard(text, linewise)
}

// pasteOnNewLine is _simplePaste's whole-line case: the text came from an
// empty-selection copy, the caret has no selection and the text is one line.
func (e *Editor) pasteOnNewLine(text string) bool {
	if !e.clipboardLines().matches(text) || e.HasMultipleCarets() || e.HasSelection() {
		return false
	}
	text = ui.PastedLineBreaks(text)
	return strings.IndexByte(text, '\n') == len(text)-1
}

// pasteLineAbove inserts a copied line at the start of the caret's line and
// keeps the caret on its text (ReplaceCommandThatPreservesSelection).
func (e *Editor) pasteLineAbove(text string) {
	start := e.buf.positionToOffset(Position{Line: e.cursor.Line})
	caret := e.buf.positionToOffset(e.cursor)
	edits := []document.Edit{{Start: start, End: start, Text: text}}
	spans := e.selectionSpans()
	_ = e.applyMappedCaretEdits(edits, spans, func(selectionSpan) SourceSelection {
		off := caret + len(text)
		return SourceSelection{off, off}
	})
}
