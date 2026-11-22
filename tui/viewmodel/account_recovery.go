package viewmodel

import (
	"crude/types/statuscenter"
)

// showAccountRecovery preserves the exact failure along with a deliberate repair
// action; it never chooses a replacement account or replays the failed operation.
func (m *App) showAccountRecovery(resumed bool, detail string) {
	issue := statuscenter.OperationIssue{Title: "Account required", Summary: "Main Agent needs an account", Detail: detail,
		Guidance: "Add an account, select it for Main Agent, then create the session.", Target: statuscenter.RepairAccounts}
	if !resumed && len(m.ws.AllAccounts()) > 0 {
		issue.Target = statuscenter.RepairMainAgent
		issue.Guidance = "Review Main Agent's selected account and model in Application Settings, then create the session."
	}
	if resumed {
		issue.Summary = "This session needs an account"
		issue.Guidance = "Your conversation is still available. Add an account, then select and Save it in Session Settings."
		if m.ActiveSession != nil && len(m.ws.AllAccounts()) > 0 {
			issue.Target = statuscenter.RepairSessionAccount
			issue.Guidance = "Your conversation is still available. Choose an account and Save the session settings before sending another prompt."
		} else if m.ActiveSession == nil {
			issue.Guidance = "The session could not open. Review its saved accounts, then try resuming again."
		}
	}
	m.presentOperationIssue(issue)
}
