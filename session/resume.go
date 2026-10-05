package session

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"time"

	"crude/agent"
	"crude/clipboard"
	"crude/diff"
	"crude/fileio"
	"crude/jsonrepair"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/search"
	"crude/sessiondb"
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
	if err := sessiondb.InterruptRunningTurns(db, meta.ID); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("resume: interrupt running turns: %w", err)
	}
	if err := sessiondb.InterruptActiveRecoveries(db, meta.ID); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("resume: interrupt active recoveries: %w", err)
	}
	if err := sessiondb.InterruptRunningCompactions(db, meta.ID); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("resume: interrupt running compactions: %w", err)
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
	tempDir, err := prepareSessionTempDir(lg.SessionDir(), meta.ID)
	if err != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("resume: session temp directory: %w", err)
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
		_ = db.Close()
		return nil, fmt.Errorf("resume: command temp directory: %w", err)
	}

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
	if editor != nil {
		editor.SetLegacyJournalWrites(false)
	}

	workspaceCfg := copyConfig(cfg)
	overrides, err := sessiondb.LoadAgentSettingOverrides(db, meta.ID)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: load session settings: %w", err)
	}
	cfg = configWithSessionOverrides(workspaceCfg, overrides)
	flags, err := sessiondb.LoadSessionFlags(db, meta.ID)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resume: load session flags: %w", err)
	}
	if flags.MultiAgent != nil {
		applySessionMultiAgent(cfg, *flags.MultiAgent)
	}
	cfg.SelectedAgent = flags.MainAgent
	if flags.MainAgent != "" {
		pin := overrides["root/"+flags.MainAgent]
		cfg.selectedMainOverride = &pin
	}
	// Session metadata is authoritative for the main account pin. An empty pin
	// remains disconnected even if a workspace or application now has accounts.
	prof, name := activeProfileCfg(cfg)
	if prof.Categories == nil {
		prof.Categories = make(map[string]CategoryConfig)
	}
	main := prof.Categories[CategoryDefault]
	// The account ID is the connection identity. Its current template supplies
	// transport settings; the old provider column is retained only as history.
	main.Inherit = ""
	main.AccountID = meta.AccountID
	prof.Categories[CategoryDefault] = main
	cfg.Agents.Profiles[name] = prof
	if cfg.selectedMainOverride != nil {
		cfg.selectedMainOverride.AccountID = meta.AccountID
	}
	activeCfg := cfg.GetActiveDefaultCategory()
	var p *provider.Provider
	if _, accountErr := cfg.resolveAccount(meta.AccountID); accountErr != nil {
		lg.Warn("SESSION", "pinned_account_unavailable", "account", meta.AccountID, "error", accountErr.Error())
	} else {
		p, err = cfg.providerForCategory(activeCfg)
		if err != nil {
			_ = lg.Close()
			_ = db.Close()
			return nil, fmt.Errorf("resume: account template: %w", err)
		}
	}

	var displayMetas []sessiondb.DisplayMeta
	var createdAts []int64
	if len(msgsWithMeta) > 0 {
		cleanMsgs := make([]provider.ChatMessage, 0, len(msgsWithMeta))
		cleanTurns := make([]int, 0, len(msgsWithMeta))
		for i, mm := range msgsWithMeta {
			if mm.Meta.DisplayRole == "hidden_wire_context" && !mm.Meta.ContextOnly {
				continue
			}
			cleanMsgs = append(cleanMsgs, mm.Msg)
			if i < len(msgTurns) {
				cleanTurns = append(cleanTurns, msgTurns[i])
			} else {
				cleanTurns = append(cleanTurns, 0)
			}
			displayMetas = append(displayMetas, mm.Meta)
			createdAts = append(createdAts, mm.CreatedAt)
		}
		msgs, msgTurns = cleanMsgs, cleanTurns
	}

	// An unnamed session receives its personal identity once, before it is
	// exposed as a live agent. Subsequent resumes use the durable value.
	if meta.AgentName.String == "" {
		name, nameErr := allocateAgentName(homeDir)
		if nameErr != nil {
			_ = lg.Close()
			_ = db.Close()
			return nil, fmt.Errorf("allocate agent identity: %w", nameErr)
		}
		if _, err := db.Exec(`UPDATE sessions SET agent_name=? WHERE id=?`, name, meta.ID); err != nil {
			_ = lg.Close()
			_ = db.Close()
			return nil, fmt.Errorf("persist agent identity: %w", err)
		}
		meta.AgentName.String, meta.AgentName.Valid = name, true
	}
	resumeTurnNumber := maxTurn
	if meta.TurnCount > maxTurn {
		resumeTurnNumber = meta.TurnCount
	}
	activeContext := append([]provider.ChatMessage(nil), msgs...)
	contextGeneration := uint64(1)
	contextReason := "resume_full_history"
	state, ok, contextErr := sessiondb.LoadContextState(db, meta.ID, meta.ID)
	if contextErr != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("resume: load active context: %w", contextErr)
	}
	if ok {
		activeContext = state.Messages
		contextGeneration = state.Generation
		contextReason = state.Reason
	}
	sess := &Session{
		resumed:   true,
		agentName: meta.AgentName.String, nameHome: homeDir,
		ID:                 meta.ID,
		cfg:                cfg,
		provider:           p,
		accountUnpinned:    meta.AccountID == "",
		selectedMainAgent:  flags.MainAgent,
		exec:               exec,
		editor:             editor,
		logger:             lg,
		permStore:          permStore,
		asyncMgr:           asyncMgr,
		tempDir:            tempDir,
		sessionID:          meta.ID,
		sessionStart:       time.Now(),
		usageRecords:       make(map[string]sessiondb.UsageRecord),
		tokens:             NewTokenTracker(lg),
		db:                 db,
		dbPath:             dbPath,
		turnNumber:         resumeTurnNumber,
		seqNum:             len(msgs),
		msgs:               msgs,
		msgTurns:           msgTurns,
		displayMetas:       displayMetas,
		createdAts:         createdAts,
		writeBuf:           make([]taggedMessage, 0, 64),
		orchestrators:      make(map[string]*Orchestrator),
		recoverySlots:      make(map[string]*recoverySlot),
		pendingRecoveries:  make(map[string]sessiondb.RecoveryCheckpoint),
		runtimeProfile:     NewRuntimeProfile(cfg),
		workspaceCfg:       workspaceCfg,
		sessionOverrides:   overrides,
		multiAgentOverride: cloneBoolPtr(flags.MultiAgent),
		activeContext:      activeContext,
		contextGeneration:  contextGeneration,
		contextReason:      contextReason,
	}
	openRecoveries, err := sessiondb.LoadOpenRecoveryCheckpoints(db, meta.ID)
	if err != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("restore recoveries: %w", err)
	}
	for _, checkpoint := range openRecoveries {
		if checkpoint.ContextOwnerID == meta.ID {
			checkpoint.Archive = reconcileInterruptedTools(checkpoint.Archive)
			checkpoint.ActiveContext = reconcileInterruptedTools(checkpoint.ActiveContext)
			if err := sess.restoreRecoveryTranscript(&checkpoint); err != nil {
				_ = lg.Close()
				_ = db.Close()
				return nil, fmt.Errorf("restore accepted recovery transcript: %w", err)
			}
			if err := sessiondb.SaveRecoveryCheckpoint(db, meta.ID, checkpoint); err != nil {
				_ = lg.Close()
				_ = db.Close()
				return nil, fmt.Errorf("restore safe recovery context: %w", err)
			}
			sess.activeContext = append([]provider.ChatMessage(nil), checkpoint.ActiveContext...)
		}
		sess.pendingRecoveries[checkpoint.Status.AgentID] = checkpoint
	}
	if records, err := sessiondb.ListUsageRecords(db); err != nil {
		return nil, fmt.Errorf("restore request usage: %w", err)
	} else {
		for _, record := range records {
			sess.usageRecords[record.RequestID] = record
		}
	}
	turns, err := sessiondb.AgentTurnGenerations(db, meta.ID)
	if err != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("restore agent turn generations: %w", err)
	}
	sess.recoveredTurnGenerations = turns
	store, err := sess.planStore()
	if err == nil {
		err = store.RestoreActivity(context.Background())
	}
	if err != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("restore active plan: %w", err)
	}
	rows, loadErr := sessiondb.ListSubAgentResults(db, meta.ID)
	if loadErr != nil {
		lg.Warn("SUBAGENT", "hydrate_results_failed", "error", loadErr.Error())
	}
	results := make([]agent.SubAgentResult, 0, len(rows))
	for _, row := range rows {
		result := subAgentResultFromRow(row)
		if result.Status == agent.SubAgentRunning {
			if checkpoint, pending := sess.pendingRecoveries[string(result.AgentID)]; pending &&
				len(checkpoint.OwnerData) > 0 {
				continue
			}
			_ = lg.Close()
			_ = db.Close()
			return nil, fmt.Errorf("resume: running child %s has no durable recovery envelope", result.AgentID)
		}
		results = append(results, result)
	}
	hasChildRecovery := false
	for _, checkpoint := range sess.pendingRecoveries {
		if checkpoint.ContextOwnerID == "" && len(checkpoint.OwnerData) > 0 {
			hasChildRecovery = true
			break
		}
	}
	if len(results) > 0 || hasChildRecovery {
		sess.subAgentSup = NewSubAgentRunner(sess, cfg.Agents.MaxConcurrent)
		for _, result := range results {
			sess.subAgentSup.Manager().HydrateCompleted(result)
		}
	}
	keepTempDir = true
	return sess, nil
}

func subAgentResultRow(parentSessionID string, result agent.SubAgentResult) sessiondb.SubAgentResultRow {
	return sessiondb.SubAgentResultRow{
		ParentSessionID: parentSessionID, ChildSessionID: result.SessionID,
		AgentID: string(result.AgentID), AgentName: result.Name, AgentCategory: result.Category,
		Status: string(result.Status), SummaryGist: result.SummaryGist,
		ReportID: result.ReportID, ReportRevision: result.ReportRevision,
		SessionLogPath: result.LogPath, FilesRead: result.FilesRead, FilesWritten: result.FilesWritten,
		Errors: result.Errors, PromptTokens: result.PromptTokens, CompletionTokens: result.CompTokens,
		CostUSD: result.CostUSD, DurationMs: result.DurationMs, CreatedAt: time.Now().UnixNano(),
	}
}

func subAgentResultFromRow(row sessiondb.SubAgentResultRow) agent.SubAgentResult {
	return agent.SubAgentResult{
		AgentID:        agent.AgentID(row.AgentID),
		Name:           row.AgentName,
		Category:       row.AgentCategory,
		SessionID:      row.ChildSessionID,
		Status:         agent.SubAgentStatus(row.Status),
		SummaryGist:    row.SummaryGist,
		ReportID:       row.ReportID,
		ReportRevision: row.ReportRevision,
		LogPath:        row.SessionLogPath,
		FilesRead:      append([]string(nil), row.FilesRead...),
		FilesWritten:   append([]string(nil), row.FilesWritten...),
		Errors:         append([]string(nil), row.Errors...),
		PromptTokens:   row.PromptTokens,
		CompTokens:     row.CompletionTokens,
		CostUSD:        row.CostUSD,
		DurationMs:     row.DurationMs,
	}
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
