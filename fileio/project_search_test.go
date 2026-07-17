package fileio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchProjectFindsFuzzyNamesAndContents(t *testing.T) {
	root := t.TempDir()
	contentPath := filepath.Join(root, "handlers.go")
	if err := os.WriteFile(contentPath, []byte("package sample\n\nfunc HandleWorkspaceEvent() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	namePath := filepath.Join(root, "workspace_registry.md")
	if err := os.WriteFile(namePath, []byte("nothing relevant\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	content, err := SearchProject(context.Background(), root, "hwe", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 || content[0].Line != 3 || content[0].NameOnly {
		t.Fatalf("expected fuzzy content match on line 3, got %+v", content)
	}
	if content[0].Context == "" || content[0].ContextStart != 1 {
		t.Fatalf("expected preview context, got %+v", content[0])
	}

	byName, err := SearchProject(context.Background(), root, "wsreg", ProjectSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) == 0 || !byName[0].NameOnly || byName[0].RelativePath != "workspace_registry.md" {
		t.Fatalf("expected filename-only fuzzy match, got %+v", byName)
	}
}

func TestSearchProjectRegexAndCase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("Alpha42\nalpha42\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	matches, err := SearchProject(context.Background(), root, `Alpha\d+`, ProjectSearchOptions{Regex: true, CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Line != 1 {
		t.Fatalf("case-sensitive regex matches=%+v", matches)
	}
	if _, err := SearchProject(context.Background(), root, "[", ProjectSearchOptions{Regex: true}); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestSearchProjectHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SearchProject(ctx, t.TempDir(), "query", ProjectSearchOptions{})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
