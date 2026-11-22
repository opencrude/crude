package network

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Guard the transport authority, not just today's list of HTTP consumers.
// Explicit wrappers may build a Client with a Transport; they may not restore
// bare clients, convenience functions or an independent http.Transport.
func TestProductionHTTPConsumersCannotBypassNetworkPolicy(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "third_party", "vendor", "node_modules", "tmp", "network":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		httpAlias := ""
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && importPath == "net/http" {
				httpAlias = "http"
				if spec.Name != nil {
					httpAlias = spec.Name.Name
				}
			}
		}
		if httpAlias == "" {
			return nil
		}
		httpName := func(expression ast.Expr) string {
			if selector, ok := expression.(*ast.SelectorExpr); ok {
				if name, ok := selector.X.(*ast.Ident); ok && name.Name == httpAlias {
					return selector.Sel.Name
				}
			}
			return ""
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				switch httpName(node) {
				case "DefaultTransport", "DefaultClient", "Get", "Head", "Post", "PostForm":
					t.Errorf("%s bypasses shared Network policy", positions.Position(node.Pos()))
				}
			case *ast.CompositeLit:
				switch httpName(node.Type) {
				case "Transport":
					t.Errorf("%s constructs an independent HTTP transport", positions.Position(node.Pos()))
				case "Client":
					hasTransport := false
					for _, field := range node.Elts {
						pair, ok := field.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := pair.Key.(*ast.Ident)
						hasTransport = hasTransport || ok && key.Name == "Transport"
					}
					if !hasTransport {
						t.Errorf("%s constructs a bare client outside Network", positions.Position(node.Pos()))
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
