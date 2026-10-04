package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/fileio"
)

func TestDocumentSaveServiceCommitsExpectedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewDocumentSaveService()
	result := service.Save(context.Background(), DocumentSaveRequest{
		BufferID: "buffer", Path: path, Data: []byte("after\n"), Revision: 7,
		ExpectedHash: sha256.Sum256(before), ExpectedExist: true,
	})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "after\n" || result.DiskHash != sha256.Sum256(got) {
		t.Fatalf("disk=%q hash=%x", got, result.DiskHash)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("mode=%#o want %#o", info.Mode().Perm(), beforeInfo.Mode().Perm())
	}
}

func TestDocumentAutosaveDeadlineCannotSurviveCancelOrReplacement(t *testing.T) {
	s := NewDocumentSaveService()
	t.Cleanup(s.Close)
	s.Schedule("buffer", "file", 1, time.Hour)
	first := s.deadlines["buffer"].epoch
	s.Schedule("buffer", "file", 2, time.Hour)
	if s.AcceptDue(DocumentAutosaveDueMsg{BufferID: "buffer", Epoch: first}) {
		t.Fatal("accepted replaced deadline")
	}
	second := s.deadlines["buffer"].epoch
	s.Cancel("buffer")
	if s.AcceptDue(DocumentAutosaveDueMsg{BufferID: "buffer", Epoch: second}) {
		t.Fatal("accepted cancelled deadline")
	}
}

func TestClosedDocumentServiceCannotWrite(t *testing.T) {
	s := NewDocumentSaveService()
	s.Close()
	path := filepath.Join(t.TempDir(), "file")
	result := s.Save(context.Background(), DocumentSaveRequest{Path: path, Data: []byte("late"), Reason: DocumentSaveManual})
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("error=%v", result.Err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("closed service wrote a file")
	}
}

func TestDocumentSaveCancelsWhileAnotherCrudeMutationOwnsLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	before := []byte("before")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock := fileio.LockTreeMutation()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := NewDocumentSaveService().Save(ctx, DocumentSaveRequest{
		Path: path, Data: []byte("after"), ExpectedExist: true, ExpectedHash: sha256.Sum256(before), Reason: DocumentSaveManual,
	})
	unlock()
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("error=%v", result.Err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before" {
		t.Fatalf("cancelled save wrote %q", data)
	}
}

func TestDocumentSaveServiceRejectsExternalChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	before := []byte("before\n")
	if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := NewDocumentSaveService().Save(context.Background(), DocumentSaveRequest{
		BufferID: "buffer", Path: path, Data: []byte("ours\n"), Revision: 2,
		ExpectedHash: sha256.Sum256(before), ExpectedExist: true,
	})
	if !errors.Is(result.Err, ErrDocumentConflict) {
		t.Fatalf("error=%v want conflict", result.Err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "external\n" {
		t.Fatalf("conflict overwrote disk with %q", got)
	}
}
