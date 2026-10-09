package clientstate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestEditorRecoveryReopenEpochCASAndClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := attach(t, s, "profile")
	k := EditorRecoveryKey{a.ClientID, "workspace", "/project/long/file"}
	r, err := s.SaveEditorRecovery(ctx, a, k, 0, json.RawMessage(`{"Version":1,"draft":"unresolved"}`))
	if err != nil || r.Revision != 1 {
		t.Fatalf("save: %+v %v", r, err)
	}
	if _, err = s.SaveEditorRecovery(ctx, a, k, 0, r.Payload); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.Release(ctx, a.ClientID, a.Epoch); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	b := attach(t, s, a.ClientID)
	if _, err = s.LoadEditorRecovery(ctx, a, k); !errors.Is(err, ErrStaleEpoch) {
		t.Fatal(err)
	}
	got, err := s.LoadEditorRecovery(ctx, b, k)
	if err != nil || string(got.Payload) != string(r.Payload) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if _, err = s.ClearEditorRecovery(ctx, b, k, 0); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	cleared, err := s.ClearEditorRecovery(ctx, b, k, got.Revision)
	if err != nil || cleared.Revision != 2 {
		t.Fatal(err)
	}
	got, err = s.LoadEditorRecovery(ctx, b, k)
	if err != nil || len(got.Payload) != 0 || got.Revision != 2 {
		t.Fatalf("clear: %+v %v", got, err)
	}
	if _, err = s.SaveEditorRecovery(ctx, b, k, 1, r.Payload); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
