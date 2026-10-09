package editor

import (
	"path/filepath"
	"testing"
)

func TestRetargetDocumentPathPreservesPendingProjectionAndNoWatcher(t *testing.T) {
	e, snap := projectionEditor(t, "work")
	e.SetCursorPosition(0, 4)
	e.InsertRune('!')
	projection, buffer := e.documentProjection, e.buffer
	cursor := e.cursor
	target := filepath.Join(t.TempDir(), "renamed.go")
	if err := e.RetargetDocumentPath(target); err != nil {
		t.Fatal(err)
	}
	if e.documentProjection != projection || e.buffer != buffer || e.BufferID() != snap.ID || e.DocumentState().PendingEdits != 1 || e.Value() != "work!" || e.cursor != cursor || e.watcher != nil {
		t.Fatal("retarget replaced document or started watcher")
	}
	if e.FilePath() != target || e.DocumentSnapshot().Path != target || e.buffer.Path != target {
		t.Fatal("path not installed")
	}
	change, err := e.CaptureDocumentEdits(projectionOwner, "edit", "")
	if err != nil || change.DocumentID != snap.ID {
		t.Fatal(change, err)
	}
	if err = e.RetargetDocumentPath("relative"); err == nil {
		t.Fatal("relative canonical path accepted")
	}
}
