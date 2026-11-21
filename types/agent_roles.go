package types

const (
	// ErrandRoleID identifies the existing workspace helper in saved roles and
	// sessions. The role's display name does not change that identity.
	ErrandRoleID = "errand"
	ErrandName   = "Errand"

	// AgentInheritMain is the ordinary role marker for following Main Agent.
	AgentInheritMain = "main"
)

// AgentNameForRole keeps the role label, not a generated personal name, for
// Errand. Distinct Errands still have distinct agent and session IDs.
func AgentNameForRole(role, name string) string {
	if role == ErrandRoleID {
		return ErrandName
	}
	return name
}

// InheritRoleModel resolves the defaults used by ordinary delegatable roles.
// Their existing inheritance mode can keep the parent's model on a different
// account. Independent background jobs use InheritAgentModel instead.
func InheritRoleModel(account, model, parentAccount, parentModel string) (string, string) {
	if account == "" {
		account = parentAccount
	}
	if model == "" {
		model = parentModel
	}
	return account, model
}

// InheritAgentModel resolves a job's sparse account/model override. A different
// account must select a model on that account; model IDs are not portable.
func InheritAgentModel(account, model, parentAccount, parentModel string) (string, string) {
	if account == "" {
		account = parentAccount
	}
	if model == "" && account == parentAccount {
		model = parentModel
	}
	return account, model
}
