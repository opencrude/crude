package session

import (
	"fmt"
	"os"
	"os/user"
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

func ResumeSession(dbPath string, cfg *Config, homeDir string) (*Session, error) {
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resume: open db: %w", err)
	}

	meta, err := sessiondb.GetMeta(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: get meta: %w", err)
	}

	msgs, msgTurns, err := sessiondb.LoadMessages(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: load messages: %w", err)
	}

	msgsWithMeta, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: load messages with meta: %w", err)
	}

	maxTurn := 0
	for _, t := range msgTurns {
		if t > maxTurn {
			maxTurn = t
		}
	}

	sessionsRoot, _ := SessionsRoot()
	fileio.SetSessionsRootFn(SessionsRoot)

	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
		SessionID:      meta.ID,
		SessionsDir:    sessionsRoot,
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: logger: %w", err)
	}
	exec := fileio.NewLocalExecWithLogger(lg)

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
		if ed, edErr := fileio.NewEditorWithSessionDir(meta.ID, sessionsRoot, lg); edErr != nil {
			lg.Warn("SESSION", "editor_init_error", "error", fmt.Sprintf("%v", edErr))
		} else {
			editor = ed
		}
	}
	if editor == nil {
		if ed, edErr := fileio.NewEditorWithLogger(meta.ID, lg); edErr != nil {
			lg.Warn("SESSION", "editor_init_error", "error", fmt.Sprintf("%v", edErr))
		} else {
			editor = ed
		}
	}

	sm := statemachine.NewConversationStateMachine(lg)

	activeCfg := cfg.GetActiveDefaultCategory()
	p := provider.FromPreset(activeCfg.Provider, activeCfg.APIKey, activeCfg.ProviderURL)
	p.Model = activeCfg.Model
	p.SetThinkingEffort(activeCfg.ThinkingEffort)

	var displayMetas []sessiondb.DisplayMeta
	var createdAts []int64
	if len(msgsWithMeta) > 0 {
		displayMetas = make([]sessiondb.DisplayMeta, len(msgsWithMeta))
		createdAts = make([]int64, len(msgsWithMeta))
		for i, mm := range msgsWithMeta {
			displayMetas[i] = mm.Meta
			createdAts[i] = mm.CreatedAt
		}
	}

	interrupted, action := detectInterruption(msgs)
	if interrupted {
		msgs = append(msgs, provider.ChatMessage{
			Role:    "user",
			Content: provider.TextContent(action),
		})
		msgTurns = append(msgTurns, maxTurn+1)
		maxTurn++
		displayMetas = append(displayMetas, sessiondb.DisplayMeta{DisplayRole: "user"})
	}

	resumeTurnNumber := maxTurn
	if meta.TurnCount > maxTurn {
		resumeTurnNumber = meta.TurnCount
	}

	sess := &Session{
		cfg:          cfg,
		provider:     p,
		exec:         exec,
		editor:       editor,
		logger:       lg,
		permStore:    permStore,
		sm:           sm,
		asyncMgr:     fileio.NewAsyncCommandManagerWithLogger(lg),
		sessionID:    meta.ID,
		sessionStart: time.Now(),
		cost:         NewCostAccumulator(lg),
		tokens:       NewTokenTracker(lg),
		db:           db,
		dbPath:       dbPath,
		turnNumber:   resumeTurnNumber,
		seqNum:       len(msgs),
		msgCounter:   len(msgs),
		msgs:         msgs,
		msgTurns:     msgTurns,
		displayMetas: displayMetas,
		createdAts:   createdAts,
		wakeCh:           make(chan WakeEvent, WakeChBufSize),
		writeBuf:         make([]taggedMessage, 0, 64),
		consumedReports:  make(map[string]bool),
		orchestrators:    make(map[string]*Orchestrator),
	}
	if cfg.IsMultiAgentEnabled() {
		sess.subAgentSup = NewSubAgentRunner(sess, cfg.Agents.MaxConcurrent)
	}
	return sess, nil
}

func detectInterruption(msgs []provider.ChatMessage) (bool, string) {
	if len(msgs) == 0 {
		return false, ""
	}
	last := msgs[len(msgs)-1]
	switch last.Role {
	case "assistant":
		if len(last.ToolCalls) > 0 {
			return true, "Continue from where you left off. The previous tool calls may or may not have executed."
		}
		return false, ""
	case "tool":
		return true, "Continue — the tool result above is the last thing that happened."
	case "user":
		return true, "You were about to respond to the message above. Please continue."
	}
	return false, ""
}

func ResolveUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	if name := os.Getenv("LOGNAME"); name != "" {
		return name
	}
	return "user"
}
