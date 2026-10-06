package types

// RepairTarget identifies an explicit user action, never an automatic fallback
// account, reset, retry, or permission grant.
type RepairTarget string

const (
	RepairSettings       RepairTarget = "settings"
	RepairAccounts       RepairTarget = "accounts"
	RepairMainAgent      RepairTarget = "main_agent"
	RepairSessionAccount RepairTarget = "session_account"
	RepairNetwork        RepairTarget = "network"
	RepairConfigFile     RepairTarget = "config_file"
)

// OperationIssue is presentation-neutral recovery context. The shell stays
// usable, while the affected operation remains unavailable until repaired.
// Detail must be credential-safe. Notices retain the same evidence independently
// of the recovery dialog; inspecting Notices is never required to understand it.
type OperationIssue struct {
	Title    string
	Summary  string
	Detail   string
	Guidance string
	Target   RepairTarget
	Path     string
}

// OperationIssueMsg delivers a later issue onto the frontend owner thread.
type OperationIssueMsg struct{ Issue OperationIssue }

// ResumeStartupMsg asks the frontend owner to activate passive startup resume.
// Account/persistence failures are presented by its normal launcher path.
type ResumeStartupMsg struct{ SessionID string }
