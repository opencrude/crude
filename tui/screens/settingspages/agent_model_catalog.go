package settingspages

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"crude/types/setting"
)

// Each secondary model picker uses its own account. Only suggestions may
// inherit the agent's connection or offer an inherited model as a choice.
func (m *Model) secondaryModelPicker() (*ui.FieldState, string, func([]setting.ModelEntry) []ui.FormOption) {
	form := m.categories[catAgents].Form
	if form != nil {
		if field := form.Field(fieldRankerModel); field != nil {
			return field, form.Field(fieldRankerAccount).Value.String(), ui.ModelOptions
		}
		if field := form.Field(fieldSuggestModel); field != nil {
			accountID, parent := m.suggestionAccountID(), m.suggestionParent()
			return field, accountID, func(models []setting.ModelEntry) []ui.FormOption {
				options := ui.ModelOptions(models)
				if inherited := parent.modelFor(accountID); inherited != "" {
					options = append([]ui.FormOption{{ID: "", Label: parent.name + "'s model", Detail: inherited}}, options...)
				}
				return options
			}
		}
	}
	return nil, "", nil
}

func (m *Model) syncSecondaryAccount() tea.Cmd {
	field, accountID, options := m.secondaryModelPicker()
	if field == nil {
		return nil
	}
	form := m.categories[catAgents].Form
	if tier := form.Field(fieldSuggestTier); tier != nil {
		tier.Options = m.suggestionTierOptions(accountID, tier.Value.String())
		tier.Validate()
	}
	account, ok := m.accounts[accountID]
	models := m.models[accountModelKey(account.ProviderID, accountID)]
	field.Options, field.Errors, field.Loading, field.LoadMessage = options(models), nil, false, ""
	if !ok || len(models) > 0 {
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models…"
	// Both fields consume the same account-scoped result when they share an account.
	if primary := form.Field(fieldModel); primary != nil && primary.Loading && m.value(catAgents, fieldConnection) == accountID {
		return nil
	}
	return m.fetchModelsForAccountCmd(account.ProviderID, accountID)
}

func (m *Model) setSecondaryModels(providerID, accountID string, models []setting.ModelEntry, problem string) {
	field, id, options := m.secondaryModelPicker()
	if field == nil || id != accountID || m.accounts[id].ProviderID != providerID {
		return
	}
	field.Loading, field.LoadMessage = false, ""
	if problem != "" {
		field.Errors = []string{problem}
		return
	}
	field.Options, field.Errors = options(models), nil
}
