package editor

import (
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/rendering"
	"crude/tui/ui"
)

// Clipboard commands are a port of VS Code's editor clipboard with its default
// editor.emptySelectionClipboard (src/vs/editor/contrib/clipboard/browser/
// clipboard.ts, common/viewModel/viewModelImpl.ts getPlainTextToCopy,
// common/cursor/cursorDeleteOperations.ts cut and cursorTypeEditOperations.ts
// _simplePaste): with no selection, Copy and Cut take the caret's whole line,
// and pasting that line inserts it above the caret's line.

// clipboardLineCopy is VS Code's InMemoryClipboardMetadataManager: the text of
// the last copy that came from an empty selection. One instance is shared by
// every editor, as VS Code shares one across editors.
var clipboardLineCopy struct {
	sync.Mutex
	text string
	ok   bool
}

func rememberClipboardCopy(text string, fromEmptySelection bool) {
	clipboardLineCopy.Lock()
	clipboardLineCopy.text, clipboardLineCopy.ok = text, fromEmptySelection
	clipboardLineCopy.Unlock()
}

// pastedFromEmptySelection is InMemoryClipboardMetadataManager.get.
func pastedFromEmptySelection(text string) bool {
	clipboardLineCopy.Lock()
	defer clipboardLineCopy.Unlock()
	if clipboardLineCopy.ok && clipboardLineCopy.text == text {
		return true
	}
	clipboardLineCopy.ok = false
	return false
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

// CopyText is getPlainTextToCopy and getDataToCopy: selected text, or the
// whole caret line with its terminator when a caret has no selection. It also
// records the copy for pasteOnNewLine. It never changes the source.
func (e *Editor) CopyText() string {
	if e == nil || e.buf == nil {
		return ""
	}
	spans := e.copySpans()
	empty, nonEmpty := false, false
	for _, s := range spans {
		a, b := s.bounds()
		empty, nonEmpty = empty || a == b, nonEmpty || a != b
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
	text := strings.Join(parts, e.lineEnding())
	rememberClipboardCopy(text, empty && len(spans) == 1)
	return text
}

// copyCmd copies through the shared clipboard owner.
func (e *Editor) copyCmd() tea.Cmd {
	text := e.CopyText()
	if text == "" {
		return nil
	}
	return rendering.CopyToClipboardCmd(text)
}

// cutCmd is VS Code's cut: CopyText, then DeleteOperations.cut as one undo step.
func (e *Editor) cutCmd() tea.Cmd {
	text := e.CopyText()
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
		return rendering.CopyToClipboardCmd(text)
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
	return rendering.CopyToClipboardCmd(text)
}

// pasteOnNewLine is _simplePaste's whole-line case: the text came from an
// empty-selection copy, the caret has no selection and the text is one line.
func (e *Editor) pasteOnNewLine(text string) bool {
	if e.HasMultipleCarets() || e.HasSelection() || !pastedFromEmptySelection(text) {
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
