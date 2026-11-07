package clientstate

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// Two concurrent clients keep their own choices; a new client starts from the
// latest one; a stale epoch of the same profile cannot overwrite it.
func TestViewMemoryIsPrivatePerClientAndSeedsNewClients(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	left, right := attach(t, s, "left-terminal"), attach(t, s, "right-terminal")
	if _, _, err := s.LoadViewMemory(ctx, left, "workspace"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first load: %v", err)
	}
	if err := s.SaveViewMemory(ctx, left, "workspace", json.RawMessage(`{"minimized":["a"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveViewMemory(ctx, right, "workspace", json.RawMessage(`{"minimized":[]}`)); err != nil {
		t.Fatal(err)
	}
	got, seeded, err := s.LoadViewMemory(ctx, left, "workspace")
	if err != nil || seeded || string(got) != `{"minimized":["a"]}` {
		t.Fatalf("left kept %s seeded=%v err=%v", got, seeded, err)
	}
	fresh := attach(t, s, "new-terminal")
	got, seeded, err = s.LoadViewMemory(ctx, fresh, "workspace")
	if err != nil || !seeded || string(got) != `{"minimized":[]}` {
		t.Fatalf("new client seed %s seeded=%v err=%v", got, seeded, err)
	}
	if _, _, err := s.LoadViewMemory(ctx, fresh, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another workspace leaked a record: %v", err)
	}
	if err := s.Release(ctx, left.ClientID, left.Epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveViewMemory(ctx, left, "workspace", json.RawMessage(`{}`)); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("released epoch wrote: %v", err)
	}
	if err := s.SaveViewMemory(ctx, right, "workspace", json.RawMessage(`not json`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid payload: %v", err)
	}
}
