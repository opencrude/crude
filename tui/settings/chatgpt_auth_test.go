package settings

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func chatgptPreset() types.ProviderPreset {
	return types.ProviderPreset{ID: "chatgpt", Name: "OpenAI (ChatGPT subscription)", StandardID: "codex"}
}

func TestChatGPTFormShowsSignInWhenNoOAuth(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	if f := m.categories[catAccounts].Form.Field(fieldAPIKey); f != nil {
		t.Errorf("chatgpt form must not show an API-key field")
	}
	status := m.value(catAccounts, "chatgpt-status")
	if status != "Not signed in" {
		t.Errorf("status = %q, want 'Not signed in'", status)
	}
	m.SelectCategory(catAccounts)
	buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " ")
	if !strings.Contains(buttons, "Sign in") || !strings.Contains(buttons, "Disconnect") {
		t.Errorf("sign-in controls = %q", buttons)
	}
}

func TestChatGPTFormShowsSignOutWhenSignedIn(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", OAuth: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}, Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	if status := m.value(catAccounts, "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status = %q", status)
	}
	m.SelectCategory(catAccounts)
	buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " ")
	if !strings.Contains(buttons, "Disconnect") || !strings.Contains(buttons, "Reconnect") {
		t.Errorf("connected account controls = %q", buttons)
	}
}

func TestSwitchToChatGPTRebuildsFormWithAuthRows(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{

		Accounts: map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Work", Connection: provider.DefaultTemplates()["openrouter"]}, "cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", OAuth: &provider.OAuthCredentials{AccessToken: "AT", AccountID: "acct_1"}, Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, chatgptPreset()})

	// Selecting a connection opens that account's own authentication rows.
	m.selectAccount("cg")
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	if f := m.categories[catAccounts].Form.Field(fieldAPIKey); f != nil {
		t.Errorf("chatgpt form must not show an API-key field after switch")
	}
	if buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " "); !strings.Contains(buttons, "Disconnect") {
		t.Errorf("chatgpt actions not shown after selecting account: %q", buttons)
	}
	if status := m.value(catAccounts, "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status = %q", status)
	}
}

func TestSetChatGPTAuthSwapsRowsAndPreservesOAuthOnCollect(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	_, generation := m.SignInRequest()
	updated, _ := m.Update(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "a@b.com"}})
	m = &updated

	if status := m.value(catAccounts, "chatgpt-status"); status != "Sign-in stored" {
		t.Errorf("status = %q", status)
	}
	if buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " "); !strings.Contains(buttons, "Disconnect") {
		t.Errorf("disconnect action missing after sign-in: %q", buttons)
	}

	// OAuth stays in the editor until the account change is confirmed.
	got := m.accountEditor.draft
	if got.OAuth == nil || got.OAuth.Email != "a@b.com" {
		t.Errorf("OAuth not preserved on account: %+v", got.OAuth)
	}
	if m.AppSettings().Accounts["cg"].OAuth != nil {
		t.Fatal("unconfirmed login changed saved accounts")
	}

	// Sign-out clears OAuth and swaps back to the sign-in button.
	updated, _ = m.Update(ChatGPTSignOutMsg{AccountID: "cg", Generation: generation})
	m = &updated
	if got := m.accountEditor.draft.OAuth; got != nil {
		t.Errorf("OAuth should be nil after sign-out: %+v", got)
	}
	if status := m.value(catAccounts, "chatgpt-status"); status != "Not signed in" {
		t.Errorf("status = %q after sign-out", status)
	}
	if buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " "); !strings.Contains(buttons, "Sign in") {
		t.Errorf("sign-in action missing after disconnect: %q", buttons)
	}
}

func TestChatGPTSignInErrorShownOnStatusRow(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}

	_, generation := m.SignInRequest()
	updated, _ := m.Update(ChatGPTSignInResultMsg{AccountID: "cg", Generation: generation, Err: &provider.ChatGPTSignInError{Fatal: true, Msg: "authorization denied"}})
	m = &updated
	f := m.categories[catAccounts].Form.Field("chatgpt-status")
	if f == nil || len(f.Errors) == 0 || f.Errors[0] != "authorization denied" {
		t.Errorf("status errors = %#v", f)
	}
	if buttons := strings.Join(NewSurface(theme.DefaultDark(), m).accountAuthBar.ButtonLabelsForTest(), " "); !strings.Contains(buttons, "Sign in") {
		t.Errorf("sign-in action missing after failure: %q", buttons)
	}
}

func TestChatGPTSignInActionIsPaintedButton(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{

		Accounts: map[string]provider.Account{"cg": {ID: "cg", ProviderID: "chatgpt", Label: "Personal", Connection: provider.DefaultTemplates()["chatgpt"]}},
	}, []types.ProviderPreset{chatgptPreset()})
	if err := model.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 90, 36)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 36)
	host.Layout()

	model.SelectCategory(catAccounts)
	surfaceRoot.refreshForm()
	host.Layout()
	host.MarkLayout()
	host.Layout()
	buf := newTestBuffer(90, 36)
	host.Paint(buf)
	if !strings.Contains(bufferPlain(buf), "Sign in") {
		t.Fatalf("sign-in button not painted:\n%s", bufferPlain(buf))
	}
}
