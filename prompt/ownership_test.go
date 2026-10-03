package prompt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var modelPromptIdentifier = regexp.MustCompile(`(?i)(prompt|instruction)`)

func TestProductionPromptTemplatesHaveOneOwner(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"agent", "app", "session", "workspace", "memory", "codexio"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, directory, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"You are the memory", "You review one proposed", "Context compaction extraction contract", "Respond with ONLY", "<runtime_report_reminder>", "[USER INTERRUPTION — READ THIS NOW]"} {
				if strings.Contains(string(data), forbidden) {
					t.Errorf("model-facing template %q lives outside prompt/: %s", forbidden, path)
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, declaration := range file.Decls {
				generic, ok := declaration.(*ast.GenDecl)
				if !ok || (generic.Tok != token.CONST && generic.Tok != token.VAR) {
					continue
				}
				for _, spec := range generic.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range value.Names {
						if modelPromptIdentifier.MatchString(name.Name) {
							t.Errorf("prompt-like production constant %s remains outside prompt/: %s", name.Name, path)
						}
					}
				}
			}
		}
	}
}
