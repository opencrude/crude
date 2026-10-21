package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"crude/agent"
	"crude/capability"
	"crude/collaboration"
	"crude/session"
)

// SessionLifecycleOperation captures resources on the serialized workspace owner.
// Run only touches the captured backend/services, never workspace collections.
// Apply installs the completed backend and its cold transcript on that owner.
type SessionLifecycleOperation struct {
	root, directory, home, sessionID, instanceID string
	cfg                                          *session.Config
	logger                                       Logger
	database                                     *WorkspaceDB
	collaboration                                *collaboration.Store
	capabilities                                 *capability.Service
	executions                                   *ExecutionService
	lsp                                          *LSPManager
	claims                                       *agent.FileOwnership
	sender                                       session.EventSender
	history                                      *LocalHistoryService
	historyErr                                   error
	historyEnabled                               bool
	existing                                     *Session
	backend                                      *session.Session
	expectedAgentID                              string
	start                                        bool
	once                                         sync.Once
	result                                       SessionLifecycleResult
}

type SessionLifecycleResult struct {
	Err             error
	owner           *Session
	views           []ChatMessageView
	children        []preparedSubAgent
	cold            bool
	agentID         string
	registryErr     error
	gate            *lifecycleEventGate
	expectedAgentID string
}

// Cold runtimes may announce retained recovery immediately. Keep that evidence
// until inventory exists, then reduce it on the installing owner, in order.
type lifecycleEventGate struct {
	mu       sync.Mutex
	sender   session.EventSender
	events   []agent.AgentEvent
	open     bool
	overflow bool
}

func (g *lifecycleEventGate) SendAgentEvent(kind string, data any) {
	g.SendAgentEventWithID(kind, data, "")
}
func (g *lifecycleEventGate) SendAgentEventWithID(kind string, data any, id string) {
	g.SendAgentEventEnvelope(agent.AgentEvent{Type: kind, Data: data, AgentID: id})
}
func (g *lifecycleEventGate) SendAgentEventEnvelope(event agent.AgentEvent) {
	g.mu.Lock()
	if !g.open {
		if len(g.events) == 1024 {
			g.overflow = true
			g.mu.Unlock()
			return
		}
		g.events = append(g.events, event)
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()
	g.sender.SendAgentEventEnvelope(event)
}
func (g *lifecycleEventGate) activate(w *Workspace) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, event := range g.events {
		w.ApplyAgentEvent(AgentEvent{Type: event.Type, Data: event.Data, AgentID: event.AgentID, Identity: event.Identity})
	}
	g.events = nil
	g.open = true
}

func (w *Workspace) PrepareAgentSession(sessionID string) (*SessionLifecycleOperation, error) {
	if w == nil || w.sessionCfg == nil {
		return nil, errors.New("Session configuration is unavailable")
	}
	if w.sender == nil {
		return nil, errors.New("Session event transport is unavailable")
	}
	return w.prepareSessionLifecycle(sessionID, w.sessionCfg, w.homeDir, true)
}

func (w *Workspace) prepareSessionLifecycle(id string, cfg *session.Config, home string, start bool) (*SessionLifecycleOperation, error) {
	if cfg == nil {
		return nil, errors.New("session configuration is unavailable")
	}
	// Exceptional startup repair remains serialized: it replaces workspace-wide
	// services. Normal configured workspace lifetimes already have these resources.
	if err := w.ensureWorkspacePersistence(); err != nil {
		return nil, fmt.Errorf("workspace persistence unavailable: %w", err)
	}
	if err := w.ensureCapabilities(context.Background()); err != nil {
		return nil, err
	}
	if err := w.ensureExecutions(); err != nil {
		return nil, err
	}
	configuration := *cfg
	configuration.AccountManager = nil
	raw, err := json.Marshal(configuration)
	if err != nil {
		return nil, err
	}
	var captured session.Config
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	captured.AccountManager = cfg.AccountManager
	op := &SessionLifecycleOperation{root: w.RootPath, directory: w.WorkspaceDir, home: home,
		sessionID: id, instanceID: w.instanceID, cfg: &captured, logger: w.Logger,
		database: w.WorkspaceDB, collaboration: w.Collaboration, capabilities: w.Capabilities, executions: w.Executions, lsp: w.LSPMgr,
		claims: w.FileOwnership, sender: w.sender, history: w.HistoryService,
		historyErr: w.localHistoryErr, historyEnabled: w.EffectiveAppSettings().LocalHistory.EnabledValue(),
		existing: w.SessionsByID[id], start: start}
	if op.existing != nil {
		op.backend = op.existing.backend
		op.expectedAgentID = op.existing.AgentID
		if op.backend == nil {
			return nil, errors.New("session backend is unavailable")
		}
	}
	return op, nil
}

func (op *SessionLifecycleOperation) Run(ctx context.Context) SessionLifecycleResult {
	op.once.Do(func() { op.result = op.run(ctx) })
	return op.result
}

func (op *SessionLifecycleOperation) run(ctx context.Context) (result SessionLifecycleResult) {
	if err := ctx.Err(); err != nil {
		result.Err = err
		return
	}
	owner := op.existing
	if owner == nil {
		var backend *session.Session
		var err error
		root := filepath.Join(op.directory, "sessions")
		if op.sessionID == "" {
			backend, err = session.NewWithSessionDir("", op.cfg, op.home, root)
		} else {
			path, pathErr := SessionDBPath(op.root, op.sessionID)
			if pathErr != nil {
				result.Err = pathErr
				return
			}
			if _, err = os.Stat(path); err == nil {
				backend, err = session.ResumeSessionWithDir(path, op.cfg, op.home, root)
			} else {
				err = fmt.Errorf("session database unavailable: %w", err)
			}
		}
		if err != nil {
			result.Err = err
			return
		}
		backend.SetWorkDir(op.root)
		backend.SetMemoryStore(op.database.Memory)
		backend.SetCollaboration(op.collaboration, op.instanceID)
		backend.SetCapabilities(op.capabilities, op.claims)
		backend.SetExecutionService(op.executions)
		if op.lsp != nil {
			backend.SetCodeIntelligence(op.lsp)
		}
		if op.history != nil {
			backend.SetHistoryHooks(op.history.BeginMessage, op.history.EndMessage)
			backend.SetHistoryContinuationHooks(op.history.BeginContinuation, op.history.EndContinuation)
			backend.SetHistoryMutationHook(op.history.RecordAgentMutation)
		} else {
			backend.SetHistoryHooks(func(context.Context, string, string) error {
				if !op.historyEnabled {
					return session.ErrHistoryDisabled
				}
				if op.historyErr != nil {
					return fmt.Errorf("local history is required but unavailable: %w", op.historyErr)
				}
				return errors.New("local history is required but unavailable")
			}, nil)
		}
		owner = NewSession(backend.SessionID(), backend.SessionDir(), backend.DB(), op.logger)
		owner.attachBackend(backend)
		owner.installSettings(backend.SettingsSnapshots())
		result.cold = true
		result.registryErr = op.database.RegisterSession(owner.SessionID, op.root, filepath.Base(op.root), backend.ModelName(), backend.SettingsSnapshot().ProviderID)
		if op.sessionID == "" {
			_ = op.database.SetAgentName(owner.SessionID, backend.AgentName())
		}
	}
	result.owner = owner
	result.expectedAgentID = op.expectedAgentID
	if op.start {
		backend := op.backend
		sender := op.sender
		if result.cold {
			backend = owner.backend
			result.gate = &lifecycleEventGate{sender: op.sender}
			sender = result.gate
		}
		orch, err := backend.NewOrchestrator(sender)
		if err != nil {
			result.Err = err
			if result.cold {
				owner.backend.Close()
			}
			return result
		}
		if result.gate != nil {
			result.gate.mu.Lock()
			overflow := result.gate.overflow
			result.gate.mu.Unlock()
			if overflow {
				backend.Close()
				result.Err = errors.New("session startup event buffer exhausted")
				return result
			}
		}
		// Existing frontend values must remain owner-thread-only.
		result.agentID = orch.ID
		if result.cold && op.sessionID != "" {
			// The temporary reader owns no shared collections or lifecycle state.
			reader := &Workspace{Logger: op.logger}
			result.views = reader.prepareSessionHydration(owner)
			children := owner.backend.CompletedSubAgentSnapshots()
			children = append(children, owner.backend.PendingSubAgentSnapshots()...)
			result.children = prepareSubAgentViews(children)
		}
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		if result.cold {
			owner.backend.Close()
		}
	}
	return result
}

func (w *Workspace) installSessionLifecycle(result SessionLifecycleResult) {
	if result.Err != nil && result.cold && result.owner != nil && result.owner.backend.Pruned() && w.WorkspaceDB != nil {
		// A failed new session closed as empty; its registry row must not outlive it.
		if err := w.WorkspaceDB.ForgetSession(result.owner.SessionID); err != nil {
			w.noteRegistryErr("session_forget_failed", err)
		}
	}
	if result.Err != nil || result.owner == nil {
		return
	}
	owner := result.owner
	if result.cold && w.SessionsByID[owner.SessionID] == nil {
		w.addSession(owner)
		// History notification retry and live setting policy keep their existing
		// authoritative owner; Run does not invoke callbacks over workspace maps.
		w.bindSessionHistory(owner.backend)
		owner.backend.StartMemoryMaintenance()
		w.deliverPendingHistoryNotifications(owner.SessionID)
		w.noteRegistryErr("session_register_failed", result.registryErr)
	}
}

func (w *Workspace) ApplyAgentSession(result SessionLifecycleResult) *NewSessionResult {
	if result.Err != nil {
		return &NewSessionResult{Error: result.Err.Error(), NeedsAccount: errors.Is(result.Err, session.ErrAccountRequired), NeedsModel: errors.Is(result.Err, session.ErrModelRequired)}
	}
	if result.owner == nil || result.cold && w.SessionsByID[result.owner.SessionID] != nil && w.SessionsByID[result.owner.SessionID] != result.owner {
		return &NewSessionResult{Error: "Session owner changed"}
	}
	if !result.cold && (w.SessionsByID[result.owner.SessionID] != result.owner || result.owner.AgentID != result.expectedAgentID) {
		return &NewSessionResult{Error: "Session runtime changed during open"}
	}
	w.installSessionLifecycle(result)
	owner := result.owner
	if owner == nil || w.SessionsByID[owner.SessionID] != owner {
		return &NewSessionResult{Error: "Session owner changed"}
	}
	owner.AgentID = result.agentID
	if owner.AgentID == "" {
		// A warm frontend without a runtime is rare, but the backend factory is
		// idempotent and the completed operation has already registered it.
		return &NewSessionResult{Error: "Session runtime is unavailable"}
	}
	w.SessionsByAgent[owner.AgentID] = owner
	w.RegisterAgentRoot(owner.AgentID)
	if result.cold && !owner.hydrated {
		w.installSessionHydration(owner, result.views)
	}
	if result.cold {
		w.installPreparedSubAgents(owner.AgentID, owner, result.children)
	}
	if result.gate != nil {
		result.gate.activate(w)
	}
	return &NewSessionResult{SessionID: owner.SessionID, OrchID: owner.AgentID}
}
