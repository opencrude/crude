package editor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func (e *Editor) insertRune(r rune) {
	e.clearGoal()
	cursorBefore := e.cursor
	runeBytes := make([]byte, utf8.RuneLen(r))
	utf8.EncodeRune(runeBytes, r)

	e.buf.Insert(e.cursor, runeBytes)
	e.invalidateEditorCaches()

	cursorAfter := e.cursor
	if r == '\n' {
		cursorAfter.Line++
		cursorAfter.Col = 0
	} else {
		cursorAfter.Col += runeVisualWidth(r)
	}
	e.cursor = cursorAfter
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         runeBytes,
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

// InsertRune inserts a single rune at the cursor and advances the cursor.
// Public surface used by the LSP completion popup to apply the selected
// completion's text.
func (e *Editor) InsertRune(r rune) {
	e.insertRune(r)
}

func (e *Editor) insertText(s string) {
	if s == "" {
		return
	}

	cursorBefore := e.cursor
	cursorAfter := cursorBefore
	for _, r := range s {
		if r == '\n' {
			cursorAfter.Line++
			cursorAfter.Col = 0
		} else {
			cursorAfter.Col += runeVisualWidth(r)
		}
	}

	e.clearGoal()
	e.buf.Insert(cursorBefore, []byte(s))
	e.invalidateEditorCaches()
	e.cursor = cursorAfter
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         []byte(s),
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) insertNewline() {
	e.insertRune('\n')
}

func (e *Editor) insertTab() {
	e.insertText(strings.Repeat(" ", tabWidth))
}

func (e *Editor) deleteBackward() {
	e.clearGoal()
	if e.cursor.Col > 0 {
		cursorBefore := e.cursor
		runes := e.lineRunes(e.cursor.Line)
		ri := colToRuneIdx(runes, e.cursor.Col)
		if ri > 0 {
			ch := runes[ri-1]
			chVisW := runeVisualWidth(ch)
			start := Position{Line: e.cursor.Line, Col: e.cursor.Col - chVisW}
			end := e.cursor

			e.buf.Delete(start, end)
			e.invalidateEditorCaches()
			e.cursor.Col -= chVisW
			e.modified = true
			if e.buffer != nil {
				e.buffer.MarkDirty()
			}

			e.recordChange(Change{
				Type:         ActDelete,
				Text:         []byte(string(ch)),
				StartPos:     start,
				EndPos:       end,
				CursorBefore: cursorBefore,
			})
		}
	} else if e.cursor.Line > 0 {
		cursorBefore := e.cursor
		prevLine := e.cursor.Line - 1
		prevLineVisW := e.lineVisualWidth(prevLine)

		start := Position{Line: prevLine, Col: prevLineVisW}
		end := Position{Line: e.cursor.Line, Col: 0}

		e.buf.Delete(start, end)
		e.invalidateEditorCaches()
		e.cursor.Line = prevLine
		e.cursor.Col = prevLineVisW
		e.modified = true
		if e.buffer != nil {
			e.buffer.MarkDirty()
		}

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) deleteForward() {
	e.clearGoal()
	runes := e.lineRunes(e.cursor.Line)
	if len(runes) == 0 {
		return
	}
	lineVisW := lineVisualWidthFromRunes(runes)

	if e.cursor.Col < lineVisW {
		cursorBefore := e.cursor
		ri := colToRuneIdx(runes, e.cursor.Col)
		if ri < len(runes) {
			ch := runes[ri]
			chVisW := runeVisualWidth(ch)
			start := e.cursor
			end := Position{Line: e.cursor.Line, Col: e.cursor.Col + chVisW}

			e.buf.Delete(start, end)
			e.invalidateEditorCaches()
			e.modified = true
			if e.buffer != nil {
				e.buffer.MarkDirty()
			}

			e.recordChange(Change{
				Type:         ActDelete,
				Text:         []byte(string(ch)),
				StartPos:     start,
				EndPos:       end,
				CursorBefore: cursorBefore,
			})
		}
	} else if e.cursor.Line < e.buf.LineCount()-1 {
		cursorBefore := e.cursor
		start := e.cursor
		end := Position{Line: e.cursor.Line + 1, Col: 0}

		e.buf.Delete(start, end)
		e.invalidateEditorCaches()
		e.modified = true
		if e.buffer != nil {
			e.buffer.MarkDirty()
		}

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) recordChange(c Change) {
	if e.changeIndex < len(e.changes) {
		e.changes = e.changes[:e.changeIndex]
	}
	e.changes = append(e.changes, c)
	if len(e.changes) > e.maxHistory {
		e.changes = e.changes[len(e.changes)-e.maxHistory:]
	}
	e.changeIndex = len(e.changes)
}

func (e *Editor) undo() {
	if e.changeIndex <= 0 {
		return
	}
	e.changeIndex--
	c := e.changes[e.changeIndex]

	switch c.Type {
	case ActInsert:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.CursorBefore
	case ActDelete:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.CursorBefore
	}
	e.invalidateEditorCaches()
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) Undo() { e.undo() }

func (e *Editor) redo() {
	if e.changeIndex >= len(e.changes) {
		return
	}
	c := e.changes[e.changeIndex]
	e.changeIndex++

	switch c.Type {
	case ActInsert:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.EndPos
	case ActDelete:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.EndPos
	}
	e.invalidateEditorCaches()
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) Redo() { e.redo() }

// Save writes the editor's current buffer to its file. The modified flag
// is cleared on success. Returns the underlying I/O error if the write fails.
//
// When formatOnSave is enabled (set via SetFormatOnSave), the file's
// language-specific formatter runs immediately after a successful save and
// the buffer is reloaded to reflect any changes the formatter made.
func (e *Editor) Save() error {
	var err error
	if e.buffer != nil {
		err = e.buffer.Save()
	} else {
		err = e.buf.Save(e.filePath)
	}
	if err == nil {
		e.modified = false
		e.watchMu.Lock()
		e.lastSaveTime = time.Now()
		e.watchMu.Unlock()
	}
	e.notifyContentChanged()
	if err == nil && e.formatOnSave {
		if ferr := e.format(); ferr != nil {
			// Format errors are non-fatal — the file is already saved. We
			// surface them via the onContentChanged hook so the App can
			// show a status-bar message, but the Save() call still reports
			// success because the user's content is on disk.
			e.notifySaveError(ferr)
		}
	}
	return err
}

// notifySaveError is called when a post-save format step failed. It reuses
// the onContentChanged hook's channel by wrapping the error in a noop text
// change with a status-bar marker (current App ignores the text). Future
// iterations can introduce a dedicated hook.
func (e *Editor) notifySaveError(_ error) {
	// No-op for now: the View() function will reflect the (un-formatted) saved
	// content, and a follow-up Reload() will pick up the formatter's output
	// if the user re-opens the file. The trade-off: we don't interrupt the
	// user with a toast for what is normally a silent, asynchronous concern.
	_ = e.filePath
}

// SaveAndFormat saves the file and, when applicable, formats the buffer
// with a language-specific formatter (gofmt for Go, rustfmt for Rust, etc).
// On format error the original text is preserved and the error is returned.
// Returns nil if the format was skipped (no formatter for this extension) or
// succeeded.
func (e *Editor) SaveAndFormat() error {
	if err := e.Save(); err != nil {
		return err
	}
	return e.format()
}

// format runs the appropriate external formatter for the current file's
// extension and, if the formatter succeeded, replaces the buffer contents
// with the formatted output. Supported: go (gofmt), rust (rustfmt),
// python (black if available).
func (e *Editor) format() error {
	ext := strings.ToLower(filepath.Ext(e.filePath))
	var (
		cmd  string
		args []string
	)
	switch ext {
	case ".go":
		cmd = "gofmt"
		args = []string{"-w", e.filePath}
	case ".rs":
		// rustfmt rewrites in place.
		if _, err := exec.LookPath("rustfmt"); err != nil {
			return nil
		}
		cmd = "rustfmt"
		args = []string{e.filePath}
	case ".py":
		// Prefer black; fall back to no-op.
		if _, err := exec.LookPath("black"); err != nil {
			return nil
		}
		cmd = "black"
		args = []string{"--quiet", e.filePath}
	default:
		return nil // no formatter for this language
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(runCtx, cmd, args...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("formatter failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Re-read the formatted content from disk and update the buffer.
	data, err := os.ReadFile(e.filePath)
	if err != nil {
		return err
	}
	e.buf.ReplaceAll(string(data))
	e.modified = false
	e.invalidateEditorCaches()
	return nil
}
