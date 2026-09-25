package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditFileSchemaWarnsAgainstIdenticalReplacement(t *testing.T) {
	for _, def := range AgentTools {
		if def.Function.Name != "edit_file" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(def.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(def.Function.Description, "Do not call edit_file if old_string and new_string are identical") ||
			!strings.Contains(schema.Properties["new_string"].Description, "MUST differ from old_string") {
			t.Fatal("model-facing edit_file schema must prohibit no-op edit calls")
		}
		return
	}
	t.Fatal("edit_file tool definition missing")
}

// A no-change patch is a failure, not a success whose body is only prose: the
// chat projection renders edit results as patch cards, so a "Replaced 1
// occurrence" line with no diff behind it reads as a patch that never existed.
func TestNoChangeEditResultIsFailure(t *testing.T) {
	identical := noChangeEditResult("beta", "beta")
	if !strings.HasPrefix(identical.Content, "Error: ") {
		t.Fatalf("identical arguments must fail: %q", identical.Content)
	}
	if !strings.Contains(identical.Content, "no changes to make: old_string and new_string are exactly the same") {
		t.Fatalf("identical-argument reason = %q", identical.Content)
	}
	recovered := noChangeEditResult("  foo()", "foo()")
	if !strings.HasPrefix(recovered.Content, "Error: ") {
		t.Fatalf("recovered no-change patch must fail: %q", recovered.Content)
	}
	if !strings.Contains(recovered.Content, "no changes to make: the patch left the file byte-identical") {
		t.Fatalf("recovered-match reason = %q", recovered.Content)
	}
	if strings.Contains(recovered.Content, "exactly the same") {
		t.Fatalf("recovered-match reason must not claim identical arguments: %q", recovered.Content)
	}
	for _, result := range []ToolResult{identical, recovered} {
		if strings.Contains(result.Content, "Replaced") {
			t.Fatalf("no-change result claims a replacement: %q", result.Content)
		}
		if result.Added != 0 || result.Removed != 0 {
			t.Fatalf("no-change result counted lines: +%d -%d", result.Added, result.Removed)
		}
		if !strings.Contains(result.Content, "Hint: ") {
			t.Fatalf("no-change result needs a model-facing hint: %q", result.Content)
		}
	}
}
