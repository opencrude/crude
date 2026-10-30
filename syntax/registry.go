package syntax

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Only explicitly selected, reviewed grammars/scanners are linked. There is
// no aggregate grammar registry or runtime executable grammar acquisition.
//
//go:embed queries/*.scm
var querySources embed.FS

type grammar struct {
	language *ts.Language
	identity Identity
	query    string
}

func LanguageForPath(path string) (LanguageID, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return Go, true
	case ".py", ".pyi":
		return Python, true
	case ".js", ".mjs", ".cjs", ".jsx":
		return JavaScript, true
	case ".ts", ".mts", ".cts":
		return TypeScript, true
	case ".tsx":
		return TSX, true
	default:
		return "", false
	}
}

func bundled(id LanguageID) (grammar, error) {
	g := grammar{identity: Identity{Language: id, Runtime: "go-tree-sitter@v0.25.0+crude-lifetime-1"}}
	var queryNames []string
	switch id {
	case Go:
		g.language = ts.NewLanguage(tsgo.Language())
		g.identity.Grammar = "tree-sitter-go@v0.25.0"
		queryNames = []string{"go"}
	case Python:
		g.language = ts.NewLanguage(tspy.Language())
		g.identity.Grammar = "tree-sitter-python@v0.25.0"
		queryNames = []string{"python"}
	case JavaScript:
		g.language = ts.NewLanguage(tsjs.Language())
		g.identity.Grammar = "tree-sitter-javascript@v0.23.1"
		queryNames = []string{"javascript"}
	case TypeScript, TSX:
		if id == TSX {
			g.language = ts.NewLanguage(tsts.LanguageTSX())
		} else {
			g.language = ts.NewLanguage(tsts.LanguageTypescript())
		}
		g.identity.Grammar = "tree-sitter-typescript@75b3874edb2dc714fb1fd77a32013d0f8699989f"
		queryNames = []string{"javascript", "typescript"}
	default:
		return grammar{}, fmt.Errorf("%w: %s", ErrUnsupported, id)
	}
	var source strings.Builder
	for _, name := range queryNames {
		data, err := querySources.ReadFile("queries/" + name + ".scm")
		if err != nil {
			return grammar{}, err
		}
		source.Write(data)
		source.WriteByte('\n')
	}
	g.query = source.String()
	g.identity.QueryDigest = sha256.Sum256([]byte(g.query))
	return g, nil
}
