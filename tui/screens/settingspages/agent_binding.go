package settingspages

import (
	"crude/tui/ui"
	"crude/types/setting"
)

// Agent editors whose empty Account and Model use another agent's choice.
// The kind is fixed by the editor, never guessed from its fields.
const (
	parentNone        = ""             // Main Agent; Memory ranking has its own account
	parentMain        = "main"         // Researcher, Crafter and Errand follow Main Agent
	parentRole        = "role"         // a named agent follows its role
	parentErrand      = "errand"       // Memory Agent follows Errand as Errand runs
	parentErrandSaved = "errand-saved" // Auto Review uses only Errand's own saved choice
)

// agentParent is the agent whose account and model an editor uses while its
// own Account and Model are empty.
type agentParent struct {
	name, account, model string
	// sameAccount is true for background jobs: the parent's model follows
	// only while the job uses the parent's account (types.InheritAgentModel).
	sameAccount bool
}

// modelFor is the model the parent supplies when the editor uses account.
func (p *agentParent) modelFor(account string) string {
	if p == nil || p.sameAccount && account != p.account {
		return ""
	}
	return p.model
}

func (m *Model) agentParent(kind string, form *ui.FormModel) *agentParent {
	if m.agents == nil || kind == parentNone {
		return nil
	}
	main := m.agents.Categories["default"]
	errand := m.agents.Categories[setting.ErrandRoleID]
	switch kind {
	case parentMain:
		return &agentParent{name: setting.AgentRoleName("default"), account: main.AccountID, model: main.Model}
	case parentRole:
		role := "crafter"
		if field := form.Field("agent-role"); field != nil && field.Value.String() != "" {
			role = field.Value.String()
		}
		category := m.agents.Categories[role]
		account, model := setting.InheritRoleModel(category.AccountID, category.Model, main.AccountID, main.Model)
		return &agentParent{name: setting.AgentRoleName(role), account: account, model: model}
	case parentErrand:
		if errand.Inherit == setting.AgentInheritMain {
			errand.Model = ""
		}
		account, model := setting.InheritRoleModel(errand.AccountID, errand.Model, main.AccountID, main.Model)
		return &agentParent{name: setting.ErrandName, account: account, model: model, sameAccount: true}
	case parentErrandSaved:
		return &agentParent{name: setting.ErrandName, account: errand.AccountID, model: errand.Model, sameAccount: true}
	}
	return nil
}

// parentOf is the parent of the agent edited in a settings category. The
// Agents page records its open editor's kind in draftParent.
func (m *Model) parentOf(category int) *agentParent {
	switch category {
	case catResearcher, catCrafter, catErrand:
		return m.agentParent(parentMain, nil)
	case catAgents:
		return m.agentParent(m.draftParent, m.categories[catAgents].Form)
	}
	return nil
}

// effectiveConnection is the account and model the edited agent uses: its
// own choices, else its parent's.
func (m *Model) effectiveConnection(category int) (string, string) {
	account, model := m.value(category, fieldConnection), m.value(category, fieldModel)
	parent := m.parentOf(category)
	switch {
	case parent == nil:
		return account, model
	case parent.sameAccount:
		return setting.InheritAgentModel(account, model, parent.account, parent.model)
	}
	return setting.InheritRoleModel(account, model, parent.account, parent.model)
}

// accountOptions lists the saved accounts. A selected account that was
// removed stays visible by name, never by ID, and cannot be chosen again.
func (m *Model) accountOptions(selected string) []ui.FormOption {
	options := m.connectionOptions(false)
	if _, ok := m.accounts[selected]; selected != "" && !ok {
		options = append(options, ui.FormOption{ID: selected, Label: setting.RemovedAccountName, Disabled: true})
	}
	return options
}

// syncConnection puts the working account name and model first, so narrow
// terminals keep the value before clipping its source.
func (m *Model) syncConnection(form *ui.FormModel, accountField, modelField string, parent *agentParent) {
	account, model := form.Field(accountField), form.Field(modelField)
	if account == nil || model == nil {
		return
	}
	selected := account.Value.String()
	account.Options, account.Placeholder = m.accountOptions(selected), "Choose an account"
	effective := selected
	if parent != nil {
		label, name := parent.name+"'s account", setting.AccountName(m.accounts, parent.account)
		account.Options = append([]ui.FormOption{{ID: "", Label: label, Detail: name}}, account.Options...)
		if name != "" {
			account.Placeholder = name + " · " + parent.name
		}
		if effective == "" {
			effective = parent.account
		}
	}
	model.Placeholder = "Choose or enter a model ID"
	if inherited := parent.modelFor(effective); inherited != "" {
		model.Placeholder = inherited + " · " + parent.name
	}
	model.Options = m.parentModelOptions(effective, parent)
}

// parentModelOptions lists the account's models, after the parent's model
// when the editor can use it. An empty list means the models are not loaded.
func (m *Model) parentModelOptions(accountID string, parent *agentParent) []ui.FormOption {
	options := ui.ModelOptions(m.models[accountModelKey(m.accounts[accountID].ProviderID, accountID)])
	if inherited := parent.modelFor(accountID); inherited != "" && len(options) != 0 {
		options = append([]ui.FormOption{{ID: "", Label: parent.name + "'s model", Detail: inherited}}, options...)
	}
	return options
}

// syncAgentConnection refreshes the Account and Model rows of the agent edited
// in a category.
func (m *Model) syncAgentConnection(category int) {
	if form := m.categories[category].Form; form != nil {
		m.syncConnection(form, fieldConnection, fieldModel, m.parentOf(category))
		m.syncCompactionHint(category)
	}
}

// suggestionParent is Errand as its open editor would run: the Model tab's
// choices, else Main Agent's. Suggestions use the same account and model.
func (m *Model) suggestionParent() *agentParent {
	account, model := m.effectiveConnection(catAgents)
	return &agentParent{name: setting.ErrandName, account: account, model: model, sameAccount: true}
}

// parentModelName is the Model column for an agent with no model of its own.
func parentModelName(parent string) string {
	if parent == "" {
		return "Not chosen"
	}
	return parent + "'s model"
}
