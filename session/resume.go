package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
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
	if err := restoreConversationState(context.Background(), db, sm, meta.ID); err != nil {
		_ = lg.Close()
		_ = db.Close()
		return nil, fmt.Errorf("resume: %w", err)
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
	activeCfg := cfg.GetActiveDefaultCategory()
	p, err := cfg.providerForCategory(activeCfg)
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
			if mm.Meta.DisplayRole == "hidden_wire_context" {
				msgs[i].WireHidden = true
			}
		}
	}

	interrupted, action := detectInterruption(msgs)
	if interrupted {
		recoveryMessage := provider.ChatMessage{
			Role:    "user",
			Content: provider.TextContent(resumeRecoveryPrefix + action),
		}
		msgs = append(msgs, recoveryMessage)
		msgTurns = append(msgTurns, maxTurn+1)
		maxTurn++
		displayMetas = append(displayMetas, canonicalizeDisplayMeta(recoveryMessage,
			sessiondb.DisplayMeta{DisplayRole: "user"}, "", len(msgs)-1, time.Now()))
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
	wireMessages := append([]provider.ChatMessage(nil), msgs...)
	wireEpoch := uint64(1)
	wireReason := "resume_without_wire_state"
	if state, ok, wireErr := sessiondb.LoadWireState(db, meta.ID); wireErr == nil && ok && !interrupted {
		wireMessages = state.Messages
		wireEpoch = state.Epoch
		wireReason = state.Reason
	} else if ok {
		wireEpoch = state.Epoch + 1
		wireReason = "resume_recovery"
	}

	sess := &Session{
		ID:                 meta.ID,
		cfg:                cfg,
		provider:           p,
		exec:               exec,
		editor:             editor,
		logger:             lg,
		permStore:          permStore,
		sm:                 sm,
		asyncMgr:           fileio.NewAsyncCommandManagerWithLogger(lg),
		sessionID:          meta.ID,
		sessionStart:       time.Now(),
		cost:               NewCostAccumulator(lg),
		tokens:             NewTokenTracker(lg),
		db:                 db,
		dbPath:             dbPath,
		turnNumber:         resumeTurnNumber,
		seqNum:             len(msgs),
		msgCounter:         len(msgs),
		msgs:               msgs,
		msgTurns:           msgTurns,
		displayMetas:       displayMetas,
		createdAts:         createdAts,
		wakeCh:             make(chan WakeEvent, WakeChBufSize),
		writeBuf:           make([]taggedMessage, 0, 64),
		consumedReports:    make(map[string]bool),
		orchestrators:      make(map[string]*Orchestrator),
		runtimeProfile:     NewRuntimeProfile(cfg),
		workspaceCfg:       workspaceCfg,
		sessionOverrides:   overrides,
		multiAgentOverride: cloneBoolPtr(flags.MultiAgent),
		wireMessages:       wireMessages,
		wireEpoch:          wireEpoch,
		wireReason:         wireReason,
	}
	rows, loadErr := sessiondb.ListSubAgentResults(db, meta.ID)
	if loadErr != nil {
		lg.Warn("SUBAGENT", "hydrate_results_failed", "error", loadErr.Error())
	}
	results := make([]agent.SubAgentResult, 0, len(rows))
	knownChildren := make(map[string]bool, len(rows))
	resultByChild := make(map[string]agent.SubAgentResult, len(rows))
	for _, row := range rows {
		result := subAgentResultFromRow(row)
		results = append(results, result)
		knownChildren[result.SessionID] = true
		resultByChild[result.SessionID] = result
	}
	for _, recovered := range recoverLegacySubAgentResults(meta.ID, lg.SessionDir(), knownChildren) {
		results = append(results, recovered)
		resultByChild[recovered.SessionID] = recovered
		if persistErr := sessiondb.InsertSubAgentResult(db, subAgentResultRow(meta.ID, recovered)); persistErr != nil {
			lg.Warn("SUBAGENT", "persist_recovered_result_failed", "agent_id", recovered.AgentID, "error", persistErr.Error())
		}
	}
	sess.recoveredSubAgentMessages = make(map[string][]sessiondb.MessageWithMeta)
	for _, review := range recoverSubAgentsFromParentTranscript(meta.ID, msgs, lg.SessionDir(), resultByChild) {
		sess.recoveredSubAgentMessages[review.result.SessionID] = review.messages
		if knownChildren[review.result.SessionID] {
			continue
		}
		knownChildren[review.result.SessionID] = true
		results = append(results, review.result)
		if persistErr := sessiondb.InsertSubAgentResult(db, subAgentResultRow(meta.ID, review.result)); persistErr != nil {
			lg.Warn("SUBAGENT", "persist_transcript_recovery_failed", "agent_id", review.result.AgentID, "error", persistErr.Error())
		}
	}
	if cfg.IsMultiAgentEnabled() || len(results) > 0 {
		sess.subAgentSup = NewSubAgentRunner(sess, cfg.Agents.MaxConcurrent)
		for _, result := range results {
			sess.subAgentSup.Manager().HydrateCompleted(result)
		}
	}
	return sess, nil
}

type recoveredSubAgentReview struct {
	result   agent.SubAgentResult
	messages []sessiondb.MessageWithMeta
}

func recoverSubAgentsFromParentTranscript(parentID string, messages []provider.ChatMessage, sessionDir string, existing map[string]agent.SubAgentResult) []recoveredSubAgentReview {
	toolResults := make(map[string]string)
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID != "" {
			toolResults[message.ToolCallID] = message.Content.String()
		}
	}
	var recovered []recoveredSubAgentReview
	seen := make(map[string]bool)
	for _, message := range messages {
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.Function.Name != "run_sub_agent" {
				continue
			}
			var params map[string]any
			if json.Unmarshal([]byte(call.Function.Arguments), &params) != nil {
				continue
			}
			name, _ := params["name"].(string)
			category, _ := params["category"].(string)
			description, _ := params["description"].(string)
			instructions, _ := params["instructions"].(string)
			toolResult := toolResults[call.ID]
			childID := childSessionIDFromResult(toolResult)
			reportPath := reportPathFromText(toolResult)
			if childID == "" || reportPath == "" {
				for _, candidate := range messages {
					text := candidate.Content.String()
					if childID != "" && !strings.Contains(text, childID) {
						continue
					}
					if childID == "" {
						childID = childSessionIDFromResult(text)
					}
					if reportPath == "" {
						reportPath = reportPathFromText(text)
					}
				}
			}
			if childID == "" && name != "" {
				matches, _ := filepath.Glob(filepath.Join(sessionDir, "reports", name+"-*-report.md"))
				if len(matches) > 0 {
					reportPath = matches[len(matches)-1]
					childID = strings.TrimSuffix(filepath.Base(reportPath), "-report.md")
				}
			}
			if childID == "" || seen[childID] {
				continue
			}
			seen[childID] = true
			result, exists := existing[childID]
			if !exists {
				result = agent.SubAgentResult{
					AgentID: agent.AgentID(childID), SessionID: childID, Name: name,
					Category: category, Status: agent.SubAgentCompleted,
					SummaryGist: summarizeText(toolResult, 240), ResultPath: reportPath,
				}
			}
			if result.Name == "" {
				result.Name = name
			}
			if result.Category == "" {
				result.Category = category
			}
			if result.ResultPath == "" {
				result.ResultPath = reportPath
			}
			spec := agent.SubAgentSpec{
				ID: agent.AgentID(childID), Name: result.Name, Category: result.Category,
				Description: description, Instructions: instructions, ParentID: agent.AgentID(parentID),
			}
			_, assignment := agent.BuildSubAgentPrompt(spec, "")
			reviewMessages := []sessiondb.MessageWithMeta{{
				Msg: provider.NewTextMessage("user", assignment), Meta: sessiondb.DisplayMeta{DisplayRole: "assignment"},
			}}
			report := readRecoveredReport(result.ResultPath)
			if report == "" {
				report = result.SummaryGist
			}
			if report != "" {
				reviewMessages = append(reviewMessages, sessiondb.MessageWithMeta{
					Msg: provider.NewTextMessage("assistant", report), Meta: sessiondb.DisplayMeta{DisplayRole: "assistant"},
				})
			}
			recovered = append(recovered, recoveredSubAgentReview{result: result, messages: reviewMessages})
		}
	}
	return recovered
}

func childSessionIDFromResult(text string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) == nil {
		for _, key := range []string{"session_id", "handle_id", "agent_id"} {
			if value, _ := payload[key].(string); value != "" {
				return value
			}
		}
	}
	return markdownField(text, "**Session:**")
}

func reportPathFromText(text string) string {
	for _, marker := range []string{"**Report:**", "Report:"} {
		if value := markdownField(text, marker); value != "" {
			return value
		}
	}
	return ""
}

func markdownField(text, marker string) string {
	index := strings.Index(text, marker)
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(text[index+len(marker):])
	if lineEnd := strings.IndexByte(value, '\n'); lineEnd >= 0 {
		value = value[:lineEnd]
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		return strings.Trim(fields[0], "`*()")
	}
	return ""
}

func readRecoveredReport(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 2<<20 {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

func recoverLegacySubAgentResults(parentSessionID, parentSessionDir string, known map[string]bool) []agent.SubAgentResult {
	var candidates []string
	if parentSessionDir != "" {
		matches, _ := filepath.Glob(filepath.Join(parentSessionDir, "subagents", "*.db"))
		candidates = append(candidates, matches...)
	}
	if legacyRoot, err := SessionsRoot(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(legacyRoot, "*.db"))
		candidates = append(candidates, matches...)
	}
	seenPaths := make(map[string]bool, len(candidates))
	var recovered []agent.SubAgentResult
	for _, path := range candidates {
		path = filepath.Clean(path)
		if seenPaths[path] {
			continue
		}
		seenPaths[path] = true
		meta, messages, err := loadSubAgentSnapshotDB(path)
		if err != nil || !meta.ParentSessionID.Valid || meta.ParentSessionID.String != parentSessionID || known[meta.ID] {
			continue
		}
		result := agent.SubAgentResult{
			AgentID: agent.AgentID(meta.ID), SessionID: meta.ID,
			Name: meta.AgentName.String, Category: meta.AgentCategory.String,
			Status: agent.SubAgentCompleted,
		}
		if !meta.ClosedAt.Valid {
			result.Status = agent.SubAgentFailedError
		}
		if meta.ClosedAt.Valid && meta.CreatedAt > 0 {
			result.DurationMs = (meta.ClosedAt.Int64 - meta.CreatedAt) / int64(time.Millisecond)
		}
		for i := len(messages) - 1; i >= 0; i-- {
			message := messages[i].Msg
			if result.SummaryGist == "" && message.Role == "assistant" && strings.TrimSpace(message.Content.String()) != "" {
				result.SummaryGist = summarizeText(message.Content.String(), 240)
			}
			if result.ResultPath == "" && message.Role == "tool" {
				var payload map[string]any
				if json.Unmarshal([]byte(message.Content.String()), &payload) == nil {
					if reportPath, _ := payload["report_path"].(string); reportPath != "" {
						result.ResultPath = reportPath
					}
				}
			}
		}
		known[meta.ID] = true
		recovered = append(recovered, result)
	}
	return recovered
}

func summarizeText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "…"
}

func subAgentResultRow(parentSessionID string, result agent.SubAgentResult) sessiondb.SubAgentResultRow {
	return sessiondb.SubAgentResultRow{
		ParentSessionID: parentSessionID, ChildSessionID: result.SessionID,
		AgentID: string(result.AgentID), AgentName: result.Name, AgentCategory: result.Category,
		Status: string(result.Status), SummaryGist: result.SummaryGist, ResultPath: result.ResultPath,
		SessionLogPath: result.LogPath, FilesRead: result.FilesRead, FilesWritten: result.FilesWritten,
		Errors: result.Errors, PromptTokens: result.PromptTokens, CompletionTokens: result.CompTokens,
		CostUSD: result.CostUSD, DurationMs: result.DurationMs, CreatedAt: time.Now().UnixNano(),
	}
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
