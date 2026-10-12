package fileio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/logger"
)

func newMutationEditor(t *testing.T, id string) *Editor {
	t.Helper()
	root := t.TempDir()
	log, err := logger.New(logger.Options{HomeDir: root, SessionID: id, SessionsDir: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return NewEditor(id, log)
}

func TestWriteTrackedPreparesExactReceiptBeforeAnyMutation(t *testing.T) {
	editor := newMutationEditor(t, "mutation-write")
	root := t.TempDir()
	target := filepath.Join(root, "new", "nested", "file.txt")
	var receipt MutationReceipt
	err := editor.WriteTracked(target, "agent content", func(got MutationReceipt) (MutationComplete, error) {
		receipt = got
		if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
			t.Fatalf("parent mutated before WAL callback: %v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("file mutated before WAL callback: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OperationID == "" || receipt.OperationName != "write_file" || len(receipt.Paths) != 3 {
		t.Fatalf("receipt=%+v", receipt)
	}
	file := receipt.Paths[len(receipt.Paths)-1]
	if file.Path != target || file.Before.Kind != MutationAbsent || file.After.Kind != MutationRegular || string(file.After.Bytes) != "agent content" {
		t.Fatalf("file receipt=%+v", file)
	}
}

func TestMutationPrepareFailureLeavesWorkspaceByteExact(t *testing.T) {
	editor := newMutationEditor(t, "mutation-failure")
	root := t.TempDir()
	target := filepath.Join(root, "new", "file.txt")
	wantErr := errors.New("history unavailable")
	err := editor.WriteTracked(target, "must not exist", func(MutationReceipt) (MutationComplete, error) { return nil, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatalf("callback failure created parent: %v", err)
	}
}

func TestTrackedWriteRejectsChangeAfterWALCommit(t *testing.T) {
	editor := newMutationEditor(t, "mutation-compare")
	target := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	completed, committed := false, true
	err := editor.WriteTracked(target, "agent", func(MutationReceipt) (MutationComplete, error) {
		if err := os.WriteFile(target, []byte("external after WAL"), 0o644); err != nil {
			return nil, err
		}
		return func(_ string, didCommit bool) error { completed, committed = true, didCommit; return nil }, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed since the operation receipt") {
		t.Fatalf("write error=%v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := string(data); got != "external after WAL" {
		t.Fatalf("compare-and-commit overwrote external content: %q", got)
	}
	if !completed || committed {
		t.Fatalf("completion completed=%v committed=%v", completed, committed)
	}
}

func TestMoveAndCopyReceiptsPreserveDestinationPreimages(t *testing.T) {
	editor := newMutationEditor(t, "mutation-move-copy")
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(source, []byte("source bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	var copyReceipt MutationReceipt
	if err := editor.CopyTracked(source, destination, func(got MutationReceipt) (MutationComplete, error) { copyReceipt = got; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if len(copyReceipt.Paths) != 1 || string(copyReceipt.Paths[0].Before.Bytes) != "destination bytes" || string(copyReceipt.Paths[0].After.Bytes) != "source bytes" {
		t.Fatalf("copy receipt=%+v", copyReceipt)
	}
	moveDestination := filepath.Join(root, "moved")
	if err := os.WriteFile(moveDestination, []byte("old move destination"), 0o644); err != nil {
		t.Fatal(err)
	}
	var moveReceipt MutationReceipt
	if err := editor.MoveTracked(source, moveDestination, func(got MutationReceipt) (MutationComplete, error) { moveReceipt = got; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if len(moveReceipt.Paths) != 2 || moveReceipt.Paths[0].After.Kind != MutationAbsent || string(moveReceipt.Paths[1].Before.Bytes) != "old move destination" || string(moveReceipt.Paths[1].After.Bytes) != "source bytes" {
		t.Fatalf("move receipt=%+v", moveReceipt)
	}
}

func TestDeleteTrackedReceiptContainsRecoverablePreimage(t *testing.T) {
	editor := newMutationEditor(t, "mutation-delete")
	target := filepath.Join(t.TempDir(), "delete.txt")
	if err := os.WriteFile(target, []byte("recover me"), 0o644); err != nil {
		t.Fatal(err)
	}
	var receipt MutationReceipt
	if err := editor.DeleteTracked(target, func(got MutationReceipt) (MutationComplete, error) { receipt = got; return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Paths) != 1 || string(receipt.Paths[0].Before.Bytes) != "recover me" || receipt.Paths[0].After.Kind != MutationAbsent {
		t.Fatalf("delete receipt=%+v", receipt)
	}
}

func TestSelectiveMutationRejectsHardlinkedFile(t *testing.T) {
	editor := newMutationEditor(t, "mutation-hardlink")
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("linked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if err := editor.WriteTracked(second, "replacement", nil); err == nil || !strings.Contains(err.Error(), "cannot preserve link topology") {
		t.Fatalf("hardlink mutation error=%v", err)
	}
	for _, path := range []string{first, second} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "linked" {
			t.Fatalf("%s=%q err=%v", path, data, err)
		}
	}
}

func TestTrackedWriteRejectsParentSymlinkSwapAfterWAL(t *testing.T) {
	editor := newMutationEditor(t, "mutation-parent-swap")
	root, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "file")
	outsideTarget := filepath.Join(outside, "file")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideTarget, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "parent-backup")
	err := editor.WriteTracked(target, "agent", func(MutationReceipt) (MutationComplete, error) {
		if err := os.Rename(parent, backup); err != nil {
			return nil, err
		}
		if err := os.Symlink(outside, parent); err != nil {
			_ = os.Rename(backup, parent)
			t.Skipf("symlinks unavailable: %v", err)
		}
		return func(string, bool) error { return nil }, nil
	})
	if err == nil || !strings.Contains(err.Error(), "mutation parent") {
		t.Fatalf("parent swap error=%v", err)
	}
	data, readErr := os.ReadFile(outsideTarget)
	if readErr != nil || string(data) != "before" {
		t.Fatalf("write escaped through swapped parent: data=%q err=%v", data, readErr)
	}
}
