package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
	"crude/types/toolresult"
)

func TestWebSearchOutageHintOpensExactFallbackSettings(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	app.capabilitySnapshot = captypes.Snapshot{Settings: captypes.WithDefaults(captypes.Settings{})}
	const label = "Set up a search fallback"
	failed := types.ChatMessage{InternalID: "offline-search", Role: types.RoleTool, ToolName: "web_search", ToolStatus: types.ToolStatusError,
		Content: "Search providers did not respond."}
	app.hydrateActiveMessages([]types.ChatMessage{failed})
	if strings.Contains(app.View().RenderContent(), label) {
		t.Fatal("an ordinary tool error made an unverified setup suggestion")
	}
	failed.ToolOutcome = toolresult.Outcome{State: toolresult.Failed, Code: captypes.WebSearchFallbackAvailable}
	app.hydrateActiveMessages([]types.ChatMessage{failed})
	clickTranscriptAttachment(t, app, label)
	if !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("click did not open Settings")
	}
	if got := paintCapabilitySettings(t, app); !strings.Contains(got, "Model fallback") || !strings.Contains(got, "Web search and fetch") {
		t.Fatalf("opened the wrong Settings page:\n%s", got)
	}
	if app.Input.Value() != "KEEP MY COMPOSER DRAFT" {
		t.Fatal("opening fallback settings changed the user's draft")
	}
}
