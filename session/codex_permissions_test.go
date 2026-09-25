package session

import (
	"path/filepath"
	"testing"
)

func TestPatchAlwaysGrantCoversEveryOperandAndOperation(t *testing.T) {
	s, err := NewWithSessionDir("codex-grants", testConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWorkDir(t.TempDir())
	move := "*** Begin Patch\n*** Update File: a\n*** Move to: b\n-a\n+b\n*** End Patch"
	if s.patchAlwaysAllowed(move) {
		t.Fatal("ungranted move approved")
	}
	s.grantPatchAlways(move)
	if !s.patchAlwaysAllowed(move) {
		t.Fatal("always grant lost move")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	escape := "*** Begin Patch\n*** Update File: a\n*** Move to: " + outside + "\n-a\n+b\n*** End Patch"
	if s.patchAlwaysAllowed(escape) {
		t.Fatal("approved unseen destination")
	}
	deletion := "*** Begin Patch\n*** Delete File: a\n*** End Patch"
	if s.patchAlwaysAllowed(deletion) {
		t.Fatal("update grant approved delete")
	}
	s.grantPatchAlways(deletion)
	if !s.patchAlwaysAllowed(deletion) {
		t.Fatal("explicit delete grant lost")
	}
}
