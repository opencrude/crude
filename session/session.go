package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/clipboard"
	"crude/collaboration"
	"crude/diff"
	"crude/fileio"
	"crude/jsonrepair"
	"crude/logger"
	"crude/memory"
	"crude/plan"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/search"
	"crude/sessiondb"
	ttypes "crude/types"
	captypes "crude/types/capability"
	"crude/types/control"
	recoverytypes "crude/types/recovery"
)

var ErrHistoryDisabled = errors.New("local history disabled")

// ErrHistoryRecorded means Continue reuses an existing immutable message scope
// instead of capturing a second before-image under the same human input ID.
var ErrHistoryRecorded = errors.New("local history message already recorded")

type EventSender interface {
	SendAgentEvent(typ string, data any)
	SendAgentEventWithID(typ string, data any, agentID string)
	// SendAgentEventEnvelope is the exact-run seam. Runtime sources preserve a
	// supplied identity rather than regenerating it on duplicate delivery.
	SendAgentEventEnvelope(event agent.AgentEvent)
}

type Session struct {
	plans      atomic.Pointer[plan.Store]
	childPlans sync.Map // exact agent ID -> *plan.Store; retained after child DB closure
	agentName  string
	nameHome   string
	ID         string
	cfg        *Config
	mu         sync.Mutex

	provider     *provider.Provider
	exec         fileio.Executer
	editor       *fileio.Editor
	logger       *logger.Logger
	permStore    *safety.PermissionStore
	asyncMgr     *fileio.AsyncCommandManager
	memoryStore  *memory.Store
	capabilities captypes.Runtime
	execution    ExecutionService
	// executionSelection is the next-input checkout, not a mutable working
	// directory. Already admitted input and live runs retain their own binding.
	executionSelection    string
	executionRevision     uint64
	controlRuntime        control.Runtime
	fileOwnership         *agent.FileOwnership
	collaborationStore    *collaboration.Store
	collaborationInstance string
	collaborationAgents   map[string]collaboration.Agent
	collaborationCancel   context.CancelFunc
	collaborationWg       sync.WaitGroup
	resumed               bool
	memorySummary         atomic.Pointer[memory.SessionSummary]

	// workDir is the workspace root this session executes tools against.
	// Relative tool paths and commands without an explicit cwd resolve
	// against it. Empty means the process working directory (tests).
	workDir string
	// tempDir is the stable session-owned cross-platform directory advertised
	// to models for transient files and inherited by command processes.
	tempDir string

	// touchedFiles accumulates files touched by tools this session; they
	// boost recall of memories scoped to the same paths.
	touchedFiles map[string]struct{}
	touchedMu    sync.Mutex

	// prevContextInjected flips on the first turn after start/resume so
	// the previous-session continuity block is injected exactly once.
	prevContextInjected atomic.Bool

	// memoryJobs serializes background memory work (extraction, summary,
	// consolidation) for this session.
	memoryJobs memoryJobState

	msgs              []provider.ChatMessage
	msgTurns          []int
	displayMetas      []sessiondb.DisplayMeta
	createdAts        []int64
	msgsMu            sync.Mutex
	systemPromptMu    sync.Mutex
	systemPrompts     map[string]string
	activeContext     []provider.ChatMessage
	contextRevision   uint64
	contextGeneration uint64
	contextReason     string

	recoveredSubAgentMessages map[string][]sessiondb.MessageWithMeta
	recoveredTurnGenerations  map[string]uint64

	sessionID    string
	sessionStart time.Time
	yolo         atomic.Bool

	usageMu       sync.RWMutex
	usageRecords  map[string]sessiondb.UsageRecord
	tokens        *TokenTracker
	guidanceMu    sync.Mutex
	guidanceCache map[string]string

	toolCallCount   int
	toolCallCountMu sync.Mutex

	db         *sql.DB
	dbPath     string
	turnNumber int
	closing    int32
	// pruned records that Close found nothing durable to keep and removed the
	// session's storage; the workspace registry must then forget the session.
	pruned        atomic.Bool
	hardCancelled int32
	activeTurns   atomic.Int32
	turnGate      sync.RWMutex
	compactionMu  sync.Mutex
	// precompaction holds each root agent's background checkpoint slot.
	capabilitySharing        capabilitySharing
	precompactionMu          sync.Mutex
	precompaction            map[string]*agent.PrecompactionSlot
	precompactionCtx         context.Context
	precompactionCancel      context.CancelFunc
	compactionCancel         context.CancelFunc
	compactionID             string
	compactionAgent          string
	compactionStarted        bool
	compactionWg             sync.WaitGroup
	sender                   EventSender
	subAgentSup              *SubAgentRunner
	subAgentMu               sync.Mutex
	settingsRevision         uint64     // protected by mu; successful settings publication generation
	settingsOperationMu      sync.Mutex // settings lifetime fence; precedes delegationMu and mu
	delegationMu             sync.Mutex
	delegationNoticeMu       sync.Mutex
	permissionReviewRevision atomic.Uint64
	delegationNotices        []provider.ChatMessage
	orchestrators            map[string]*Orchestrator
	orchMu                   sync.Mutex
	orchWg                   sync.WaitGroup
	orchNextID               atomic.Int64
	recoveryMu               sync.Mutex
	recoverySlots            map[string]*recoverySlot
	pendingRecoveries        map[string]sessiondb.RecoveryCheckpoint
	admissionMu              sync.Mutex
	pendingAdmissions        map[*InputAdmissionIntent]struct{}
	// queued is the durable input queue held while the root compacts.
	queueMu      sync.Mutex
	queueAdmitMu sync.Mutex
	queued       []QueuedInput
	lifecycleMu  sync.Mutex

	// runtimeProfile holds ephemeral per-orchestrator config overrides
	// that never persist to disk.
	runtimeProfile *RuntimeProfile

	// workspaceCfg is the inherited workspace baseline. s.cfg is the effective
	// per-session config after applying overrides loaded from this session's DB.
	workspaceCfg      *Config
	sessionOverrides  map[string]sessiondb.AgentSettingOverride
	selectedMainAgent string
	// multiAgentOverride is the explicit session flag. nil inherits workspace.
	multiAgentOverride *bool
	// codeIntelOverride is the explicit session code-intelligence flag; nil
	// follows the workspace's LSP switch.
	codeIntelOverride *bool
	codeIntel         CodeIntelligence
	// maxSubAgentsOverride caps this session's running children; nil
	// follows the workspace setting.
	maxSubAgentsOverride *int
	turnLogsOverride     *bool
	// disabledCapabilities is the session-wide capability gate. Every agent
	// of this session (root and children) reads it live at each request and
	// tool call; settings operations replace it atomically.
	disabledCapabilities atomic.Pointer[[]string]
	historyMu            sync.RWMutex
	historyStart         func(context.Context, string, string) error
	historyEnd           func(context.Context, string, string) error
	historyContinue      func(context.Context, string, string) error
	historyContinueEnd   func(context.Context, string, string) error
	beforeTurn           func(context.Context, string) error
	historyMutation      func(context.Context, HistoryMutation) error
	historyWg            sync.WaitGroup
	historyFinalizers    atomic.Int32
}

// HistoryMutation is a successful, direct filesystem mutation reported by an
// agent. Local History verifies these targets against immutable snapshots before
// allowing restore; the report alone is never restore authority.
type HistoryMutation struct {
	SessionID string
	MessageID string
	AgentID   string
	Receipt   fileio.MutationReceipt
	Committed *bool
}

type historyScope struct {
	sessionID string
	messageID string
	agentID   string
}

type historyScopeKey struct{}

func withHistoryScope(ctx context.Context, sessionID, messageID, agentID string) context.Context {
	return context.WithValue(ctx, historyScopeKey{}, historyScope{sessionID: sessionID, messageID: messageID, agentID: agentID})
}

func withHistoryActor(ctx context.Context, agentID string) context.Context {
	scope, ok := historyScopeFromContext(ctx)
	if !ok {
		return ctx
	}
	scope.agentID = agentID
	return context.WithValue(ctx, historyScopeKey{}, scope)
}

func historyScopeFromContext(ctx context.Context) (historyScope, bool) {
	if ctx == nil {
		return historyScope{}, false
	}
	scope, ok := ctx.Value(historyScopeKey{}).(historyScope)
	return scope, ok && scope.sessionID != "" && scope.messageID != "" && scope.agentID != ""
}

func (s *Session) SetHistoryHooks(start, end func(context.Context, string, string) error) {
	s.historyMu.Lock()
	s.historyStart, s.historyEnd = start, end
	s.historyMu.Unlock()
}

func (s *Session) SetHistoryContinuationHooks(start, end func(context.Context, string, string) error) {
	s.historyMu.Lock()
	s.historyContinue, s.historyContinueEnd = start, end
	s.historyMu.Unlock()
}

func (s *Session) SetBeforeTurnHook(hook func(context.Context, string) error) {
	s.historyMu.Lock()
	s.beforeTurn = hook
	s.historyMu.Unlock()
}

func (s *Session) runBeforeTurn(ctx context.Context) error {
	s.historyMu.RLock()
	hook := s.beforeTurn
	s.historyMu.RUnlock()
	if hook == nil {
		return nil
	}
	return hook(ctx, s.sessionID)
}

func (s *Session) SetHistoryMutationHook(record func(context.Context, HistoryMutation) error) {
	s.historyMu.Lock()
	s.historyMutation = record
	s.historyMu.Unlock()
}

func (s *Session) HasActiveTurns() bool {
	if s == nil {
		return false
	}
	return s.activeTurns.Load() > 0 || s.historyFinalizers.Load() > 0 ||
		(s.SubAgentRunner() != nil && s.SubAgentRunner().Running()) || (s.asyncMgr != nil && s.asyncMgr.Running())
}

func newMessageID() string { return provider.NewMessageID() }

func (s *Session) historyHooks() (func(context.Context, string, string) error, func(context.Context, string, string) error) {
	s.historyMu.RLock()
	defer s.historyMu.RUnlock()
	return s.historyStart, s.historyEnd
}

func (s *Session) historyContinuationHooks() (func(context.Context, string, string) error, func(context.Context, string, string) error) {
	s.historyMu.RLock()
	defer s.historyMu.RUnlock()
	return s.historyContinue, s.historyContinueEnd
}

func (s *Session) prepareHistoryMutation(ctx context.Context, receipt fileio.MutationReceipt) (fileio.MutationComplete, error) {
	if !receipt.HasChanges() {
		return nil, nil
	}
	scope, ok := historyScopeFromContext(ctx)
	if !ok || len(receipt.Paths) == 0 {
		return nil, nil
	}
	s.historyMu.RLock()
	record := s.historyMutation
	s.historyMu.RUnlock()
	if record == nil {
		return nil, nil
	}
	if err := record(ctx, HistoryMutation{SessionID: scope.sessionID, MessageID: scope.messageID, AgentID: scope.agentID, Receipt: receipt}); err != nil {
		if s.logger != nil {
			s.logger.Warn("HISTORY", "agent_mutation_prepare_failed", "agent_id", scope.agentID, "source", receipt.OperationName, "error", err.Error())
		}
		return nil, err
	}
	return func(operationID string, committed bool) error {
		completion := committed
		return record(context.Background(), HistoryMutation{
			SessionID: scope.sessionID, MessageID: scope.messageID, AgentID: scope.agentID,
			Receipt: fileio.MutationReceipt{OperationID: operationID, OperationName: receipt.OperationName}, Committed: &completion,
		})
	}, nil
}

// New creates a backend session in the global sessions directory.
// It is a thin wrapper around NewWithSessionDir using the legacy
// ~/.crude/sessions/ root. New workspaces should use NewWithSessionDir
// to place session data under the workspace folder.
func New(id string, cfg *Config, homeDir string) (*Session, error) {
	sessionsRoot, _ := SessionsRoot()
	return NewWithSessionDir(id, cfg, homeDir, sessionsRoot)
}

// NewWithSessionDir creates a backend session with an explicit session
// directory. All session artifacts (logs, DB, reports) live under that
// directory. Used by workspace/ to keep sessions inside the workspace
// folder.
func NewWithSessionDir(id string, cfg *Config, homeDir, sessionDir string) (*Session, error) {
	workspaceCfg := copyConfig(cfg)
	cfg = copyConfig(cfg)
	mainCat := cfg.GetActiveDefaultCategory()
	p, err := cfg.providerForCategory(mainCat)
	if err != nil {
		return nil, fmt.Errorf("provider standard: %w", err)
	}
	if strings.TrimSpace(mainCat.Model) == "" {
		return nil, ErrModelRequired
	}
	if mainCat.ModelInputPrice > 0 {
		provider.SetModelPrice(mainCat.Model, provider.ModelPrice{
			InputPricePerToken:       mainCat.ModelInputPrice,
			OutputPricePerToken:      mainCat.ModelOutputPrice,
			InputCachedPricePerToken: mainCat.ModelCachePrice,
		})
	}
	// Register the configured modality set so tool-boundary capability
	// checks see the settings truth immediately, before any turn runs.
	provider.MergeModelModalities(mainCat.Model, mainCat.ModelModalities)

	if sessionDir != "" {
		_ = os.MkdirAll(sessionDir, 0700)
	}

	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
		SessionsDir:    sessionDir,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	resourcesTransferred := false
	var editor *fileio.Editor
	var db *sql.DB
	defer func() {
		if !resourcesTransferred {
			if db != nil {
				_ = db.Close()
			}
			_ = lg.Close()
		}
	}()
	exec := fileio.NewLocalExecWithLogger(lg)
	sessionID := lg.SessionID()
	tempDir, err := prepareSessionTempDir(lg.SessionDir(), sessionID)
	if err != nil {
		_ = lg.Close()
		return nil, fmt.Errorf("session temp directory: %w", err)
	}
	keepTempDir := false
	defer func() {
		if !keepTempDir {
			_ = os.RemoveAll(tempDir)
		}
	}()
	asyncMgr := fileio.NewAsyncCommandManagerWithLogger(lg)
	if err := asyncMgr.SetTempDir(tempDir); err != nil {
		_ = lg.Close()
		return nil, fmt.Errorf("command temp directory: %w", err)
	}
	// When callers pass an empty id (workspace CreateSession), reuse the
	// logger-generated sessionID that already keys the session directory
	// and SQLite file. This keeps Session.ID aligned with on-disk identity
	// without inventing a second scheme.
	if id == "" {
		id = sessionID
	}
	lg.Info("SESSION", "start", "version", "1.0")
	lg.Info("SESSION", "config", "template", p.ID, "account", p.AccountID, "model", mainCat.Model)

	permStore := safety.NewPermissionStore()
	permStore.SetLogger(lg)
	safety.SetCapabilitiesLogger(lg)

	provider.SetLogger(lg)
	search.SetLogger(lg)
	clipboard.SetLogger(lg)
	jsonrepair.SetLogger(lg)
	diff.SetLogger(lg)
	fileio.SetSafetyLogger(lg)

	editor = fileio.NewEditor(sessionID, lg)

	name, nameErr := allocateAgentName(homeDir)
	if nameErr != nil {
		_ = lg.Close()
		return nil, fmt.Errorf("allocate agent name: %w", nameErr)
	}
	var dbPath string
	if sessionDir != "" {
		dbPath = filepath.Join(sessionDir, sessionID+".db")
		openDB, dbErr := sessiondb.OpenAndMigrate(dbPath)
		if dbErr != nil {
			lg.Warn("SESSION", "db_open_error", "error", fmt.Sprintf("%v", dbErr))
		} else {
			db = openDB
			wd, _ := os.Getwd()
			projectName := "unknown"
			if wd != "" {
				projectName = filepath.Base(wd)
			}
			if sErr := sessiondb.Create(db, sessiondb.SessionMeta{
				ID:          sessionID,
				AgentName:   sql.NullString{String: name, Valid: true},
				ProjectPath: wd,
				ProjectName: projectName,
				Model:       mainCat.Model,
				Provider:    p.ID,
				AccountID:   mainCat.AccountID,
				CreatedAt:   time.Now().UnixNano(),
			}); sErr != nil {
				_ = db.Close()
				_ = lg.Close()
				return nil, fmt.Errorf("create session metadata: %w", sErr)
			}
		}
	}

	sess := &Session{
		agentName: name, nameHome: homeDir,
		ID:                  id,
		cfg:                 cfg,
		provider:            p,
		exec:                exec,
		editor:              editor,
		logger:              lg,
		permStore:           permStore,
		asyncMgr:            asyncMgr,
		tempDir:             tempDir,
		subAgentSup:         nil,
		sessionID:           sessionID,
		sessionStart:        time.Now(),
		usageRecords:        make(map[string]sessiondb.UsageRecord),
		tokens:              NewTokenTracker(lg),
		db:                  db,
		dbPath:              dbPath,
		collaborationAgents: make(map[string]collaboration.Agent),
		orchestrators:       make(map[string]*Orchestrator),
		recoverySlots:       make(map[string]*recoverySlot),
		pendingRecoveries:   make(map[string]sessiondb.RecoveryCheckpoint),
		runtimeProfile:      NewRuntimeProfile(cfg),
		workspaceCfg:        workspaceCfg,
		sessionOverrides:    make(map[string]sessiondb.AgentSettingOverride),
	}

	if err := sess.loadQueuedInputs(); err != nil {
		return nil, err
	}
	if err := sess.loadExecutionSelection(); err != nil {
		return nil, err
	}
	if dir := lg.SessionDir(); dir != "" {
		_ = os.MkdirAll(filepath.Join(dir, "reports"), 0755)
	}

	keepTempDir = true
	resourcesTransferred = true
	return sess, nil
}

func (s *Session) Close() {
	if !atomic.CompareAndSwapInt32(&s.closing, 0, 1) {
		return
	}
	// Closing rejects new settings work before joining operations already admitted.
	// Publish the closing log only after their final settings logs have completed;
	// do not hold mu (or the lifetime fence) during the remaining shutdown.
	s.settingsOperationMu.Lock()
	s.logger.Info("SESSION", "close", "has_messages", s.HasMessages())
	s.settingsOperationMu.Unlock()
	s.stopCompactionForClose()
	s.compactionWg.Wait()
	s.closePrecompaction("")
	// Join any accepted queue admission before closing its database. Late
	// kicks recheck closing under this same barrier and cannot touch storage.
	s.queueAdmitMu.Lock()
	s.admissionMu.Lock()
	pendingAdmissions := make([]*InputAdmissionIntent, 0, len(s.pendingAdmissions))
	for intent := range s.pendingAdmissions {
		pendingAdmissions = append(pendingAdmissions, intent)
	}
	s.admissionMu.Unlock()
	s.queueAdmitMu.Unlock()
	for _, intent := range pendingAdmissions {
		result := s.CommitInputAdmission(context.Background(), intent)
		if result.Status == InputAdmissionRejected && s.logger != nil {
			s.logger.Error("INPUT", "close_admission_preserve_failed", "input_id", intent.InputID, "error", result.Err)
		}
	}
	s.mu.Lock()
	collaborationCancel := s.collaborationCancel
	s.collaborationCancel = nil
	s.mu.Unlock()
	if collaborationCancel != nil {
		collaborationCancel()
		s.collaborationWg.Wait()
	}
	s.orchMu.Lock()
	orchIDs := make([]string, 0, len(s.orchestrators))
	for id := range s.orchestrators {
		orchIDs = append(orchIDs, id)
	}
	s.orchMu.Unlock()
	for _, id := range orchIDs {
		s.CloseOrchestrator(id)
	}
	s.orchWg.Wait()
	if s.asyncMgr != nil {
		s.asyncMgr.CloseAndWait()
	}
	s.reconcileRootExecutionPins()
	if s.subAgentSup != nil {
		s.subAgentSup.SessionCancel()
		for _, h := range s.subAgentSup.Manager().ActiveHandles() {
			if h.Cancel != nil {
				h.Cancel()
			}
		}
		s.subAgentSup.Wait()
	}
	s.historyWg.Wait()

	// Join memory workers before their workspace DB is closed. Optional
	// provider work resumes from durable watermarks on the next open.
	s.finishMemoryJobs()

	hasMsgs := s.HasMessages() || len(s.QueuedInputs()) != 0
	// A preflight-blocked turn may own an exact pending input without a
	// transcript yet. Never prune its recovery DB as an empty session.
	if s.db != nil && !hasMsgs {
		checkpoints, err := sessiondb.LoadOpenRecoveryCheckpoints(s.db, s.sessionID)
		if err != nil {
			s.logger.Error("RECOVERY", "close_pending_read_failed", "error", err.Error())
			hasMsgs = true // Preserve data when emptiness cannot be proven.
		} else {
			hasMsgs = len(checkpoints) > 0
		}
	}
	// Collaboration lives in the workspace database. A peer's delivery to this
	// idle session, or any run, event or claim of its agents, is work it must
	// keep even without a transcript; otherwise its directory entry retires.
	if store := s.CollaborationStore(); store != nil && !hasMsgs {
		retired, err := store.RetireUnreferencedSession(context.Background(), s.sessionID)
		if err != nil {
			s.logger.Error("COLLAB", "close_retire_failed", "error", err.Error())
		}
		hasMsgs = err != nil || !retired
	}

	if s.db != nil {
		if err := sessiondb.CloseSession(s.db, s.sessionID); err != nil {
			s.logger.Error("SESSION", "close_session_error", "error", err.Error())
		}
		if err := s.db.Close(); err != nil {
			s.logger.Error("SESSION", "db_close_error", "error", err.Error())
		}
	}

	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
	s.logger.Close()
	if !hasMsgs {
		_ = s.logger.Remove()
		pruned := true
		if s.dbPath != "" {
			if err := os.Remove(s.dbPath); err != nil && !os.IsNotExist(err) {
				pruned = false // The database stays resumable and registered.
			}
		}
		s.pruned.Store(pruned)
	}
}

// Pruned reports that Close removed this empty session's storage, so it no
// longer exists anywhere and must not stay registered.
func (s *Session) Pruned() bool { return s != nil && s.pruned.Load() }

func (s *Session) SessionDir() string {
	return s.logger.SessionDir()
}

func prepareSessionTempDir(sessionDir, sessionID string) (string, error) {
	base := ""
	if sessionDir != "" {
		base = filepath.Join(sessionDir, "tmp")
	} else {
		base = filepath.Join(os.TempDir(), "crude", "sessions", filepath.Base(sessionID))
	}
	dir := filepath.Join(base, "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Session) TempDir() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tempDir
}

// WorkDir returns the workspace root this session resolves relative paths
// and commands against.
func (s *Session) WorkDir() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	dir := s.workDir
	s.mu.Unlock()
	if dir != "" {
		return dir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// SetWorkDir re-roots this session's tool execution. Called by workspace
// create/switch so a resumed or newly created session follows its project.
func (s *Session) SetWorkDir(dir string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.workDir = dir
	s.mu.Unlock()
	s.systemPromptMu.Lock()
	s.systemPrompts = nil
	s.systemPromptMu.Unlock()
	s.guidanceMu.Lock()
	s.guidanceCache = nil
	s.guidanceMu.Unlock()
	if s.db != nil && dir != "" {
		if err := sessiondb.UpdateProject(s.db, s.sessionID, dir, filepath.Base(dir)); err != nil && s.logger != nil {
			s.logger.Warn("SESSION", "project_root_update_failed", "path", dir, "error", err.Error())
		}
	}
}

func (s *Session) DB() *sql.DB { return s.db }

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) RuntimeProfile() *RuntimeProfile { return s.runtimeProfile }

// ModelName returns the display name of the active (main agent) model.
func (s *Session) ModelName() string {
	return s.cfg.GetActiveDefaultCategory().Model
}

// ModelIdentity reports configuration, not the last request's historical usage.
func (s *Session) ModelIdentity() ttypes.TurnIdentity {
	s.mu.Lock()
	defer s.mu.Unlock()
	main := s.cfg.GetActiveDefaultCategory()
	return ttypes.TurnIdentity{ModelID: main.Model, AccountID: main.AccountID, ProviderID: s.cfg.Accounts[main.AccountID].ProviderID}
}

func (s *Session) systemPrompt(multiAgent bool) string {
	model := ""
	if s != nil && s.cfg != nil {
		model = s.ModelName()
	}
	return s.systemPromptForModel(multiAgent, model)
}

func (s *Session) systemPromptForModel(multiAgent bool, model string) string {
	root := ""
	if s != nil {
		root = s.WorkDir()
	}
	return s.systemPromptForModelAtRoot(multiAgent, model, root)
}

func (s *Session) systemPromptForModelAtRoot(multiAgent bool, model, root string) string {
	if s == nil {
		return rootprompt.BuildRoot(rootprompt.RootOptions{MultiAgent: multiAgent})
	}
	s.mu.Lock()
	var cfg *Config
	if s.cfg != nil {
		cfg = copyConfig(s.cfg)
	}
	s.mu.Unlock()
	s.systemPromptMu.Lock()
	defer s.systemPromptMu.Unlock()
	if s.systemPrompts == nil {
		s.systemPrompts = make(map[string]string)
	}
	instructions := ""
	if cfg != nil {
		instructions = cfg.GetActiveDefaultCategory().Instructions
	}
	catalog := ""
	if multiAgent {
		catalog = agentProfileCatalog(cfg)
	}
	key := fmt.Sprintf("%t:%s:%s:%s:%s", multiAgent, model, instructions, catalog, root)
	if prompt, ok := s.systemPrompts[key]; ok {
		return prompt
	}
	// The prompt must advertise the same working directory tools resolve
	// against; a stale cwd here misdirects the model after a workspace
	// switch even when execution itself is correctly re-rooted.
	prompt := rootprompt.BuildRoot(rootprompt.RootOptions{
		MultiAgent: multiAgent, WorkDir: root, TempDir: s.TempDir(), Model: model,
	})
	prompt = rootprompt.ApplyRootOverlays(prompt, instructions, s.agentName, catalog)
	s.systemPrompts[key] = prompt
	return prompt
}

func (s *Session) SessionID() string {
	return s.sessionID
}

func (s *Session) SessionStart() time.Time {
	return s.sessionStart
}

func (s *Session) CostSnapshot() CostSnapshot {
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	snapshot := CostSnapshot{ByModel: make(map[string]ModelCost)}
	var latest int64
	for _, record := range s.usageRecords {
		snapshot.TotalPromptTokens += record.PromptTokens
		snapshot.TotalCompletionTokens += record.CompletionTokens
		snapshot.TotalCachedTokens += record.CachedTokens
		if record.CostKnown {
			snapshot.TotalCost += record.CostUSD
		}
		model := snapshot.ByModel[record.ModelID]
		model.PromptTokens += record.PromptTokens
		model.CompletionTokens += record.CompletionTokens
		model.CachedTokens += record.CachedTokens
		if record.CostKnown {
			model.Cost += record.CostUSD
		}
		snapshot.ByModel[record.ModelID] = model
		if record.RequestKind != "permission_review" && record.UpdatedAt >= latest {
			latest = record.UpdatedAt
			snapshot.LastPromptTokens = record.PromptTokens
			snapshot.LastCompletionTokens = record.CompletionTokens
			snapshot.LastCachedTokens = record.CachedTokens
		}
	}
	return snapshot
}

func (s *Session) Logger() *logger.Logger { return s.logger }

// initSubAgentRunner creates the runner only on demand. Registration and
// identity are published together, including when Settings enabled delegation
// after the orchestrator had already started.
func (s *Session) initSubAgentRunner() bool {
	s.orchMu.Lock()
	defer s.orchMu.Unlock()
	s.subAgentMu.Lock()
	defer s.subAgentMu.Unlock()
	if atomic.LoadInt32(&s.closing) != 0 {
		return false
	}
	if s.subAgentSup != nil {
		return true
	}
	var owner *Orchestrator
	for _, o := range s.orchestrators {
		if !o.closing.Load() {
			owner = o
			break
		}
	}
	var cfg *Config
	if s.runtimeProfile != nil {
		id := ""
		if owner != nil {
			id = owner.ID
		}
		cfg = s.runtimeProfile.GetEffectiveConfig(id)
	} else {
		s.mu.Lock()
		if s.cfg != nil {
			cfg = copyConfig(s.cfg)
		}
		s.mu.Unlock()
	}
	if cfg == nil || !cfg.IsMultiAgentEnabled() {
		return false
	}
	runner := NewSubAgentRunner(s, s.effectiveMaxSubAgents())
	if owner != nil {
		runner.SetSender(owner.sender)
		runner.SetOrchestratorID(owner.ID)
		owner.turnMu.Lock()
		runner.SetParentTurnGeneration(owner.turnGeneration)
		owner.turnMu.Unlock()
	}
	s.subAgentSup = runner
	return true
}

func (s *Session) SubAgentRunner() *SubAgentRunner {
	s.subAgentMu.Lock()
	defer s.subAgentMu.Unlock()
	return s.subAgentSup
}

// SubAgentSnapshot is the durable review state for a completed child. The
// child remains part of its parent session; it is never a root resumable
// session even though it owns a separate transcript database.
type SubAgentSnapshot struct {
	Result      agent.SubAgentResult
	Model       string
	Messages    []sessiondb.MessageWithMeta
	Compactions []sessiondb.CompactionRecord
	Recovery    *recoverytypes.Status
}

func (s *Session) CompletedSubAgentSnapshots() []SubAgentSnapshot {
	if s == nil || s.SubAgentRunner() == nil {
		return nil
	}
	results := s.SubAgentRunner().Manager().CompletedResults()
	snapshots := make([]SubAgentSnapshot, 0, len(results))
	for _, result := range results {
		snapshot := SubAgentSnapshot{Result: result}
		if dbPath := s.completedSubAgentDBPath(result.SessionID); dbPath != "" {
			if meta, messages, compactions, err := loadSubAgentSnapshotDB(dbPath, string(result.AgentID)); err == nil {
				snapshot.Model = meta.Model
				for i := range messages {
					if messages[i].Msg.Role == "user" {
						messages[i].Meta.DisplayRole = "assignment"
						break
					}
				}
				snapshot.Messages = messages
				snapshot.Compactions = compactions
			}
		}
		if len(snapshot.Messages) == 0 && s.recoveredSubAgentMessages != nil {
			snapshot.Messages = append([]sessiondb.MessageWithMeta(nil), s.recoveredSubAgentMessages[result.SessionID]...)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func (s *Session) completedSubAgentDBPath(childSessionID string) string {
	childSessionID = filepath.Base(childSessionID)
	if childSessionID == "" || childSessionID == "." {
		return ""
	}
	if dir := s.SessionDir(); dir != "" {
		path := filepath.Join(dir, "subagents", childSessionID+".db")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func loadSubAgentSnapshotDB(dbPath, agentID string) (sessiondb.SessionMeta, []sessiondb.MessageWithMeta, []sessiondb.CompactionRecord, error) {
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return sessiondb.SessionMeta{}, nil, nil, err
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	meta, err := sessiondb.GetMeta(db)
	if err != nil {
		return sessiondb.SessionMeta{}, nil, nil, err
	}
	if err := sessiondb.InterruptRunningCompactions(db, meta.ID); err != nil {
		return sessiondb.SessionMeta{}, nil, nil, err
	}
	messages, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		return sessiondb.SessionMeta{}, nil, nil, err
	}
	compactions, err := sessiondb.ListCompactionRecords(db, meta.ID, agentID)
	return meta, messages, compactions, err
}

func (s *Session) HardCancel() {
	atomic.StoreInt32(&s.hardCancelled, 1)
	s.CancelCompaction("")
	s.orchMu.Lock()
	orchestrators := make([]*Orchestrator, 0, len(s.orchestrators))
	for _, orchestrator := range s.orchestrators {
		orchestrators = append(orchestrators, orchestrator)
	}
	s.orchMu.Unlock()
	for _, orchestrator := range orchestrators {
		orchestrator.CancelTurn()
	}
	if runner := s.SubAgentRunner(); runner != nil {
		runner.CancelAllActive()
	}
	s.logger.Info(
		"CANCEL", "hard_cancel",
		"orchestrators", len(orchestrators), "sub_agents_cancelled", s.SubAgentRunner() != nil,
	)
}

// CancelOrchestrator cancels only the active turn owned by id. The
// orchestrator's generation-bound context prevents the request from leaking
// into a later turn.
func (s *Session) CancelOrchestrator(id string) (bool, []string) {
	if s == nil || id == "" {
		return false, nil
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[id]
	s.orchMu.Unlock()
	if orchestrator == nil {
		if s.logger != nil {
			s.logger.Warn("AGENT", "cancel_unrouted", "orchestrator_id", id)
		}
		return false, nil
	}
	cancelled, ids := orchestrator.CancelTurn()
	if s.logger != nil {
		s.logger.Info("AGENT", "cancel_requested", "orchestrator_id", id, "cancelled", cancelled, "turns", len(ids))
	}
	return cancelled, ids
}

// SubmitInput registers a cancellable turn before handing it to the session
// loop, eliminating the enqueue-to-context race.
func (s *Session) SubmitInput(id string, input provider.UserInput) bool {
	if s == nil || id == "" {
		return false
	}
	if s.compactionActive(id) {
		return false
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[id]
	s.orchMu.Unlock()
	return orchestrator != nil && orchestrator.SubmitInput(input)
}

// ResolveBlock routes a request-correlated blocking outcome to its owning
// orchestrator. Stale panels cannot resolve a later tool request.
func (s *Session) ResolveBlock(id string, response ttypes.BlockResponse) bool {
	if s == nil || id == "" || response.RequestID == "" {
		return false
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[id]
	s.orchMu.Unlock()
	if orchestrator != nil {
		return orchestrator.ResolveBlock(response)
	}
	runner := s.SubAgentRunner()
	return runner != nil && runner.ResolveBlock(agent.AgentID(id), response)
}

func (s *Session) isHardCancelled() bool {
	return atomic.LoadInt32(&s.hardCancelled) == 1
}

// IsTurnActive reports whether this session currently owns a provider/tool
// turn. Context and journal mutation commands use it to reject unsafe overlap.
func (s *Session) IsTurnActive() bool {
	if s == nil {
		return false
	}
	if s.HasActiveTurns() {
		return true
	}
	if s.activeTurns.Load() > 0 {
		return true
	}
	if s.compactionActive("") {
		return true
	}
	s.orchMu.Lock()
	orchestrators := make([]*Orchestrator, 0, len(s.orchestrators))
	for _, orchestrator := range s.orchestrators {
		orchestrators = append(orchestrators, orchestrator)
	}
	s.orchMu.Unlock()
	for _, orchestrator := range orchestrators {
		if orchestrator.TurnActive() {
			return true
		}
	}
	return false
}

func (s *Session) clearHardCancel() {
	atomic.StoreInt32(&s.hardCancelled, 0)
}

func (s *Session) NewOrchestrator(sender EventSender) (*Orchestrator, error) {
	s.orchMu.Lock()
	if atomic.LoadInt32(&s.closing) == 1 {
		s.orchMu.Unlock()
		return nil, fmt.Errorf("session is closing")
	}
	for _, existing := range s.orchestrators {
		if existing != nil && !existing.closing.Load() {
			s.orchMu.Unlock()
			return existing, nil
		}
	}
	nextID := s.orchNextID.Add(1)
	o := NewOrchestrator(fmt.Sprintf("%s_orch_%d", s.ID, nextID), s)
	o.sender = sender

	// Initialize a runtime profile override for this orchestrator.
	// This is a fresh, empty override that starts from base config.
	s.runtimeProfile.GetOrCreateOverride(o.ID)

	// A restored runner is attached before the orchestrator is published.
	if runner := s.SubAgentRunner(); runner != nil {
		runner.SetSender(sender)
		runner.SetOrchestratorID(o.ID)
	}

	if atomic.LoadInt32(&s.closing) == 1 {
		s.orchMu.Unlock()
		s.runtimeProfile.RemoveOverride(o.ID)
		return nil, fmt.Errorf("session is closing")
	}
	registered, err := s.registerCollaborator(context.Background(), o.ID, s.AgentName(), s.sessionID, "", CategoryDefault, s.db)
	if err != nil {
		s.orchMu.Unlock()
		s.runtimeProfile.RemoveOverride(o.ID)
		return nil, fmt.Errorf("register workspace agent: %w", err)
	}
	o.RuntimeEpoch = registered.RuntimeEpoch
	if s.resumed {
		s.recoveryMu.Lock()
		for previousID, checkpoint := range s.pendingRecoveries {
			if checkpoint.ContextOwnerID != s.sessionID {
				continue
			}
			delete(s.pendingRecoveries, previousID)
			checkpoint.Status.AgentID = o.ID
			s.pendingRecoveries[o.ID] = checkpoint
			o.turnGeneration = max(o.turnGeneration, checkpoint.Status.Generation)
			if err := sessiondb.RebindRecoveryAgent(s.db, s.sessionID, previousID, o.ID); err != nil {
				s.recoveryMu.Unlock()
				s.orchMu.Unlock()
				s.detachCollaborator(context.Background(), o.ID)
				s.runtimeProfile.RemoveOverride(o.ID)
				return nil, fmt.Errorf("rebind passive recovery: %w", err)
			}
			break
		}
		s.recoveryMu.Unlock()
		if err := s.recoverChildCollaborators(context.Background(), o.ID); err != nil {
			s.orchMu.Unlock()
			s.detachCollaborator(context.Background(), o.ID)
			return nil, fmt.Errorf("recover workspace children: %w", err)
		}
		if store := s.CollaborationStore(); store != nil {
			page, err := store.Page(context.Background(), collaboration.PageQuery{Limit: 1})
			if err != nil {
				s.orchMu.Unlock()
				s.detachCollaborator(context.Background(), o.ID)
				return nil, fmt.Errorf("capture passive resume boundary: %w", err)
			}
			o.passiveThrough = page.Snapshot
		}
	}
	if s.orchestrators == nil {
		s.orchestrators = make(map[string]*Orchestrator)
	}
	s.orchestrators[o.ID] = o
	s.orchWg.Add(1)
	s.orchMu.Unlock()

	go func() {
		defer s.orchWg.Done()
		s.RunOrchestrator(o, sender)
	}()

	return o, nil
}

func (s *Session) CloseOrchestrator(id string) {
	s.orchMu.Lock()
	o, ok := s.orchestrators[id]
	delete(s.orchestrators, id)
	s.orchMu.Unlock()
	if ok && o.closing.CompareAndSwap(false, true) {
		s.closePrecompaction(id)
		o.CancelTurn()
		if runner := o.subAgentRunner(); runner != nil {
			runner.CancelAllActive()
		}
		close(o.stopCh)
		o.turnMu.Lock()
		generation := o.turnGeneration
		o.turnMu.Unlock()
		o.endTurn(generation)
	}
	// Clean up the runtime profile override for this orchestrator.
	s.runtimeProfile.RemoveOverride(id)
}
