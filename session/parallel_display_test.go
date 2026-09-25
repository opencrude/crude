package session

import (
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
)

func TestParallelResultSnapshotPreservesChildOperands(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"main.go"}}]}`
	result := provider.NewTextMessage("tool", `{"results":[{"recipient_name":"read_file","result":"contents","error":false}]}`)
	result.ToolCallID = "call"
	meta := canonicalizeDisplayMeta(result, sessiondb.DisplayMeta{ToolName: "multi_tool_use.parallel", ToolArgs: "tool_uses: "}, input, 0, time.Now())
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].ToolInput != input {
		t.Fatalf("parallel snapshot lost child inputs: %+v", meta.Snapshots)
	}
}

func TestStoppedBoundaryIsVisibleButNotHumanInput(t *testing.T) {
	msg := provider.NewTextMessage("user", "The user stopped this turn.")
	msg.InternalID = "stopped:1"
	msg.Origin = provider.OriginTurnStopped
	msg.Visibility = provider.VisibilityContextOnly
	meta := canonicalizeDisplayMeta(msg, sessiondb.DisplayMeta{}, "", 0, time.Now())
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].LifecyclePhase != "stopped" || meta.Snapshots[0].Role != "status" || meta.Snapshots[0].InternalID != msg.InternalID {
		t.Fatalf("invalid persisted stop notice: %+v", meta)
	}
	if isProviderControlMessage(msg) {
		t.Fatal("stop notice hidden from display hydration")
	}
}
