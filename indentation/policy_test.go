package indentation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRulesNestedGlobsUnsetAndRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project", "src")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	put := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(root, ".editorconfig"), "[*]\nindent_size=7\n")
	put(filepath.Join(root, "project", ".editorconfig"), "root=true\n[*]\nindent_style=space\nindent_size=2\n[*.{txt,md}]\ntab_width=8\n")
	put(filepath.Join(dir, ".editorconfig"), "[file{1..3}.txt]\nindent_style=tab\nindent_size=tab\n[file2.txt]\nindent_size=unset\n")
	for name, want := range map[string]Choices{"file1.txt": {Style: Tabs, SizeIsTab: true, TabWidth: 8}, "file2.txt": {Style: Tabs, TabWidth: 8}, "file4.txt": {Style: Spaces, Size: 2, TabWidth: 8}, "file.go": {Style: Spaces, Size: 2, TabWidth: 2}} {
		got, err := ReadProject(t.Context(), filepath.Join(dir, name))
		if err != nil || got != want {
			t.Fatalf("%s: %+v %v want %+v", name, got, err, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadProject(ctx, filepath.Join(dir, "file1.txt")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPolicyPrecedenceKeepsWidthsDistinct(t *testing.T) {
	detected := Choices{Style: Spaces, Size: 2, TabWidth: 4}
	settings := Choices{Style: Tabs, Size: 4, TabWidth: 8}
	project := Choices{Style: Spaces, Size: 3, TabWidth: 6}
	explicit := Choices{Size: 5}
	got := Resolve(detected, settings, project, explicit)
	if got != (Choices{Style: Spaces, Size: 5, TabWidth: 6}) {
		t.Fatal(got)
	}
	project.Size, project.SizeIsTab = 0, true
	got = Resolve(detected, settings, project, Choices{TabWidth: 7})
	if got.Size != 7 || got.TabWidth != 7 {
		t.Fatal("indent_size=tab resolved before final layer", got)
	}
}

func TestProjectInvalidOrOversizedIsNotAValidPartialPolicy(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, ".editorconfig")
	path := filepath.Join(root, "file.txt")
	for _, text := range []string{"root=true\n[*]\nindent_style=unknown\n", "root=true\n[*]\nindent_size=33\n", "root=true\n[*]\nindent_style=tab\ntab_width=0\n", strings.Repeat("#", (1<<20)+1)} {
		if err := os.WriteFile(config, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadProject(t.Context(), path)
		if err == nil || got != (Choices{}) {
			t.Fatal("invalid project config silently succeeded", got, err)
		}
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadProject(t.Context(), path); err != nil || got != (Choices{}) {
		t.Fatal("missing config is not ordinary inheritance", got, err)
	}
}
