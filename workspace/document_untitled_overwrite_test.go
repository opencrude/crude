package workspace

import (
	"bytes"
	"crude/textencoding"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/fileio"
	"crude/localhistory"
)

func TestUntitledOverwriteRequiresExactReviewAndKeepsRecoverablePreimage(t *testing.T) {
	f := newReviewedFixture(t)
	target := filepath.Join(filepath.Dir(f.path), "target.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	draft, err := f.documents.CreateUntitled(f.owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	edit := documentEdit(t, f.documents, f.owner, draft.ID, "draft-edit", draft.Revision, 0, 0, "after\n")
	preview, err := f.documents.PrepareUntitledOverwrite(t.Context(), f.owner, draft.ID, edit.Revision, target, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || preview.Files[0].Path != target || preview.Text == "" {
		t.Fatalf("review omitted the exact target or changes: %+v", preview)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "before\n" {
		t.Fatalf("preparing a review changed the target: %q %v", data, err)
	}
	foreign := document.Owner{ClientID: "other", Epoch: 1}
	if result := f.documents.ApplyReviewedEdit(t.Context(), foreign, preview.ID, preview.Digest, f.store, f.files); result.State != "refused" {
		t.Fatalf("another owner applied this review: %+v", result)
	}
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files)
	if result.State != "committed" || result.SafetyReference != preview.ID || result.SafetySnapshotID == "" {
		t.Fatalf("approved review did not commit with a safety snapshot: %+v", result)
	}
	saved, err := f.documents.Snapshot(draft.ID)
	if err != nil || saved.Path != target || saved.Dirty || !bytes.Equal(saved.Text, []byte("after\n")) {
		t.Fatalf("save did not preserve the draft identity: %+v %v", saved, err)
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "after\n" {
		t.Fatalf("approved replacement wrote wrong bytes: %q %v", data, err)
	}
	safety, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID)
	if err != nil || safety.Entries["target.txt"].Hash != reviewedHash("before\n") {
		t.Fatalf("replacement did not retain the exact old file: %+v %v", safety, err)
	}
	plan, err := f.store.PlanRestorePaths(t.Context(), safety.ID, localhistory.SnapshotOptions{DisableGitIgnore: true}, []string{"target.txt"})
	if err != nil || len(plan.Changes) != 1 {
		t.Fatalf("the old file has no restore path: %+v %v", plan, err)
	}
	if replay := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files); replay != result {
		t.Fatalf("replaying the review changed its original outcome: %+v", replay)
	}
	if detail, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID); err != nil || !detail.CanRelease || detail.OriginalState != "committed" {
		t.Fatalf("replacement safety evidence cannot be inspected: %+v %v", detail, err)
	}
	if err := f.documents.WithHistoryMutation(t.Context(), []string{target}, func() error {
		_, err := f.store.ApplyRestoreChoices(t.Context(), plan.ID, []localhistory.RestoreChoice{{Path: "target.txt"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if restored, err := f.documents.Snapshot(draft.ID); err != nil || string(restored.Text) != "before\n" {
		t.Fatalf("restoring the preimage bypassed the document owner: %+v %v", restored, err)
	}
}

func TestUntitledOverwriteUnconfirmedCommitRetainsDraftAndSafety(t *testing.T) {
	f := newReviewedFixture(t)
	target := filepath.Join(filepath.Dir(f.path), "target.txt")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft, err := f.documents.CreateUntitled(f.owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	edit := documentEdit(t, f.documents, f.owner, draft.ID, "draft-edit", draft.Revision, 0, 0, "after")
	preview, err := f.documents.PrepareUntitledOverwrite(t.Context(), f.owner, draft.ID, edit.Revision, target, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	f.files.commitWorkspaceEdit = func(from, to string) error {
		if err := fileio.CommitAtomicFile(from, to); err != nil {
			return err
		}
		return errors.New("injected unknown result after the disk effect")
	}
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files)
	if result.State != "unconfirmed" {
		t.Fatalf("uncertain write was reported as settled: %+v", result)
	}
	if _, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID); err != nil {
		t.Fatal("uncertain replacement lost its retained preimage:", err)
	}
	snap, err := f.documents.Snapshot(draft.ID)
	if err != nil || snap.Path != "" || string(snap.Text) != "after" || !snap.Conflict {
		t.Fatalf("uncertain replacement lost the draft or its conflict fence: %+v %v", snap, err)
	}
	if again := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files); again.State != "unconfirmed" {
		t.Fatalf("uncertain review was replayed: %+v", again)
	}
	db := f.documents.stateDB
	root := f.documents.root
	f.documents.Close()
	restarted := NewDocumentService(root, nil, nil, db)
	defer restarted.Close()
	snap, err = restarted.OpenUntitled(f.owner, draft.ID)
	if err != nil || snap.Path != "" || string(snap.Text) != "after" || !snap.Conflict {
		t.Fatalf("restart lost the draft or uncertain-save fence: %+v %v", snap, err)
	}
	if again, err := restarted.InspectReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest); err != nil || again.State != "unconfirmed" {
		t.Fatalf("restart lost the original uncertain receipt: %+v %v", again, err)
	}
}

func TestUntitledOverwriteRefusesChangedTargetAndDraft(t *testing.T) {
	for _, change := range []string{"target", "draft", "replacement", "opened"} {
		t.Run(change, func(t *testing.T) {
			f := newReviewedFixture(t)
			target := filepath.Join(filepath.Dir(f.path), "target.txt")
			if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
				t.Fatal(err)
			}
			draft, err := f.documents.CreateUntitled(f.owner, textencoding.UTF8)
			if err != nil {
				t.Fatal(err)
			}
			edit := documentEdit(t, f.documents, f.owner, draft.ID, "first", draft.Revision, 0, 0, "draft")
			preview, err := f.documents.PrepareUntitledOverwrite(t.Context(), f.owner, draft.ID, edit.Revision, target, textencoding.UTF8)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "target":
				if err := os.WriteFile(target, []byte("later"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "draft":
				documentEdit(t, f.documents, f.owner, draft.ID, "later", edit.Revision, 0, 0, "x")
			case "replacement":
				if err := os.Rename(target, target+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "opened":
				if _, err := f.documents.Open(document.Owner{ClientID: "peer", Epoch: 1}, target); err != nil {
					t.Fatal(err)
				}
			}
			result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files)
			if result.State != "refused" || result.Error == "" {
				t.Fatalf("stale review was not refused: %+v", result)
			}
			data, err := os.ReadFile(target)
			expected := "before"
			if change == "target" {
				expected = "later"
			}
			if err != nil || string(data) != expected {
				t.Fatalf("stale review overwrote later work: %q %v", data, err)
			}
			current, err := f.documents.Snapshot(draft.ID)
			if err != nil || current.Path != "" || !current.Dirty {
				t.Fatalf("refusal lost the unnamed draft: %+v %v", current, err)
			}
		})
	}
}
