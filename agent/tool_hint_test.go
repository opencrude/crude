package agent

import (
	"strings"
	"testing"

	"crude/tools"
)

// An Opus run called "jewel_grep_files" when its tool list offered the
// namespaced grep tool, and got only "not available". The error must name
// the tool it meant so the next call succeeds.
func TestUnavailableToolNamesTheClosestTool(t *testing.T) {
	defs := []tools.ToolDef{
		{Function: tools.ToolFuncDef{Name: "grep_files"}},
		{Function: tools.ToolFuncDef{Name: "execute_command"}},
		{Function: tools.ToolFuncDef{Name: "async_execute_command"}},
	}
	if hint := closestToolHint("jewel_grep_files", defs); !strings.Contains(hint, `"grep_files"`) {
		t.Fatalf("hint = %q", hint)
	}
	if hint := closestToolHint("egg_async_execute_command", defs); !strings.Contains(hint, `"async_execute_command"`) {
		t.Fatalf("longest match not chosen: %q", hint)
	}
	if hint := closestToolHint("bash_placeholder", defs); hint != "" {
		t.Fatalf("an invented tool got a hint: %q", hint)
	}
}
