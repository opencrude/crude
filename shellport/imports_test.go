package shellport

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The port is a seam owned by neither layer; it must never depend on the shell.
func TestPortDoesNotImportTUI(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if _, err := os.Stat(name); err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			if path := strings.Trim(spec.Path.Value, `"`); path == "crude/tui" || strings.HasPrefix(path, "crude/tui/") {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}
