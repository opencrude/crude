package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func settingsPrompt(t *testing.T, result ui.EventResult) PromptMsg {
	t.Helper()
	if !result.Handled || result.Cmd == nil {
		t.Fatalf("expected handled settings prompt command: %+v", result)
	}
	msg, ok := result.Cmd().(PromptMsg)
	if !ok {
		t.Fatalf("command returned %T, want PromptMsg", result.Cmd())
	}
	return msg
}

func TestLayeredRemovalPromptsKeepCatalogMounted(t *testing.T) {
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		settings := accountSettingsFixture()
		th := theme.DefaultDark()
		m := New(th, settings, nil)
		s := NewSurface(th, m)
		s.ShowAccounts()
		m.selectAccount("work")
		s.refreshForm()
		host := ui.NewHost(th, s)
		host.Resize(size.W, size.H)
		host.Layout()
		title := s.WindowTitle()
		before := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.remove"}))
		if prompt.Target != m || prompt.Kind != PromptRemoveAccount || !s.PromptRelevant(prompt) {
			t.Fatalf("size=%+v wrong prompt: %+v", size, prompt)
		}
		host.MarkLayout()
		host.Layout()
		buf := surface.NewCellBuffer(size.W, size.H)
		host.Paint(buf)
		after := bufferPlain(buf)
		for _, label := range []string{"Accounts", "Work", "[ Edit ]", "[ Remove ]"} {
			if !strings.Contains(after, label) {
				t.Fatalf("size=%+v missing %q while prompt is pending:\n%s", size, label, after)
			}
		}
		if s.WindowTitle() != title || before != after || strings.Contains(after, "Keep account") || strings.Contains(after, "Remove Work?") {
			t.Fatalf("size=%+v prompt replaced the catalog inline:\nbefore:\n%s\nafter:\n%s", size, before, after)
		}
		if len(m.accounts) != len(settings.Accounts) {
			t.Fatal("requesting removal changed the accounts")
		}
	}
}

func TestLayeredPromptRejectsStaleRemovalAndRequiresPayload(t *testing.T) {
	th := theme.DefaultDark()
	settings := accountSettingsFixture()
	m := New(th, settings, nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	m.selectAccount("work")
	s.refreshForm()
	account := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.remove"}))
	if account.ID != "work" || account.Label != "Work" || !s.PromptRelevant(account) {
		t.Fatalf("account prompt not relevant: %+v", account)
	}
	if res := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove"}); res.Cmd != nil {
		t.Fatal("account removed without a prompt payload")
	}
	if res := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove", Payload: "home"}); res.Cmd != nil {
		t.Fatal("different account ID was accepted for the selected account")
	}
	m.selectAccount("home")
	if s.PromptRelevant(account) {
		t.Fatal("old account selection accepted a queued prompt")
	}
	m.selectAccount("work")
	changed := m.accounts["work"]
	changed.Label = "Updated"
	m.accounts["work"] = changed
	if s.PromptRelevant(account) {
		t.Fatal("modified account preimage accepted a queued prompt")
	}
	m.accounts["work"] = settings.Accounts["work"]
	m.accountBusy = true
	if s.PromptRelevant(account) {
		t.Fatal("busy settings accepted an account prompt")
	}
	m.accountBusy = false
	replacement := NewSurface(th, New(th, settings, nil))
	if replacement.PromptRelevant(account) {
		t.Fatal("replaced settings owner accepted the old prompt")
	}
	if !s.PromptRelevant(account) {
		t.Fatal("unchanged account prompt became stale")
	}
	request := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.confirm-remove", Payload: account.ID})
	if request.Cmd == nil {
		t.Fatal("accepted account removal did not request persistence")
	}
	change, ok := request.Cmd().(AccountChangeMsg)
	if !ok || change.Target != m || change.Change.ID != "work" || change.Change.Before == nil || change.Change.After != nil || len(m.accounts) != 2 {
		t.Fatalf("account changed before acknowledgement: %+v", change)
	}
	m.accountBusy = false
}

func TestLayeredDirtyEditorBackEscAndScopeDecision(t *testing.T) {
	for _, tc := range []struct {
		name, editField, editAction, back string
		index                             int
	}{{"account", fieldAccountLabel, "settings.account.edit", "settings.account.cancel-edit", catAccounts}} {
		t.Run(tc.name, func(t *testing.T) {
			th := theme.DefaultDark()
			m := New(th, accountSettingsFixture(), nil)
			s := NewSurface(th, m)
			s.ShowAccounts()
			s.HandleAction(ui.EventContext{}, ui.Action{ID: tc.editAction})
			m.SetValueForTest(tc.index, tc.editField, "Unsaved edit")
			s.refreshForm()
			for _, action := range []string{tc.back, "form.cancel"} {
				prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: action}))
				if prompt.Kind != PromptDiscardEditor || !s.PromptRelevant(prompt) || m.value(tc.index, tc.editField) != "Unsaved edit" {
					t.Fatalf("%s lost dirty editor before decision: %+v", action, prompt)
				}
				for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
					out := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
					if !strings.Contains(out, "Unsaved edit") || !strings.Contains(out, "Back") || strings.Contains(out, "Discard changes") {
						t.Fatalf("size=%+v editor disappeared behind inline prompt:\n%s", size, out)
					}
				}
				s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
				if m.value(tc.index, tc.editField) != "Unsaved edit" {
					t.Fatal("cancel/stay lost editor draft")
				}
			}
			prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.scope", Payload: scopeTabWorkspace}))
			if prompt.Kind != PromptDiscardEditor || m.Scope() != types.SettingsScopeApplication || m.SelectedCategory() != tc.index {
				t.Fatalf("scope changed before dirty-editor decision: %+v", prompt)
			}
			s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
			if m.Scope() != types.SettingsScopeApplication || m.value(tc.index, tc.editField) != "Unsaved edit" {
				t.Fatal("staying switched scope or lost edits")
			}
			prompt = settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.scope", Payload: scopeTabWorkspace}))
			res := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: prompt.Kind})
			if res.Cmd == nil {
				t.Fatal("confirmed discard did not route pending scope")
			}
			if scope, ok := res.Cmd().(ScopeMsg); !ok || scope.Scope != types.SettingsScopeWorkspace || m.accountEditor != nil {
				t.Fatalf("discard failed to return pending scope: %+v", res)
			}
		})
	}
}

func TestDirtyEditorCategoryChangeWaitsForDecision(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Unsaved edit")
	host := ui.NewHost(th, s)
	host.Resize(124, 28)
	host.Layout()
	host.Focus(WorkspaceSettingsListID)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	prompt := settingsPrompt(t, result)
	if prompt.Kind != PromptDiscardEditor || m.SelectedCategory() != catAccounts || m.accountEditor == nil {
		t.Fatalf("category changed before discard decision: prompt=%+v selected=%d", prompt, m.SelectedCategory())
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
	if m.SelectedCategory() != catAccounts || m.value(catAccounts, fieldAccountLabel) != "Unsaved edit" {
		t.Fatal("staying in editor lost changes or switched category")
	}
	host.MarkLayout()
	host.Layout()
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	prompt = settingsPrompt(t, result)
	if prompt.Kind != PromptDiscardEditor || !s.PromptRelevant(prompt) {
		t.Fatalf("category navigation did not request a fresh prompt: %+v", prompt)
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: prompt.Kind})
	if m.accountEditor != nil || m.SelectedCategory() != catAgents {
		t.Fatal("explicit discard failed to navigate to requested category")
	}
	if out := bufferPlain(paintAccountSurface(t, s, 70, 18)); !strings.Contains(out, "Accounts") || !strings.Contains(out, "[ Remove ]") {
		t.Fatalf("navigated account table not painted:\n%s", out)
	}
}

func TestSettingsValidationErrorStaysOnEditorWhenBackPrompts(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "")
	res := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if res.Cmd != nil || m.accountEditor == nil {
		t.Fatalf("invalid account was submitted or its editor closed: %+v", res)
	}
	if field := m.categories[catAccounts].Form.Field(fieldAccountLabel); field == nil || len(field.Errors) == 0 {
		t.Fatal("validation error is not attached to the account name field")
	}
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.cancel-edit"}))
	if prompt.Kind != PromptDiscardEditor || !s.PromptRelevant(prompt) {
		t.Fatalf("back from invalid editor skipped dirty prompt: %+v", prompt)
	}
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		out := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(out, "Edit account") || !strings.Contains(out, "Account name") || !strings.Contains(out, "Back") || strings.Contains(out, "Discard unsaved account") {
			t.Fatalf("size=%+v invalid editor was replaced by inline prompt:\n%s", size, out)
		}
	}
}

func TestDirtySettingsClosePromptsWithoutHidingPreferences(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	m.SelectCategory(catAppearance)
	s.refreshForm()
	m.SetValueForTest(catAppearance, "preset", "light")
	if !m.hasUnsavedPreferences() {
		t.Fatal("fixture did not dirty preferences")
	}
	if prompt := settingsPrompt(t, s.RequestClose()); prompt.Kind != PromptDiscardSettings {
		t.Fatalf("window close did not ask about preference drafts: %+v", prompt)
	}
	if m.value(catAppearance, "preset") != "light" {
		t.Fatal("window close glyph silently discarded dirty preferences")
	}
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "form.cancel"}); result.Cmd != nil || m.value(catAppearance, "preset") != "light" {
		t.Fatal("leaving the form focus discarded or closed unsaved preferences")
	}
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.cancel"}))
	if prompt.Target != m || prompt.Kind != PromptDiscardSettings || !s.PromptRelevant(prompt) {
		t.Fatalf("close discarded dirty preferences silently: %+v", prompt)
	}
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		out := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(out, "Theme") || !strings.Contains(out, "Save") || strings.Contains(out, "Discard unsaved preferences?") {
			t.Fatalf("size=%+v preferences replaced by inline confirmation:\n%s", size, out)
		}
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
	if m.value(catAppearance, "preset") != "light" {
		t.Fatal("staying discarded preferences")
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: PromptDiscardSettings})
	if result.Cmd == nil {
		t.Fatal("confirmed preference discard did not close settings")
	}
	if _, ok := result.Cmd().(CancelMsg); !ok {
		t.Fatalf("confirmed preference discard returned %T, want CancelMsg", result.Cmd())
	}
}

func TestDirtyEditorEscRequestsPromptFromHost(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Unsaved edit")
	host := ui.NewHost(th, s)
	host.Resize(70, 18)
	host.Layout()
	host.Focus(WorkspaceSettingsEditorBarID)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if prompt := settingsPrompt(t, result); prompt.Kind != PromptDiscardEditor || m.accountEditor == nil {
		t.Fatalf("Esc silently closed dirty editor: %+v", prompt)
	}
}

func TestLayeredDiscardPromptRejectsChangedDraft(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Proposed")
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.cancel-edit"}))
	if !s.PromptRelevant(prompt) {
		t.Fatal("unchanged account draft was rejected")
	}
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Later result")
	if s.PromptRelevant(prompt) {
		t.Fatal("prompt could discard a newer account draft")
	}
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.stay"})
	if m.accountEditor == nil {
		t.Fatal("staying in editor discarded the newer draft")
	}

}

func TestConfirmedDiscardFocusesUnrelatedSettingsPage(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	host := ui.NewHost(th, s)
	host.Resize(104, 28)
	host.Layout()
	s.SetFocusHandler(func(id ui.ComponentID) { _ = host.Focus(id) })
	s.ShowAccounts()
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	m.SetValueForTest(catAccounts, fieldAccountLabel, "Unsaved edit")
	prompt := settingsPrompt(t, s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.cancel-edit"}))
	if prompt.Kind != PromptDiscardEditor || !s.PromptRelevant(prompt) {
		t.Fatal("dirty account did not request an owner-bound decision")
	}
	m.nextCategory = catAppearance
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.discard.confirm", Payload: prompt.Kind})
	host.MarkLayout()
	host.Layout()
	if m.SelectedCategory() != catAppearance || s.form.Field("preset") == nil || host.Focused() != WorkspaceSettingsFormID {
		t.Fatalf("discard did not focus the requested form: category=%d focus=%q", m.SelectedCategory(), host.Focused())
	}
}
