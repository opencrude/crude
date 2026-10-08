package viewmodel

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/history"
	"crude/sessiondb"
	"crude/tui/ui"
	"crude/types"
	"crude/types/reasoning"
	"crude/types/recovery"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh fixture: no provider calls or real user data.
// F2 attention, F3 recovered, F4 Network Connection, F5 Proxy, F6 Certificates,
// F1 disclosure, F7 theme switch, F8 return to chat, F9 quit,
// F10 acknowledged retry/wait, Shift+F11 resumed reasoning/tool, F12 wrapped-gap
// draft. Bare F11 belongs to Alacritty fullscreen and must not be used to drive it.
func TestRunRecoveryVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_RECOVERY") != "1" {
		t.Skip("requires the repository's dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width, app.height = 160, 40
	if os.Getenv("CRUDE_VISUAL_RECOVERY_MEDIA") == "1" {
		app.SessionsByAgent = map[string]*workspace.Session{"visual-recovery": workspace.NewSession("visual-media", t.TempDir(), nil, stubLogger{})}
	}
	app.AddOrchestrator("visual-recovery")
	state := app.AgentMgr.Get("visual-recovery").State
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Time: time.Now(), Content: "Complete the refactor and verify it, keeping the accepted edits."})
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleAssistant, Time: time.Now(), Content: "The refactor is saved. Verification is still pending; recovery will continue from these exact results."})
	app.maybeHydrateComposerDraft()
	app.Input.SetInput("My next instruction is still a draft. Recovery must not consume it.")
	if os.Getenv("CRUDE_VISUAL_RECOVERY_MEDIA") == "1" {
		var media bytes.Buffer
		if err := png.Encode(&media, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "retained.png")
		if err := os.WriteFile(path, media.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		text := "Retained draft: [image] with [notes]. Do not send during recovery."
		entry := history.Entry{Text: text, Attachments: []history.Attachment{
			{Start: 16, End: 23, Reference: sessiondb.Reference{ID: "image", Label: "[image]", Kind: sessiondb.KindPastedImage, Title: "Retained screenshot", Mime: "image/png", FilePath: path}},
			{Start: 29, End: 36, Reference: sessiondb.Reference{ID: "notes", Label: "[notes]", Kind: sessiondb.KindPastedText, Content: "Exact recalled notes", Title: "Notes"}},
		}}
		cmd := app.recallPrompt(app.Input.RecallPrompt(entry))
		app.applyPromptHistory(cmd().(promptHistoryRestoredMsg))
		if len(app.Input.PromptReferences()) != 2 {
			t.Fatal("visual fixture failed to restore mixed attachments")
		}
	}
	model := &runRecoveryVisualModel{App: app, status: recovery.Status{
		ID: "visual-incident", AgentID: state.ID, Generation: 1,
		StartedAt: time.Now(), UpdatedAt: time.Now(), Attempt: 7,
	}}
	model.setRecovery(recovery.Recovering)
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type runRecoveryVisualModel struct {
	*App
	status recovery.Status
	light  bool
}

func (model *runRecoveryVisualModel) Init() tea.Cmd { return tickCmd() }

func (model *runRecoveryVisualModel) setRecovery(state recovery.State) {
	model.status.State, model.status.UpdatedAt = state, time.Now()
	model.status.RetryAt = time.Time{}
	switch state {
	case recovery.Recovering:
		model.status.Summary = "The provider is temporarily unavailable"
		model.status.Detail = "HTTP 503: The upstream gateway interrupted the connection. Accepted output and completed file changes are retained; no historical tool will be replayed."
		model.status.Action = "Recovery continues automatically. Stop remains available."
		model.status.RetryAt = time.Now().Add(5 * time.Minute)
	case recovery.NeedsAttention:
		model.status.Summary = "The selected account needs attention"
		model.status.Detail = "HTTP 401: Authentication was rejected for the configured endpoint. Your unfinished run and accepted results are kept; Crude will not switch accounts or repeatedly send invalid requests."
		model.status.Action = "Update Accounts, then Continue run from the menu or use /retry."
	case recovery.Recovered:
		model.status.Summary = "The run recovered"
		model.status.Detail = "The connection is healthy and the completed verification was saved."
		model.status.Action = ""
	}
	_ = model.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: model.status.AgentID, Data: model.status})
	if state == recovery.Recovered {
		_ = model.handleAgentEvent(types.AgentEventMsg{Type: "done", AgentID: model.status.AgentID, Data: map[string]any{"reason": "completed", "content": "Verification passed."}})
	}
}

func (model *runRecoveryVisualModel) selectNetworkPage(label string) {
	if body := model.settingsSurface(model.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
		for _, item := range body.Model().CategoryItems() {
			if item.Label == label {
				body.Model().SelectCategory(item.Payload.(int))
				body.SetModel(body.Model())
				window := model.overlayHost.Window(ui.OverlayWorkspaceSettings)
				if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				window.MarkContentDirty()
				return
			}
		}
	}
}

func (model *runRecoveryVisualModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF1:
			if renderer := model.activeMessageList(); renderer != nil {
				renderer.ToggleDisclosure(model.status.ID)
			}
			return model, nil
		case tea.KeyF2:
			model.setRecovery(recovery.NeedsAttention)
			return model, nil
		case tea.KeyF3:
			model.setRecovery(recovery.Recovered)
			return model, nil
		case tea.KeyF4:
			model.openNetworkSettings()
			return model, nil
		case tea.KeyF5:
			model.selectNetworkPage("Proxy")
			return model, nil
		case tea.KeyF6:
			model.selectNetworkPage("Certificates")
			return model, nil
		case tea.KeyF7:
			model.light = !model.light
			preset := "dark"
			if model.light {
				preset = "light"
			}
			model.ApplyThemePreset(preset)
			return model, nil
		case tea.KeyF8:
			model.overlayHost.Close(ui.OverlayWorkspaceSettings)
			return model, nil
		case tea.KeyF9:
			return model, tea.Quit
		case tea.KeyF10:
			model.status.State = recovery.Recovering
			model.status.RetryAt = time.Time{}
			model.status.Summary, model.status.UpdatedAt = "Retrying unfinished run", time.Now()
			_ = model.handleAgentEvent(types.AgentEventMsg{Type: "recovery", AgentID: model.status.AgentID, Data: model.status})
			_ = model.handleAgentEvent(types.AgentEventMsg{Type: "phase", AgentID: model.status.AgentID, Data: map[string]any{
				"name": "waiting_provider", "provider": "Test Provider", "request_id": "resumed-request",
			}})
			return model, nil
		case tea.KeyF11:
			_ = model.handleAgentEvent(types.AgentEventMsg{Type: "thinking", AgentID: model.status.AgentID, Data: reasoning.Delta{
				Text: "Checking the resumed work without replaying accepted changes", Kind: reasoning.Summary, BlockID: "resumed-thought",
			}})
			_ = model.handleAgentEvent(types.AgentEventMsg{Type: "tool_call", AgentID: model.status.AgentID, Data: map[string]any{
				"name": "read_async_output", "tool_id": "resumed-read", "args": `{"handle":"recovery_repo_tests"}`,
			}})
			return model, nil
		case tea.KeyF12:
			width := max(1, model.inputRect.W-2*model.Theme.Spacing.PadLR)
			model.Input.SetInput(strings.Repeat("a", width) + " x")
			model.Input.SetCursorFlat(width)
			return model, nil
		}
	}
	_, command := model.App.Update(message)
	return model, command
}
