package fileio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryMoveNeverReplacesEvenEmptyTarget(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("directory moves intentionally fail closed on this platform")
	}
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := MoveDirectoryNoReplace(source, target); err == nil {
		t.Fatal("replaced existing empty directory")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("failed move lost source")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := MoveDirectoryNoReplace(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source remains after move")
	}
}
