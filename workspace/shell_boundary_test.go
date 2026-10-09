package workspace

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestWorkspaceDoesNotOwnShellSelectionOrTerminalRuntime(t *testing.T) {
	for _, name := range []string{"ActiveSession", "FocusStack", "ModelName"} {
		if _, exists := reflect.TypeOf(Workspace{}).FieldByName(name); exists {
			t.Errorf("workspace regained client-specific %s authority", name)
		}
	}
	for _, owner := range []struct {
		typeOf reflect.Type
		field  string
	}{{reflect.TypeOf(AgentManager{}), "active"}, {reflect.TypeOf(AgentNode{}), "Hidden"}, {reflect.TypeOf(AgentState{}), "ThinkingPaneID"}} {
		if _, exists := owner.typeOf.FieldByName(owner.field); exists {
			t.Errorf("%s regained shell-specific %s authority", owner.typeOf.Name(), owner.field)
		}
	}
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if dependency == "crude/tui" || strings.HasPrefix(dependency, "crude/tui/") || dependency == "charm.land/bubbletea/v2" {
				t.Errorf("%s imports terminal runtime %s into domain ownership", path, dependency)
			}
		}
	}
}
