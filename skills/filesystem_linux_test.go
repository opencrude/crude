package skills_test

import (
	"errors"
	"path/filepath"
	"testing"

	"crude/skills"
	"golang.org/x/sys/unix"
)

func TestLoadRejectsNamedPipesWithoutBlocking(t *testing.T) {
	directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
	if err := unix.Mkfifo(filepath.Join(directory, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Load(directory); !errors.Is(err, skills.ErrUnsupported) {
		t.Fatalf("named pipe activation = %v", err)
	}
}
