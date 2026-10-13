package workspace

import "testing"

func TestStreamingReplacementCallHasOneRowNotOnePerDelta(t *testing.T) {
	c := NewConversation()
	c.UpdateToolCall("old-call", "_tc1", "apply_patch", "old.go")
	c.findTool("_tc1", "old-call").ToolStatus = "success"
	for i := 0; i < 40; i++ {
		c.UpdateToolStreaming("replacement-call", "_tc1", "apply_patch", map[string]any{"target": "new.go", "new_lines_int": i + 1})
	}
	if len(c.Messages()) != 2 {
		t.Fatalf("one replacement call created %d transcript rows", len(c.Messages()))
	}
	if got := c.findTool("_tc1", "replacement-call"); got.ToolID != "replacement-call" || got.ToolLinesAdded != 40 {
		t.Fatalf("lost replacement updates: %+v", got)
	}
	old := c.findTool("_tc1", "old-call")
	c.UpdateToolStreaming("old-call", "_tc1", "apply_patch", map[string]any{"target": "late.go"})
	if len(c.Messages()) != 2 || old.ToolStatus != "success" || old.ToolProgressLabel == "late.go" {
		t.Fatal("late settled delta cloned or reactivated the old call")
	}
}
