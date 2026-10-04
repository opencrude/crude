package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	captypes "crude/types/capability"
)

func TestRelatedToolFamilySnapshotsKeepExactAuthoredInput(t *testing.T) {
	input := `{"content":"Full authored body with escaped \"quotes\" and a retained ending","references":[{"kind":"file","id":"report.md"}]}`
	for _, name := range []string{
		"agent_list", "agent_inspect", "agent_start", "agent_send", "agent_respond",
		"agent_control", "publish_report", "artifact_read", "agent_wait", "board_read",
		"remember", "update_memory", "forget_memory", "recall_memory", "memory_feedback",
		"search_history", "capability_search", "capability_activate", "mcp_read_resource",
		"mcp_get_prompt", "mcp__docs__lookup",
	} {
		result := provider.NewTextMessage("tool", "result")
		result.ToolCallID = "call"
		meta := canonicalizeDisplayMeta(result, sessiondb.DisplayMeta{ToolName: name, ToolArgs: "abbreviated"}, input, 0, time.Unix(100, 0))
		if len(meta.Snapshots) != 1 || meta.Snapshots[0].ToolInput != input {
			t.Fatalf("%s snapshot lost authored input: %+v", name, meta.Snapshots)
		}
	}
}

type structuredResultRuntime struct{ result captypes.Result }

func (r structuredResultRuntime) Prepare(context.Context, captypes.RuntimeActor) (captypes.RuntimeSnapshot, error) {
	return captypes.RuntimeSnapshot{}, nil
}

func (r structuredResultRuntime) Execute(context.Context, captypes.RuntimeActor, captypes.RuntimeSnapshot, string, map[string]any) (captypes.Result, error) {
	return r.result, nil
}

func TestMCPBinaryAndUnknownEvidenceReachToolResults(t *testing.T) {
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	owner := &Session{logger: lg}
	owner.SetCapabilities(structuredResultRuntime{result: captypes.Result{
		Content:    []captypes.Content{{Type: "resource", MIMEType: "application/pdf", URI: "docs://document", Data: base64.StdEncoding.EncodeToString([]byte("bytes"))}},
		Structured: json.RawMessage(`{"status":"pending"}`), Unknown: true,
	}}, agent.NewFileOwnership())
	live := &agent.Agent{AgentID: "owner", Policy: safety.ReadOnlyPolicy(), ToolFn: func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{}, errors.New("unexpected native tool")
	}}
	owner.bindAgentRuntime(t.Context(), live, captypes.RuntimeActor{ID: "owner"}, "Owner")
	result, err := live.ToolFn(t.Context(), "mcp_read_resource", map[string]any{"connection_id": "docs", "uri": "docs://document"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Binary resource", "application/pdf", "5 bytes", "docs://document", "\nResult unknown:"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("lost MCP evidence %q: %s", want, result.Content)
		}
	}
}
