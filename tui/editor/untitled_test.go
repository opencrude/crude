package editor

import (
	"crypto/sha256"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/tui/theme"
)

func TestUntitledProjectionAcceptsPublicationAndRetainsPrivateRecovery(t *testing.T) {
	s := document.Snapshot{ID: "untitled:ABCDEFGHIJKLMNOPQRSTUVWXYZ", Name: "Untitled-1",
		Revision: 1, DiskHash: sha256.Sum256(nil)}
	ed := New(theme.DefaultDark())
	t.Cleanup(ed.Close)
	if err := ed.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	if ed.FilePath() != "" || ed.BaseTitle() != "Untitled-1" || ed.Modified() {
		t.Fatalf("wrong unnamed presentation: path=%q title=%q dirty=%v", ed.FilePath(), ed.BaseTitle(), ed.Modified())
	}
	if err := ed.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	state := s
	plan, err := ed.PrepareDocumentUpdate(projectionOwner, 0, nil, state, &s, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.CommitDocumentUpdate(plan); err != nil {
		t.Fatal(err)
	}
	ed.InsertRune('x')
	recovery, unresolved := ed.ExportDocumentRecovery()
	if !unresolved || recovery.Validate() != nil || string(recovery.OptimisticText) != "x" {
		t.Fatalf("unnamed private draft cannot be recovered: %+v", recovery)
	}
	other := New(theme.DefaultDark())
	t.Cleanup(other.Close)
	if err := other.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	if err := other.RestoreDocumentRecovery(recovery); err != nil || other.Value() != "x" {
		t.Fatalf("could not restore unnamed draft: value=%q error=%v", other.Value(), err)
	}
	manager := NewBufferManager()
	if manager.InstallPrepared(ed.DocumentBuffer()) != ed.DocumentBuffer() || manager.Get(s.ID) != ed.DocumentBuffer() {
		t.Fatal("manager did not index pathless buffer by identity")
	}
	path := filepath.Join(t.TempDir(), "saved.txt")
	if err := manager.Retarget(s.ID, path); err != nil || manager.Get(path) != ed.DocumentBuffer() || manager.Get(s.ID) != nil {
		t.Fatalf("first save could not retarget the existing buffer: %v", err)
	}
}

func TestUntitledEmptyFirstSaveUpdatesPeerDiskBaseline(t *testing.T) {
	s := document.Snapshot{ID: "untitled:ABCDEFGHIJKLMNOPQRSTUVWXYZ", Name: "Untitled-1", Revision: 1, DiskHash: sha256.Sum256(nil)}
	ed := New(theme.DefaultDark())
	t.Cleanup(ed.Close)
	if err := ed.InstallDocument(s); err != nil {
		t.Fatal(err)
	}
	if err := ed.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	s.Path = filepath.Join(t.TempDir(), "empty.txt")
	s.DiskExists = true
	plan, err := ed.PrepareDocumentUpdate(projectionOwner, 1, nil, s, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.CommitDocumentUpdate(plan); err != nil {
		t.Fatal(err)
	}
	if snapshot := ed.BufferSnapshot(); !snapshot.ExpectedExists || ed.DiskGeneration() == 0 {
		t.Fatalf("empty save did not update the peer's disk baseline: %+v", snapshot)
	}
}
