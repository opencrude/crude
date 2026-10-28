package tools

import (
	"reflect"
	"strings"
	"testing"

	"crude/codexio"
)

func TestForModelReplacesOnlyEditorsWithFullNativeTool(t *testing.T) {
	before := append([]ToolDef(nil), AgentTools...)
	for _, model := range []string{"gpt-5", "openai/GPT-5.5", "my-gpt-model"} {
		defs := ForModel(AgentTools, model)
		counts := map[string]int{}
		for _, def := range defs {
			counts[def.Function.Name]++
			if def.Function.Name == "apply_patch" {
				upstream := codexio.CodexTool()
				if def.Type != "custom" || def.Function.Description != upstream.Description || !reflect.DeepEqual(def.Format, &upstream.Format) {
					t.Fatal("tool guide modified")
				}
			}
		}
		if counts["apply_patch"] != 1 || counts["write_file"] != 0 || counts["edit_file"] != 0 {
			t.Fatal(counts)
		}
		for _, name := range []string{"move_file", "delete_file", "copy_file"} {
			if counts[name] != 1 {
				t.Fatalf("removed %s", name)
			}
		}
		if !reflect.DeepEqual(defs, ForModel(defs, model)) {
			t.Fatal("model selection not idempotent")
		}
	}
	if !reflect.DeepEqual(before, AgentTools) {
		t.Fatal("mutated shared definitions")
	}
	if !reflect.DeepEqual(before, ForModel(AgentTools, "claude-test")) {
		t.Fatal("non-GPT changed")
	}
}

func TestPatchRawPreviewAndSummary(t *testing.T) {
	input := "*** Begin Patch\n*** Update File: old name.go\n*** Move to: new name.go\n@@\n-old\n+new\n*** End Patch"
	if got := SummarizeArgs("apply_patch", input); got != "old name.go, new name.go" {
		t.Fatal(got)
	}
	if got := FormatPreview("apply_patch", map[string]any{"input": input}); !strings.Contains(got, "−1/+1") || !strings.Contains(got, "new name.go") {
		t.Fatal(got)
	}
}

func TestPatchSummaryKeepsOriginalPathSpelling(t *testing.T) {
	input := "*** Begin Patch\n*** Update File: /project/link/../a.go\n*** Move to: C:/Work/project/b.go\n@@\n-old\n+new\n*** End Patch\n"
	if got := SummarizeArgs("apply_patch", input); got != "/project/link/../a.go, C:/Work/project/b.go" {
		t.Fatalf("patch title rewrote the original path operands: %q", got)
	}
}
