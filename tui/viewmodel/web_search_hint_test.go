package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/types/conversation"
	"crude/types/toolresult"
)

func TestWebSearchOutageHintOpensSearchMethods(t *testing.T) {
	app := newTranscriptAttachmentApp(t)
	app.capabilitySnapshot = captypes.Snapshot{Settings: captypes.WithDefaults(captypes.Settings{})}
	const label = "Set up search methods"
	failed := conversation.ChatMessage{InternalID: "offline-search", Role: conversation.RoleTool, ToolName: "web_search", ToolStatus: conversation.ToolStatusError,
		Content: "Search providers did not respond."}
	app.hydrateActiveMessages([]conversation.ChatMessage{failed})
	if strings.Contains(app.View().RenderContent(), label) {
		t.Fatal("an ordinary tool error made an unverified setup suggestion")
	}
	failed.ToolOutcome = toolresult.Outcome{State: toolresult.Failed, Code: captypes.WebSearchOutage}
	app.hydrateActiveMessages([]conversation.ChatMessage{failed})
	clickTranscriptAttachment(t, app, label)
	if !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("click did not open Settings")
	}
	if got := paintCapabilitySettings(t, app); !strings.Contains(got, "Web search and fetch") || !strings.Contains(got, "There are no account methods") ||
		!strings.Contains(got, "[ Add method ]") || strings.Contains(got, "fallback") {
		t.Fatalf("opened the wrong Settings page:\n%s", got)
	}
	if app.Input.Value() != "KEEP MY COMPOSER DRAFT" {
		t.Fatal("opening search methods changed the user's draft")
	}
}
