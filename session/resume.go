package session

import (
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"crude/agent"
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

// ResumeSession loads a backend session from a database file in the
// global sessions directory. New code should use ResumeSessionWithDir.
func ResumeSession(dbPath string, cfg *Config, homeDir string) (*Session, error) {
	sessionsRoot, _ := SessionsRoot()
	return ResumeSessionWithDir(dbPath, cfg, homeDir, sessionsRoot)
}

// ResumeSessionWithDir loads a backend session from a database file and
// places all runtime artifacts under the provided sessionDir. Used by
// workspace/ to keep resumed sessions inside the workspace folder.
func ResumeSessionWithDir(dbPath string, cfg *Config, homeDir, sessionDir string) (*Session, error) {
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

	fileio.SetSessionsRootFn(func() (string, error) { return sessionDir, nil })

	lg, err := logger.New(logger.Options{
		HomeDir:        homeDir,
		TurnLogEnabled: cfg.TurnLogsEnabled,
		SessionID:      meta.ID,
		SessionsDir:    sessionDir,
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
	if sessionDir != "" {
		if ed, edErr := fileio.NewEditorWithSessionDir(meta.ID, sessionDir, lg); edErr != nil {
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

	workspaceCfg := copyConfig(cfg)
	overrides, err := sessiondb.LoadAgentSettingOverrides(db, meta.ID)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: load session settings: %w", err)
	}
	cfg = configWithSessionOverrides(workspaceCfg, overrides)
	activeCfg := cfg.GetActiveDefaultCategory()
	p, err := providerForCategory(activeCfg)
	if err != nil {
		_ = lg.Close()
		db.Close()
		return nil, fmt.Errorf("resume: provider standard: %w", err)
	}

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
			Content: provider.TextContent(resumeRecoveryPrefix + action),
		})
		msgTurns = append(msgTurns, maxTurn+1)
		maxTurn++
		displayMetas = append(displayMetas, sessiondb.DisplayMeta{DisplayRole: "user"})
		if _, err := sessiondb.ReplaceMessagesWithMeta(db, meta.ID, msgs, msgTurns, displayMetas); err != nil {
			_ = lg.Close()
			_ = db.Close()
			return nil, fmt.Errorf("resume: persist recovery message: %w", err)
		}
	}

	resumeTurnNumber := maxTurn
	if meta.TurnCount > maxTurn {
		resumeTurnNumber = meta.TurnCount
	}

	sess := &Session{
		ID:               meta.ID,
		cfg:              cfg,
		provider:         p,
		exec:             exec,
		editor:           editor,
		logger:           lg,
		permStore:        permStore,
		sm:               sm,
		asyncMgr:         fileio.NewAsyncCommandManagerWithLogger(lg),
		sessionID:        meta.ID,
		sessionStart:     time.Now(),
		cost:             NewCostAccumulator(lg),
		tokens:           NewTokenTracker(lg),
		db:               db,
		dbPath:           dbPath,
		turnNumber:       resumeTurnNumber,
		seqNum:           len(msgs),
		msgCounter:       len(msgs),
		msgs:             msgs,
		msgTurns:         msgTurns,
		displayMetas:     displayMetas,
		createdAts:       createdAts,
		wakeCh:           make(chan WakeEvent, WakeChBufSize),
		writeBuf:         make([]taggedMessage, 0, 64),
		consumedReports:  make(map[string]bool),
		orchestrators:    make(map[string]*Orchestrator),
		runtimeProfile:   NewRuntimeProfile(cfg),
		workspaceCfg:     workspaceCfg,
		sessionOverrides: overrides,
	}
	if cfg.IsMultiAgentEnabled() {
		sess.subAgentSup = NewSubAgentRunner(sess, cfg.Agents.MaxConcurrent)
		if rows, loadErr := sessiondb.ListSubAgentResults(db, meta.ID); loadErr != nil {
			lg.Warn("SUBAGENT", "hydrate_results_failed", "error", loadErr.Error())
		} else {
			for _, row := range rows {
				sess.subAgentSup.Manager().HydrateCompleted(subAgentResultFromRow(row))
			}
		}
	}
	return sess, nil
}

func subAgentResultFromRow(row sessiondb.SubAgentResultRow) agent.SubAgentResult {
	return agent.SubAgentResult{
		AgentID:      agent.AgentID(row.AgentID),
		Name:         row.AgentName,
		Category:     row.AgentCategory,
		SessionID:    row.ChildSessionID,
		Status:       agent.SubAgentStatus(row.Status),
		SummaryGist:  row.SummaryGist,
		ResultPath:   row.ResultPath,
		LogPath:      row.SessionLogPath,
		FilesRead:    append([]string(nil), row.FilesRead...),
		FilesWritten: append([]string(nil), row.FilesWritten...),
		Errors:       append([]string(nil), row.Errors...),
		PromptTokens: row.PromptTokens,
		CompTokens:   row.CompletionTokens,
		CostUSD:      row.CostUSD,
		DurationMs:   row.DurationMs,
	}
}

func detectInterruption(msgs []provider.ChatMessage) (bool, string) {
	if len(msgs) == 0 {
		return false, ""
	}
	last := msgs[len(msgs)-1]
	if last.Role == "user" && strings.HasPrefix(last.ContentString(), resumeRecoveryPrefix) {
		return false, ""
	}
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

const resumeRecoveryPrefix = "[Resume recovery]\n"

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
