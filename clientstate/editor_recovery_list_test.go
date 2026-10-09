package clientstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestEditorRecoveryListingBoundedPrivateAndStageCAS(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "private.db"))
	a := attach(t, s, "profile")
	b := attach(t, s, "other")
	ctx := context.Background()
	raw := json.RawMessage(`{"Version":1,"draft":"private"}`)
	save := func(owner Attachment, workspace, path string) EditorRecoveryRecord {
		t.Helper()
		r, err := s.SaveEditorRecovery(ctx, owner, EditorRecoveryKey{owner.ClientID, workspace, path}, 0, raw)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	backup := save(a, "workspace", "/original#discard-backup-1")
	save(a, "workspace", "/second#discard-backup-2")
	save(a, "other-workspace", "/excluded")
	save(b, "workspace", "/excluded")
	dead := save(a, "workspace", "/dead")
	if _, err := s.ClearEditorRecovery(ctx, a, dead.Key, dead.Revision); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListEditorRecoveries(ctx, a, "workspace")
	if err != nil || len(items) != 2 || items[0].Bytes != int64(len(raw)) || items[0].Epoch != a.Epoch {
		t.Fatalf("listing: %+v %v", items, err)
	}
	source := EditorRecoveryKey{a.ClientID, "workspace", "/original"}
	staged, err := s.StageEditorRecovery(ctx, a, backup, source, 0)
	if err != nil || string(staged.Payload) != string(raw) {
		t.Fatalf("stage: %+v %v", staged, err)
	}
	if _, err = s.StageEditorRecovery(ctx, a, backup, source, staged.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrote unread: %v", err)
	}
	if _, err = s.ClearEditorRecovery(ctx, a, source, staged.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEditorRecovery(ctx, a, backup.Key, backup.Revision, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StageEditorRecovery(ctx, a, backup, source, staged.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale backup: %v", err)
	}
	for i := 0; i < 130; i++ {
		save(a, "workspace", fmt.Sprintf("/limit/%03d", i))
	}
	items, err = s.ListEditorRecoveries(ctx, a, "workspace")
	if err != nil || len(items) != MaxEditorRecoveryRecords {
		t.Fatalf("bound: %d %v", len(items), err)
	}
	if err = s.Release(ctx, a.ClientID, a.Epoch); err != nil {
		t.Fatal(err)
	}
	_ = attach(t, s, a.ClientID)
	if _, err = s.ListEditorRecoveries(ctx, a, "workspace"); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale listing: %v", err)
	}
	if _, err = s.StageEditorRecovery(ctx, a, backup, source, 2); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale staging: %v", err)
	}
}
