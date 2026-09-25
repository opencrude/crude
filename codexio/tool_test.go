// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCodexTool(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(CodexTool())
	if err != nil {
		t.Fatal(err)
	}
	var tool map[string]any
	err = json.Unmarshal(raw, &tool)
	if err != nil {
		t.Fatal(err)
	}
	format, _ := tool["format"].(map[string]any)
	if tool["type"] != "custom" || tool["name"] != "apply_patch" || format["type"] != "grammar" ||
		format["syntax"] != "lark" || !strings.Contains(tool["description"].(string), "FREEFORM") {
		t.Fatalf("unexpected tool: %s", raw)
	}
	grammar, err := os.ReadFile("apply_patch.lark")
	if err != nil {
		t.Fatal(err)
	}
	if format["definition"] != string(grammar) || !strings.HasPrefix(string(grammar), "start: begin_patch") {
		t.Fatalf("the grammar is not the embedded Codex grammar: %q", format["definition"])
	}
}
