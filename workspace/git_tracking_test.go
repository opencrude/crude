package workspace

import (
	"path/filepath"
	"testing"
)

// An ordinary status read must see both local and fetched changes immediately.
func TestGitSnapshotTrackingFollowsGitStatus(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)
	read := func(wantAhead, wantBehind int) {
		t.Helper()
		snapshot, err := service.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Ahead != wantAhead || snapshot.Behind != wantBehind {
			t.Fatalf("snapshot ↑%d ↓%d, want ↑%d ↓%d", snapshot.Ahead, snapshot.Behind, wantAhead, wantBehind)
		}

	}
	read(1, 0)
	read(1, 0)

	writeFile(t, filepath.Join(ours, "app.txt"), "header\nvalue = 4\nfooter\n")
	gitMust(t, ours, "commit", "-am", "external commit")
	read(2, 0) // HEAD moved outside Crude.

	if err := service.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	read(2, 1) // The upstream moved.
	read(2, 1)
}
