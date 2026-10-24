package viewmodel

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/remoteconn"
	"crude/tui/ui"
	"crude/tui/wm"
	ctypes "crude/types"
	"crude/workspace"
)

// Isolated production UI for sequential scripts/tuishot.ps1 captures. F2 changes
// theme, F3 docks the chat to expose Connections, F9 exits. No network/provider
// calls or user configuration are involved.
func TestWindowedChatAndConnectionsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_WINDOWED_CHAT") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	app, manager := remoteSidebarFixture(t, 160, 40)
	app.SetRemoteManager(&visualConnectionManager{fakeRemoteManager: manager}, remoteconn.Local)
	app.remotes.snapshot = manager.snapshot
	app.AddOrchestrator("background")
	back := app.ws.AgentMgr().Get("background").State
	back.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleUser, Origin: provider.OriginPeer,
		Content:       strings.Repeat("Shaded peer evidence remains behind the floating chat.\n", 24),
		Collaboration: &provider.CollaborationAttribution{SenderName: "Reviewer", Kind: "peer_reply"}})
	app.syncAgentMessages(back.ID, false)
	app.AddOrchestrator("window-chat")
	state := app.ws.AgentMgr().Get("window-chat").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant, Content: "Ordinary text and its trailing padding stay on this window's background.\n\nBlank rows stay solid too."})
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleTool, ToolName: "read_file", ToolArgs: "parser.go", ToolStatus: ctypes.ToolStatusSuccess})
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleTool, ToolName: "update_plan", ToolStatus: ctypes.ToolStatusCalling, ToolInput: `{"plan":[{"step":"Check chat padding","status":"in_progress"}]}`})
	app.syncAgentMessages(state.ID, false)
	app.openAgentInWindow(state.ID)
	model := &windowedChatVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type visualConnectionManager struct{ *fakeRemoteManager }

func (manager *visualConnectionManager) Snapshot(ctx context.Context) (remoteconn.Snapshot, error) {
	select {
	case <-time.After(3 * time.Second):
		return manager.fakeRemoteManager.Snapshot(ctx)
	case <-ctx.Done():
		return remoteconn.Snapshot{}, ctx.Err()
	}
}

type windowedChatVisualModel struct {
	*App
	light bool
}

func (*windowedChatVisualModel) Init() tea.Cmd { return tickCmd() }

func (model *windowedChatVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			model.light = !model.light
			preset := "dark"
			if model.light {
				preset = "light"
			}
			model.ApplyThemePreset(preset)
			return model, nil
		case tea.KeyF3:
			for _, group := range model.Win.FloatingGroupIDs() {
				model.dockPaneWindow(group)
			}
			model.overlayHost.Close(ui.OverlayContextMenu)
			return model, model.OpenRemotes()
		case tea.KeyF9:
			return model, tea.Quit
		}
	}
	_, cmd := model.App.Update(msg)
	if _, resized := msg.(tea.WindowSizeMsg); resized {
		// The fixture creates its window before the native terminal reports
		// its size. Bound that initial test window to the captured grid.
		for _, pane := range model.mainPanes.windows {
			w, h := pane.fw.Size()
			pane.fw.SetSize(min(w, model.width-4), min(h, model.height-4))
			wm.CenterOnScreen(pane.fw, model.width, model.height)
		}
	}
	return model, cmd
}
