package agent

import (
	"testing"

	"crude/safety"
	"crude/tools"
	captypes "crude/types/capability"
)

func TestUnknownAndChangedDynamicToolsFailClosed(t *testing.T) {
	if PolicyAllows(safety.DefaultPolicy(), "mcp__unknown__write") {
		t.Fatal("unknown dynamic tool allowed")
	}
	read := tools.ToolMetadata{Name: "mcp__source__read", Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: "revision", Dynamic: true}
	if !PolicyAllowsMetadata(safety.ReadOnlyPolicy(), read) {
		t.Fatal("granted read-only tool denied")
	}
	write := read
	write.Effect = captypes.EffectWrite
	if PolicyAllowsMetadata(safety.ReadOnlyPolicy(), write) {
		t.Fatal("read-only child gained write authority")
	}
	read.Fingerprint = ""
	if PolicyAllowsMetadata(safety.DefaultPolicy(), read) {
		t.Fatal("unversioned dynamic tool allowed")
	}
}

func TestNativeEffectMetadataCoversCapabilitiesAndCollaboration(t *testing.T) {
	for _, tool := range append(append(append([]tools.ToolDef(nil), tools.AgentTools...), tools.CapabilityTools...), tools.CollaborationTools...) {
		if _, exists := tools.NativeMetadata(tool.Function.Name); !exists {
			t.Errorf("missing metadata %s", tool.Function.Name)
		}
	}
	if !PolicyAllows(safety.ReadOnlyPolicy(), "agent_send") {
		t.Fatal("communication incorrectly required file writes")
	}
	if PolicyAllows(safety.ReadOnlyPolicy(), "agent_start") {
		t.Fatal("read-only policy gained agent execution authority")
	}
}
