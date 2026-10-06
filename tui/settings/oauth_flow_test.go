package settings

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// TestOAuthResultReachesModelThroughFrameDispatch verifies that a
// ChatGPTSignInResultMsg dispatched through the WindowFrameWidget (the path
// used by viewmodel.forwardEventToWindowContent) reaches the settings model
// and swaps the sign-in row for the sign-out row.
func TestOAuthResultReachesModelThroughFrameDispatch(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	// The real app passes the WorkspaceSettings widget as the frame's
	// eventTarget so unhandled host events reach HandleEvent → model.Update.
	ws := &workspaceSettingsForTest{model: model, surface: NewSurface(th, model)}
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", ws.surface, ws, true, 90, 24)
	ws.surface.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model.SelectCategory(catAccounts)
	ws.surface.refreshForm()
	ws.surface.FocusForm()
	host.Layout()

	if status := model.value(catAccounts, "chatgpt-status"); status != "Not signed in" {
		t.Fatalf("initial status = %q, want 'Not signed in'", status)
	}

	// Dispatch the sign-in result exactly as viewmodel does.
	_, generation := model.SignInRequest()
	res := frame.Dispatch(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}})
	if res.Cmd != nil {
		t.Fatalf("unexpected cmd from dispatch: %v", res.Cmd)
	}

	if status := model.value(catAccounts, "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status after dispatch = %q, want stored sign-in", status)
	}
	if buttons := ws.surface.accountAuthBar.ButtonLabelsForTest(); !slices.Contains(buttons, "Disconnect") {
		t.Errorf("disconnect button should appear after sign-in: %v", buttons)
	}
}

// TestFetchModelsAfterSwitchFromOpenCodeGo simulates the reported bug: another
// connection used OpenCode Go, but fetching from the selected ChatGPT account
// must use that account's own transport.
func TestFetchModelsAfterSwitchFromOpenCodeGo(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{
		{ID: "opencode-go", Name: "OpenCode Go", StandardID: "openai"},
		chatgptPreset(),
	})

	model.selectAccount("cg")
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	// Sign in.
	_, generation := model.SignInRequest()
	updated, _ := model.Update(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}})
	model = &updated

	// Now fetch models and inspect the FetchModelsMsg payload.
	cmd := model.fetchModelsCmd()
	if cmd == nil {
		t.Fatal("fetchModelsCmd returned nil")
	}
	msg := cmd()
	fm, ok := msg.(FetchModelsMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want FetchModelsMsg", msg)
	}
	if fm.ProviderID != "chatgpt" {
		t.Errorf("ProviderID = %q, want chatgpt", fm.ProviderID)
	}
	if fm.BaseURL != "https://chatgpt.com/backend-api/codex" {
		t.Errorf("BaseURL = %q, want chatgpt codex endpoint", fm.BaseURL)
	}
	if fm.StandardID != "codex" {
		t.Errorf("StandardID = %q, want codex", fm.StandardID)
	}
}

// workspaceSettingsForTest mirrors popup.WorkspaceSettings without importing
// the popup package (which would create an import cycle in tests).
type workspaceSettingsForTest struct {
	model   *Model
	surface *WorkspaceSettingsSurface
}

func (ws *workspaceSettingsForTest) Component() ui.Component { return ws.surface }

func (ws *workspaceSettingsForTest) HandleEvent(msg tea.Msg) tea.Cmd {
	oldPane := ws.model.Pane()
	_, cmd := ws.model.Update(msg)
	ws.surface.SetModel(ws.model)
	if oldPane == 1 && ws.model.Pane() == 0 {
		ws.surface.FocusCategories()
	}
	return cmd
}
