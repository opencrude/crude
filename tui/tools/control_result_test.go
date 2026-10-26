package tools

import (
	"strings"
	"testing"

	"crude/types"
	"crude/types/control"
	"crude/types/inspect"
	"encoding/json"
)

func structuredText(result StructuredResult) string {
	var b strings.Builder
	b.WriteString(result.Summary)
	for _, section := range result.Sections {
		b.WriteString("|" + section.Title + "|" + section.Metadata + "|" + section.Body)
	}
	return b.String()
}

func TestDiagnosticProjectionKeepsReadableFindingsAndDisclosure(t *testing.T) {
	page := inspect.Page{SnapshotID: "snap", Total: 100, Returned: 2, Next: &inspect.Request{SnapshotID: "snap", Offset: 2}, Records: []inspect.Record{
		{Kind: "finding", ID: "f1", Value: json.RawMessage(`{"title":"UI froze 37×","detail":"longest frame paint 42ms"}`)},
		{Kind: "latency", ID: "ui.view", Value: json.RawMessage(`{"window":{"p95":23000}}`)},
	}}
	raw, _ := json.Marshal(page)
	content, _ := json.Marshal(control.Result{RequestID: "one", OperationID: "one", State: control.Applied, Output: raw})
	result := BuildStructuredResult("crude_control", string(content), `{"command":"performance.shell"}`)
	if text := structuredText(result); !strings.Contains(text, "UI froze 37×") || !strings.Contains(text, "more available") {
		t.Fatal("findings not readable", text)
	}
	seenRaw, seenNext := false, false
	for _, section := range result.Sections {
		if strings.Contains(section.Title, "ui.view") {
			seenRaw = section.DetailOnly
		}
		for _, field := range section.Details {
			if field.Label == "snapshot id" && field.Value == "snap" {
				seenNext = section.DetailOnly
			}
		}
	}
	if !seenRaw || !seenNext {
		t.Fatal("raw data/continuation lost from disclosure")
	}
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
