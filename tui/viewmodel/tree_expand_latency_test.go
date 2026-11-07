package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Expanding a folder through the real core link is instant: the directory
// read, the reply and the install together stay far under one frame budget
// of human perception. The user saw 800 ms; the ceiling is 100 ms.
func TestConnectedTreeFolderExpandIsInstant(t *testing.T) {
	root := t.TempDir()
	for i := range 6 {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := range 40 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.go", j)), []byte("package p\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, ""), root)
	shells := []remoteJourneyShell{shell}
	runAppCommand(t, shell.app, shell.app.beginClientFileTreeLoad())
	model := shell.app.Tree.Model
	remoteJourneyUntil(t, shells, func() bool { return model.NodeByPath(filepath.Join(root, "pkg0")) != nil })
	var worst time.Duration
	for i := range 6 {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		start := time.Now()
		cmd := model.ToggleExpand(dir)
		runAppCommand(t, shell.app, cmd)
		remoteJourneyUntil(t, shells, func() bool {
			node := model.NodeByPath(dir)
			return node != nil && node.Children != nil && !node.Loading
		})
		worst = max(worst, time.Since(start))
	}
	t.Logf("worst folder expand: %s", worst)
	if worst > 100*time.Millisecond {
		t.Fatalf("expanding a folder took %s, want under 100ms", worst)
	}
}
