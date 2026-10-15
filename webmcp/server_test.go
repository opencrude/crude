package webmcp

import (
	"slices"
	"testing"

	"crude/tools"
	captypes "crude/types/capability"
)

func TestDescriptorListsExactlyTheServedTools(t *testing.T) {
	descriptor, ok := captypes.Builtin(captypes.WebConnectionID)
	if !ok || !slices.Equal(descriptor.Tools, ToolNames()) {
		t.Fatalf("descriptor tools=%v served=%v", descriptor.Tools, ToolNames())
	}
	_, effects := New(nil, 1<<20)
	for _, name := range ToolNames() {
		if effects[name] != captypes.EffectRead {
			t.Fatalf("%s effect=%q", name, effects[name])
		}
	}
}

func TestBuiltinToolNamesNeverShadowNativeTools(t *testing.T) {
	for _, set := range [][]tools.ToolDef{tools.AgentTools, tools.CollaborationTools, tools.CapabilityTools, tools.ControlTools} {
		for _, definition := range set {
			if slices.Contains(ToolNames(), definition.Function.Name) {
				t.Fatalf("built-in web tool %q shadows a native tool", definition.Function.Name)
			}
		}
	}
}
