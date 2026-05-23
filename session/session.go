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

type EventSender interface {
	SendAgentEvent(typ string, data any)
	SendAgentEventWithID(typ string, data any, agentID string)
}

type EventInput interface {
	GetInputCh() <-chan string
	GetCancelCh() <-chan struct{}
}

type Session struct {
	cfg *Config
	mu  sync.Mutex

	provider  *provider.Provider
	exec      fileio.Executer
	editor    *fileio.Editor
	logger    *logger.Logger
	permStore *safety.PermissionStore
	sm        *statemachine.ConversationStateMachine
	asyncMgr  *fileio.AsyncCommandManager

	msgs         []provider.ChatMessage
	msgTurns     []int
	displayMetas []sessiondb.DisplayMeta
	createdAts   []int64
	msgsMu       sync.Mutex
	pendingBlock bool
	blockMu      sync.Mutex
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

	toolCallCount    int
	toolCallCountMu  sync.Mutex

	db        *sql.DB
	dbPath    string
	turnNumber  int
	toolCallSeq int
	seqNum     int
	writeBuf  []taggedMessage
	writeMu   sync.Mutex
	flushWg   sync.WaitGroup
	closing      int32
	wakeCh       chan WakeEvent
	sender       EventSender
	subAgentSup *SubAgentRunner
}

func New(cfg *Config, homeDir string) (*Session, error) {
	mainCat := cfg.GetActiveMainCategory()
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
		cfg:            cfg,
		provider:       p,
		exec:           exec,
		editor:         editor,
		logger:         lg,
		permStore:      permStore,
		sm:             sm,
		asyncMgr:       fileio.NewAsyncCommandManagerWithLogger(lg),
		subAgentSup:   nil,
		sessionID:      sessionID,
		sessionStart:   time.Now(),
		wakeCh:         make(chan WakeEvent, 8),
		cost:           NewCostAccumulator(lg),
		tokens:         NewTokenTracker(lg),
		db:             db,
		dbPath:         dbPath,
		writeBuf:       make([]taggedMessage, 0, 64),
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

func (s *Session) Editor() *fileio.Editor { return s.editor }

func (s *Session) Config() *Config { return s.cfg }

func (s *Session) ModelName() string {
	return s.cfg.GetActiveMainCategory().Model
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
