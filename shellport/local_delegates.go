package shellport

import (
	"context"
	"crude/fileio"
	"crude/localhistory"
	"crude/memory"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
	"time"
)

func (l *local) CreateFile(path string) workspace.FileOperationResultMsg {
	return l.ws.CreateFile(path)
}

func (l *local) CreateFolder(path string) workspace.FileOperationResultMsg {
	return l.ws.CreateFolder(path)
}

func (l *local) RenamePath(oldPath, newPath string) workspace.FileOperationResultMsg {
	return l.ws.RenamePath(oldPath, newPath)
}

func (l *local) DeletePath(path string) workspace.FileOperationResultMsg {
	return l.ws.DeletePath(path)
}

func (l *local) ApplyWorkspaceEdit(edits []workspace.FileEdit) workspace.FileOperationResultMsg {
	return l.ws.ApplyWorkspaceEdit(edits)
}

func (l *local) ApplyLSPRename(ctx context.Context, edits []workspace.FileEdit) (workspace.FileOperationResultMsg, string) {
	return l.ws.ApplyLSPRename(ctx, edits)
}

func (l *local) StartProjectSearch(query string, opts fileio.ProjectSearchOptions) uint64 {
	return l.ws.StartProjectSearch(query, opts)
}

func (l *local) CancelProjectSearch() {
	l.ws.CancelProjectSearch()
}

func (l *local) SetEventBus(bus *workspace.EventBus) {
	l.ws.SetEventBus(bus)
}

func (l *local) SwitchProject(projectDir string) error {
	return l.ws.SwitchProject(projectDir)
}

func (l *local) TakeStartupIssues() []types.OperationIssue {
	return l.ws.TakeStartupIssues()
}

func (l *local) LastRegistryError() error {
	return l.ws.LastRegistryError()
}

func (l *local) SetEventSender(sender session.EventSender) {
	l.ws.SetEventSender(sender)
}

func (l *local) PreviewGitDiscard(ctx context.Context, change workspace.GitChange) (workspace.GitDiscardPreview, error) {
	return l.ws.PreviewGitDiscard(ctx, change)
}

func (l *local) DiscardGitReviewed(ctx context.Context, preview workspace.GitDiscardPreview) (string, error) {
	return l.ws.DiscardGitReviewed(ctx, preview)
}

func (l *local) DiscardGitHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) (string, error) {
	return l.ws.DiscardGitHunk(ctx, comparison, hunk)
}

func (l *local) ResolveGitConflict(ctx context.Context, path, fingerprint string, choices []workspace.GitConflictChoice) (string, error) {
	return l.ws.ResolveGitConflict(ctx, path, fingerprint, choices)
}

func (l *local) PrepareCommitMessageWriter() (CommitMessageWriter, error) {
	writer, err := l.ws.PrepareCommitMessageWriter()
	if err != nil {
		return nil, err
	}
	return writer, nil
}

func (l *local) LoadAppSettings() types.AppSettings {
	return l.ws.LoadAppSettings()
}

func (l *local) AppDefaults() types.AppSettings {
	return l.ws.AppDefaults()
}

func (l *local) LoadWorkspaceOverrides() types.WorkspaceOverrides {
	return l.ws.LoadWorkspaceOverrides()
}

func (l *local) WorkspaceAgentDefaults() types.AgentsSettings {
	return l.ws.WorkspaceAgentDefaults()
}

func (l *local) SaveAccountChange(change types.AccountChange) error {
	return l.ws.SaveAccountChange(change)
}

func (l *local) AllAccounts() map[string]provider.Account {
	return l.ws.AllAccounts()
}

func (l *local) PreparePreferencesSave(scope types.SettingsScope, edits []types.SettingEdit) func() workspace.PreferencesSaveResult {
	return l.ws.PreparePreferencesSave(scope, edits)
}

func (l *local) ApplyPreferencesSave(result workspace.PreferencesSaveResult) {
	l.ws.ApplyPreferencesSave(result)
}

func (l *local) PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	return l.ws.PrepareAccountModels(templateID, accountID, baseURL, standardID)
}

func (l *local) PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	return l.ws.PrepareDraftAccountModels(account, baseURL, standardID)
}

func (l *local) LoadToolLayout() (workspace.ToolLayout, error) {
	return l.ws.LoadToolLayout()
}

func (l *local) SaveToolLayout(layout workspace.ToolLayout) error {
	return l.ws.SaveToolLayout(layout)
}

func (l *local) AdvanceAgentState(now time.Time) workspace.AgentReduction {
	return l.ws.AdvanceAgentState(now)
}

func (l *local) AgentActivity(agentID string) types.AgentActivityView {
	return l.ws.AgentActivity(agentID)
}

func (l *local) AgentModelIdentity(agentID string) types.TurnIdentity {
	return l.ws.AgentModelIdentity(agentID)
}

func (l *local) AgentTurnActive(agentID string) bool {
	return l.ws.AgentTurnActive(agentID)
}

func (l *local) UsageSummary(agentID string, scope types.UsageScope) (types.UsageSummary, bool) {
	return l.ws.UsageSummary(agentID, scope)
}

func (l *local) YoloForAgent(agentID string) bool {
	return l.ws.YoloForAgent(agentID)
}

func (l *local) PlanModeForAgent(agentID string) bool {
	return l.ws.PlanModeForAgent(agentID)
}

func (l *local) SetPlanModeForAgent(agentID string, enabled bool) error {
	return l.ws.SetPlanModeForAgent(agentID, enabled)
}

func (l *local) GoalForAgent(agentID string) (types.Goal, bool) {
	return l.ws.GoalForAgent(agentID)
}

func (l *local) GoalAction(agentID, action, objective string) (types.Goal, bool, error) {
	return l.ws.GoalAction(agentID, action, objective)
}

func (l *local) SetYoloForAgent(agentID string, enabled bool) bool {
	return l.ws.SetYoloForAgent(agentID, enabled)
}

func (l *local) ApplyAgentEvent(evt workspace.AgentEvent) workspace.AgentReduction {
	return l.ws.ApplyAgentEvent(evt)
}

func (l *local) ApplyAgentStatusEvent(evt workspace.AgentEvent) {
	l.ws.ApplyAgentStatusEvent(evt)
}

func (l *local) AppendAgentMessages(id string, messages []workspace.Message, stopped bool) bool {
	return l.ws.AppendAgentMessages(id, messages, stopped)
}

func (l *local) RegisterAgentRoot(id string) *workspace.AgentNode {
	return l.ws.RegisterAgentRoot(id)
}

func (l *local) ForgetAgentInventory(id string) {
	l.ws.ForgetAgentInventory(id)
}

func (l *local) SyncAgentStatusInventory() {
	l.ws.SyncAgentStatusInventory()
}

func (l *local) CancelAgent(agentID string) {
	l.ws.CancelAgent(agentID)
}

func (l *local) CloseAgent(agentID string) {
	l.ws.CloseAgent(agentID)
}

func (l *local) BeginAgentCancellation(id string) bool {
	return l.ws.BeginAgentCancellation(id)
}

func (l *local) SteerAgent(agentID string, input provider.UserInput) bool {
	return l.ws.SteerAgent(agentID, input)
}

func (l *local) ResolveQuestion(agentID, blockID string, answers, questions map[string]string, cancelled bool) bool {
	return l.ws.ResolveQuestion(agentID, blockID, answers, questions, cancelled)
}

func (l *local) RejectQuestion(agentID, blockID, detail string) bool {
	return l.ws.RejectQuestion(agentID, blockID, detail)
}

func (l *local) ResolveConfirmation(agentID, blockID, choice string) bool {
	return l.ws.ResolveConfirmation(agentID, blockID, choice)
}

func (l *local) PrepareAgentRecovery(agentID, incidentID string, generation uint64) func() error {
	return l.ws.PrepareAgentRecovery(agentID, incidentID, generation)
}

func (l *local) PrepareCancelAgentRecovery(agentID, incidentID string, generation uint64) func() error {
	return l.ws.PrepareCancelAgentRecovery(agentID, incidentID, generation)
}

func (l *local) RejectAgentRecoveryCancellation(id, incident string) {
	l.ws.RejectAgentRecoveryCancellation(id, incident)
}

func (l *local) PrepareAsyncCommandTermination(agentID string, ref fileio.AsyncCmdRef) (func() error, error) {
	return l.ws.PrepareAsyncCommandTermination(agentID, ref)
}

func (l *local) PrepareCompactForAgent(agentID string) (session.PreparedCompaction, types.CompactResultMsg) {
	return l.ws.PrepareCompactForAgent(agentID)
}

func (l *local) HydrateSubAgents(parentID string) []string {
	return l.ws.HydrateSubAgents(parentID)
}

func (l *local) PrepareAgentInputAdmission(agentID string, input provider.UserInput, draftRevision uint64) (*workspace.InputAdmissionIntent, error) {
	return l.ws.PrepareAgentInputAdmission(agentID, input, draftRevision)
}

func (l *local) CommitAgentInputAdmission(ctx context.Context, intent *workspace.InputAdmissionIntent) workspace.InputAdmissionResult {
	return l.ws.CommitAgentInputAdmission(ctx, intent)
}

func (l *local) ApplyAgentInputAdmission(intent *workspace.InputAdmissionIntent, result workspace.InputAdmissionResult, input provider.UserInput, labels []string) bool {
	return l.ws.ApplyAgentInputAdmission(intent, result, input, labels)
}

func (l *local) PrepareSkillInput(ctx context.Context, agentID, id, arguments string) (provider.UserInput, error) {
	return l.ws.PrepareSkillInput(ctx, agentID, id, arguments)
}

func (l *local) CapabilityAction(ctx context.Context, action captypes.Action) (captypes.Snapshot, []captypes.Entry, string, error) {
	return l.ws.CapabilityAction(ctx, action)
}

func (l *local) CapabilityRuntimeSnapshot(ctx context.Context) (captypes.Snapshot, error) {
	return l.ws.CapabilityRuntimeSnapshot(ctx)
}

func (l *local) CapabilitySnapshot(ctx context.Context) (captypes.Snapshot, error) {
	return l.ws.CapabilitySnapshot(ctx)
}

func (l *local) AppendHistoryRestoreEvent(result localhistory.RestoreResult) error {
	return l.ws.AppendHistoryRestoreEvent(result)
}

func (l *local) ApplyHistoryRestoreChoices(ctx context.Context, planID string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error) {
	return l.ws.ApplyHistoryRestoreChoices(ctx, planID, choices)
}

func (l *local) BoardSnapshot(ctx context.Context, query, thread string, since, snapshot int64) (workspace.BoardSnapshot, error) {
	return l.ws.BoardSnapshot(ctx, query, thread, since, snapshot)
}

func (l *local) MarkBoardRead(ctx context.Context, through int64) error {
	return l.ws.MarkBoardRead(ctx, through)
}

func (l *local) SessionForAgent(agentID string) *workspace.Session {
	return l.ws.SessionForAgent(agentID)
}

func (l *local) ListSessions() []sessiondb.SessionMeta {
	return l.ws.ListSessions()
}

func (l *local) NewAgentSession() *workspace.NewSessionResult {
	return l.ws.NewAgentSession()
}

func (l *local) OpenAgentSession(sessionID string) *workspace.NewSessionResult {
	return l.ws.OpenAgentSession(sessionID)
}

func (l *local) ForkAgentSession(sessionID string) *workspace.NewSessionResult {
	return l.ws.ForkAgentSession(sessionID)
}

func (l *local) BackendSession(sessionID string) *session.Session {
	return l.ws.BackendSession(sessionID)
}

func (l *local) SessionConnectionIssue(sessionID string) error {
	return l.ws.SessionConnectionIssue(sessionID)
}

func (l *local) PrepareMultiAgentToggle(sessionID string) (*workspace.SessionSettingsOperation, error) {
	return l.ws.PrepareMultiAgentToggle(sessionID)
}

func (l *local) PrepareSessionCapabilityToggle(sessionID, id string, enabled bool) (*workspace.SessionSettingsOperation, error) {
	return l.ws.PrepareSessionCapabilityToggle(sessionID, id, enabled)
}

func (l *local) PrepareSessionSettingsSave(sessionID string, settings types.Settings) (*workspace.SessionSettingsOperation, error) {
	return l.ws.PrepareSessionSettingsSave(sessionID, settings)
}

func (l *local) PrepareSessionSettingsReset(sessionID string) (*workspace.SessionSettingsOperation, error) {
	return l.ws.PrepareSessionSettingsReset(sessionID)
}

func (l *local) ApplySessionSettingsResult(result workspace.SessionSettingsResult) bool {
	return l.ws.ApplySessionSettingsResult(result)
}

func (l *local) SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error) {
	return l.ws.SearchMemoryEpisodes(ctx, query, limit)
}

func (l *local) SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error) {
	return l.ws.SearchMemories(ctx, query)
}

func (l *local) InspectMemory(ctx context.Context, id string) (workspace.MemoryDetail, error) {
	return l.ws.InspectMemory(ctx, id)
}

func (l *local) PinMemory(ctx context.Context, id string, expectedRevision int64, pinned bool) error {
	return l.ws.PinMemory(ctx, id, expectedRevision, pinned)
}

func (l *local) ForgetMemoryRecord(ctx context.Context, id string, expectedRevision int64) error {
	return l.ws.ForgetMemoryRecord(ctx, id, expectedRevision)
}

func (l *local) CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error) {
	return l.ws.CorrectMemory(ctx, replacement)
}
