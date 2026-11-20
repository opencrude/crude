package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStageCoreImageDoesNotPublishChangedSource(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(t.TempDir(), "crude.exe")
	approved := []byte("ready image")
	digest := fmt.Sprintf("%x", sha256.Sum256(approved))
	if err := os.WriteFile(exe, []byte("other image"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := stageCoreImage(home, exe, digest); err == nil {
		t.Fatal("staged different bytes than the selected image")
	}
	entries, err := os.ReadDir(CoreImageDir(home))
	if err != nil || len(entries) != 0 {
		t.Fatalf("a failed start left a published or temporary image: %v, %v", entries, err)
	}
	if err := os.WriteFile(exe, approved, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := stageCoreImage(home, exe, digest)
	if err != nil {
		t.Fatalf("the same build could not start after the source was put back: %v", err)
	}
	if err := verifyCoreImage(path, digest); err != nil {
		t.Fatalf("staging did not retain the selected image: %v", err)
	}
}
