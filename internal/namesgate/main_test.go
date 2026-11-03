package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository passes the syntactic gate, with the reviewed exceptions.
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
// file type or a server; test files and unrelated short words do not.
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

// These examples are independent of the gate's name-building implementation.
// They exercise actual catalog values and ordinary uses of similar words.
func TestCatalogBranchesAndUnrelatedWords(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
 func inspect(language string, server string, command string, filename string, grammar LanguageID) {
  if language == "go" {} // short language ID
  if language == "R" {} // one-letter language name
  if language == "C++" {} // Linguist name
  if language == Python {} // named identifier
  if language == syntax.Go {} // qualified identifier
  if server == "nil" {} // short server ID
  if command == "gopls" {}
  if filename == "Makefile" {}
  if filename == ".rs" {}
  switch grammar { case TypeScript: }
  _ = "gopher"; _ = "pythonista"; _ = "going"
  x := "go"; _ = x
 }
 `
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, _, err := run(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings {
		got = append(got, f.literal)
	}
	want := "go,R,C++,Python,Go,nil,gopls,Makefile,.rs,TypeScript"
	if strings.Join(got, ",") != want {
		t.Fatalf("findings = %v, want %s", got, want)
	}
}

func TestExceptionsMustBeUsed(t *testing.T) {
	_, unused, err := run(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(unused) == 0 {
		t.Fatal("unused exceptions were not reported")
	}
	if _, err := parseAllow("pkg/file.go\tGo\t\n"); err == nil {
		t.Fatal("exception without reason accepted")
	}
	if _, err := parseAllow("pkg/file.go\tGo\treason\npkg/file.go\tGo\treason\n"); err == nil {
		t.Fatal("duplicate exception accepted")
	}
}
