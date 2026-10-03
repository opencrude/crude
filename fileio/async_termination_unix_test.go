//go:build !windows

package fileio

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKillRefStopsOwnedDescendantsButNotOtherGroups(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	directory := t.TempDir()
	selectedFile, neighborFile := filepath.Join(directory, "selected"), filepath.Join(directory, "neighbor")
	selected, err := manager.Start("selected-tree", fmt.Sprintf(`(sleep 1; printf selected > %q) & wait`, selectedFile), directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start("other-tree", fmt.Sprintf(`sleep 1; printf neighbor > %q`, neighborFile), directory, 0); err != nil {
		t.Fatal(err)
	}
	if err := manager.KillRef(selected.Ref()); err != nil {
		t.Fatal(err)
	}
	if info := waitAsyncCommand(t, manager, selected.Handle); info.Status != AsyncKilled {
		t.Fatalf("selected tree status=%s", info.Status)
	}
	if info := waitAsyncCommand(t, manager, "other-tree"); info.Status != AsyncCompleted {
		t.Fatalf("independent group was affected: %+v", info)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(selectedFile); !os.IsNotExist(err) {
		t.Fatalf("terminated descendant remained alive: %v", err)
	}
	if data, err := os.ReadFile(neighborFile); err != nil || string(data) != "neighbor" {
		t.Fatalf("unrelated command did not finish: %q, %v", data, err)
	}
}
