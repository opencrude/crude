package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository passes the gate: product Go code names no language, tool or
// server that the catalog owns, except the reviewed exceptions.
func TestRepositoryNamesNoCatalogLanguage(t *testing.T) {
	findings, unused, err := run(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.path+": "+f.literal)
	}
	if len(findings) > 0 || len(unused) > 0 {
		t.Fatalf("names gate: findings %v; unused exceptions %v", lines, unused)
	}
}

// A language-named branch in product code fails the gate, whether it names a
// file type or a server; test files and ordinary short words do not.
func TestGateFindsLanguageBranches(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/a.go", "package pkg\n\nfunc f(ext string) string {\n\tif ext == \".rs\" {\n\t\treturn \"rust-analyzer\"\n\t}\n\treturn \"nil\"\n}\n")
	write("pkg/a_test.go", "package pkg\n\nvar _ = \"gopls\"\n")
	findings, _, err := run(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.literal)
	}
	if strings.Join(got, ",") != ".rs,rust-analyzer" {
		t.Fatalf("findings = %v", got)
	}
}
