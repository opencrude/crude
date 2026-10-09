package remote

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/fileio"
	"crude/localhistory"
	"crude/memory"
	"crude/shellport"
	captypes "crude/types/capability"
	"crude/workspace"
)

// ---- Source control -------------------------------------------------------

// gitService adapts the typed Git requests. Every method is already invoked
// from a command goroutine by the shell.
type gitService struct{ w *Workspace }

var _ shellport.GitService = gitService{}

func (g gitService) client() *coreclient.GitService {
	client, id := g.w.conn()
	return coreclient.NewGitService(client, id)
}

func (g gitService) AnnotateFile(ctx context.Context, absPath, content string) (workspace.GitFileAnnotation, error) {
	return g.client().AnnotateFile(ctx, absPath, content)
}
func (g gitService) Checkout(ctx context.Context, branch string) error {
	return g.client().Checkout(ctx, branch)
}
func (g gitService) CommitStaged(ctx context.Context, expected []string, message string) error {
	return g.client().CommitStaged(ctx, expected, message)
}
func (g gitService) CompareChanges(ctx context.Context, changes []workspace.GitChange) ([]workspace.GitChangeComparison, error) {
	return g.client().CompareChanges(ctx, changes)
}
func (g gitService) InspectCommit(ctx context.Context, hash string) (workspace.GitCommitEvidence, error) {
	return g.client().InspectCommit(ctx, hash)
}
func (g gitService) Snapshot(ctx context.Context) (workspace.GitSnapshot, error) {
	return g.client().Snapshot(ctx)
}
func (g gitService) Stage(ctx context.Context, paths ...string) error {
	return g.w.call(ctx, coreprotocol.GitStagePaths, coreprotocol.GitPathsRequest{Paths: paths}, &coreprotocol.BoolResult{})
}
func (g gitService) Unstage(ctx context.Context, paths ...string) error {
	return g.w.call(ctx, coreprotocol.GitUnstagePaths, coreprotocol.GitPathsRequest{Paths: paths}, &coreprotocol.BoolResult{})
}
func (g gitService) StageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error {
	return g.client().StageHunk(ctx, comparison, hunk)
}
func (g gitService) StageReviewed(ctx context.Context, comparison workspace.GitComparison) error {
	return g.client().StageReviewed(ctx, comparison)
}
func (g gitService) UnstageHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) error {
	return g.client().UnstageHunk(ctx, comparison, hunk)
}
func (g gitService) UnstageReviewed(ctx context.Context, comparison workspace.GitComparison) error {
	return g.client().UnstageReviewed(ctx, comparison)
}

func (w *Workspace) Git() shellport.GitService       { return gitService{w: w} }
func (w *Workspace) EnsureGit() shellport.GitService { return gitService{w: w} }

func (w *Workspace) PreviewGitDiscard(ctx context.Context, change workspace.GitChange) (workspace.GitDiscardPreview, error) {
	return w.rpc().PreviewGitDiscard(ctx, change)
}
func (w *Workspace) DiscardGitReviewed(ctx context.Context, preview workspace.GitDiscardPreview) (string, error) {
	return w.rpc().DiscardGitReviewed(ctx, preview)
}
func (w *Workspace) DiscardGitHunk(ctx context.Context, comparison workspace.GitComparison, hunk workspace.GitHunkRange) (string, error) {
	return w.rpc().DiscardGitHunk(ctx, comparison, hunk)
}

// commitWriter generates through the core's Aeron role, streaming the draft
// as request-scoped progress to this shell only.
type commitWriter struct {
	w *Workspace
}

func (w *Workspace) PrepareCommitMessageWriter() (shellport.CommitMessageWriter, error) {
	return commitWriter{w: w}, nil
}

func (c commitWriter) Write(ctx context.Context, stageAll bool, emit func(string)) (string, error) {
	var sent strings.Builder
	var mu sync.Mutex
	progress := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if emit != nil && strings.HasPrefix(text, sent.String()) && len(text) > sent.Len() {
			emit(text[sent.Len():])
			sent.Reset()
			sent.WriteString(text)
		}
	}
	var out coreprotocol.CommitMessageResult
	err := c.w.callWithProgress(ctx, coreprotocol.GitCommitMessage, coreprotocol.CommitMessageRequest{StageAll: stageAll}, &out, progress)
	return out.Message, err
}

// ---- Local History --------------------------------------------------------

type historyStore struct{ w *Workspace }

var _ shellport.LocalHistoryStore = historyStore{}
var _ shellport.LocalHistoryService = historyStore{}

func (h historyStore) svc() *coreclient.HistoryService { return h.w.rpc().History() }

func (h historyStore) FileHistory(path string, limit int) ([]localhistory.SnapshotRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return h.svc().FileHistory(ctx, path, limit)
}
func (h historyStore) PlanRestore(ctx context.Context, id string, opts localhistory.SnapshotOptions) (localhistory.RestorePlan, error) {
	return h.svc().PlanRestore(ctx, id, opts)
}
func (h historyStore) PlanRestorePaths(ctx context.Context, id string, opts localhistory.SnapshotOptions, paths []string) (localhistory.RestorePlan, error) {
	return h.svc().PlanRestorePaths(ctx, id, opts, paths)
}
func (h historyStore) Prune(ctx context.Context, policy localhistory.RetentionPolicy) (localhistory.PruneResult, error) {
	return h.svc().Prune(ctx, policy)
}

// Root is the core's history location; a connected shell never opens it.
func (h historyStore) Root() string { return "" }

func (h historyStore) SnapshotIfChanged(ctx context.Context, opts localhistory.SnapshotOptions) (localhistory.Manifest, bool, error) {
	manifest, err := h.svc().Capture(ctx, opts)
	return manifest, err == nil && manifest.ID != "", err
}
func (h historyStore) Stats() (localhistory.Stats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return h.svc().Stats(ctx)
}
func (h historyStore) Timeline(limit int) ([]localhistory.SnapshotRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return h.svc().Timeline(ctx, limit)
}
func (h historyStore) VerifyAll(ctx context.Context) error { return h.svc().VerifyAll(ctx) }

func (h historyStore) AgentUndoableScopes(sessionID, agentID string) ([]localhistory.MessageScope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var out coreprotocol.UndoScopesResult
	err := h.w.call(ctx, coreprotocol.HistoryUndoScopes, coreprotocol.UndoScopesRequest{SessionID: sessionID, AgentID: agentID}, &out)
	return out.Scopes, err
}
func (h historyStore) PlanAgentRestore(ctx context.Context, sessionID, messageID, agentID string) (localhistory.RestorePlan, error) {
	return h.svc().PlanAgentRestore(ctx, sessionID, messageID, agentID)
}
func (h historyStore) PlanMessageRestore(ctx context.Context, sessionID, messageID string) (localhistory.RestorePlan, error) {
	return h.svc().PlanMessageRestore(ctx, sessionID, messageID)
}

func (w *Workspace) LocalHistory() shellport.LocalHistoryStore     { return historyStore{w: w} }
func (w *Workspace) HistoryService() shellport.LocalHistoryService { return historyStore{w: w} }

func (w *Workspace) AppendHistoryRestoreEvent(result localhistory.RestoreResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return w.call(ctx, coreprotocol.HistoryRestoreEvent, coreprotocol.HistoryRestoreEventRequest{Result: result}, &coreprotocol.BoolResult{})
}

func (w *Workspace) ApplyHistoryRestoreChoices(ctx context.Context, planID string, choices []localhistory.RestoreChoice) (localhistory.RestoreResult, error) {
	return w.rpc().History().ApplyRestoreChoices(ctx, planID, choices)
}

// ---- Memory and board -----------------------------------------------------

type memoryStore struct{ w *Workspace }

var _ shellport.MemoryStore = memoryStore{}

func (m memoryStore) SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error) {
	return m.w.rpc().SearchMemoryEpisodes(ctx, query, limit)
}
func (m memoryStore) SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error) {
	return m.w.rpc().SearchMemories(ctx, query)
}
func (m memoryStore) InspectMemory(ctx context.Context, id string) (workspace.MemoryDetail, error) {
	return m.w.rpc().InspectMemory(ctx, id)
}
func (m memoryStore) PinMemory(ctx context.Context, id string, expectedRevision int64, pinned bool) error {
	return m.w.rpc().PinMemory(ctx, id, expectedRevision, pinned)
}
func (m memoryStore) ForgetMemoryRecord(ctx context.Context, id string, expectedRevision int64) error {
	return m.w.rpc().ForgetMemoryRecord(ctx, id, expectedRevision)
}
func (m memoryStore) CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error) {
	return m.w.rpc().CorrectMemory(ctx, replacement)
}

func (w *Workspace) SearchMemoryEpisodes(ctx context.Context, query string, limit int) ([]memory.Episode, error) {
	return memoryStore{w: w}.SearchMemoryEpisodes(ctx, query, limit)
}
func (w *Workspace) SearchMemories(ctx context.Context, query memory.ListQuery) ([]memory.ScoredRecord, error) {
	return memoryStore{w: w}.SearchMemories(ctx, query)
}
func (w *Workspace) InspectMemory(ctx context.Context, id string) (workspace.MemoryDetail, error) {
	return memoryStore{w: w}.InspectMemory(ctx, id)
}
func (w *Workspace) PinMemory(ctx context.Context, id string, expectedRevision int64, pinned bool) error {
	return memoryStore{w: w}.PinMemory(ctx, id, expectedRevision, pinned)
}
func (w *Workspace) ForgetMemoryRecord(ctx context.Context, id string, expectedRevision int64) error {
	return memoryStore{w: w}.ForgetMemoryRecord(ctx, id, expectedRevision)
}
func (w *Workspace) CorrectMemory(ctx context.Context, replacement memory.Record) (memory.Record, error) {
	return memoryStore{w: w}.CorrectMemory(ctx, replacement)
}

// BindMemory pins memory requests to this attachment.
func (w *Workspace) BindMemory() shellport.MemoryStore { return memoryStore{w: w} }

func (w *Workspace) BoardSnapshot(ctx context.Context, query, thread string, since, snapshot int64) (workspace.BoardSnapshot, error) {
	return w.rpc().BoardSnapshot(ctx, query, thread, since, snapshot)
}
func (w *Workspace) MarkBoardRead(ctx context.Context, through int64) error {
	return w.rpc().MarkBoardRead(ctx, through)
}

// ---- Capabilities ---------------------------------------------------------

func (w *Workspace) CapabilityRuntimeSnapshot(ctx context.Context) (captypes.Snapshot, error) {
	result, err := w.rpc().CapabilityRuntimeSnapshot(ctx)
	return result.Snapshot, err
}

func (w *Workspace) CapabilitySnapshot(ctx context.Context) (captypes.Snapshot, error) {
	result, err := w.rpc().CapabilitySnapshot(ctx)
	return result.Snapshot, err
}

// CapabilityAction converts edited connection/source values into write-only
// updates against the replica the edit started from.
func (w *Workspace) CapabilityAction(ctx context.Context, action captypes.Action) (captypes.Snapshot, []captypes.Entry, string, error) {
	base := w.capabilityBase()
	request := coreprotocol.CapabilityActionRequest{Action: action, ExpectedRevision: base.revision}
	if action.Connection != nil && (action.Operation == "save-connection" || action.Operation == "test") {
		conn := *action.Connection
		request.ConnectionSecrets = connectionSecrets(conn, base.connections[conn.ID], base.presence[conn.ID])
		conn.Args, conn.Environment, conn.Headers, conn.SecretRefs, conn.CredentialRef = nil, nil, nil, nil, ""
		conn.URL = ""
		request.Action.Connection = &conn
	}
	if action.Source != nil && action.Operation == "save-source" {
		source := *action.Source
		if before, ok := base.sources[source.ID]; ok && before.Location == source.Location {
			request.SourceLocation.Keep = true
		} else {
			location := source.Location
			request.SourceLocation.Value = &location
		}
		source.Location = ""
		request.Action.Source = &source
	}
	result, err := w.rpc().CapabilityAction(ctx, request)
	if err != nil {
		return captypes.Snapshot{}, nil, "", err
	}
	w.refreshSettings()
	return result.Snapshot, result.Entries, result.Content, nil
}

// capabilityState is captured when an action is issued; Capability actions
// run on command goroutines, so the replica is read under the settings lock.
type capabilityState struct {
	revision    string
	connections map[string]captypes.ConnectionConfig
	sources     map[string]captypes.SourceConfig
	presence    map[string]coreprotocol.ConnectionSecretPresence
}

func (w *Workspace) capabilityBase() capabilityState {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	fetch := w.settings.current.fetch
	caps := fetch.Effective.Capabilities
	return capabilityState{revision: fetch.CapabilitiesRevision, connections: maps.Clone(caps.Connections),
		sources: maps.Clone(caps.Sources), presence: maps.Clone(fetch.ConnectionSecretFields)}
}

func connectionSecrets(conn, before captypes.ConnectionConfig, present coreprotocol.ConnectionSecretPresence) coreprotocol.ConnectionSecretUpdates {
	var out coreprotocol.ConnectionSecretUpdates
	if before.ID != "" && conn.URL == before.URL {
		out.Endpoint.Keep = true
	} else {
		url := conn.URL
		out.Endpoint.Value = &url
	}
	if present.Args && slices.Equal(conn.Args, storedArgs) {
		out.Args.Keep = true
	} else if len(conn.Args) != 0 {
		args := slices.Clone(conn.Args)
		out.Args.Value = &args
	}
	out.Environment = mapUpdate(conn.Environment, present.Environment)
	out.Headers = mapUpdate(conn.Headers, present.Headers)
	out.SecretRefs = mapUpdate(conn.SecretRefs, present.SecretRefs)
	if present.CredentialRef && conn.CredentialRef == StoredSecret {
		out.CredentialRef.Keep = true
	} else if conn.CredentialRef != "" {
		ref := conn.CredentialRef
		out.CredentialRef.Value = &ref
	}
	return out
}

var storedArgs = []string{StoredSecret}

func storedMap() map[string]string { return map[string]string{storedKey: StoredSecret} }

func mapUpdate(value map[string]string, present bool) coreprotocol.SecretUpdate[map[string]string] {
	if present && len(value) == 1 && value[storedKey] == StoredSecret {
		return coreprotocol.SecretUpdate[map[string]string]{Keep: true}
	}
	if len(value) == 0 {
		return coreprotocol.SecretUpdate[map[string]string]{}
	}
	copy := maps.Clone(value)
	return coreprotocol.SecretUpdate[map[string]string]{Value: &copy}
}

// markStoredCapabilities shows each redacted connection field as stored.
func markStoredCapabilities(caps captypes.Settings, presence map[string]coreprotocol.ConnectionSecretPresence) captypes.Settings {
	caps = captypes.Clone(caps)
	for id, conn := range caps.Connections {
		p := presence[id]
		if p.Args {
			conn.Args = slices.Clone(storedArgs)
		}
		if p.Environment {
			conn.Environment = storedMap()
		}
		if p.Headers {
			conn.Headers = storedMap()
		}
		if p.SecretRefs {
			conn.SecretRefs = storedMap()
		}
		if p.CredentialRef {
			conn.CredentialRef = StoredSecret
		}
		caps.Connections[id] = conn
	}
	return caps
}

// ---- Files and project search --------------------------------------------

func (w *Workspace) fileContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callTimeout)
}

func (w *Workspace) CreateFile(path string) workspace.FileOperationResultMsg {
	ctx, cancel := w.fileContext()
	defer cancel()
	return w.rpc().CreateFile(ctx, path)
}
func (w *Workspace) CreateFolder(path string) workspace.FileOperationResultMsg {
	ctx, cancel := w.fileContext()
	defer cancel()
	return w.rpc().CreateFolder(ctx, path)
}
func (w *Workspace) RenamePath(oldPath, newPath string) workspace.FileOperationResultMsg {
	ctx, cancel := w.fileContext()
	defer cancel()
	return w.rpc().RenamePath(ctx, oldPath, newPath)
}
func (w *Workspace) DeletePath(path string) workspace.FileOperationResultMsg {
	ctx, cancel := w.fileContext()
	defer cancel()
	return w.rpc().DeletePath(ctx, path)
}
func (w *Workspace) ApplyWorkspaceEdit(edits []workspace.FileEdit) workspace.FileOperationResultMsg {
	ctx, cancel := w.fileContext()
	defer cancel()
	return w.rpc().ApplyWorkspaceEdit(ctx, edits)
}
func (w *Workspace) ApplyLSPRename(ctx context.Context, edits []workspace.FileEdit) (workspace.FileOperationResultMsg, string) {
	result := w.rpc().ApplyWorkspaceEdit(ctx, edits)
	return result, ""
}

// StartProjectSearch runs a cancellable core search and publishes the shell's
// usual completion message; a newer search supersedes an older one.
func (w *Workspace) StartProjectSearch(query string, opts fileio.ProjectSearchOptions) uint64 {
	w.searchMu.Lock()
	if w.searchCancel != nil {
		w.searchCancel()
	}
	w.searchSeq++
	id := w.searchSeq
	ctx, cancel := context.WithCancel(context.Background())
	w.searchCancel = cancel
	w.searchMu.Unlock()
	rpc := w.rpc()
	go func() {
		started := time.Now()
		matches, err := rpc.SearchProject(ctx, query, opts)
		w.searchMu.Lock()
		current := w.searchSeq == id
		w.searchMu.Unlock()
		if !current || ctx.Err() != nil {
			return
		}
		w.send(workspace.ProjectSearchCompletedMsg{Result: workspace.ProjectSearchResult{RequestID: id, Matches: matches, Err: err, Duration: time.Since(started)}})
	}()
	return id
}

func (w *Workspace) CancelProjectSearch() {
	w.searchMu.Lock()
	defer w.searchMu.Unlock()
	if w.searchCancel != nil {
		w.searchCancel()
		w.searchCancel = nil
	}
	w.searchSeq++
}
