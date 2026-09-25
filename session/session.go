package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"crude/agent"
	"crude/clipboard"
	"crude/codexio"
	"crude/diff"
	"crude/fileio"
	"crude/jsonrepair"
	"crude/logger"
	"crude/memory"
	"crude/plan"
	"crude/provider"
	"crude/safety"
	"crude/search"
	"crude/sessiondb"
	ttypes "crude/types"
)

// WakeChBufSize is the buffer size for the session wake channel.
// Must be large enough to handle bursts of concurrent sub-agent completions
// without dropping wake events.
const WakeChBufSize = 32

var ErrHistoryDisabled = errors.New("local history disabled")

type EventSender interface {
	SendAgentEvent(typ string, data any)
	SendAgentEventWithID(typ string, data any, agentID string)
}

type Session struct {
	plans     atomic.Pointer[plan.Store]
	agentName string
	nameHome  string
	ID        string
	cfg       *Config
	mu        sync.Mutex

	provider *provider.Provider
	// An older transcript without a connection pin stays disconnected until
	// the user explicitly saves a session connection.
	accountUnpinned bool
	exec            fileio.Executer
	editor          *fileio.Editor
	logger          *logger.Logger
	permStore       *safety.PermissionStore
	asyncMgr        *fileio.AsyncCommandManager
	memoryStore     *memory.Store
	memorySummary   atomic.Pointer[memory.SessionSummary]

	// workDir is the workspace root this session executes tools against.
	// Relative tool paths and commands without an explicit cwd resolve
	// against it. Empty means the process working directory (tests).
	workDir string

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
	contextGeneration uint64
	contextReason     string

	recoveredSubAgentMessages map[string][]sessiondb.MessageWithMeta
	recoveredTurnGenerations  map[string]uint64

	sessionID    string
	sessionStart time.Time
	yolo         atomic.Bool

	usageMu      sync.RWMutex
	usageRecords map[string]sessiondb.UsageRecord
	tokens       *TokenTracker

	toolCallCount   int
	toolCallCountMu sync.Mutex

	db                       *sql.DB
	dbPath                   string
	turnNumber               int
	toolCallSeq              int
	seqNum                   int
	writeBuf                 []taggedMessage
	writeMu                  sync.Mutex
	closing                  int32
	hardCancelled            int32
	activeTurns              atomic.Int32
	turnGate                 sync.RWMutex
	compactionMu             sync.Mutex
	compactionCancel         context.CancelFunc
	compactionID             string
	compactionAgent          string
	compactionStarted        bool
	compactionWg             sync.WaitGroup
	wakeCh                   chan WakeEvent
	sender                   EventSender
	subAgentSup              *SubAgentRunner
	subAgentMu               sync.Mutex
	delegationMu             sync.Mutex
	delegationNoticeMu       sync.Mutex
	permissionReviewRevision atomic.Uint64
	delegationNotices        []provider.ChatMessage
	orchestrators            map[string]*Orchestrator
	orchMu                   sync.Mutex
	orchWg                   sync.WaitGroup
	orchNextID               atomic.Int64

	// consumedReports tracks sub-agent IDs whose results the main agent has already
	// consumed during an active turn (typically via poll_sub_agent). When a WakeSubAgentDone
	// event later arrives for a consumed sub-agent, the wake is suppressed to prevent a
	// redundant turn where the agent would see the same report/notification again.
	consumedReports   map[string]bool
	consumedReportsMu sync.Mutex

	// runtimeProfile holds ephemeral per-orchestrator config overrides
	// that never persist to disk.
	runtimeProfile *RuntimeProfile

	// workspaceCfg is the inherited workspace baseline. s.cfg is the effective
	// per-session config after applying overrides loaded from this session's DB.
	workspaceCfg     *Config
	sessionOverrides map[string]sessiondb.AgentSettingOverride
	// multiAgentOverride is the explicit session flag. nil inherits workspace.
	multiAgentOverride *bool
	historyMu          sync.RWMutex
	historyStart       func(context.Context, string, string) error
	historyEnd         func(context.Context, string, string) error
	historyContinue    func(context.Context, string, string) error
	historyContinueEnd func(context.Context, string, string) error
	beforeTurn         func(context.Context, string) error
	historyMutation    func(context.Context, HistoryMutation) error
	historyWg          sync.WaitGroup
	historyFinalizers  atomic.Int32
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

func newMessageID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("session: generate message ID: %v", err))
	}
	return "msg-" + hex.EncodeToString(value[:])
}

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
	if err := cfg.pinAccounts(); err != nil {
		return nil, err
	}
	mainCat := cfg.GetActiveDefaultCategory()
	p, err := cfg.providerForCategory(mainCat)
	if err != nil {
		return nil, fmt.Errorf("provider standard: %w", err)
	}
	if mainCat.ModelContextWindow > 0 {
		provider.SetModelContext(mainCat.Model, mainCat.ModelContextWindow)
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
	fileio.SetSessionsRootFn(func() (string, error) { return sessionDir, nil })

	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
		SessionsDir:    sessionDir,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	exec := fileio.NewLocalExecWithLogger(lg)
	sessionID := lg.SessionID()
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

	var editor *fileio.Editor
	if sessionDir != "" {
		if ed, edErr := fileio.NewEditorWithSessionDir(sessionID, sessionDir, lg); edErr != nil {
			lg.Warn("SESSION", "editor_init_error", "error", fmt.Sprintf("%v", edErr))
		} else {
			editor = ed
		}
	}
	if editor == nil {
		fileioSessionID := fileio.GenerateSessionID()
		if ed, edErr := fileio.NewEditorWithLogger(fileioSessionID, lg); edErr != nil {
			lg.Warn("SESSION", "editor_init_error", "error", fmt.Sprintf("%v", edErr))
		} else {
			editor = ed
		}
	}
	if editor != nil {
		editor.SetLegacyJournalWrites(false)
	}

	name, nameErr := allocateAgentName(homeDir)
	if nameErr != nil {
		_ = lg.Close()
		return nil, fmt.Errorf("allocate agent name: %w", nameErr)
	}
	var db *sql.DB
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
			if err := sessiondb.SaveAgentSettingOverrides(db, sessionID, pinnedAccountOverrides(cfg)); err != nil {
				_ = db.Close()
				_ = lg.Close()
				return nil, fmt.Errorf("persist account connections: %w", err)
			}
		}
	}

	sess := &Session{
		agentName: name, nameHome: homeDir,
		ID:               id,
		cfg:              cfg,
		provider:         p,
		exec:             exec,
		editor:           editor,
		logger:           lg,
		permStore:        permStore,
		asyncMgr:         fileio.NewAsyncCommandManagerWithLogger(lg),
		subAgentSup:      nil,
		sessionID:        sessionID,
		sessionStart:     time.Now(),
		wakeCh:           make(chan WakeEvent, WakeChBufSize),
		usageRecords:     make(map[string]sessiondb.UsageRecord),
		tokens:           NewTokenTracker(lg),
		db:               db,
		dbPath:           dbPath,
		writeBuf:         make([]taggedMessage, 0, 64),
		consumedReports:  make(map[string]bool),
		orchestrators:    make(map[string]*Orchestrator),
		runtimeProfile:   NewRuntimeProfile(cfg),
		workspaceCfg:     workspaceCfg,
		sessionOverrides: pinnedAccountOverrides(cfg),
	}

	if dir := lg.SessionDir(); dir != "" {
		_ = os.MkdirAll(filepath.Join(dir, "reports"), 0755)
	}

	return sess, nil
}

func (s *Session) Close() {
	atomic.StoreInt32(&s.closing, 1)
	s.logger.Info("SESSION", "close", "has_messages", s.HasMessages())
	s.stopCompactionForClose()
	s.compactionWg.Wait()
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
	if s.subAgentSup != nil {
		s.subAgentSup.SessionCancel()
		for _, h := range s.subAgentSup.Manager().ActiveHandles() {
			s.subAgentSup.Manager().Cancel(h.Spec.ID)
			s.subAgentSup.FileOwnership().Release(h.Spec.ID)
		}
		s.subAgentSup.Wait()
	}
	s.historyWg.Wait()

	s.flushWriteBufSync()

	// Join memory workers before their workspace DB is closed. Optional
	// provider work resumes from durable watermarks on the next open.
	s.finishMemoryJobs()

	if s.editor != nil {
		s.editor.Close()
	}
	hasMsgs := s.HasMessages()

	if s.db != nil {
		if err := sessiondb.CloseSession(s.db, s.sessionID); err != nil {
			s.logger.Error("SESSION", "close_session_error", "error", err.Error())
		}
		if err := s.db.Close(); err != nil {
			s.logger.Error("SESSION", "db_close_error", "error", err.Error())
		}
	}

	s.logger.Close()
	if !hasMsgs {
		_ = s.logger.Remove()
		if s.dbPath != "" {
			_ = os.Remove(s.dbPath)
		}
	}
}

func (s *Session) WakeCh() chan<- WakeEvent {
	return s.wakeCh
}

func (s *Session) SessionDir() string {
	return s.logger.SessionDir()
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
	if s == nil {
		return agent.BuildSystemPrompt(multiAgent)
	}
	s.systemPromptMu.Lock()
	defer s.systemPromptMu.Unlock()
	if s.systemPrompts == nil {
		s.systemPrompts = make(map[string]string)
	}
	key := fmt.Sprintf("%t:%s", multiAgent, model)
	if prompt, ok := s.systemPrompts[key]; ok {
		return prompt
	}
	// The prompt must advertise the same working directory tools resolve
	// against; a stale cwd here misdirects the model after a workspace
	// switch even when execution itself is correctly re-rooted.
	prompt := agent.BuildSystemPromptForModel(multiAgent, s.WorkDir(), model)
	if !codexio.IsGPT(model) {
		prompt += fmt.Sprintf("\n<agent_identity>Your name is %s.</agent_identity>", s.agentName)
	}
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
	runner := NewSubAgentRunner(s, cfg.Agents.MaxConcurrent)
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
	if legacyRoot, err := SessionsRoot(); err == nil {
		path := filepath.Join(legacyRoot, childSessionID+".db")
		if _, statErr := os.Stat(path); statErr == nil {
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

// MarkReportConsumed records that the main agent has already consumed a sub-agent's
// result during an active turn (e.g. via poll_sub_agent). The corresponding
// WakeSubAgentDone event will be suppressed to prevent a redundant wake-up turn
// where the agent would re-read the same report it already processed.
//
// This handles the edge case where the agent polls a completed sub-agent during
// an active turn (against the "don't poll" instruction, which LLMs sometimes
// ignore), reads the report, and then gets woken up again by the queued wake
// event when the turn ends — resulting in a wasteful duplicate turn.
func (s *Session) MarkReportConsumed(agentID string) {
	s.consumedReportsMu.Lock()
	if s.consumedReports == nil {
		s.consumedReports = make(map[string]bool)
	}
	s.consumedReports[agentID] = true
	s.consumedReportsMu.Unlock()
}

// IsAndClearReportConsumed checks whether a sub-agent's result was already consumed
// by the main agent during an earlier turn. If so, it removes the entry (each
// sub-agent completes at most once, so the entry is no longer needed) and returns
// true, indicating the wake event should be suppressed. Returns false if the
// sub-agent was not previously consumed, meaning the wake should be processed.
func (s *Session) IsAndClearReportConsumed(agentID string) bool {
	s.consumedReportsMu.Lock()
	consumed := s.consumedReports[agentID]
	delete(s.consumedReports, agentID)
	s.consumedReportsMu.Unlock()
	return consumed
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
		return false, nil
	}
	return orchestrator.CancelTurn()
}

// SubmitBtw atomically admits a note to id's current turn.
func (s *Session) SubmitBtw(id string, input provider.UserInput) bool {
	if s == nil || id == "" {
		return false
	}
	s.orchMu.Lock()
	orchestrator := s.orchestrators[id]
	s.orchMu.Unlock()
	return orchestrator != nil && orchestrator.SubmitBtw(input)
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

func (s *Session) drainWakeCh() {
	for {
		select {
		case <-s.wakeCh:
			s.logger.Debug("SLEEP", "drained_stale_wake")
		default:
			return
		}
	}
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
		o.CancelTurn()
		if runner := o.subAgentRunner(); runner != nil {
			for _, childID := range runner.CancelAllActive() {
				s.MarkReportConsumed(string(childID))
			}
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
