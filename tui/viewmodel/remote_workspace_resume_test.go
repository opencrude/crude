package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/termdock"
	"crude/types"
)

// A session resumed from the Workspace panel after a core restart takes the
// keyboard: typing "/" opens the command list in its composer instead of
// going to the panel the user clicked in.
func TestRemoteWorkspacePanelResumeHandsKeyboardToComposer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ECHO\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntimeConfig(t, server.URL, nil)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	one.app.Input.SetInput("hello")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool {
		return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	sessionID := one.app.ActiveSession.SessionID
	one.app.StopAsyncIO()
	if err := one.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	resumed := newRemoteJourneyShell(t, remoteJourneyRuntimeConfig(t, server.URL, nil), root)
	app := resumed.app
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	if !app.termDockFocused {
		t.Fatal("precondition: the Workspace panel has the keyboard")
	}
	runAppCommand(t, app, app.resumeWorkspaceSession(sessionID))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool {
		return app.ActiveSession != nil && app.ActiveSession.SessionID == sessionID
	})
	if app.termDockFocused || app.termDockVisible() {
		t.Fatal("the Workspace panel kept the keyboard after the session was resumed")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	runAppCommand(t, app, cmd)
	app.forceFrame()
	if view := ansi.Strip(app.View().RenderContent()); app.Input.Value() != "/" || !strings.Contains(view, "Plan mode: explore read-only") {
		t.Fatalf("typing / did not open the command list in the resumed composer (draft %q):\n%s", app.Input.Value(), view)
	}
}
