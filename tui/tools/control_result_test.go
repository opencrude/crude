package tools

import (
	"strings"
	"testing"

	"crude/types"
)

func structuredText(result StructuredResult) string {
	var b strings.Builder
	b.WriteString(result.Summary)
	for _, section := range result.Sections {
		b.WriteString("|" + section.Title + "|" + section.Metadata + "|" + section.Body)
	}
	return b.String()
}

func TestCrudeControlProjectionIsReadable(t *testing.T) {
	input := `{"command":"settings.set","arguments":{"scope":"workspace","path":"editor.tab_size","value":4}}`
	if !IsStructuredTool("crude_control") || !IsStructuredTool("crude_commands") {
		t.Fatal("control tools are not routed through structured projection")
	}
	title := BuildToolTitle(types.ChatMessage{Role: types.RoleTool, ToolName: "crude_control", ToolArgs: input, ToolInput: input, ToolStatus: types.ToolStatusSuccess,
		Content: `{"request_id":"control-1","operation_id":"control-1","state":"applied","revision":7,"output":{"applied":true}}`}, types.ToolPreviewDone)
	if title.Operand != "settings.set" || strings.Contains(title.Text(), "{") || !strings.Contains(title.Metadata, "Applied") {
		t.Fatalf("control title: %+v", title)
	}
	result := BuildStructuredResult("crude_commands", `[{"id":"settings.get","scope":"domain","effect":"read","human_only":false,"input":{"id":"settings.get.input","revision":1,"document":{"type":"object","description":"Read settings.","properties":{}}},"output":{"id":"o","revision":1,"document":{}},"requires":{},"availability":{"available":true}}]`, `{"query":"settings"}`)
	if text := structuredText(result); !strings.Contains(text, "1 command|settings.get|domain · read|Read settings.") {
		t.Fatalf("discovery projection: %s", text)
	}
}

func TestClaimToolsProjectReadableScope(t *testing.T) {
	if !IsCollaborationTool("agent_narrow_claims") || !IsStructuredTool("agent_update_claims") {
		t.Fatal("claim tools are not routed through collaboration projection")
	}
	result := BuildStructuredResult("agent_narrow_claims", `{"released":[{"id":"claim-1","path":"a.go","agent_id":"x","version":1}],"claims":[{"id":"claim-2","path":"b.go","agent_id":"x","version":2}]}`, `{}`)
	if text := structuredText(result); !strings.Contains(text, "1 owned path · 1 released|File scope||b.go") || strings.Contains(text, "{") {
		t.Fatalf("claim projection: %s", text)
	}
}
