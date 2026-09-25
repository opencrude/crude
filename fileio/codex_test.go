package fileio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/codexio"
)

func TestCodexMoveOverwriteSequentialReceipts(t *testing.T) {
	ed := newMutationEditor(t, "codex-move")
	root := t.TempDir()
	for path, text := range map[string]string{"source": "one\r\ntwo\r\n", "dest": "destination\n", "deleted": "remove\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	patch, err := codexio.Parse("*** Begin Patch\n*** Update File: source\n*** Move to: dest\n@@\n one\n-two\n+TWO\n*** Add File: nested/new\n+hello\n*** Update File: ./nested/new\n@@\n-hello\n+world\n*** Delete File: deleted\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	var receipts []MutationReceipt
	committed := 0
	changes, err := ed.ApplyCodexPatch(t.Context(), root, patch, codexio.PreserveLineEndings, func(r MutationReceipt) (MutationComplete, error) {
		if r.OperationName != "apply_patch" {
			t.Fatalf("wrong history attribution: %s", r.OperationName)
		}
		receipts = append(receipts, r)
		return func(_ string, ok bool) error {
			if ok {
				committed++
			}
			return nil
		}, nil
	})
	if err != nil || committed != 5 || len(changes) != 5 {
		t.Fatalf("changes %v commits %d err %v", changes, committed, err)
	}
	if string(receipts[0].Paths[0].Before.Bytes) != "destination\n" {
		t.Fatal("move lost destination preimage")
	}
	if string(receipts[1].Paths[0].Before.Bytes) != "one\r\ntwo\r\n" {
		t.Fatal("move lost source preimage")
	}
	for path, want := range map[string]string{"dest": "one\r\nTWO\r\n", "nested/new": "world\n"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"source", "deleted"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("not removed: %s %v", path, err)
		}
	}
}

func TestCodexPartialFailureTrackedAndExternalPaths(t *testing.T) {
	ed := newMutationEditor(t, "codex-partial")
	root, external := t.TempDir(), filepath.Join(t.TempDir(), "external")
	patch, err := codexio.Parse("*** Begin Patch\n*** Add File: " + external + "\n+outside\n*** Update File: missing\n-old\n+new\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ed.ApplyCodexPatch(t.Context(), root, patch, codexio.PreserveLineEndings, nil)
	if err == nil || len(changes) != 1 || changes[0].Path != external {
		t.Fatalf("partial: %+v %v", changes, err)
	}
	got, err := os.ReadFile(external)
	if err != nil || string(got) != "outside\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestCodexHistoryFailureAndCancellation(t *testing.T) {
	ed := newMutationEditor(t, "codex-fail")
	root := t.TempDir()
	patch, err := codexio.Parse("*** Begin Patch\n*** Add File: new/file\n+x\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("history failed")
	changes, err := ed.ApplyCodexPatch(t.Context(), root, patch, codexio.PreserveLineEndings, func(MutationReceipt) (MutationComplete, error) { return nil, failure })
	if !errors.Is(err, failure) || len(changes) != 0 {
		t.Fatalf("%v %v", changes, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatal("failed prepare created directories")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	changes, err = ed.ApplyCodexPatch(ctx, root, patch, codexio.PreserveLineEndings, nil)
	if !errors.Is(err, context.Canceled) || len(changes) != 0 {
		t.Fatalf("cancelled patch: %v %v", changes, err)
	}
}

func TestCodexPreservesNULTextAndOverwritesBinaryPreimage(t *testing.T) {
	ed := newMutationEditor(t, "codex-nul")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte{0xff, 0}, 0600); err != nil {
		t.Fatal(err)
	}
	patch, err := codexio.Parse("*** Begin Patch\n*** Add File: a\n+hello\x00world\n*** Update File: a\n-hello\x00world\n+new\x00text\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ed.ApplyCodexPatch(t.Context(), root, patch, codexio.PreserveLineEndings, nil)
	if err != nil || len(changes) != 2 {
		t.Fatalf("%v %v", changes, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "a"))
	if err != nil || string(got) != "new\x00text\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestCodexMoveToSamePathFollowsUpstream(t *testing.T) {
	ed := newMutationEditor(t, "codex-self")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch, err := codexio.Parse("*** Begin Patch\n*** Update File: a\n*** Move to: ./a\n-old\n+new\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	changes, err := ed.ApplyCodexPatch(t.Context(), root, patch, codexio.PreserveLineEndings, nil)
	if err != nil || len(changes) != 2 {
		t.Fatalf("%v %v", changes, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Fatalf("upstream writes then deletes on self-move: %v", err)
	}
}
