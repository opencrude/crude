package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"strings"

	"crypto/rand"
	"encoding/hex"

	"crude/agent"
	"crude/fileio"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/statemachine"
	"crude/tools"
)

type SubAgentRunner struct {
	session       *Session
	mgr           *agent.SubAgentManager
	fileOwnership *agent.FileOwnership
	rateLimiter   *agent.RateLimiter
	sender        EventSender
	parentToolFn  func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	wg            sync.WaitGroup
	wakeCh        chan<- WakeEvent
	sessionCtx    context.Context
	sessionCancel context.CancelFunc
}

func NewSubAgentRunner(s *Session, maxConcurrent int) *SubAgentRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &SubAgentRunner{
		session:       s,
		mgr:           agent.NewSubAgentManager(maxConcurrent),
		fileOwnership: agent.NewFileOwnership(),
		rateLimiter:   agent.NewRateLimiter(),
		wakeCh:        s.WakeCh(),
		sessionCtx:    ctx,
		sessionCancel: cancel,
	}
}

func (r *SubAgentRunner) Manager() *agent.SubAgentManager {
	return r.mgr
}

func (r *SubAgentRunner) FileOwnership() *agent.FileOwnership {
	return r.fileOwnership
}

func (r *SubAgentRunner) Wait() {
	r.wg.Wait()
}

func (r *SubAgentRunner) SessionCancel() {
	r.sessionCancel()
}

func (r *SubAgentRunner) CancelAllActive() []agent.AgentID {
	var ids []agent.AgentID
	for _, h := range r.mgr.ActiveHandles() {
		ids = append(ids, h.Spec.ID)
	}
	for _, id := range ids {
		r.mgr.Cancel(id)
		r.fileOwnership.Release(id)
	}
	r.sessionCancel()
	return ids
}

func (r *SubAgentRunner) SetSender(sender EventSender) {
	r.sender = sender
}

func (r *SubAgentRunner) SetParentToolFn(fn func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)) {
	r.parentToolFn = fn
}

func (r *SubAgentRunner) RunSubAgent(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	name := getStrParam(params, "name")
	description := getStrParam(params, "description")
	if name == "" || description == "" {
		return tools.ToolResult{}, fmt.Errorf("run_sub_agent: 'name' and 'description' are required")
	}

	category := getStrParam(params, "category")
	if category == "" {
		category = agent.CatCrafter
	}

	spec := agent.SubAgentSpec{
		ID:          agent.AgentID(name + "-" + generateShortID()),
		Name:        name,
		Category:    category,
		Description: description,
		Instructions: getStrParam(params, "instructions"),
		PolicyName:  getStrParam(params, "policy"),
		MaxSteps:    getIntParam(params, "max_steps"),
		Depth:       1,
		ParentID:    agent.AgentID("main"),
	}

	if raw, ok := params["allowed_tools"]; ok {
		spec.AllowedTools = toStringSlice(raw)
	}
	if raw, ok := params["denied_tools"]; ok {
		spec.DeniedTools = toStringSlice(raw)
	}
	if raw, ok := params["file_ownership"]; ok {
		spec.FileOwnership = toStringSlice(raw)
	}

	profileInstructions := r.resolveProfile(name, category, &spec)

	r.applyCategoryDefaults(&spec)

	parentPolicy := safety.DefaultPolicy()
	spec.Policy = agent.ComposePolicy(parentPolicy, spec.PolicyName, spec.Category)

	if len(spec.AllowedTools) > 0 {
		spec.Policy = agent.UpgradePolicyForAllowedTools(spec.Policy, spec.AllowedTools)
	}

	if err := r.mgr.Validate(spec); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("%s", err)
	}

	if len(spec.FileOwnership) > 0 {
		if err := r.fileOwnership.PreClaim(spec.ID, spec.Name, spec.FileOwnership); err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("%s", err)
		}
	}

	wait := true
	if w, ok := params["wait"]; ok {
		if b, ok := w.(bool); ok {
			wait = b
		}
	}

	handle, err := r.launchSubAgent(ctx, spec, profileInstructions)
	if err != nil {
		r.fileOwnership.Release(spec.ID)
		return tools.ToolResult{Content: fmt.Sprintf("Error launching sub-agent: %s", err)}, fmt.Errorf("launch: %w", err)
	}

	if !wait {
		go func() {
			select {
			case <-r.sessionCtx.Done():
				r.mgr.Cancel(spec.ID)
			case <-handle.ResultCh:
			}
		}()
		resultJSON, _ := json.Marshal(map[string]string{
			"handle_id":  string(spec.ID),
			"name":       spec.Name,
			"category":   spec.Category,
			"status":     "running",
			"session_id": handle.SessionID,
		})
		return tools.ToolResult{Content: string(resultJSON)}, nil
	}

	select {
	case result := <-handle.ResultCh:
		// The agent now has the final result via synchronous wait; mark consumed
		// so the pending WakeSubAgentDone event is suppressed. Currently, wait=true
		// is forced to false in multi-agent mode, but this guards against future changes.
		r.session.MarkReportConsumed(string(spec.ID))
		return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
	case <-ctx.Done():
		return tools.ToolResult{Content: fmt.Sprintf("Sub-agent %q still running (turn ended). Use poll_sub_agent with handle_id=%q to check later.", spec.Name, spec.ID)}, nil
	}
}

func formatSubAgentResult(result agent.SubAgentResult) string {
	var sb strings.Builder
	icon := "OK"
	switch result.Status {
	case agent.SubAgentCompleted:
		icon = "OK"
	case agent.SubAgentCrashed:
		icon = "CRASH"
	case agent.SubAgentFailedTimeout:
		icon = "TIMEOUT"
	case agent.SubAgentFailedQuota:
		icon = "QUOTA"
	case agent.SubAgentFailedError:
		icon = "ERROR"
	case agent.SubAgentFailedPolicy:
		icon = "POLICY"
	case agent.SubAgentCancelled:
		icon = "CANCELLED"
	default:
		icon = string(result.Status)
	}
	sb.WriteString(fmt.Sprintf("## Sub-Agent Result: %s [%s]\n", result.Name, result.Category))
	sb.WriteString(fmt.Sprintf("**Status:** %s", icon))
	if icon == "CRASH" {
		sb.WriteString(" — **the agent process crashed unexpectedly (internal error, NOT a normal failure). Do NOT retry the same sub-agent configuration without investigating the crash errors below.**")
	}
	if result.DurationMs > 0 {
		dur := time.Duration(result.DurationMs) * time.Millisecond
		sb.WriteString(fmt.Sprintf(" (%s)", dur.Round(time.Second).String()))
	}
	sb.WriteString("\n")
	if result.SessionID != "" {
		sb.WriteString(fmt.Sprintf("**Session:** %s\n", result.SessionID))
	}
	if result.ResultPath != "" {
		sb.WriteString(fmt.Sprintf("\n**Report:** %s (use read_file to read the full report)\n", result.ResultPath))
	}
	if len(result.SummaryGist) > 0 {
		sb.WriteString("\n")
		sb.WriteString(result.SummaryGist)
		sb.WriteString("\n")
	}
	if len(result.FilesRead) > 0 {
		sb.WriteString(fmt.Sprintf("\n**Files read:** %d\n", len(result.FilesRead)))
		for _, f := range result.FilesRead {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}
	if len(result.FilesWritten) > 0 {
		sb.WriteString(fmt.Sprintf("\n**Files written:** %d\n", len(result.FilesWritten)))
		for _, f := range result.FilesWritten {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}
	costInfo := ""
	if result.PromptTokens > 0 || result.CompTokens > 0 {
		costInfo += fmt.Sprintf("**Tokens:** %d prompt + %d completion", result.PromptTokens, result.CompTokens)
	}
	if result.CostUSD > 0 {
		if costInfo != "" {
			costInfo += " | "
		}
		costInfo += fmt.Sprintf("**Cost:** $%.4f", result.CostUSD)
	}
	if costInfo != "" {
		sb.WriteString("\n" + costInfo + "\n")
	}
	if len(result.Errors) > 0 {
		sb.WriteString(fmt.Sprintf("\n**Errors:** %d\n", len(result.Errors)))
		for _, e := range result.Errors {
			if len(e) > 200 {
				e = e[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("- %s\n", e))
		}
	}
	return strings.TrimSpace(sb.String())
}

func (r *SubAgentRunner) PollSubAgent(params map[string]any) (tools.ToolResult, error) {
	handleID := getStrParam(params, "handle_id")
	if handleID == "" {
		return tools.ToolResult{}, fmt.Errorf("poll_sub_agent: 'handle_id' is required")
	}

	handle, ok := r.mgr.GetActive(agent.AgentID(handleID))
	if ok && handle.ReportDone() {
		reportPath, _ := handle.ReportPath()
		result := agent.SubAgentResult{
			AgentID:      handle.Spec.ID,
			Name:         handle.Spec.Name,
			Category:     handle.Spec.Category,
			SessionID:    handle.SessionID,
			Status:       agent.SubAgentRunning,
			ResultPath:   reportPath,
			FilesRead:    r.fileOwnership.ReadClaimsByAgent(handle.Spec.ID),
			FilesWritten: r.fileOwnership.WriteClaimsByAgent(handle.Spec.ID),
			DurationMs:   time.Since(handle.StartedAt).Milliseconds(),
		}
		promptTokens, compTokens, costUSD, errs := handle.Usage()
		result.PromptTokens = promptTokens
		result.CompTokens = compTokens
		result.CostUSD = costUSD
		result.Errors = errs
		// The agent now has the result and report path; suppress the
		// pending WakeSubAgentDone to avoid a redundant wake-up turn.
		r.session.MarkReportConsumed(handleID)
		return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
	}

	if ok {
		elapsed := time.Since(handle.StartedAt).Round(time.Second)
		stepsUsed := handle.StepCount.Load()
		maxSteps := handle.Spec.MaxSteps
		if maxSteps == 0 {
			maxSteps = 1000
		}

		status := fmt.Sprintf("Sub-agent %q [%s] is still running.\nElapsed: %s | Steps: %d/%d | Model: %s",
			handle.Spec.Name, handle.Spec.Category, elapsed, stepsUsed, maxSteps, handle.Spec.Model)

		return tools.ToolResult{Content: status}, nil
	}

	if finishedHandle, found := r.mgr.GetFinished(agent.AgentID(handleID)); found {
		if completedResult, rFound := r.mgr.GetCompleted(agent.AgentID(handleID)); rFound {
			// Agent already has the completed result; suppress the pending wake.
			r.session.MarkReportConsumed(handleID)
			return tools.ToolResult{Content: formatSubAgentResult(completedResult)}, nil
		}
		result := agent.SubAgentResult{
			AgentID:      finishedHandle.Spec.ID,
			Name:         finishedHandle.Spec.Name,
			Category:     finishedHandle.Spec.Category,
			SessionID:    finishedHandle.SessionID,
			Status:       agent.SubAgentCompleted,
			DurationMs:   time.Since(finishedHandle.StartedAt).Milliseconds(),
		}
		promptTokens, compTokens, costUSD, errs := finishedHandle.Usage()
		result.PromptTokens = promptTokens
		result.CompTokens = compTokens
		result.CostUSD = costUSD
		result.Errors = errs
		if reportPath, done := finishedHandle.ReportPath(); done {
			result.ResultPath = reportPath
		}
		// Agent now has the finished result; suppress the pending wake.
		r.session.MarkReportConsumed(handleID)
		return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
	}

	if completedResult, found := r.mgr.GetCompleted(agent.AgentID(handleID)); found {
		// Agent already has the completed result; suppress the pending wake.
		r.session.MarkReportConsumed(handleID)
		return tools.ToolResult{Content: formatSubAgentResult(completedResult)}, nil
	}
	return tools.ToolResult{Content: "Sub-agent not found. It may have never started."}, nil
}

func (r *SubAgentRunner) CancelSubAgent(params map[string]any) (tools.ToolResult, error) {
	handleID := getStrParam(params, "handle_id")
	if handleID == "" {
		return tools.ToolResult{}, fmt.Errorf("cancel_sub_agent: 'handle_id' is required")
	}

	result, ok := r.mgr.Cancel(agent.AgentID(handleID))
	if !ok {
		return tools.ToolResult{Content: `{"status":"not_found","error":"Handle ID not found or already completed"}`}, nil
	}

	r.fileOwnership.Release(agent.AgentID(handleID))
	return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
}

func (r *SubAgentRunner) ListSubAgents(params map[string]any) (tools.ToolResult, error) {
	includeCompleted := false
	if raw, ok := params["include_completed"]; ok {
		if b, ok := raw.(bool); ok {
			includeCompleted = b
		}
	}

	entries := r.mgr.ListStatus(includeCompleted)
	if len(entries) == 0 {
		return tools.ToolResult{Content: "No sub-agents."}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		status := "running"
		if e.Status != "" {
			status = e.Status
		}
		sb.WriteString(fmt.Sprintf("- %s [%s] %s", e.Name, e.Category, status))
		if e.ElapsedSeconds > 0 {
			sb.WriteString(fmt.Sprintf(" (%s)", (time.Duration(e.ElapsedSeconds)*time.Second).Round(time.Second)))
		}
		if e.StepsUsed > 0 {
			sb.WriteString(fmt.Sprintf(" steps=%d", e.StepsUsed))
		}
		sb.WriteString("\n")
	}
	return tools.ToolResult{Content: strings.TrimSpace(sb.String())}, nil
}

func (r *SubAgentRunner) WriteReport(agentID agent.AgentID, params map[string]any) (tools.ToolResult, error) {
	report := getStrParam(params, "report")
	if report == "" {
		return tools.ToolResult{Content: "Error: write_report requires a non-empty 'report' parameter. You MUST write a meaningful summary of your work, findings, and results in the 'report' field. Call write_report again with the report content filled in."}, fmt.Errorf("write_report: 'report' parameter is required and cannot be empty — provide a summary of your work and retry")
	}

	sessionDir := r.session.logger.SessionDir()
	if sessionDir == "" {
		return tools.ToolResult{}, fmt.Errorf("write_report: session directory not available")
	}

	reportsDir := filepath.Join(sessionDir, "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_report: failed to create reports directory: %w", err)
	}

	reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-report.md", agentID))
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_report: failed to write report: %w", err)
	}

	if handle, ok := r.mgr.GetActive(agentID); ok {
		handle.SetReportPath(reportPath)
	}

	lines := strings.Count(report, "\n") + 1
	resultJSON, _ := json.Marshal(map[string]string{
		"status":      "report_written",
		"report_path": reportPath,
	})
	return tools.ToolResult{Content: string(resultJSON), Added: lines}, nil
}

func (r *SubAgentRunner) launchSubAgent(ctx context.Context, spec agent.SubAgentSpec, profileInstructions string) (*agent.SubAgentHandle, error) {
	sessionsRoot, _ := SessionsRoot()
	subSessionID := string(spec.ID)
	var subDB *sql.DB
	if sessionsRoot != "" {
		dbPath := filepath.Join(sessionsRoot, subSessionID+".db")
		var err error
		subDB, err = sessiondb.OpenAndMigrate(dbPath)
		if err != nil {
			r.session.logger.Warn("SUBAGENT", "db_open_error", "error", err.Error())
		} else {
			wd, _ := filepath.Abs(".")
			projectName := "unknown"
			if wd != "" {
				projectName = filepath.Base(wd)
			}
			sessiondb.Create(subDB, sessiondb.SessionMeta{
				ID:              subSessionID,
				ProjectPath:     wd,
				ProjectName:     projectName,
				Model:           spec.Model,
				Provider:        spec.Provider,
				CreatedAt:       time.Now().UnixNano(),
				ParentSessionID: toNullString(r.session.sessionID),
				AgentCategory:   toNullString(spec.Category),
				AgentName:       toNullString(spec.Name),
				Depth:           spec.Depth,
			})
		}
	}

	resolvedProvider := spec.Provider
	resolvedAPIKey := spec.APIKey
	resolvedURL := spec.ProviderURL
	resolvedModelName := spec.Model
	resolvedThinkingEffort := spec.ThinkingEffort

	if resolvedProvider == "" {
		mainCat := r.session.cfg.GetActiveMainCategory()
		resolvedProvider = mainCat.Provider
	}
	if resolvedAPIKey == "" || resolvedURL == "" {
		if prov, ok := r.session.cfg.Providers[resolvedProvider]; ok {
			if resolvedAPIKey == "" {
				resolvedAPIKey = prov.APIKey
			}
			if resolvedURL == "" {
				resolvedURL = prov.ProviderURL
			}
		} else {
			mainCat := r.session.cfg.GetActiveMainCategory()
			if resolvedAPIKey == "" {
				resolvedAPIKey = mainCat.APIKey
			}
			if resolvedURL == "" {
				resolvedURL = mainCat.ProviderURL
			}
		}
	}
	if resolvedModelName == "" {
		mainCat := r.session.cfg.GetActiveMainCategory()
		resolvedModelName = mainCat.Model
	}
	if resolvedThinkingEffort == "" {
		mainCat := r.session.cfg.GetActiveMainCategory()
		resolvedThinkingEffort = mainCat.ThinkingEffort
	}

	r.session.logger.Info("SUBAGENT", "launch", "agent_id", spec.ID, "category", spec.Category, "provider", resolvedProvider, "model", resolvedModelName)

	p := provider.FromPreset(resolvedProvider, resolvedAPIKey, resolvedURL)
	p.Model = resolvedModelName
	if resolvedThinkingEffort != "" {
		p.SetThinkingEffort(resolvedThinkingEffort)
	}
	if spec.ModelContextWindow > 0 {
		provider.SetModelContext(resolvedModelName, spec.ModelContextWindow)
	}
	if spec.ModelInputPrice > 0 {
		provider.SetModelPrice(resolvedModelName, provider.ModelPrice{
			InputPricePerToken:       spec.ModelInputPrice,
			OutputPricePerToken:      spec.ModelOutputPrice,
			InputCachedPricePerToken: spec.ModelCachePrice,
		})
	}

	allToolDefs := tools.AgentTools
	filteredTools := agent.FilterTools(allToolDefs, spec.AllowedTools, spec.DeniedTools, spec.Category, true)

	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 1000
	}

	a := agent.NewAgent(p, p.Model, r.session.logger)
	a.AgentID = spec.ID
	a.Depth = spec.Depth
	a.ParentID = spec.ParentID
	a.MaxSteps = maxSteps
	a.Policy = spec.Policy
	a.Yolo = true
	a.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
	a.FilteredTools = filteredTools

	subCtx, subCancel := context.WithCancel(r.sessionCtx)
	a.Ctx = subCtx
	r.session.logger.Info("SUBAGENT", "ctx_independence", "agent_id", spec.ID, "session_ctx_valid", r.sessionCtx.Err() == nil)

	subSM := statemachine.NewConversationStateMachine(r.session.logger)
	blockCh := make(chan string, 8)

	handle := &agent.SubAgentHandle{
		Spec:      spec,
		Agent:     a,
		Cancel:    subCancel,
		ResultCh:  make(chan agent.SubAgentResult, 1),
		BlockCh:   blockCh,
		StartedAt: time.Now(),
		SessionID: subSessionID,
		DB:        subDB,
		SM:        subSM,
	}

	a.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)

	prompt := agent.BuildSubAgentPrompt(spec, profileInstructions)

	if err := r.mgr.Register(handle); err != nil {
		r.fileOwnership.Release(spec.ID)
		if subDB != nil {
			sessiondb.CloseSession(subDB, subSessionID)
			subDB.Close()
		}
		return nil, fmt.Errorf("register: %w", err)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.runSubAgentLoop(subCtx, handle, prompt)
	}()

	return handle, nil
}

func (r *SubAgentRunner) runSubAgentLoop(ctx context.Context, handle *agent.SubAgentHandle, prompt string) {
	done := false
	crashed := false

	// Send agent identity to TUI for tab labeling
	if r.sender != nil {
		r.sender.SendAgentEventWithID("sub_agent_started", map[string]string{
			"agent_id":   string(handle.Spec.ID),
			"agent_name": handle.Spec.Name,
			"category":   handle.Spec.Category,
			"model":      handle.Spec.Model,
			"provider":   handle.Spec.Provider,
			"task":       handle.Spec.Description,
		}, string(handle.Spec.ID))
	}

	defer func() {
		r.fileOwnership.Release(handle.Spec.ID)

		if rec := recover(); rec != nil {
			crashed = true
			stack := debug.Stack()
			r.session.logger.Error("SUBAGENT", "crash", "agent_id", handle.Spec.ID, "error", fmt.Sprintf("%v", rec))
			result := agent.SubAgentResult{
				AgentID:   handle.Spec.ID,
				Name:      handle.Spec.Name,
				Category:  handle.Spec.Category,
				SessionID: handle.SessionID,
				Status:    agent.SubAgentCrashed,
				Errors:    []string{fmt.Sprintf("CRASH: %v\n\nStack trace:\n%s", rec, stack)},
			}
			handle.ResultCh <- result
			r.mgr.MarkFinished(handle.Spec.ID, result)
			r.persistResult(result)
			if handle.DB != nil {
				sessiondb.CloseSession(handle.DB, handle.SessionID)
				handle.DB.Close()
			}
			return
		}

		if !done && !crashed {
			r.session.logger.Error("SUBAGENT", "unexpected_exit", "agent_id", handle.Spec.ID)
			result := agent.SubAgentResult{
				AgentID:   handle.Spec.ID,
				Name:      handle.Spec.Name,
				Category:  handle.Spec.Category,
				SessionID: handle.SessionID,
				Status:    agent.SubAgentCrashed,
				Errors:    []string{"Agent exited unexpectedly without completing or panicking — this is an internal system bug"},
			}
			handle.ResultCh <- result
			r.mgr.MarkFinished(handle.Spec.ID, result)
			r.persistResult(result)
			if handle.DB != nil {
				sessiondb.CloseSession(handle.DB, handle.SessionID)
				handle.DB.Close()
			}
			return
		}
	}()

	agentMsgs := handle.Agent.Run(ctx, nil, prompt, func(evt agent.AgentEvent) {
		evt.AgentID = string(handle.Spec.ID)
		if r.sender != nil {
			r.sender.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
		}
		if evt.Type == "usage" {
			if data, ok := evt.Data.(map[string]any); ok {
				var promptT, compT int
				if v, ok := data["prompt"]; ok {
					switch n := v.(type) {
					case float64:
						promptT = int(n)
					case int:
						promptT = n
					}
				}
				if v, ok := data["completion"]; ok {
					switch n := v.(type) {
					case float64:
						compT = int(n)
					case int:
						compT = n
					}
				}
				if promptT > 0 || compT > 0 {
					handle.StepCount.Add(1)
					handle.AddUsage(promptT, compT)
				}
				if cost, ok := data["cost"].(float64); ok {
					handle.AddCost(cost)
				}
			}
		}
		if evt.Type == "error" {
			if msg, ok := evt.Data.(string); ok && msg != "" {
				handle.AddError(msg)
			}
		}
		if evt.Type == "done" {
			if data, ok := evt.Data.(map[string]any); ok {
				if reason, ok := data["reason"].(string); ok && reason != "completed" && reason != "cancelled" {
					handle.SetTermReason(reason)
				}
			}
		}
	}, handle.BlockCh, handle.SM, handle.SessionID, nil)

	result := r.buildResultFromMessages(handle, agentMsgs)

	if !handle.ReportDone() {
		r.session.logger.Warn("SUBAGENT", "no_report", "agent_id", handle.Spec.ID, "name", handle.Spec.Name)
		r.autoGenerateReport(handle, agentMsgs, &result)
	}

	if r.sender != nil {
		r.sender.SendAgentEventWithID("sub_agent_completed", map[string]string{
			"agent_id":    string(handle.Spec.ID),
			"agent_name":  handle.Spec.Name,
			"category":    handle.Spec.Category,
			"status":      string(result.Status),
			"result_path": result.ResultPath,
		}, string(handle.Spec.ID))
	}

	handle.ResultCh <- result
	r.mgr.MarkFinished(handle.Spec.ID, result)

	// Send wake event to main agent (non-blocking — don't block sub-agent shutdown if channel is full)
	if r.wakeCh != nil {
		select {
		case r.wakeCh <- WakeEvent{
			Type:     WakeSubAgentDone,
			Data:     result,
			SourceID: string(handle.Spec.ID),
		}:
		default:
		}
	}

	done = true
	r.persistResult(result)

	if handle.DB != nil {
		sessiondb.CloseSession(handle.DB, handle.SessionID)
		handle.DB.Close()
	}
}

func (r *SubAgentRunner) buildResultFromMessages(handle *agent.SubAgentHandle, msgs []provider.ChatMessage) agent.SubAgentResult {
	status := agent.SubAgentCompleted
	if ctx := handle.Agent.Ctx; ctx != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = agent.SubAgentFailedTimeout
		} else if ctx.Err() == context.Canceled {
			status = agent.SubAgentCancelled
		}
	}

	switch handle.TermReason() {
	case "stop_condition_met", "context_limit_reached":
		status = agent.SubAgentFailedError
	case "max_steps_reached":
		status = agent.SubAgentFailedTimeout
	}

	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "tool" && agent.IsToolError(m.Content) {
			if strings.Contains(m.Content, "429") || strings.Contains(m.Content, "quota") {
				status = agent.SubAgentFailedQuota
				break
			}
			if strings.Contains(m.Content, "401") || strings.Contains(m.Content, "403") || strings.Contains(m.Content, "invalid api key") || strings.Contains(m.Content, "authentication") {
				status = agent.SubAgentFailedQuota
				break
			}
		}
		if m.Role == "assistant" {
			if m.Content != "" && (strings.Contains(m.Content, "429") || strings.Contains(m.Content, "quota")) {
				status = agent.SubAgentFailedQuota
				break
			}
		}
	}

	gist := ""
	resultPath := ""
	logPath := handle.SessionID
	if reportPath, ok := handle.ReportPath(); ok {
		resultPath = reportPath
	}
	if handle.SessionID != "" {
		sessionsRoot, _ := SessionsRoot()
		if sessionsRoot != "" {
			logPath = filepath.Join(sessionsRoot, handle.SessionID)
		}
	}

	if gist == "" {
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if m.Role == "assistant" && len(m.ToolCalls) == 0 && m.Content != "" {
				gist = m.Content
				break
			}
		}
	}
	promptTokens, compTokens, costUSD, errs := handle.Usage()
	return agent.SubAgentResult{
		AgentID:      handle.Spec.ID,
		Name:         handle.Spec.Name,
		Category:     handle.Spec.Category,
		SessionID:    handle.SessionID,
		Status:       status,
		SummaryGist:  gist,
		ResultPath:   resultPath,
		LogPath:      logPath,
		FilesRead:    r.fileOwnership.ReadClaimsByAgent(handle.Spec.ID),
		FilesWritten: r.fileOwnership.WriteClaimsByAgent(handle.Spec.ID),
		DurationMs:   time.Since(handle.StartedAt).Milliseconds(),
		Errors:       errs,
		PromptTokens: promptTokens,
		CompTokens:   compTokens,
		CostUSD:      costUSD,
	}
}

func (r *SubAgentRunner) autoGenerateReport(handle *agent.SubAgentHandle, msgs []provider.ChatMessage, result *agent.SubAgentResult) {
	sessionDir := r.session.logger.SessionDir()
	if sessionDir == "" {
		return
	}

	reportsDir := filepath.Join(sessionDir, "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Sub-Agent Report: %s [%s]\n\n", handle.Spec.Name, handle.Spec.Category))
	sb.WriteString(fmt.Sprintf("Task: %s\n\n", handle.Spec.Description))

	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && msgs[i].Content != "" && len(msgs[i].ToolCalls) == 0 {
			sb.WriteString("## Summary\n\n")
			sb.WriteString(msgs[i].Content)
			sb.WriteString("\n")
			break
		}
	}

	filesRead := r.fileOwnership.ReadClaimsByAgent(handle.Spec.ID)
	filesWritten := r.fileOwnership.WriteClaimsByAgent(handle.Spec.ID)
	if len(filesRead) > 0 {
		sb.WriteString("\n## Files Read\n\n")
		for _, f := range filesRead {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}
	if len(filesWritten) > 0 {
		sb.WriteString("\n## Files Written\n\n")
		for _, f := range filesWritten {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	if len(result.Errors) > 0 {
		sb.WriteString("\n## Errors\n\n")
		for _, e := range result.Errors {
			if len(e) > 200 {
				e = e[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("- %s\n", e))
		}
	}

	reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-report.md", handle.Spec.ID))
	if err := os.WriteFile(reportPath, []byte(sb.String()), 0644); err != nil {
		return
	}

	handle.SetReportPath(reportPath)
	result.ResultPath = reportPath
}

func (r *SubAgentRunner) autoWriteReportFromContext(handle *agent.SubAgentHandle, spec agent.SubAgentSpec) (tools.ToolResult, error) {
	sessionDir := r.session.logger.SessionDir()
	if sessionDir == "" {
		return tools.ToolResult{}, fmt.Errorf("write_report: session directory not available")
	}

	reportsDir := filepath.Join(sessionDir, "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_report: failed to create reports directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Sub-Agent Report: %s [%s]\n\n", spec.Name, spec.Category))
	sb.WriteString(fmt.Sprintf("Task: %s\n\n", spec.Description))

	filesRead := r.fileOwnership.ReadClaimsByAgent(spec.ID)
	filesWritten := r.fileOwnership.WriteClaimsByAgent(spec.ID)
	if len(filesRead) > 0 {
		sb.WriteString("## Files Read\n\n")
		for _, f := range filesRead {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}
	if len(filesWritten) > 0 {
		sb.WriteString("\n## Files Written\n\n")
		for _, f := range filesWritten {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-report.md", spec.ID))
	if err := os.WriteFile(reportPath, []byte(sb.String()), 0644); err != nil {
		return tools.ToolResult{}, fmt.Errorf("write_report: failed to write report: %w", err)
	}

	handle.SetReportPath(reportPath)

	resultJSON, _ := json.Marshal(map[string]string{
		"status":      "report_auto_generated",
		"report_path": reportPath,
	})
	return tools.ToolResult{Content: string(resultJSON)}, nil
}

func (r *SubAgentRunner) buildProxiedToolFn(spec agent.SubAgentSpec, blockCh chan string, handle *agent.SubAgentHandle) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	parentFn := r.parentToolFn
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if !agent.PolicyAllows(spec.Policy, tool) {
			return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Tool %q is not allowed for this agent. Your policy is %q.", tool, spec.PolicyName)
		}

		isReadOnlyExec := spec.Policy.AllowedCapabilities[safety.CanExecuteReadOnly] && !spec.Policy.AllowedCapabilities[safety.CanExecute]
		if isReadOnlyExec && (tool == "execute_command" || tool == "async_execute_command") {
			command := getStrParam(params, "command")
			if command != "" {
				safetyRes := safety.CheckCommandSafety(command, r.session.logger)
				if safetyRes.Risk != safety.RiskSafe {
					return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Command %q is not safe for read-only execution (risk: %s, reason: %s). Only read-only commands like git status, git diff, git log, ls, cat, etc. are allowed.", command, safetyRes.Risk, safetyRes.Reason)
				}
			}
		}

		if tool == "question" {
			questionText := getStrParam(params, "question")
			if r.sender != nil && questionText != "" {
				r.sender.SendAgentEventWithID("sub_agent_question", map[string]string{
					"agent_id":   string(spec.ID),
					"agent_name": spec.Name,
					"category":   spec.Category,
					"question":   questionText,
					"task":       spec.Description,
				}, string(spec.ID))
			}
			return tools.ToolResult{Content: "Question forwarded to main agent. Continue with your task using available information."}, nil
		}

		if isPlanningTool(tool) {
			return r.executeSubAgentPlanningTool(handle, tool, params)
		}

		if isWriteTool(tool) {
			target := getStrParam(params, "path")
			if target == "" {
				target = getStrParam(params, "source")
			}
			if target != "" {
				if err := r.fileOwnership.CheckOrClaim(spec.ID, spec.Name, target, true); err != nil {
					return tools.ToolResult{}, err
				}
				if r.sender != nil {
					r.sender.SendAgentEventWithID("file_claim", map[string]string{
						"agent_id":   string(spec.ID),
						"agent_name": spec.Name,
						"category":   spec.Category,
						"path":       target,
					}, string(spec.ID))
				}
			}
		}

		if isAsyncCommandTool(tool) {
			return r.executeSubAgentAsyncCommand(handle, tool, params)
		}

		resolvedProvider := spec.Provider
		if resolvedProvider == "" {
			resolvedProvider = r.session.cfg.GetActiveMainCategory().Provider
		}
		if err := r.rateLimiter.Wait(ctx, resolvedProvider); err != nil {
			return tools.ToolResult{}, fmt.Errorf("Rate limit exceeded for provider %q", resolvedProvider)
		}

		if tool == "write_report" {
			report := getStrParam(params, "report")
			if report != "" {
				return r.WriteReport(spec.ID, params)
			}
			return tools.ToolResult{Content: "Error: write_report requires a non-empty 'report' parameter. You MUST write a meaningful summary of your work, findings, and results in the 'report' field. Call write_report again with the report content filled in."}, fmt.Errorf("write_report: 'report' parameter is required and cannot be empty — provide a summary of your work and retry")
		}

		if parentFn != nil {
			p := params
			if p == nil {
				p = make(map[string]any)
			}
			p["_subagent"] = true
			return parentFn(ctx, tool, p)
		}
		result := tools.ExecuteWithJSON(tool, params, r.session.exec, r.session.editor, r.session.yolo, r.session.logger, ctx)
		if agent.IsToolError(result.Content) {
			return result, fmt.Errorf("%s", result.Content)
		}
		return result, nil
	}
}

func (r *SubAgentRunner) persistResult(result agent.SubAgentResult) {
	if r.session.db == nil {
		return
	}
	sessiondb.InsertSubAgentResult(r.session.db, sessiondb.SubAgentResultRow{
		ParentSessionID:  r.session.sessionID,
		ChildSessionID:   result.SessionID,
		AgentID:          string(result.AgentID),
		AgentName:        result.Name,
		AgentCategory:    result.Category,
		Status:           string(result.Status),
		SummaryGist:      result.SummaryGist,
		ResultPath:       result.ResultPath,
		SessionLogPath:   result.LogPath,
		FilesRead:        result.FilesRead,
		FilesWritten:     result.FilesWritten,
		Errors:           result.Errors,
		PromptTokens:     result.PromptTokens,
		CompletionTokens: result.CompTokens,
		CostUSD:          result.CostUSD,
		DurationMs:       result.DurationMs,
		CreatedAt:        time.Now().UnixNano(),
	})
}

func (r *SubAgentRunner) resolveProfile(name string, category string, spec *agent.SubAgentSpec) string {
	activeProfile := r.session.cfg.Agents.ActiveProfile
	if activeProfile == "" {
		activeProfile = "default"
	}
	profiles := r.session.cfg.Agents.Profiles
	if profiles == nil {
		return ""
	}
	profile, ok := profiles[activeProfile]
	if !ok {
		return ""
	}

	var instructionsParts []string

	if catCfg, ok := profile.Categories[category]; ok {
		if spec.Model == "" && catCfg.Model != "" {
			spec.Model = catCfg.Model
		}
		if spec.Provider == "" && catCfg.Provider != "" {
			spec.Provider = catCfg.Provider
		}
		if spec.ProviderURL == "" && catCfg.ProviderURL != "" {
			spec.ProviderURL = catCfg.ProviderURL
		}
		if spec.APIKey == "" && catCfg.APIKey != "" {
			spec.APIKey = catCfg.APIKey
		}
		if spec.ThinkingEffort == "" && catCfg.ThinkingEffort != "" {
			spec.ThinkingEffort = catCfg.ThinkingEffort
		}
		if spec.PolicyName == "" && catCfg.Policy != "" {
			spec.PolicyName = catCfg.Policy
		}
		if spec.MaxSteps == 0 && catCfg.MaxSteps > 0 {
			spec.MaxSteps = catCfg.MaxSteps
		}
		if len(spec.AllowedTools) == 0 && len(catCfg.AllowedTools) > 0 {
			spec.AllowedTools = catCfg.AllowedTools
		}
		if len(spec.DeniedTools) == 0 && len(catCfg.DeniedTools) > 0 {
			spec.DeniedTools = catCfg.DeniedTools
		}
		if spec.ModelContextWindow == 0 && catCfg.ModelContextWindow > 0 {
			spec.ModelContextWindow = catCfg.ModelContextWindow
		}
		if spec.ModelInputPrice == 0 && catCfg.ModelInputPrice > 0 {
			spec.ModelInputPrice = catCfg.ModelInputPrice
		}
		if spec.ModelOutputPrice == 0 && catCfg.ModelOutputPrice > 0 {
			spec.ModelOutputPrice = catCfg.ModelOutputPrice
		}
		if spec.ModelCachePrice == 0 && catCfg.ModelCachePrice > 0 {
			spec.ModelCachePrice = catCfg.ModelCachePrice
		}
		if catCfg.Description != "" {
			instructionsParts = append(instructionsParts, catCfg.Description)
		}
	}

	if agentProfiles := r.session.cfg.Agents.AgentProfiles; agentProfiles != nil {
		if ap, ok := agentProfiles[name]; ok {
			if spec.Model == "" && ap.Model != "" {
				spec.Model = ap.Model
			}
			if spec.Provider == "" && ap.Provider != "" {
				spec.Provider = ap.Provider
			}
			if spec.Instructions == "" && ap.Instructions != "" {
				spec.Instructions = ap.Instructions
			}
			if spec.MaxSteps == 0 && ap.MaxSteps > 0 {
				spec.MaxSteps = ap.MaxSteps
			}
			if len(spec.AllowedTools) == 0 && len(ap.AllowedTools) > 0 {
				spec.AllowedTools = ap.AllowedTools
			}
			if len(spec.DeniedTools) == 0 && len(ap.DeniedTools) > 0 {
				spec.DeniedTools = ap.DeniedTools
			}
			if len(spec.FileOwnership) == 0 && len(ap.FileOwnership) > 0 {
				spec.FileOwnership = ap.FileOwnership
			}
			if ap.Instructions != "" {
				instructionsParts = append(instructionsParts, ap.Instructions)
			}
		}
	}

	if len(instructionsParts) > 0 {
		return strings.Join(instructionsParts, "\n\n")
	}
	return ""
}

func (r *SubAgentRunner) applyCategoryDefaults(spec *agent.SubAgentSpec) {
	if spec.MaxSteps == 0 {
		switch spec.Category {
		case agent.CatResearcher:
			spec.MaxSteps = 1000
		case agent.CatCrafter:
			spec.MaxSteps = 1000
		case agent.CatAeron:
			spec.MaxSteps = 1000
		}
	}
	if spec.Model == "" || spec.Provider == "" || spec.APIKey == "" || spec.ProviderURL == "" {
		mainCat := r.session.cfg.GetActiveMainCategory()
		if spec.Model == "" {
			spec.Model = mainCat.Model
		}
		if spec.Provider == "" {
			spec.Provider = mainCat.Provider
		}
		if spec.APIKey == "" {
			spec.APIKey = mainCat.APIKey
		}
		if spec.ProviderURL == "" {
			spec.ProviderURL = mainCat.ProviderURL
		}
	}
	if spec.PolicyName == "" {
		switch spec.Category {
		case agent.CatResearcher:
			spec.PolicyName = "read-only"
		case agent.CatCrafter:
			spec.PolicyName = "no-delete"
		case agent.CatAeron:
			spec.PolicyName = "no-delete"
		}
	}
}

func isWriteTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		return true
	}
	return false
}

func isPlanningTool(name string) bool {
	switch name {
	case "plan", "mark_step_complete", "mark_step_doing", "mark_step_failed",
		"report_blocker", "resolve_blocker", "list_goal_status":
		return true
	}
	return false
}

func isAsyncCommandTool(name string) bool {
	switch name {
	case "async_execute_command", "check_async_command", "kill_async_command", "read_async_output":
		return true
	}
	return false
}

func (r *SubAgentRunner) executeSubAgentPlanningTool(handle *agent.SubAgentHandle, tool string, params map[string]any) (tools.ToolResult, error) {
	sm := handle.SM
	sessionID := handle.SessionID

	switch tool {
	case "plan":
		objective := getStrParam(params, "objective")
		if objective == "" {
			objective = "multi-step plan"
		}

		rawSteps, ok := params["steps"]
		if !ok {
			return tools.ToolResult{}, fmt.Errorf("plan: missing 'steps' parameter")
		}
		stepsJSON, err := json.Marshal(rawSteps)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("plan: failed to marshal steps: %w", err)
		}
		var steps []map[string]any
		if err := json.Unmarshal(stepsJSON, &steps); err != nil {
			return tools.ToolResult{}, fmt.Errorf("plan: invalid steps format: %w", err)
		}

		state := sm.GetOrCreateState(sessionID)
		if rawGoalID, ok := state.GetFromContext("plan_goal_id"); ok {
			if goalID, ok := rawGoalID.(string); ok && goalID != "" {
				if g := sm.GetActiveGoal(sessionID); g != nil && g.ID == goalID {
					return tools.ToolResult{}, fmt.Errorf("plan: a plan already exists. Complete or abandon the current plan before creating a new one.")
				}
			}
		}
		state.AddToContext("plan_steps", nil)
		state.AddToContext("plan_step_statuses", nil)
		state.AddToContext("plan_step_labels", nil)
		state.AddToContext("plan_goal_id", nil)

		goal := sm.CreateGoal(sessionID, "plan: "+objective, objective, statemachine.PriorityNormal, false)

		stepStatuses := make(map[string]string)
		stepLabels := make(map[string]string)
		for _, step := range steps {
			id, _ := step["id"].(string)
			desc, _ := step["description"].(string)
			stepLabels[id] = desc
			stepStatuses[id] = "pending"
		}
		state.AddToContext("plan_steps", steps)
		state.AddToContext("plan_step_statuses", stepStatuses)
		state.AddToContext("plan_step_labels", stepLabels)
		state.AddToContext("plan_goal_id", goal.ID)

		status := agent.BuildStateMachineStatus(sm, sessionID)
		return tools.ToolResult{Content: status}, nil

	case "mark_step_complete":
		stepIDs := resolveStepIDs(params["step_id"])
		if len(stepIDs) == 0 {
			return tools.ToolResult{}, fmt.Errorf("mark_step_complete: missing 'step_id'")
		}
		state := sm.GetOrCreateState(sessionID)

		for _, stepID := range stepIDs {
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					if _, exists := m[stepID]; !exists {
						return tools.ToolResult{}, fmt.Errorf("mark_step_complete: step %q not found in plan", stepID)
					}
					m[stepID] = "completed"
					state.AddToContext("plan_step_statuses", m)
				}
			}
		}

		activateIDs := resolveStepIDs(params["activate_steps"])
		for _, actID := range activateIDs {
			if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := raw.(map[string]string); ok {
					if _, exists := m[actID]; !exists {
						return tools.ToolResult{}, fmt.Errorf("mark_step_complete: activate step %q not found in plan", actID)
					}
					if m[actID] == "pending" || m[actID] == "active" {
						m[actID] = "doing"
						state.AddToContext("plan_step_statuses", m)
					}
				}
			}
		}

		allDone := false
		if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
			if m, ok := raw.(map[string]string); ok && len(m) > 0 {
				allDone = true
				for _, st := range m {
					if st != "completed" && st != "failed" {
						allDone = false
						break
					}
				}
			}
		}
		if allDone {
			if rawGoalID, ok := state.GetFromContext("plan_goal_id"); ok {
				if goalID, ok := rawGoalID.(string); ok && goalID != "" {
					sm.CompleteGoalByID(sessionID, goalID, map[string]any{"status": "plan_complete"})
				}
			}
			state.AddToContext("plan_steps", nil)
			state.AddToContext("plan_step_statuses", nil)
			state.AddToContext("plan_step_labels", nil)
			state.AddToContext("plan_goal_id", nil)
		}

		status := agent.BuildStateMachineStatus(sm, sessionID)
		if status != "" {
			return tools.ToolResult{Content: status}, nil
		}
		var resultParts []string
		if len(stepIDs) == 1 {
			resultParts = append(resultParts, fmt.Sprintf("Step %q marked as complete.", stepIDs[0]))
		} else {
			resultParts = append(resultParts, fmt.Sprintf("Steps %v marked as complete.", stepIDs))
		}
		if len(activateIDs) > 0 {
			resultParts = append(resultParts, fmt.Sprintf("Step %v marked as doing.", activateIDs))
		}
		return tools.ToolResult{Content: strings.Join(resultParts, " ")}, nil

	case "mark_step_doing":
		stepIDs := resolveStepIDs(params["step_id"])
		if len(stepIDs) == 0 {
			return tools.ToolResult{}, fmt.Errorf("mark_step_doing: missing 'step_id'")
		}
		state := sm.GetOrCreateState(sessionID)

		if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
			if m, ok := raw.(map[string]string); ok {
				for _, stepID := range stepIDs {
					if _, exists := m[stepID]; !exists {
						return tools.ToolResult{}, fmt.Errorf("mark_step_doing: step %q not found in plan", stepID)
					}
					if m[stepID] == "pending" || m[stepID] == "active" {
						m[stepID] = "doing"
					}
				}
				state.AddToContext("plan_step_statuses", m)
			}
		}

		status := agent.BuildStateMachineStatus(sm, sessionID)
		if status != "" {
			return tools.ToolResult{Content: status}, nil
		}
		if len(stepIDs) == 1 {
			return tools.ToolResult{Content: fmt.Sprintf("Step %q marked as doing.", stepIDs[0])}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("Steps %v marked as doing.", stepIDs)}, nil

	case "mark_step_failed":
		stepID := getStrParam(params, "step_id")
		reason := getStrParam(params, "reason")
		if stepID == "" {
			return tools.ToolResult{}, fmt.Errorf("mark_step_failed: missing 'step_id'")
		}
		if reason == "" {
			reason = "unknown error"
		}
		state := sm.GetOrCreateState(sessionID)
		if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
			if m, ok := raw.(map[string]string); ok {
				if _, exists := m[stepID]; !exists {
					return tools.ToolResult{}, fmt.Errorf("mark_step_failed: step %q not found in plan", stepID)
				}
				m[stepID] = "failed"
				state.AddToContext("plan_step_statuses", m)
			}
		}

		allDone := false
		if raw, ok := state.GetFromContext("plan_step_statuses"); ok {
			if m, ok := raw.(map[string]string); ok && len(m) > 0 {
				allDone = true
				for _, st := range m {
					if st != "completed" && st != "failed" {
						allDone = false
						break
					}
				}
			}
		}
		if allDone {
			if rawGoalID, ok := state.GetFromContext("plan_goal_id"); ok {
				if goalID, ok := rawGoalID.(string); ok && goalID != "" {
					sm.CompleteGoalByID(sessionID, goalID, map[string]any{"status": "plan_all_steps_done"})
				}
			}
			state.AddToContext("plan_steps", nil)
			state.AddToContext("plan_step_statuses", nil)
			state.AddToContext("plan_step_labels", nil)
			state.AddToContext("plan_goal_id", nil)
		}

		status := agent.BuildStateMachineStatus(sm, sessionID)
		if status != "" {
			return tools.ToolResult{Content: status}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("Step %q marked as failed: %s", stepID, reason)}, nil

	case "report_blocker":
		blockerType := getStrParam(params, "blocker_type")
		message := getStrParam(params, "message")
		if message == "" {
			return tools.ToolResult{}, fmt.Errorf("report_blocker: missing 'message'")
		}
		var bt statemachine.BlockingConditionType
		switch blockerType {
		case "missing_info":
			bt = statemachine.BlockMissingParameter
		case "confirmation_needed":
			bt = statemachine.BlockConfirmationNeeded
		case "api_error":
			bt = statemachine.BlockExternalAPIError
		case "ambiguous":
			bt = statemachine.BlockAmbiguousInput
		case "auth_required":
			bt = statemachine.BlockAuthRequired
		default:
			bt = statemachine.BlockConfirmationNeeded
		}
		blocker := &statemachine.BlockingCondition{
			Type:    bt,
			Message: message,
		}
		sm.PauseWithBlocker(sessionID, blocker)
		state := sm.GetOrCreateState(sessionID)
		if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
			if m, ok := statuses.(map[string]string); ok {
				for id, st := range m {
					if st == "doing" || st == "active" {
						m[id] = "blocked"
					}
				}
				state.AddToContext("plan_step_statuses", m)
			}
		}
		status := agent.BuildStateMachineStatus(sm, sessionID)
		if status != "" {
			return tools.ToolResult{Content: status}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("Blocker reported: [%s] %s", blockerType, message)}, nil

	case "resolve_blocker":
		resolved := sm.TryResolveBlocker(sessionID, getStrParam(params, "message"))
		if resolved {
			state := sm.GetOrCreateState(sessionID)
			if statuses, ok := state.GetFromContext("plan_step_statuses"); ok {
				if m, ok := statuses.(map[string]string); ok {
					for id, st := range m {
						if st == "blocked" {
							m[id] = "doing"
						}
					}
					state.AddToContext("plan_step_statuses", m)
				}
			}
			status := agent.BuildStateMachineStatus(sm, sessionID)
			if status != "" {
				return tools.ToolResult{Content: status}, nil
			}
			return tools.ToolResult{Content: "Blocker resolved. Plan execution can resume."}, nil
		}
		return tools.ToolResult{Content: "No active blocker to resolve."}, nil

	case "list_goal_status":
		status := agent.BuildStateMachineStatus(sm, sessionID)
		if status == "" {
			return tools.ToolResult{Content: "No active goals or plans."}, nil
		}
		return tools.ToolResult{Content: status}, nil

	default:
		return tools.ToolResult{}, fmt.Errorf("unknown planning tool: %s", tool)
	}
}

func (r *SubAgentRunner) executeSubAgentAsyncCommand(handle *agent.SubAgentHandle, tool string, params map[string]any) (tools.ToolResult, error) {
	mgr := handle.Agent.AsyncMgr
	if mgr == nil {
		return tools.ToolResult{Content: "Error: async command system not available for this sub-agent"}, fmt.Errorf("async command system not available")
	}

	switch tool {
	case "async_execute_command":
		command := getStrParam(params, "command")
		handleName := getStrParam(params, "handle")
		if command == "" {
			return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'command'")
		}
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'handle'")
		}
		cwd := getStrParam(params, "cwd")
		timeout := tools.GetIntArgAny(params, "timeout_seconds", 0)
		info, err := mgr.Start(handleName, command, cwd, timeout)
		if err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("async_execute_command: %w", err)
		}
		return tools.ToolResult{Content: fmt.Sprintf("Started async command [%s]: PID %d, status=%s, command=%q", info.Handle, info.PID, info.Status, command)}, nil

	case "check_async_command":
		handleName := getStrParam(params, "handle")
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("check_async_command: missing 'handle'")
		}
		info, ok := mgr.Check(handleName)
		if !ok {
			return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] not found — it may have already been cleaned up or never started.", handleName)}, nil
		}
		dur := info.Duration.Truncate(time.Second).String()
		status := fmt.Sprintf("Handle: %s\nPID: %d\nStatus: %s\nExit code: %d\nDuration: %s", info.Handle, info.PID, info.Status, info.ExitCode, dur)
		if info.Error != "" {
			status += "\nError: " + info.Error
		}
		return tools.ToolResult{Content: status}, nil

	case "kill_async_command":
		handleName := getStrParam(params, "handle")
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("kill_async_command: missing 'handle'")
		}
		if err := mgr.Kill(handleName); err != nil {
			return tools.ToolResult{}, fmt.Errorf("kill_async_command: %w", err)
		}
		return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] killed.", handleName)}, nil

	case "read_async_output":
		handleName := getStrParam(params, "handle")
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("read_async_output: missing 'handle'")
		}
		sinceByte := tools.GetIntArgAny(params, "since_byte", 0)
		out, nextByte, err := mgr.ReadOutput(handleName, sinceByte)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("read_async_output: %w", err)
		}
		if out == "" {
			return tools.ToolResult{Content: fmt.Sprintf("[%s] next_byte=%d (no new output since byte %d)", handleName, nextByte, sinceByte)}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("[%s output from byte %d]\n%s\n[next_byte=%d]", handleName, sinceByte, out, nextByte)}, nil

	default:
		return tools.ToolResult{}, fmt.Errorf("unknown async command tool: %s", tool)
	}
}

func generateShortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func toStringSlice(val any) []string {
	if val == nil {
		return nil
	}
	if arr, ok := val.([]any); ok {
		result := make([]string, 0, len(arr))
		for _, v := range arr {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func getIntParam(params map[string]any, key string) int {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
