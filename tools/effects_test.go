package tools

import (
	"testing"

	captypes "crude/types/capability"
)

func TestCrudeControlMetadataRequiresContextResolution(t *testing.T) {
	metadata, ok := NativeMetadata("crude_control")
	if !ok || !metadata.ContextResolved || metadata.Effect != "" {
		t.Fatalf("crude_control metadata = %+v, %v", metadata, ok)
	}
	discovery, ok := NativeMetadata("crude_commands")
	if !ok || discovery.ContextResolved || discovery.Effect != captypes.EffectRead {
		t.Fatalf("crude_commands metadata = %+v, %v", discovery, ok)
	}
}
