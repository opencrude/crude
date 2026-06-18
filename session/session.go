package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"crude/clipboard"
	"crude/diff"
	"crude/fileio"
	"crude/js"
	"crude/jsonrepair"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/search"
	"crude/sessiondb"
	"crude/statemachine"
)

// WakeChBufSize is the buffer size for the session wake channel.
// Must be large enough to handle bursts of concurrent sub-agent completions
// without dropping wake events.
const WakeChBufSize = 32

type EventSender interface {
	SendAgentEvent(typ string, data any)
	SendAgentEventWithID(typ string, data any, agentID string)
}

type EventInput interface {
	GetInputCh() <-chan provider.UserInput
	GetCancelCh() <-chan struct{}
}

type Session struct {
	ID  string
	cfg *Config
	mu  sync.Mutex

	provider  *provider.Provider
	exec      fileio.Executer
	editor    *fileio.Editor
	logger    *logger.Logger
	permStore *safety.PermissionStore
	sm        *statemachine.ConversationStateMachine
	asyncMgr  *fileio.AsyncCommandManager

	msgs          []provider.ChatMessage
	msgTurns      []int
	displayMetas  []sessiondb.DisplayMeta
	createdAts    []int64
	msgsMu        sync.Mutex
	pendingBlock  bool
	blockMu       sync.Mutex
	lastBlockResp string
	blockRespMu   sync.Mutex
	msgCounter    int
	msgCounterMu  sync.Mutex

	sessionID    string
	sessionStart time.Time
	knowledgeDir string
	yolo         bool

	cost   *CostAccumulator
	tokens *TokenTracker

	autoHandleCounter int
	autoHandleMu      sync.Mutex

	toolCallCount   int
	toolCallCountMu sync.Mutex

	db            *sql.DB
	dbPath        string
	turnNumber    int
	toolCallSeq   int
	seqNum        int
	writeBuf      []taggedMessage
	writeMu       sync.Mutex
	flushWg       sync.WaitGroup
	closing       int32
	hardCancelled int32
	wakeCh        chan WakeEvent
	sender        EventSender
	subAgentSup   *SubAgentRunner
	orchestrators map[string]*Orchestrator
	orchMu        sync.Mutex
	orchNextID    atomic.Int64

	// consumedReports tracks sub-agent IDs whose results the main agent has already
	// consumed during an active turn (typically via poll_sub_agent). When a WakeSubAgentDone
	// event later arrives for a consumed sub-agent, the wake is suppressed to prevent a
	// redundant turn where the agent would see the same report/notification again.
	consumedReports   map[string]bool
	consumedReportsMu sync.Mutex

	// runtimeProfile holds ephemeral per-orchestrator config overrides
	// that never persist to disk.
	runtimeProfile *RuntimeProfile
}

func New(id string, cfg *Config, homeDir string) (*Session, error) {
	mainCat := cfg.GetActiveDefaultCategory()
	p := provider.FromPreset(mainCat.Provider, mainCat.APIKey, mainCat.ProviderURL)
	p.Model = mainCat.Model
	p.SetThinkingEffort(mainCat.ThinkingEffort)
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

	sessionsRoot, _ := SessionsRoot()
	if sessionsRoot != "" {
		os.MkdirAll(sessionsRoot, 0700)
		sessiondb.CleanOldSessions(sessionsRoot)
	}
	fileio.SetSessionsRootFn(SessionsRoot)

	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
		SessionsDir:    sessionsRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	exec := fileio.NewLocalExecWithLogger(lg)
	sessionID := lg.SessionID()
	lg.Info("SESSION", "start", "version", "1.0")
	lg.Info("SESSION", "config", "provider", mainCat.Provider, "model", mainCat.Model)

	permStore := safety.NewPermissionStore()
	permStore.SetLogger(lg)
	safety.SetCapabilitiesLogger(lg)

	provider.SetLogger(lg)
	search.SetLogger(lg)
	js.SetLogger(lg)
	clipboard.SetLogger(lg)
	jsonrepair.SetLogger(lg)
	diff.SetLogger(lg)
	statemachine.SetGoalLogger(lg)
	statemachine.SetStepResultLogger(lg)
	fileio.SetSafetyLogger(lg)

	var editor *fileio.Editor
	if sessionsRoot != "" {
		if ed, edErr := fileio.NewEditorWithSessionDir(sessionID, sessionsRoot, lg); edErr != nil {
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

	sm := statemachine.NewConversationStateMachine(lg)

	var db *sql.DB
	var dbPath string
	if sessionsRoot != "" {
		dbPath = filepath.Join(sessionsRoot, sessionID+".db")
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
				ProjectPath: wd,
				ProjectName: projectName,
				Model:       mainCat.Model,
				Provider:    mainCat.Provider,
				CreatedAt:   time.Now().UnixNano(),
			}); sErr != nil {
				lg.Warn("SESSION", "db_session_create_error", "error", fmt.Sprintf("%v", sErr))
			}
		}
	}

	sess := &Session{
		ID:              id,
		cfg:             cfg,
		provider:        p,
		exec:            exec,
		editor:          editor,
		logger:          lg,
		permStore:       permStore,
		sm:              sm,
		asyncMgr:        fileio.NewAsyncCommandManagerWithLogger(lg),
		subAgentSup:     nil,
		sessionID:       sessionID,
		sessionStart:    time.Now(),
		wakeCh:          make(chan WakeEvent, WakeChBufSize),
		cost:            NewCostAccumulator(lg),
		tokens:          NewTokenTracker(lg),
		db:              db,
		dbPath:          dbPath,
		writeBuf:        make([]taggedMessage, 0, 64),
		consumedReports: make(map[string]bool),
		orchestrators:   make(map[string]*Orchestrator),
		runtimeProfile:  NewRuntimeProfile(cfg),
	}
	if cfg.IsMultiAgentEnabled() {
		sess.subAgentSup = NewSubAgentRunner(sess, cfg.Agents.MaxConcurrent)
	}

	if dir := lg.SessionDir(); dir != "" {
		os.MkdirAll(filepath.Join(dir, "reports"), 0755)
	}

	return sess, nil
}

func (s *Session) Close() {
	atomic.StoreInt32(&s.closing, 1)
	s.logger.Info("SESSION", "close", "has_messages", s.HasMessages())

	s.flushWg.Wait()
	s.flushWriteBufSync()

	if s.editor != nil {
		s.editor.Close()
	}
	hasMsgs := s.HasMessages()

	if hasMsgs && s.cost != nil {
		s.persistCostSnapshot()
	}

	if s.subAgentSup != nil {
		s.subAgentSup.SessionCancel()
		for _, h := range s.subAgentSup.Manager().ActiveHandles() {
			s.subAgentSup.Manager().Cancel(h.Spec.ID)
			s.subAgentSup.FileOwnership().Release(h.Spec.ID)
		}
		s.subAgentSup.Wait()
	}

	if s.db != nil {
		if err := sessiondb.CloseSession(s.db, s.sessionID); err != nil {
			s.logger.Error("SESSION", "close_session_error", "error", err.Error())
		}
		s.persistCostToDB()
		if err := s.db.Close(); err != nil {
			s.logger.Error("SESSION", "db_close_error", "error", err.Error())
		}
	}

	s.logger.Close()
	if !hasMsgs {
		s.logger.Remove()
		if s.dbPath != "" {
			os.Remove(s.dbPath)
		}
	}
}

func (s *Session) WakeCh() chan<- WakeEvent {
	return s.wakeCh
}

func (s *Session) SessionDir() string {
	return s.logger.SessionDir()
}

func (s *Session) DB() *sql.DB { return s.db }

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) RuntimeProfile() *RuntimeProfile { return s.runtimeProfile }

func (s *Session) ModelName() string {
	return s.cfg.GetActiveDefaultCategory().Model
}

func (s *Session) SessionID() string {
	return s.sessionID
}

func (s *Session) SessionStart() time.Time {
	return s.sessionStart
}

func (s *Session) CostSnapshot() CostSnapshot {
	if s.cost != nil {
		return s.cost.Snapshot()
	}
	return CostSnapshot{}
}

func (s *Session) Logger() *logger.Logger { return s.logger }

// initSubAgentRunner lazily creates the sub-agent runner if multi-agent is enabled.
// Returns true if the runner is available after the call.
func (s *Session) initSubAgentRunner() bool {
	if s.subAgentSup != nil {
		return true
	}
	if s.cfg != nil && s.cfg.IsMultiAgentEnabled() {
		s.subAgentSup = NewSubAgentRunner(s, s.cfg.Agents.MaxConcurrent)
		if s.sender != nil {
			s.subAgentSup.SetSender(s.sender)
		}
		return true
	}
	return false
}

func (s *Session) SubAgentRunner() *SubAgentRunner {
	return s.subAgentSup
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
	if s.subAgentSup != nil {
		s.subAgentSup.CancelAllActive()
	}
	s.logger.Info("CANCEL", "hard_cancel", "sub_agents_cancelled", s.subAgentSup != nil)
}

func (s *Session) isHardCancelled() bool {
	return atomic.LoadInt32(&s.hardCancelled) == 1
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
	nextID := s.orchNextID.Add(1)
	o := NewOrchestrator(fmt.Sprintf("%s_orch_%d", s.ID, nextID), s)

	// Initialize a runtime profile override for this orchestrator.
	// This is a fresh, empty override that starts from base config.
	s.runtimeProfile.GetOrCreateOverride(o.ID)

	if s.subAgentSup == nil && s.cfg.IsMultiAgentEnabled() {
		s.subAgentSup = NewSubAgentRunner(s, s.cfg.Agents.MaxConcurrent)
	}
	if s.subAgentSup != nil {
		s.subAgentSup.SetSender(sender)
		o.subAgentSup = s.subAgentSup
	}

	s.orchMu.Lock()
	if s.orchestrators == nil {
		s.orchestrators = make(map[string]*Orchestrator)
	}
	s.orchestrators[o.ID] = o
	s.orchMu.Unlock()

	go s.RunOrchestrator(o, sender)

	return o, nil
}

func (s *Session) CloseOrchestrator(id string) {
	s.orchMu.Lock()
	o, ok := s.orchestrators[id]
	delete(s.orchestrators, id)
	s.orchMu.Unlock()
	if ok {
		o.closing.Store(true)
		close(o.InputCh)
		close(o.BtwCh)
	}
	// Clean up the runtime profile override for this orchestrator.
	s.runtimeProfile.RemoveOverride(id)
}
