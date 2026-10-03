package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicationNeverReplacesLateAuthoredDirectory(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "test-skill")
	_, err := publishSkill(context.Background(), destination, "test-skill", func(root *os.Root) error {
		if err := writeNewFile(context.Background(), root, "SKILL.md", []byte("---\nname: test-skill\ndescription: Good.\n---\nBody\n"), 0644); err != nil {
			return err
		}
		return os.Mkdir(destination, 0755)
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("late authored destination = %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatalf("publication replaced an authored empty directory: %v, %v", entries, err)
	}
	assertStagingRemoved(t, parent)
}

func TestPublicationRollsBackCancelledStaging(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "test-skill")
	ctx, cancel := context.WithCancel(context.Background())
	_, err := publishSkill(ctx, destination, "test-skill", func(root *os.Root) error {
		if err := writeNewFile(ctx, root, "SKILL.md", []byte("---\nname: test-skill\ndescription: Good.\n---\nBody\n"), 0644); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("staging cancellation = %v", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled staging published a skill: %v", err)
	}
	assertStagingRemoved(t, parent)
}

func TestPublicationRejectsRenamedParent(t *testing.T) {
	parent := t.TempDir()
	container := filepath.Join(parent, "container")
	if err := os.Mkdir(container, 0755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(container, "test-skill")
	moved := filepath.Join(parent, "moved-container")
	_, err := publishSkill(context.Background(), destination, "test-skill", func(root *os.Root) error {
		if err := writeNewFile(context.Background(), root, "SKILL.md", []byte("---\nname: test-skill\ndescription: Good.\n---\nBody\n"), 0644); err != nil {
			return err
		}
		if err := os.Rename(container, moved); err != nil {
			return err
		}
		return os.Mkdir(container, 0755)
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("renamed parent = %v", err)
	}
	for _, directory := range []string{container, moved} {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatalf("publication affected replaced parent or leaked staging: %v, %v", entries, err)
		}
	}
}

func TestPublicationPreservesReplacedStaging(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "test-skill")
	var replacement string
	_, err := publishSkill(context.Background(), destination, "test-skill", func(root *os.Root) error {
		if err := writeNewFile(context.Background(), root, "SKILL.md", []byte("---\nname: test-skill\ndescription: Good.\n---\nBody\n"), 0644); err != nil {
			return err
		}
		replacement = root.Name()
		if err := os.Rename(replacement, replacement+"-owned"); err != nil {
			return err
		}
		if err := os.Mkdir(replacement, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(replacement, "authored.md"), []byte("later authored work"), 0644)
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("replaced staging = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(replacement, "authored.md"))
	if err != nil || string(data) != "later authored work" {
		t.Fatalf("rollback removed later authored work: %q, %v", data, err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaced staging was published: %v", err)
	}
}

func assertStagingRemoved(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "test-skill" {
			t.Fatalf("unpublished temporary work remains: %s", entry.Name())
		}
	}
}
