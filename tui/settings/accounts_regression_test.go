package settings

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestAccountCatalogFilterAndScrollbarWithThousandAccounts(t *testing.T) {
	th := theme.DefaultDark()
	accounts := make(map[string]provider.Account, 1000)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("account-%04d", i)
		accounts[id] = provider.Account{ID: id, ProviderID: "openrouter", Label: id, APIKey: "not-for-display", Connection: provider.DefaultTemplates()["openrouter"]}
	}
	m := New(th, types.AppSettings{Accounts: accounts}, nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	host := ui.NewHost(th, s)
	host.Resize(124, 28)
	host.Layout()
	host.Focus(WorkspaceSettingsAccountFilterID)
	for _, letter := range "account-0999" {
		host.Handle(tea.KeyPressMsg{Code: letter, Text: string(letter)})
	}
	if rows := m.accountRows(); len(rows) != 1 || rows[0].ID != "account-0999" {
		t.Fatalf("filtered rows=%d want the last account", len(rows))
	}
	buf := surface.NewCellBuffer(124, 28)
	host.Paint(buf)
	if out := bufferPlain(buf); !strings.Contains(out, "account-0999") || !strings.Contains(out, "Add account") || strings.Contains(out, "not-for-display") {
		t.Fatalf("filter leaked or hid account controls:\n%s", out)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	host.Layout()
	if len(m.accountRows()) != 1000 || !s.accountScroll.Needed() {
		t.Fatal("clearing the filter did not restore the scrollable catalog")
	}
	track := s.accountScroll.Bounds()
	top, _ := s.accounts.table.Scroll().ThumbBounds(track.H)
	click := host.Handle(tea.MouseClickMsg{X: track.X, Y: track.Y + top, Button: tea.MouseLeft})
	if !click.Handled || !s.accountScroll.IsDragging() {
		t.Fatal("scrollbar thumb click did not capture the pointer")
	}
	motion := host.Handle(tea.MouseMotionMsg{X: track.X, Y: track.Y + track.H + 3, Button: tea.MouseLeft})
	if !motion.Handled || s.accounts.table.Scroll().Offset() != s.accounts.table.Scroll().MaxOffset() {
		t.Fatal("captured scrollbar drag did not reach the last account")
	}
	host.Handle(tea.MouseReleaseMsg{X: track.X, Y: track.Y + track.H + 3, Button: tea.MouseLeft})
	if got, want := s.accounts.table.Scroll().Offset(), s.accounts.table.Scroll().MaxOffset(); got != want {
		t.Fatalf("catalog release snapped to selected row: %d/%d", got, want)
	}
	buf = surface.NewCellBuffer(124, 28)
	host.Paint(buf)
	if !strings.Contains(bufferPlain(buf), "account-0999") {
		t.Fatal("dragged catalog to bottom without painting last account")
	}
	host.MarkLayout()
	host.Layout()
	if got, want := s.accounts.table.Scroll().Offset(), s.accounts.table.Scroll().MaxOffset(); got != want {
		t.Fatalf("settled frame snapped back to selected account: %d/%d", got, want)
	}
}

func TestAccountNoticeStaysAboveActionsAtBothWidths(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	m.accountError = "Account could not be saved"
	host := ui.NewHost(th, s)
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		host.Resize(size.W, size.H)
		host.Layout()
		buf := surface.NewCellBuffer(size.W, size.H)
		host.Paint(buf)
		noticeY := min(s.accountAdd.Bounds().Y, s.accountBar.Bounds().Y) - 1
		if notice := settingsRow(buf, noticeY, size.W); !strings.Contains(notice, "Account could not be saved") {
			t.Fatalf("size=%+v notice missing from reserved row: %q", size, notice)
		}
		for _, actionY := range []int{s.accountAdd.Bounds().Y, s.accountBar.Bounds().Y, s.accountMoreBar.Bounds().Y} {
			if actions := settingsRow(buf, actionY, size.W); strings.Contains(actions, "Account could not be saved") {
				t.Fatalf("size=%+v notice painted over account action row: %q", size, actions)
			}
		}
		if s.accounts.Bounds().Y+s.accounts.Bounds().H > noticeY {
			t.Fatalf("size=%+v account rows overlap notice row", size)
		}
	}
}

func TestCatalogActionsShareBottomAnchorAndMeasuredGaps(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	host := ui.NewHost(th, s)
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 88, H: 24}, {W: 70, H: 18}} {
		host.Resize(size.W, size.H)
		lastRow := size.H - 3 // one rule and the shared settings footer
		for _, catalog := range []string{"agents", "accounts"} {
			switch catalog {
			case "agents":
				m.SelectCategory(catAgents)
			case "accounts":
				m.SelectCategory(catAccounts)
			}
			s.refreshForm()
			host.MarkLayout()
			host.Layout()
			groups := []*ui.ActionBar{s.agents.bar}
			table := s.agents.table.Bounds()
			xOffset, yOffset := s.agents.Bounds().X, s.agents.Bounds().Y
			if catalog == "agents" && s.agents.stackActions {
				groups = append(groups, s.agents.more)
			}
			if catalog == "accounts" {
				groups = []*ui.ActionBar{s.accountAdd, s.accountBar, s.accountMoreBar}
				table = s.accounts.Bounds()
				xOffset, yOffset = 0, 0
			}
			if groups[len(groups)-1].Bounds().Y+yOffset != lastRow || groups[0].Bounds().X+xOffset != s.contentRect.X {
				t.Fatalf("%s at %+v: actions lack shared bottom/left anchors: %+v, %+v", catalog, size, groups[0].Bounds(), groups[len(groups)-1].Bounds())
			}
			first := groups[0].Bounds().Y + yOffset
			if table.Y+yOffset+table.H >= first-1 {
				t.Fatalf("%s at %+v: table overlaps action/notice area: table=%+v actions=%+v", catalog, size, table, groups[0].Bounds())
			}
			for i, group := range groups {
				r := group.Bounds()
				r.X += xOffset
				r.Y += yOffset
				if r.X+r.W > s.contentRect.X+s.contentRect.W || r.Y > lastRow || r.Y < lastRow-2 {
					t.Fatalf("%s at %+v: group %d extends outside its action area: %+v", catalog, size, i, r)
				}
				if i > 0 && r.Y == groups[i-1].Bounds().Y && r.X-groups[i-1].Bounds().X-groups[i-1].Bounds().W != s.tokens.ButtonGap() {
					t.Fatalf("%s at %+v: groups do not share button gap", catalog, size)
				}
			}
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			for _, label := range map[string][]string{"agents": {"Add", "Edit", "Clone", "Reset", "Remove"}, "accounts": {"Add account", "Edit", "Clone", "Remove"}}[catalog] {
				if !strings.Contains(bufferPlain(buf), label) {
					t.Fatalf("%s at %+v: %q is clipped or missing", catalog, size, label)
				}
			}
		}
	}
}

func accountSettingsFixture() types.AppSettings {
	return types.AppSettings{

		Accounts: map[string]provider.Account{
			"home": {ID: "home", ProviderID: "openrouter", Label: "Home", APIKey: "home-secret", Connection: provider.DefaultTemplates()["openrouter"]},
			"work": {ID: "work", ProviderID: "openrouter", Label: "Work", APIKey: "work-secret", Connection: provider.DefaultTemplates()["openrouter"]},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Provider: "openrouter", AccountID: "home", Model: "home-model"}}},
	}
}

func TestAccountTableShowsEveryConnectionAndKeepsButtonsStable(t *testing.T) {
	th := theme.DefaultDark()
	settings := accountSettingsFixture()
	for i := 0; i < 6; i++ {
		id, service := fmt.Sprintf("account-%d", i), fmt.Sprintf("service-%d", i)
		settings.Accounts[id] = provider.Account{ID: id, ProviderID: service, Label: fmt.Sprintf("Account %d", i), APIKey: fmt.Sprintf("private-%d", i), Connection: provider.Template{Name: fmt.Sprintf("Service %d", i), ProviderURL: "https://example.invalid/v1", APIStandard: "openai", AuthType: provider.AuthProtocol}}
	}
	m := New(th, settings, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	s := NewSurface(th, m)
	m.SelectCategory(catAccounts)
	s.refreshForm()
	host := ui.NewHost(th, s)
	paint := func(w, h int) string {
		host.Resize(w, h)
		host.MarkLayout()
		host.Layout()
		buf := surface.NewCellBuffer(w, h)
		host.Paint(buf)
		return bufferPlain(buf)
	}
	for frame := 0; frame < 20; frame++ {
		out := paint(124, 24)
		for _, want := range []string{"Connection", "Account", "Credentials", "OpenRouter", "Home", "Work", "Add account", "Clone", "Remove"} {
			if !strings.Contains(out, want) {
				t.Fatalf("frame %d missing %q:\n%s", frame, want, out)
			}
		}
		for i := 0; i < 6; i++ {
			if !strings.Contains(out, fmt.Sprintf("Account %d", i)) || strings.Contains(out, fmt.Sprintf("private-%d", i)) {
				t.Fatalf("frame %d hid or leaked account %d:\n%s", frame, i, out)
			}
		}
		s.SetModel(m)
	}
	if out := paint(64, 24); !strings.Contains(out, "Account") || !strings.Contains(out, "Connection") || !strings.Contains(out, "Add account") || !strings.Contains(out, "Account 5") {
		t.Fatalf("narrow account table lost header, add control, or rows:\n%s", out)
	}
}

func TestAccountCloneRequiresCompleteEditorBeforeRowExists(t *testing.T) {
	m := New(theme.DefaultDark(), accountSettingsFixture(), []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAccounts)
	s.refreshForm()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.clone"})
	if m.accountEditor == nil || m.accountEditor.draft.ID == "work" || m.accountEditor.draft.ProviderID != "openrouter" || m.accountEditor.draft.APIKey != "" || m.accountEditor.draft.OAuth != nil || m.accountEditor.draft.Connection.ProviderURL != m.accounts["work"].Connection.ProviderURL || len(m.accounts) != 2 {
		t.Fatal("Clone did not open a detached connection copy without credentials")
	}
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}); result.Cmd != nil || len(m.accounts) != 2 {
		t.Fatal("incomplete account entered the table")
	}
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Team")
	m.SetValueForTest(catAccounts, fieldAPIKey, "team-secret")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if result.Cmd == nil {
		t.Fatalf("valid Add action did not request persistence: %s", m.accountError)
	}
	request, ok := result.Cmd().(AccountChangeMsg)
	if !ok || request.Change.Before != nil || request.Change.After == nil || request.Change.After.Label != "Team" || request.Change.After.APIKey != "team-secret" {
		t.Fatalf("wrong account-only request: %+v", request)
	}
	if len(m.accounts) != 2 || m.AppSettings().Agents.Categories["default"].AccountID != "home" {
		t.Fatal("new account changed the Main Agent before persistence acknowledgement")
	}
	settings := accountSettingsFixture()
	settings.Accounts[request.Change.ID] = *request.Change.After
	m.Update(AccountChangeResultMsg{Target: m, Change: request.Change, Settings: settings})
	if len(m.accounts) != 3 || m.accounts[request.Change.ID].Label != "Team" || m.AppSettings().Agents.Categories["default"].AccountID != "home" || m.accountEditor != nil {
		t.Fatal("acknowledged account changed the Main Agent")
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.clone"})
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.cancel-edit"})
	if len(m.accounts) != 3 || m.accountEditor != nil {
		t.Fatal("cancel created an account")
	}
}

func TestAccountEditRemovalAndSelectionDoNotRewriteOtherSessions(t *testing.T) {
	m := New(theme.DefaultDark(), accountSettingsFixture(), []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAccounts)
	s.refreshForm()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Team")
	m.SetValueForTest(catAccounts, fieldAPIKey, "team-key")
	change := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}).Cmd().(AccountChangeMsg).Change
	if change.Before.APIKey != "work-secret" || change.After.APIKey != "team-key" || m.accounts["work"].APIKey != "work-secret" || m.AppSettings().Agents.Categories["default"].AccountID != "home" {
		t.Fatal("editing Work changed saved credentials or session routing before acknowledgement")
	}
	m.Update(AccountChangeResultMsg{Target: m, Change: change, Err: fmt.Errorf("write failed")})
	if m.accountEditor == nil || m.accounts["work"].APIKey != "work-secret" || !strings.Contains(m.accountError, "write failed") {
		t.Fatal("failed save discarded editor or changed persisted account")
	}
	discard := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.cancel-edit"}))
	if discard.Target != m || discard.Kind != PromptDiscardEditor || m.accountEditor == nil || m.value(catAccounts, fieldAccountLabel) != "Team" {
		t.Fatalf("dirty editor did not remain mounted: %+v", discard)
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
	if m.accountEditor == nil || m.value(catAccounts, fieldAPIKey) != "team-key" {
		t.Fatal("staying in the editor lost unsaved changes")
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: PromptDiscardEditor})
	if m.accountEditor != nil || m.accounts["work"].APIKey != "work-secret" {
		t.Fatal("explicit discard failed to restore the account table")
	}
	remove := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.remove"}))
	if remove.Kind != PromptRemoveAccount || remove.Target != m || remove.ID != "work" || remove.Label != "Work" || !s.PromptRelevant(remove) || len(m.accounts) != 2 {
		t.Fatalf("remove did not request a safe, owner-bound prompt: %+v", remove)
	}
	request := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove", Payload: remove.ID}).Cmd().(AccountChangeMsg)
	if request.Target != m || request.Change.After != nil || request.Change.Before.ID != "work" || len(m.accounts) != 2 {
		t.Fatal("accepted prompt mutated the account before persistence")
	}
}

func paintAccountSurface(t *testing.T, s *WorkspaceSettingsSurface, w, h int) *surface.CellBuffer {
	t.Helper()
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(w, h)
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	return buf
}

func TestAccountEditorOAuthResultsAreDraftScoped(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{Accounts: map[string]provider.Account{"personal": {ID: "personal", ProviderID: "chatgpt", Label: "Personal", OAuth: &provider.OAuthCredentials{AccessToken: "valid"}, Connection: provider.DefaultTemplates()["chatgpt"]}}}, []types.ProviderPreset{chatgptPreset()})
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	id, gen := m.SignInRequest()
	m.cancelAccountEdit()
	m.Update(ChatGPTSignInResultMsg{AccountID: id, Generation: gen, Creds: &provider.OAuthCredentials{AccessToken: "late"}})
	if got := m.accounts["personal"].OAuth.AccessToken; got != "valid" {
		t.Fatalf("closed editor accepted late OAuth result: %q", got)
	}
	if err := m.beginAccountEdit(true, "chatgpt"); err != nil {
		t.Fatal(err)
	}
	id, gen = m.SignInRequest()
	m.Update(ChatGPTSignInResultMsg{AccountID: id, Generation: gen, Creds: &provider.OAuthCredentials{AccessToken: "new"}})
	if m.accountEditor.draft.OAuth == nil || m.accountEditor.draft.OAuth.AccessToken != "new" || len(m.accounts) != 1 {
		t.Fatal("OAuth result did not stay in new account draft")
	}
}

func TestAccountModelFetchUsesEditedUnsavedKey(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{Accounts: map[string]provider.Account{"router": {ID: "router", ProviderID: "openrouter", Label: "Work", APIKey: "original", Connection: provider.Template{ProviderURL: "https://openrouter.ai/api/v1", APIStandard: "openai"}}}}, []types.ProviderPreset{{ID: "custom", Name: "Custom"}, {ID: "openrouter", Name: "OpenRouter"}})
	m.selectAccount("router")
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	m.SetValueForTest(catAccounts, fieldAPIKey, "draft-key")
	request := m.fetchModelsForAccountCmd("openrouter", "router")().(FetchModelsMsg)
	if request.Account.APIKey != "draft-key" || request.BaseURL != "https://openrouter.ai/api/v1" || m.accounts["router"].APIKey != "original" {
		t.Fatalf("model discovery changed saved credentials or ignored edited key: %+v", request)
	}
}

func TestAccountsUnavailableInWorkspaceScope(t *testing.T) {
	m := New(theme.DefaultDark(), accountSettingsFixture(), []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}})
	m.SelectCategory(catAccounts)
	m.SetScope(types.SettingsScopeWorkspace)
	for _, item := range m.CategoryItems() {
		if item.Label == "Accounts" {
			t.Fatal("workspace settings exposed application accounts or credentials")
		}
	}
}
