package workspace

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectArtifactRejectsWindowsJunctionParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeProjectContextFixture(t, outside, "commands.json", "[]")
	junction := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	if content, _, err := readProjectArtifact(t.Context(), root, filepath.Join("junction", "commands.json"), 100); err == nil || content != nil {
		t.Fatalf("junction allowed an outside artifact read: %v", err)
	}
}
