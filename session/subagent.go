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
	"sync/atomic"
	"time"

	"slices"
	"strings"

	"crypto/rand"
	"encoding/hex"

	"crude/agent"
	"crude/codexio"
	"crude/fileio"
	"crude/plan"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
)

type SubAgentRunner struct {
	session        *Session
	orchestratorID string
	effectiveCfg   *Config // per-orchestrator effective config (nil = use base)
	mgr            *agent.SubAgentManager
	fileOwnership  *agent.FileOwnership
	rateLimiter    *agent.RateLimiter
	sender         EventSender
	parentToolFn   func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	wg             sync.WaitGroup
	wakeCh         chan<- WakeEvent
	sessionCtx     context.Context
	sessionCancel  context.CancelFunc
	parentTurn     atomic.Uint64
	running        atomic.Int32
	turnMu         sync.Mutex
	turnCounts     map[uint64]int
	turnDone       map[uint64]chan struct{}
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
		turnCounts:    make(map[uint64]int),
		turnDone:      make(map[uint64]chan struct{}),
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

func (r *SubAgentRunner) Running() bool { return r != nil && r.running.Load() > 0 }

func (r *SubAgentRunner) TurnRunning(generation uint64) bool {
	if r == nil || generation == 0 {
		return false
	}
	r.turnMu.Lock()
	running := r.turnCounts[generation] > 0
	r.turnMu.Unlock()
	return running
}

func (r *SubAgentRunner) WaitForTurn(generation uint64) {
	if r == nil || generation == 0 {
		return
	}
	r.turnMu.Lock()
	if r.turnCounts[generation] == 0 {
		r.turnMu.Unlock()
		return
	}
	done := r.turnDone[generation]
	r.turnMu.Unlock()
	<-done
}

func (r *SubAgentRunner) beginTurnChild(generation uint64) {
	r.running.Add(1)
	r.turnMu.Lock()
	if r.turnCounts[generation] == 0 {
		r.turnDone[generation] = make(chan struct{})
	}
	r.turnCounts[generation]++
	r.turnMu.Unlock()
}

func (r *SubAgentRunner) finishTurnChild(generation uint64) {
	r.turnMu.Lock()
	r.turnCounts[generation]--
	if r.turnCounts[generation] == 0 {
		close(r.turnDone[generation])
		delete(r.turnCounts, generation)
		delete(r.turnDone, generation)
	}
	r.turnMu.Unlock()
	r.running.Add(-1)
}

func (r *SubAgentRunner) SessionCancel() {
	r.sessionCancel()
}

func (r *SubAgentRunner) CancelAllActive() []agent.AgentID {
	return r.cancelActive(func(*agent.SubAgentHandle) bool { return true })
}

func (r *SubAgentRunner) CancelActiveForTurn(generation uint64) []agent.AgentID {
	if generation == 0 {
		return nil
	}
	return r.cancelActive(func(handle *agent.SubAgentHandle) bool {
		return handle.Spec.ParentTurn == generation
	})
}

func (r *SubAgentRunner) cancelActive(matches func(*agent.SubAgentHandle) bool) []agent.AgentID {
	var ids []agent.AgentID
	for _, h := range r.mgr.ActiveHandles() {
		if matches(h) {
			ids = append(ids, h.Spec.ID)
		}
	}
	for _, id := range ids {
		r.mgr.Cancel(id)
		r.fileOwnership.Release(id)
	}
	return ids
}

func (r *SubAgentRunner) SetParentTurnGeneration(generation uint64) {
	if r != nil {
		r.parentTurn.Store(generation)
	}
}

// ResolveBlock routes a request-correlated answer to one live child. A child
// has its own Agent.Run loop and therefore must never consume the parent
// orchestrator's blocking-response channel.
func (r *SubAgentRunner) ResolveBlock(id agent.AgentID, response ttypes.BlockResponse) bool {
	if r == nil || response.RequestID == "" {
		return false
	}
	handle, ok := r.mgr.GetActive(id)
	if !ok || handle == nil || handle.BlockCh == nil {
		return false
	}
	select {
	case handle.BlockCh <- response:
		return true
	default:
		return false
	}
}

func (r *SubAgentRunner) SetSender(sender EventSender) {
	r.sender = sender
}

func (r *SubAgentRunner) SetOrchestratorID(id string) {
	r.orchestratorID = id
	// Resolve the effective config for this orchestrator.
	if id != "" && r.session != nil && r.session.runtimeProfile != nil {
		r.effectiveCfg = r.session.runtimeProfile.GetEffectiveConfig(id)
	}
}

func (r *SubAgentRunner) SetEffectiveConfig(cfg *Config) {
	r.effectiveCfg = cfg
}

func (r *SubAgentRunner) SetParentToolFn(fn func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)) {
	r.parentToolFn = fn
}

func (r *SubAgentRunner) RunSubAgent(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	// Serialize launch admission, not the child's work or wait=true. This also
	// makes concurrent first launches share one parent/configuration snapshot.
	r.session.delegationMu.Lock()
	locked := true
	defer func() {
		if locked {
			r.session.delegationMu.Unlock()
		}
	}()
	if owner, ok := ctx.Value(delegationOwnerKey{}).(delegationOwner); ok {
		r.SetParentTurnGeneration(owner.generation)
		r.effectiveCfg = r.session.runtimeProfile.GetEffectiveConfig(owner.id)
		if !r.effectiveCfg.IsMultiAgentEnabled() {
			return delegationDisabledResult(), nil
		}
		// Tools retain the launching turn's media/history context, not another
		// turn's mutable proxy. Child policy is still enforced by its own wrapper.
		cat := r.effectiveCfg.GetActiveDefaultCategory()
		parentProvider, err := r.effectiveCfg.providerForCategory(cat)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("sub-agent parent provider: %w", err)
		}
		r.parentToolFn = r.session.buildToolFn(false, parentProvider, cat.ModelModalities)
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "Turn cancelled before sub-agent launch."}, err
	}
	description := getStrParam(params, "description")
	if description == "" {
		return tools.ToolResult{}, fmt.Errorf("run_sub_agent: 'description' is required")
	}

	category := getStrParam(params, "category")
	if category == "" {
		category = agent.CatCrafter
	}
	name := getStrParam(params, "name")
	if name == "" {
		name = deriveSubAgentName(category, description)
	}

	spec := agent.SubAgentSpec{
		ID:           agent.AgentID("agent-" + generateShortID()),
		Name:         name,
		Category:     category,
		Description:  description,
		Instructions: getStrParam(params, "instructions"),
		PolicyName:   getStrParam(params, "policy"),
		Depth:        1,
		ParentID:     agent.AgentID(r.orchestratorID),
		ParentTurn:   r.parentTurn.Load(),
	}
	if history, ok := historyScopeFromContext(ctx); ok {
		spec.HistoryMessageID = history.messageID
	}

	if raw, ok := params["allowed_tools"]; ok {
		spec.AllowedTools = toStringSlice(raw)
	}
	if raw, ok := params["denied_tools"]; ok {
		spec.DeniedTools = toStringSlice(raw)
	}
	if raw, ok := params["file_ownership"]; ok {
		spec.FileOwnership = toStringSlice(raw)
		for index, path := range spec.FileOwnership {
			if path != "" && !filepath.IsAbs(path) {
				spec.FileOwnership[index] = filepath.Join(r.session.WorkDir(), path)
			}
		}
	}

	profileInstructions := r.resolveProfile(name, category, &spec)
	activeConfig := r.effectiveCfg
	if activeConfig == nil {
		r.session.mu.Lock()
		activeConfig = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}
	if categoryCfg := activeConfig.GetCategoryConfig(spec.Category); categoryCfg.Enabled != nil && !*categoryCfg.Enabled {
		return tools.ToolResult{Content: "Agent role is disabled in settings."}, fmt.Errorf("%s role is disabled", spec.Category)
	}
	if profile, exists := activeConfig.Agents.AgentProfiles[name]; exists && profile.Enabled != nil && !*profile.Enabled {
		return tools.ToolResult{Content: "Agent is disabled in settings."}, fmt.Errorf("%s agent is disabled", name)
	}
	identity, err := allocateAgentName(r.session.nameHome)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("allocate child name: %w", err)
	}
	spec.Name = identity + " sub " + r.session.AgentName()
	if callID, _ := ctx.Value(agent.SubAgentToolCallIDKey{}).(string); callID != "" {
		spec.ParentToolCallID = callID
	}

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

	r.session.delegationMu.Unlock()
	locked = false

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
			"agent_id":   string(spec.ID),
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
		// so the pending WakeSubAgentDone event is suppressed.
		r.session.MarkReportConsumed(string(spec.ID))
		if result.Status != agent.SubAgentCompleted && result.Status != agent.SubAgentCancelled {
			return tools.ToolResult{Content: formatSubAgentResult(result)}, fmt.Errorf("sub-agent %s: %s", spec.Name, result.Status)
		}
		return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
	case <-ctx.Done():
		return tools.ToolResult{Content: fmt.Sprintf("Sub-agent %q still running (turn ended). Agent ID: %s. Use poll_sub_agent with handle_id=%q to check later.", spec.Name, spec.ID, spec.ID)}, nil
	}
}

func deriveSubAgentName(category, description string) string {
	var normalized strings.Builder
	separator := false
	for _, r := range strings.ToLower(description) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
			separator = false
		} else if !separator {
			normalized.WriteByte(' ')
			separator = true
		}
	}
	words := strings.Fields(normalized.String())
	if len(words) > 3 {
		words = words[:3]
	}
	name := strings.Trim(strings.Join(words, "-"), "-")
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-")
	}
	if name == "" {
		name = category + "-agent"
	}
	return name
}

func formatSubAgentResult(result agent.SubAgentResult) string {
	var sb strings.Builder
	var icon string
	switch result.Status {
	case agent.SubAgentCompleted:
		icon = "OK"
	case agent.SubAgentCrashed:
		icon = "CRASH"
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
	fmt.Fprintf(&sb, "## Sub-Agent Result: %s [%s]\n", result.Name, result.Category)
	fmt.Fprintf(&sb, "**Agent:** %s\n", result.AgentID)
	fmt.Fprintf(&sb, "**Status:** %s", icon)
	switch icon {
	case "CRASH":
		sb.WriteString(" — **the agent process crashed unexpectedly (internal error, NOT a normal failure). Do NOT retry the same sub-agent configuration without investigating the crash errors below.**")
	case "ERROR":
		if len(result.Errors) > 0 {
			err := result.Errors[0]
			if strings.Contains(err, "400") || strings.Contains(err, "messages must not be empty") {
				sb.WriteString(" — **the provider rejected the request. Check that the sub-agent prompt includes a user message (not just a system message), or the model may not be available.**")
			} else if strings.Contains(err, "429") {
				sb.WriteString(" — **rate limited. Wait before retrying.**")
			} else if strings.Contains(err, "401") || strings.Contains(err, "403") || strings.Contains(err, "invalid api key") {
				sb.WriteString(" — **authentication failed. Check your API key.**")
			} else if strings.Contains(err, "5") && (strings.Contains(err, "5") || strings.Contains(err, "50")) {
				sb.WriteString(" — **the API server returned an error. This may be transient; retry might work.**")
			} else {
				fmt.Fprintf(&sb, " — **the agent failed with an error: %s**", truncateString(err, 80))
			}
		}
	case "QUOTA":
		sb.WriteString(" — **rate limited or quota exceeded. Wait before retrying.**")
	case "CANCELLED":
		sb.WriteString(" — **the agent did not complete. Treat any output as partial and do not report this as a successful result.**")
	}
	if result.DurationMs > 0 {
		dur := time.Duration(result.DurationMs) * time.Millisecond
		fmt.Fprintf(&sb, " (%s)", dur.Round(time.Second).String())
	}
	sb.WriteString("\n")
	if result.SessionID != "" {
		fmt.Fprintf(&sb, "**Session:** %s\n", result.SessionID)
	}
	if result.ResultPath != "" {
		fmt.Fprintf(&sb, "\n**Report:** %s (use read_file to read the full report)\n", result.ResultPath)
	}
	if len(result.SummaryGist) > 0 {
		sb.WriteString("\n")
		sb.WriteString(result.SummaryGist)
		sb.WriteString("\n")
	}
	if len(result.FilesRead) > 0 {
		fmt.Fprintf(&sb, "\n**Files read:** %d\n", len(result.FilesRead))
		for _, f := range result.FilesRead {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	if len(result.FilesWritten) > 0 {
		fmt.Fprintf(&sb, "\n**Files written:** %d\n", len(result.FilesWritten))
		for _, f := range result.FilesWritten {
			fmt.Fprintf(&sb, "- %s\n", f)
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
		fmt.Fprintf(&sb, "\n**Errors:** %d\n", len(result.Errors))
		for _, e := range result.Errors {
			if len(e) > 200 {
				e = e[:200] + "..."
			}
			fmt.Fprintf(&sb, "- %s\n", e)
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
		// An intermediate report is not the terminal result. Keep its later
		// completion notification: the child may still fail or be cancelled.
		return tools.ToolResult{Content: formatSubAgentResult(result)}, nil
	}

	if ok {
		elapsed := time.Since(handle.StartedAt).Round(time.Second)
		stepsUsed := handle.StepCount.Load()
		status := fmt.Sprintf("Sub-agent %q [%s] is still running.\nElapsed: %s | Steps: %d | Model: %s",
			handle.Spec.Name, handle.Spec.Category, elapsed, stepsUsed, handle.Spec.Model)

		return tools.ToolResult{Content: status}, nil
	}

	if finishedHandle, found := r.mgr.GetFinished(agent.AgentID(handleID)); found {
		if completedResult, rFound := r.mgr.GetCompleted(agent.AgentID(handleID)); rFound {
			// Agent already has the completed result; suppress the pending wake.
			r.session.MarkReportConsumed(handleID)
			return tools.ToolResult{Content: formatSubAgentResult(completedResult)}, nil
		}
		status := agent.SubAgentFailedError
		if finishedHandle.Agent != nil && finishedHandle.Agent.Ctx != nil &&
			finishedHandle.Agent.Ctx.Err() == context.Canceled {
			status = agent.SubAgentCancelled
		}
		result := agent.SubAgentResult{
			AgentID:    finishedHandle.Spec.ID,
			Name:       finishedHandle.Spec.Name,
			Category:   finishedHandle.Spec.Category,
			SessionID:  finishedHandle.SessionID,
			Status:     status,
			DurationMs: time.Since(finishedHandle.StartedAt).Milliseconds(),
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
	r.session.MarkReportConsumed(handleID)
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
	slices.SortFunc(entries, func(a, b agent.SubAgentStatusEntry) int {
		return strings.Compare(a.HandleID, b.HandleID)
	})

	var sb strings.Builder
	for _, e := range entries {
		status := "running"
		if e.Status != "" {
			status = e.Status
		}
		fmt.Fprintf(&sb, "- %s (%s) [%s] %s", e.Name, e.HandleID, e.Category, status)
		if e.ElapsedSeconds > 0 {
			fmt.Fprintf(&sb, " (%s)", (time.Duration(e.ElapsedSeconds) * time.Second).Round(time.Second))
		}
		if e.StepsUsed > 0 {
			fmt.Fprintf(&sb, " steps=%d", e.StepsUsed)
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

	name := ""
	if handle, ok := r.mgr.GetActive(agentID); ok {
		handle.SetReportPath(reportPath)
		name = handle.Spec.Name
	}

	lines := strings.Count(report, "\n") + 1
	resultJSON, _ := json.Marshal(map[string]string{
		"status":      "report_written",
		"report_path": reportPath,
		"handle_id":   string(agentID),
		"name":        name,
	})
	return tools.ToolResult{Content: string(resultJSON), Added: lines}, nil
}

func (r *SubAgentRunner) launchSubAgent(ctx context.Context, spec agent.SubAgentSpec, profileInstructions string) (*agent.SubAgentHandle, error) {
	subSessionID := string(spec.ID)
	resolvedModelName := spec.Model
	resolvedThinkingEffort := spec.ThinkingEffort

	cfg := r.effectiveCfg
	if cfg == nil {
		r.session.mu.Lock()
		cfg = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}

	categoryCfg := cfg.GetCategoryConfig(spec.Category)
	if resolvedModelName == "" {
		resolvedModelName = categoryCfg.Model
	}
	if resolvedThinkingEffort == "" {
		resolvedThinkingEffort = categoryCfg.ThinkingEffort
	}
	if spec.AccountID == "" {
		spec.AccountID = categoryCfg.AccountID
	}
	spec.Model = resolvedModelName
	spec.ThinkingEffort = resolvedThinkingEffort
	turnCfg := CategoryConfig{
		AccountID: spec.AccountID, Model: spec.Model, ThinkingEffort: spec.ThinkingEffort,
		ModelTemperature: spec.ModelTemperature, ModelTopP: spec.ModelTopP,
	}
	p, err := cfg.providerForCategory(turnCfg)
	if err != nil {
		return nil, fmt.Errorf("sub-agent account %q: %w", spec.AccountID, err)
	}
	// Provider identity is derived for telemetry and persistence only; account
	// ID above is the only connection selector.
	spec.Provider = p.ID
	spec.ProviderURL = p.BaseURL
	spec.APIStandard = p.StandardID()

	var subDB *sql.DB
	if dbPath, pathErr := r.subAgentDBPath(subSessionID); pathErr != nil {
		r.session.logger.Warn("SUBAGENT", "db_path_error", "error", pathErr.Error())
	} else if dbPath != "" {
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
			_ = sessiondb.Create(subDB, sessiondb.SessionMeta{
				ID:              subSessionID,
				ProjectPath:     wd,
				ProjectName:     projectName,
				Model:           spec.Model,
				Provider:        spec.Provider,
				AccountID:       spec.AccountID,
				CreatedAt:       time.Now().UnixNano(),
				ParentSessionID: toNullString(r.session.sessionID),
				AgentCategory:   toNullString(spec.Category),
				AgentName:       toNullString(spec.Name),
				Depth:           spec.Depth,
			})
		}
	}

	r.session.logger.Info("SUBAGENT", "launch", "agent_id", spec.ID, "category", spec.Category, "template", p.ID, "account", spec.AccountID, "model", resolvedModelName)
	if spec.ModelContextWindow > 0 {
		p.SetContextWindow(spec.ModelContextWindow)
	}
	if spec.ModelInputPrice > 0 {
		provider.SetModelPrice(resolvedModelName, provider.ModelPrice{
			InputPricePerToken:       spec.ModelInputPrice,
			OutputPricePerToken:      spec.ModelOutputPrice,
			InputCachedPricePerToken: spec.ModelCachePrice,
		})
	}

	allToolDefs := tools.AgentTools
	filteredTools := agent.FilterToolsForModel(allToolDefs, spec.AllowedTools, spec.DeniedTools, spec.Category, true, p.Model)

	a := agent.NewAgent(p, p.Model, r.session.logger)
	a.MaxOutputTokens = spec.ModelMaxOutput
	_, agentProfile := activeProfileCfg(cfg)
	a.CompactionAgentProfile = agentProfile + "/" + CategoryAeron
	if compactionCfg := cfg.GetCategoryConfig(CategoryAeron); compactionCfg.Model != "" {
		if compactionProvider, compactionErr := cfg.providerForCategory(compactionCfg); compactionErr == nil {
			a.CompactionProvider = compactionProvider
			a.CompactionMaxOutput = compactionCfg.ModelMaxOutput
		} else {
			r.session.logger.Warn("COMPACT", "aeron_provider_unavailable", "agent_id", spec.ID, "error", compactionErr.Error())
		}
	}
	if cfg != nil {
		a.CommandPolicy = safety.CommandPolicyFromSettings(cfg.Safety)
	}
	if spec.ContextCompactionPercent != nil {
		a.ContextCompactionPercent = *spec.ContextCompactionPercent
	}
	a.AgentID = spec.ID
	a.TurnGeneration = 1
	a.Depth = spec.Depth
	a.ParentID = spec.ParentID
	a.Policy = spec.Policy
	a.Yolo = true
	a.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
	a.FilteredTools = tools.ForModel(filteredTools, p.Model)

	baseCtx := r.sessionCtx
	if scope, ok := historyScopeFromContext(ctx); ok {
		baseCtx = withHistoryScope(baseCtx, scope.sessionID, scope.messageID, scope.agentID)
	}
	subCtx, subCancel := context.WithCancel(baseCtx)
	a.Ctx = subCtx
	r.session.logger.Info("SUBAGENT", "ctx_independence", "agent_id", spec.ID, "session_ctx_valid", r.sessionCtx.Err() == nil)

	blockCh := make(chan ttypes.BlockResponse, 8)

	handle := &agent.SubAgentHandle{
		Spec:      spec,
		Agent:     a,
		Cancel:    subCancel,
		ResultCh:  make(chan agent.SubAgentResult, 1),
		BlockCh:   blockCh,
		StartedAt: time.Now(),
		SessionID: subSessionID,
		DB:        subDB,
	}

	a.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)

	systemPrompt, userPrompt := agent.BuildSubAgentPromptInDir(spec, profileInstructions, r.session.WorkDir())

	if err := ctx.Err(); err != nil {
		r.fileOwnership.Release(spec.ID)
		if subDB != nil {
			_ = sessiondb.CloseSession(subDB, subSessionID)
			_ = subDB.Close()
		}
		subCancel()
		return nil, err
	}
	if err := r.mgr.Register(handle); err != nil {
		r.fileOwnership.Release(spec.ID)
		if subDB != nil {
			_ = sessiondb.CloseSession(subDB, subSessionID)
			subDB.Close()
		}
		return nil, fmt.Errorf("register: %w", err)
	}
	r.persistResult(handle, agent.SubAgentResult{
		AgentID: spec.ID, SessionID: subSessionID, Name: spec.Name,
		Category: spec.Category, Status: agent.SubAgentRunning,
	})

	r.wg.Add(1)
	r.beginTurnChild(spec.ParentTurn)
	go func() {
		defer r.wg.Done()
		defer r.finishTurnChild(spec.ParentTurn)
		r.runSubAgentLoop(subCtx, handle, systemPrompt, userPrompt)
	}()

	return handle, nil
}

func (r *SubAgentRunner) subAgentDBPath(sessionID string) (string, error) {
	if r == nil || r.session == nil || r.session.logger == nil {
		return "", nil
	}
	parentDir := r.session.logger.SessionDir()
	if parentDir == "" {
		return "", nil
	}
	dir := filepath.Join(parentDir, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sub-agent session directory: %w", err)
	}
	return filepath.Join(dir, filepath.Base(sessionID)+".db"), nil
}

func (r *SubAgentRunner) runSubAgentLoop(ctx context.Context, handle *agent.SubAgentHandle, systemPrompt, userPrompt string) {
	done := false
	crashed := false

	// Send agent identity to TUI for tab labeling
	if r.sender != nil {
		r.sender.SendAgentEventWithID("sub_agent_started", map[string]string{
			"agent_id":       string(handle.Spec.ID),
			"agent_name":     handle.Spec.Name,
			"category":       handle.Spec.Category,
			"model":          handle.Spec.Model,
			"provider":       handle.Spec.Provider,
			"task":           handle.Spec.Description,
			"prompt":         userPrompt,
			"parent_id":      string(handle.Spec.ParentID),
			"parent_tool_id": handle.Spec.ParentToolCallID,
		}, string(handle.Spec.ID))
		r.sender.SendAgentEventWithID("turn_started", ttypes.TurnIdentity{
			Generation: 1,
			ProviderID: handle.Spec.Provider,
			AccountID:  handle.Spec.AccountID,
			ModelID:    handle.Spec.Model,
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
			r.finalizeSubAgent(handle, result)
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
			r.finalizeSubAgent(handle, result)
			return
		}
	}()

	assignment := provider.NewTextMessage("user", userPrompt)
	assignment.Origin = provider.OriginAssignment
	userMsgs := []provider.ChatMessage{assignment}
	contextGeneration := uint64(1)
	handle.Agent.PrepareCompaction = func(source agent.CompactionSource) error {
		data := compactionLifecycleData(source.RequestID, source.Trigger, source.Role, 0, 0, source.BeforeTokens, 0)
		data["context_window"] = source.ContextWindow
		data["provider_id"] = source.ProviderID
		data["account_id"] = source.AccountID
		data["model_id"] = source.ModelID
		record := compactionRecordFromData(handle.SessionID, string(handle.Spec.ID), sessiondb.CompactionPreparing, data)
		return sessiondb.BeginContextCompaction(handle.DB, handle.SessionID, string(handle.Spec.ID), sessiondb.ContextState{
			AgentID: string(handle.Spec.ID), Generation: contextGeneration, Reason: "compaction_source",
			Messages: append([]provider.ChatMessage(nil), source.Messages...),
		}, record)
	}
	handle.Agent.CommitCompaction = func(commit agent.CompactionCommit) error {
		contextGeneration++
		data := compactionLifecycleData(commit.RequestID, commit.Trigger, commit.Role, compactionStageCount, compactionStageCount, commit.BeforeTokens, commit.AfterTokens)
		data["context_window"] = commit.ContextWindow
		data["provider_id"] = commit.ProviderID
		data["account_id"] = commit.AccountID
		data["model_id"] = commit.ModelID
		record := compactionRecordFromData(handle.SessionID, string(handle.Spec.ID), sessiondb.CompactionCompleted, data)
		return sessiondb.CommitContextCompaction(handle.DB, handle.SessionID, string(handle.Spec.ID), sessiondb.ContextState{
			AgentID: string(handle.Spec.ID), Generation: contextGeneration, Reason: "automatic_compaction",
			Messages: append([]provider.ChatMessage(nil), commit.Messages...),
		}, record)
	}
	if handle.DB != nil {
		if err := sessiondb.BeginAgentTurn(handle.DB, handle.SessionID, string(handle.Spec.ID), 1, string(provider.OriginAssignment)); err != nil {
			r.session.logger.Warn("SUBAGENT", "turn_state_begin_failed", "agent_id", handle.Spec.ID, "error", err.Error())
		}
	}
	agentMsgs := handle.Agent.Run(ctx, userMsgs, systemPrompt, func(evt agent.AgentEvent) {
		evt.AgentID = string(handle.Spec.ID)
		if err := r.session.persistCompactionEvent(handle.DB, handle.SessionID, evt.AgentID, evt); err != nil {
			r.session.logger.Warn("COMPACT", "child_lifecycle_persist_failed", "agent_id", evt.AgentID, "error", err.Error())
		}
		if r.sender != nil {
			r.sender.SendAgentEventWithID(evt.Type, evt.Data, evt.AgentID)
		}
		if evt.Type == "usage" {
			r.session.processUsageEvent(evt.Data, handle.Spec.Model, evt.AgentID, string(handle.Spec.ParentID))
			if handle.DB != nil {
				if record, ok := sessiondb.UsageRecordFromEvent(evt.Data, handle.SessionID, evt.AgentID, string(handle.Spec.ParentID), handle.Spec.Model); ok {
					if record.AccountID == "" {
						record.AccountID = handle.Spec.AccountID
					}
					_ = sessiondb.UpsertUsageRecord(handle.DB, record)
				}
			}
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
			// Agent.Run emits no done event after an unrecovered API/stream error.
			// A later done event supersedes this marker if the run recovered.
			handle.SetTermReason("error")
		}
		if evt.Type == "done" {
			if data, ok := evt.Data.(map[string]any); ok {
				if reason, ok := data["reason"].(string); ok {
					handle.SetTermReason(reason)
				}
			}
		}
	}, handle.BlockCh, handle.SessionID, nil)
	if handle.DB != nil {
		active := handle.Agent.ActiveContext()
		generation := contextGeneration
		reason := "session_start"
		if contextCheckpointContent(active) != "" && generation == 1 {
			generation = 2
			contextGeneration = generation
		}
		if contextCheckpointContent(active) != "" {
			reason = "automatic_compaction"
		}
		if err := sessiondb.SaveContextState(handle.DB, handle.SessionID, string(handle.Spec.ID), sessiondb.ContextState{
			AgentID: string(handle.Spec.ID), Generation: generation, Reason: reason, Messages: active,
		}); err != nil {
			r.session.logger.Warn("SUBAGENT", "context_state_save_failed", "agent_id", handle.Spec.ID, "error", err.Error())
		}
	}

	if err := persistSubAgentTranscript(handle, agentMsgs); err != nil {
		handle.AddError("persist transcript: " + err.Error())
		r.session.logger.Warn(
			"SUBAGENT", "transcript_persist_failed",
			"agent_id", handle.Spec.ID, "error", err.Error(),
		)
	}
	result := r.buildResultFromMessages(handle, agentMsgs)

	if !handle.ReportDone() {
		r.session.logger.Warn("SUBAGENT", "no_report", "agent_id", handle.Spec.ID, "name", handle.Spec.Name)
		r.autoGenerateReport(handle, agentMsgs, &result)
	}

	done = true
	r.finalizeSubAgent(handle, result)
}

// finalizeSubAgent is the sole terminal delivery path for successful, failed,
// cancelled, panicked, and unexpected child exits. Persistence alone is not a
// completion signal: both the UI event and parent wake are mandatory.
func (r *SubAgentRunner) finalizeSubAgent(handle *agent.SubAgentHandle, result agent.SubAgentResult) {
	if handle == nil {
		return
	}
	if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
		handle.Agent.AsyncMgr.CloseAndWait()
	}
	if result.HistoryMessageID == "" {
		result.HistoryMessageID = handle.Spec.HistoryMessageID
	}
	if handle.DB != nil {
		turnState := sessiondb.TurnFailed
		switch result.Status {
		case agent.SubAgentCompleted:
			turnState = sessiondb.TurnCompleted
		case agent.SubAgentCancelled:
			turnState = sessiondb.TurnInterrupted
		}
		if err := sessiondb.FinishAgentTurn(handle.DB, handle.SessionID, string(handle.Spec.ID), 1, turnState); err != nil {
			r.session.logger.Warn("SUBAGENT", "turn_state_finish_failed", "agent_id", handle.Spec.ID, "error", err.Error())
		}
	}
	r.persistResult(handle, result)
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

	// Completion wakes are part of the delivery contract. Backpressure is
	// preferable to silently losing a result; session cancellation unblocks
	// shutdown if no orchestrator remains to drain the queue.
	r.deliverCompletionWake(result)
	if handle.DB != nil {
		_ = sessiondb.CloseSession(handle.DB, handle.SessionID)
		_ = handle.DB.Close()
	}
	r.mgr.MarkFinished(handle.Spec.ID, result)
}

func persistSubAgentTranscript(
	handle *agent.SubAgentHandle,
	messages []provider.ChatMessage,
) error {
	if handle == nil || handle.DB == nil {
		return nil
	}
	var persisted []provider.ChatMessage
	var turns []int
	var metas []sessiondb.DisplayMeta
	rawToolArgs := toolCallArgsIndex(messages)
	toolNames := make(map[string]string)
	toolArgs := make(map[string]string)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			toolNames[call.ID] = call.Function.Name
			var params map[string]any
			if parsed, err := provider.ToolCallParams(call); err == nil {
				params = parsed
				toolArgs[call.ID] = agent.FormatToolArgs(params)
			}
		}
	}
	assignmentSeen := false
	baseSystemSkipped := false
	turnIdentity := ttypes.TurnIdentity{
		Generation: 1,
		ProviderID: handle.Spec.Provider,
		AccountID:  handle.Spec.AccountID,
		ModelID:    handle.Spec.Model,
	}
	for _, message := range messages {
		if message.Role == "system" && !baseSystemSkipped {
			baseSystemSkipped = true
			continue
		}
		persisted = append(persisted, message)
		turns = append(turns, 1)
		displayRole := message.Role
		if message.Role == "user" && !assignmentSeen {
			displayRole = "assignment"
			assignmentSeen = true
		}
		meta := sessiondb.DisplayMeta{DisplayRole: displayRole}
		if isProviderControlMessage(message) {
			meta = sessiondb.DisplayMeta{
				SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				DisplayRole:     "hidden_wire_context",
				ContextOnly:     true,
			}
		}
		if message.Role == "system" {
			meta = sessiondb.DisplayMeta{
				SnapshotVersion: sessiondb.ConversationSnapshotVersion,
				DisplayRole:     "hidden_wire_context",
				ContextOnly:     true,
			}
		}
		if message.Role == "tool" {
			meta.ToolName = toolNames[message.ToolCallID]
			meta.ToolArgs = toolArgs[message.ToolCallID]
			if message.ToolError {
				meta.ToolStatus = "error"
			} else {
				meta.ToolStatus = "success"
			}
		}
		if message.Role == "assistant" || message.Role == "tool" {
			meta.TurnGeneration = turnIdentity.Generation
			meta.ProviderID = turnIdentity.ProviderID
			meta.AccountID = turnIdentity.AccountID
			meta.ModelID = turnIdentity.ModelID
		}
		if message.Role != "system" && !isProviderControlMessage(message) {
			meta = canonicalizeDisplayMeta(message, meta, rawToolArgs[message.ToolCallID], len(persisted)-1, time.Now())
		}
		metas = append(metas, meta)
	}
	if len(persisted) == 0 {
		return nil
	}
	_, err := sessiondb.ReplaceMessagesWithMeta(
		handle.DB, handle.SessionID, persisted, turns, metas,
	)
	return err
}

func (r *SubAgentRunner) deliverCompletionWake(result agent.SubAgentResult) bool {
	if r == nil || r.wakeCh == nil {
		return false
	}
	select {
	case r.wakeCh <- WakeEvent{
		Type: WakeSubAgentDone, Data: result, SourceID: string(result.AgentID), HistoryMessageID: result.HistoryMessageID,
	}:
		return true
	case <-r.sessionCtx.Done():
		return false
	}
}

func (r *SubAgentRunner) buildResultFromMessages(handle *agent.SubAgentHandle, msgs []provider.ChatMessage) agent.SubAgentResult {
	status := agent.SubAgentCompleted
	if ctx := handle.Agent.Ctx; ctx != nil && ctx.Err() != nil {
		status = agent.SubAgentCancelled
	}

	switch handle.TermReason() {
	case "stop_condition_met", "context_limit_reached":
		if status == agent.SubAgentCompleted {
			status = agent.SubAgentFailedError
		}
	}

	// Check for tool-level errors (quota, auth, etc.)
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		mc := m.ContentString()
		if m.Role == "tool" && agent.IsToolError(mc) {
			if strings.Contains(mc, "429") || strings.Contains(mc, "quota") {
				status = agent.SubAgentFailedQuota
				break
			}
			if strings.Contains(mc, "401") || strings.Contains(mc, "403") || strings.Contains(mc, "invalid api key") || strings.Contains(mc, "authentication") {
				status = agent.SubAgentFailedQuota
				break
			}
		}
		if m.Role == "assistant" {
			if mc != "" && (strings.Contains(mc, "429") || strings.Contains(mc, "quota")) {
				status = agent.SubAgentFailedQuota
				break
			}
		}
	}

	// Calls and read claims are activity, not a deliverable. An unrecovered
	// stream error can leave both tool calls and a partial assistant message in
	// the transcript; neither proves the child completed its assignment. A
	// report written through write_report is authored evidence, unlike the
	// diagnostic report generated below after this status is decided.
	authoredReport := handle.ReportDone()
	finalAnswer := false
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "assistant" {
			finalAnswer = len(m.ToolCalls) == 0 && strings.TrimSpace(m.ContentString()) != ""
			break
		}
	}
	if status == agent.SubAgentCompleted && !authoredReport &&
		(handle.TermReason() == "error" || !finalAnswer) {
		status = agent.SubAgentFailedError
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
			mc := m.ContentString()
			if m.Role == "assistant" && len(m.ToolCalls) == 0 && mc != "" {
				gist = mc
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
	fmt.Fprintf(&sb, "# Sub-Agent Report: %s [%s]\n\n", handle.Spec.Name, handle.Spec.Category)
	fmt.Fprintf(&sb, "Task: %s\n\n", handle.Spec.Description)
	fmt.Fprintf(&sb, "Status: %s\n\n", result.Status)

	// Determine if the agent actually ran or failed immediately
	hadAssistantMsg := false
	for _, m := range msgs {
		if m.Role == "assistant" && (m.ContentString() != "" || len(m.ToolCalls) > 0) {
			hadAssistantMsg = true
			break
		}
	}

	if result.Status == agent.SubAgentCancelled && !hadAssistantMsg {
		sb.WriteString("## Cancellation Summary\n\n")
		sb.WriteString("The sub-agent was cancelled before it produced a complete result. No success should be inferred from this report.\n")
	} else if len(result.Errors) > 0 && !hadAssistantMsg {
		// Agent never ran — API error on first call.
		sb.WriteString("## Failure Summary\n\n")
		sb.WriteString("The sub-agent encountered an error before it could start working. The LLM API call failed, so no assistant messages were generated and no work was performed.\n\n")
		if len(result.Errors) == 1 {
			fmt.Fprintf(&sb, "**Error:** %s\n", result.Errors[0])
		} else {
			fmt.Fprintf(&sb, "**Errors (%d total):**\n", len(result.Errors))
			for _, e := range result.Errors {
				fmt.Fprintf(&sb, "- %s\n", e)
			}
		}
		sb.WriteString("\n**No files were read or written.**\n")
	} else {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "assistant" && msgs[i].ContentString() != "" && len(msgs[i].ToolCalls) == 0 {
				sb.WriteString("## Summary\n\n")
				sb.WriteString(msgs[i].ContentString())
				sb.WriteString("\n")
				break
			}
		}

		filesRead := r.fileOwnership.ReadClaimsByAgent(handle.Spec.ID)
		filesWritten := r.fileOwnership.WriteClaimsByAgent(handle.Spec.ID)
		if len(filesRead) > 0 {
			sb.WriteString("\n## Files Read\n\n")
			for _, f := range filesRead {
				fmt.Fprintf(&sb, "- %s\n", f)
			}
		}
		if len(filesWritten) > 0 {
			sb.WriteString("\n## Files Written\n\n")
			for _, f := range filesWritten {
				fmt.Fprintf(&sb, "- %s\n", f)
			}
		}
	}

	if len(result.Errors) > 0 {
		sb.WriteString("\n## Errors\n\n")
		for _, e := range result.Errors {
			if len(e) > 200 {
				e = e[:200] + "..."
			}
			fmt.Fprintf(&sb, "- %s\n", e)
		}
	}

	reportPath := filepath.Join(reportsDir, fmt.Sprintf("%s-report.md", handle.Spec.ID))
	if err := os.WriteFile(reportPath, []byte(sb.String()), 0644); err != nil {
		return
	}

	// This fallback is a diagnostic artifact, not an agent-authored report.
	// Do not set ReportDone: consumers must never mistake it for completion.
	result.ResultPath = reportPath
}

func (r *SubAgentRunner) buildProxiedToolFn(spec agent.SubAgentSpec, blockCh chan ttypes.BlockResponse, handle *agent.SubAgentHandle) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	parentFn := r.parentToolFn
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if !agent.PolicyAllows(spec.Policy, tool) {
			return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Tool %q is not allowed for this agent. Your policy is %q", tool, spec.PolicyName)
		}
		r.session.resolveRelativeParams(tool, params)
		if tool == "apply_patch" {
			if err := agent.CheckPatchPolicy(spec.Policy, getStrParam(params, "input")); err != nil {
				return tools.ToolResult{}, err
			}
		}

		isReadOnlyExec := spec.Policy.AllowedCapabilities[safety.CanExecuteReadOnly] && !spec.Policy.AllowedCapabilities[safety.CanExecute]
		if isReadOnlyExec && (tool == "execute_command" || tool == "async_execute_command" || tool == "exec_command" || tool == "write_stdin") {
			command := getStrParam(params, "command")
			if tool == "exec_command" {
				command = getStrParam(params, "cmd")
			}
			if tool == "write_stdin" && getStrParam(params, "chars") != "" {
				return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] interactive input requires execute permission")
			}
			if command != "" {
				safetyRes := safety.CheckCommandSafety(command, r.session.logger)
				if safetyRes.Risk != safety.RiskSafe {
					return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Command %q is not safe for read-only execution (risk: %s, reason: %s). Only read-only commands like git status, git diff, git log, ls, cat, etc. are allowed", command, safetyRes.Risk, safetyRes.Reason)
				}
			}
		}

		if tool == "question" {
			questionText := summarizeSubAgentQuestions(params)
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
		if tool == "read_file" {
			r.fileOwnership.RecordRead(spec.ID, getStrParam(params, "path"))
		}

		if isWriteTool(tool) {
			for _, target := range writeToolTargets(tool, params) {
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

		if tool == "exec_command" || tool == "write_stdin" {
			if handle == nil || handle.Agent == nil {
				return tools.ToolResult{}, fmt.Errorf("child command manager unavailable")
			}
			return executeCodexCommand(ctx, handle.Agent.AsyncMgr, r.session.WorkDir(), tool, params, handle.Agent.CommandPolicy)
		}
		if isAsyncCommandTool(tool) {
			return r.executeSubAgentAsyncCommand(handle, tool, params)
		}

		if tool == "execute_command" {
			return r.executeSubAgentCommandAutoPromote(ctx, handle, params)
		}

		if err := r.rateLimiter.Wait(ctx, spec.AccountID); err != nil {
			return tools.ToolResult{}, fmt.Errorf("rate limit exceeded for account %q", spec.AccountID)
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
			if handle != nil && handle.Agent != nil && handle.Agent.Provider != nil {
				p["_media_model"] = handle.Agent.Model
				p["_media_standard"] = handle.Agent.Provider.StandardID()
				p["_media_modalities"] = append([]string(nil), spec.ModelModalities...)
			}
			return parentFn(withHistoryActor(ctx, string(spec.ID)), tool, p)
		}
		var opts *tools.ToolOptions
		if handle != nil && handle.Agent != nil && handle.Agent.Provider != nil {
			opts = toolOptionsForProvider(handle.Agent.Provider, spec.ModelModalities, params)
		}
		result := tools.ExecuteWithJSONOptions(tool, params, r.session.exec, r.session.editor, r.session.YoloEnabled(), r.session.logger, ctx, opts)
		if agent.IsToolError(result.Content) {
			return result, fmt.Errorf("%s", result.Content)
		}
		return result, nil
	}
}

func writeToolTargets(tool string, params map[string]any) []string {
	switch tool {
	case "apply_patch":
		patch, err := codexio.Parse(getStrParam(params, "input"))
		if err != nil {
			return nil
		}
		return patch.Paths(getStrParam(params, "_patch_root"))
	case "write_file", "edit_file", "delete_file":
		return nonEmptyStrings(getStrParam(params, "path"))
	case "move_file":
		return nonEmptyStrings(getStrParam(params, "source"), getStrParam(params, "destination"))
	case "copy_file":
		return nonEmptyStrings(getStrParam(params, "destination"))
	default:
		return nil
	}
}

func nonEmptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func summarizeSubAgentQuestions(params map[string]any) string {
	raw, ok := params["questions"].([]any)
	if !ok {
		return ""
	}
	lines := make([]string, 0, len(raw))
	for _, value := range raw {
		question, ok := value.(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(getStrParam(question, "question"))
		if text == "" {
			continue
		}
		header := strings.TrimSpace(getStrParam(question, "header"))
		if header != "" {
			text = header + ": " + text
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

func (r *SubAgentRunner) persistResult(_ *agent.SubAgentHandle, result agent.SubAgentResult) {
	if r.session.db == nil {
		return
	}
	if err := sessiondb.InsertSubAgentResult(r.session.db, subAgentResultRow(r.session.sessionID, result)); err != nil {
		r.session.logger.Error("SUBAGENT", "persist_result_failed", "agent_id", result.AgentID, "error", err.Error())
	}
}

func (r *SubAgentRunner) resolveProfile(name string, category string, spec *agent.SubAgentSpec) string {
	// Use effective config if available, otherwise fall back to base config.
	cfg := r.effectiveCfg
	if cfg == nil {
		r.session.mu.Lock()
		cfg = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}
	if profile, ok := cfg.Agents.AgentProfiles[name]; ok && profile.Category != "" {
		category, spec.Category = profile.Category, profile.Category
	}
	activeProfile := cfg.Agents.ActiveProfile
	if activeProfile == "" {
		activeProfile = "default"
	}
	profiles := cfg.Agents.Profiles
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
		if spec.AccountID == "" && catCfg.AccountID != "" {
			spec.AccountID = catCfg.AccountID
		}
		if spec.ThinkingEffort == "" && catCfg.ThinkingEffort != "" {
			spec.ThinkingEffort = catCfg.ThinkingEffort
		}
		if spec.PolicyName == "" && catCfg.Policy != "" {
			spec.PolicyName = catCfg.Policy
		}
		constrainConfiguredTools(spec, catCfg.AllowedTools, catCfg.DeniedTools)
		if spec.ModelContextWindow == 0 && catCfg.ModelContextWindow > 0 {
			spec.ModelContextWindow = catCfg.ModelContextWindow
		}
		if spec.ModelMaxOutput == 0 && catCfg.ModelMaxOutput > 0 {
			spec.ModelMaxOutput = catCfg.ModelMaxOutput
		}
		if spec.ContextCompactionPercent == nil && catCfg.ContextCompactionPercent != nil {
			spec.ContextCompactionPercent = cloneIntPtr(catCfg.ContextCompactionPercent)
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
		if spec.ModelModalities == nil && catCfg.ModelModalities != nil {
			spec.ModelModalities = slices.Clone(catCfg.ModelModalities)
		}
		if catCfg.Description != "" {
			instructionsParts = append(instructionsParts, catCfg.Description)
		}
		if catCfg.Instructions != "" {
			instructionsParts = append(instructionsParts, catCfg.Instructions)
		}
	}

	if agentProfiles := cfg.Agents.AgentProfiles; agentProfiles != nil {
		if ap, ok := agentProfiles[name]; ok {
			if ap.Model != "" {
				if ap.Model != spec.Model {
					spec.ModelContextWindow = 0
					spec.ModelMaxOutput = 0
					spec.ModelInputPrice, spec.ModelOutputPrice, spec.ModelCachePrice = 0, 0, 0
					spec.ModelModalities = nil
					spec.ModelTemperature, spec.ModelTopP = nil, nil
				}
				spec.Model = ap.Model
			}
			if ap.AccountID != "" {
				spec.AccountID = ap.AccountID
			}
			if ap.ThinkingEffort != "" {
				spec.ThinkingEffort = ap.ThinkingEffort
			}
			if ap.ModelContextWindow > 0 {
				spec.ModelContextWindow = ap.ModelContextWindow
			}
			if ap.ModelMaxOutput > 0 {
				spec.ModelMaxOutput = ap.ModelMaxOutput
			}
			if ap.ModelModalities != nil {
				spec.ModelModalities = append(make([]string, 0, len(ap.ModelModalities)), ap.ModelModalities...)
			}
			if ap.ModelTemperature != nil {
				spec.ModelTemperature = cloneFloatPtr(ap.ModelTemperature)
			}
			if ap.ModelTopP != nil {
				spec.ModelTopP = cloneFloatPtr(ap.ModelTopP)
			}
			constrainConfiguredTools(spec, ap.AllowedTools, ap.DeniedTools)
			if len(spec.FileOwnership) == 0 && len(ap.FileOwnership) > 0 {
				spec.FileOwnership = ap.FileOwnership
			}
			if ap.ContextCompactionPercent != nil {
				spec.ContextCompactionPercent = cloneIntPtr(ap.ContextCompactionPercent)
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
	cfg := r.effectiveCfg
	if cfg == nil {
		r.session.mu.Lock()
		cfg = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}
	categoryCfg := cfg.GetCategoryConfig(spec.Category)
	if spec.Model == "" {
		spec.Model = categoryCfg.Model
	}
	if spec.AccountID == "" {
		spec.AccountID = categoryCfg.AccountID
	}
	if spec.ThinkingEffort == "" {
		spec.ThinkingEffort = categoryCfg.ThinkingEffort
	}
	if spec.Model == categoryCfg.Model {
		if spec.ModelContextWindow == 0 {
			spec.ModelContextWindow = categoryCfg.ModelContextWindow
		}
		if spec.ModelMaxOutput == 0 {
			spec.ModelMaxOutput = categoryCfg.ModelMaxOutput
		}
		if spec.ModelInputPrice == 0 {
			spec.ModelInputPrice = categoryCfg.ModelInputPrice
		}
		if spec.ModelOutputPrice == 0 {
			spec.ModelOutputPrice = categoryCfg.ModelOutputPrice
		}
		if spec.ModelCachePrice == 0 {
			spec.ModelCachePrice = categoryCfg.ModelCachePrice
		}
		if spec.ModelModalities == nil {
			spec.ModelModalities = slices.Clone(categoryCfg.ModelModalities)
		}
		if spec.ModelTemperature == nil {
			spec.ModelTemperature = categoryCfg.ModelTemperature
		}
		if spec.ModelTopP == nil {
			spec.ModelTopP = categoryCfg.ModelTopP
		}
		if spec.ContextCompactionPercent == nil && categoryCfg.ContextCompactionPercent != nil {
			spec.ContextCompactionPercent = cloneIntPtr(categoryCfg.ContextCompactionPercent)
		}
	}
	if spec.PolicyName == "" {
		spec.PolicyName = categoryCfg.Policy
		if spec.PolicyName == "" {
			switch spec.Category {
			case agent.CatResearcher:
				spec.PolicyName = "read-only"
			case agent.CatCrafter, agent.CatAeron:
				spec.PolicyName = "no-delete"
			}
		}
	}
}

func isWriteTool(name string) bool {
	switch name {
	case "apply_patch", "write_file", "edit_file", "delete_file", "move_file", "copy_file":
		return true
	}
	return false
}

func isPlanningTool(name string) bool {
	switch name {
	case "update_plan", "get_plan":
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
	if handle == nil || handle.DB == nil || handle.SessionID == "" {
		return tools.ToolResult{}, fmt.Errorf("planning unavailable for sub-agent")
	}
	store := plan.NewStore(handle.DB, handle.SessionID)
	return executePlanToolWithStore(context.Background(), store, handle.SessionID, tool, params)
}

func (r *SubAgentRunner) executeSubAgentAsyncCommand(handle *agent.SubAgentHandle, tool string, params map[string]any) (tools.ToolResult, error) {
	mgr := handle.Agent.AsyncMgr
	if mgr == nil {
		return tools.ToolResult{Content: "Error: async command system not available for this sub-agent"}, fmt.Errorf("async command system not available")
	}

	switch tool {
	case "async_execute_command":
		command := getStrParam(params, "command")
		if sr := safety.CheckCommandSafetyWithPolicy(command, r.session.logger, handle.Agent.CommandPolicy); sr.Risk.IsAlwaysBlocked() {
			return tools.ToolResult{}, fmt.Errorf("[SAFETY BLOCK] %s", sr.Reason)
		}
		handleName := getStrParam(params, "handle")
		if command == "" {
			return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'command'")
		}
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("async_execute_command: missing 'handle'")
		}
		cwd := getStrParam(params, "cwd")
		if cwd == "" {
			cwd = r.session.WorkDir()
		}
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

func (r *SubAgentRunner) executeSubAgentCommandAutoPromote(ctx context.Context, handle *agent.SubAgentHandle, params map[string]any) (tools.ToolResult, error) {
	cmd := getStrParam(params, "command")
	if cmd == "" {
		return tools.ToolResult{}, fmt.Errorf("execute_command: missing 'command'")
	}

	a := handle.Agent
	mgr := a.AsyncMgr
	if mgr == nil {
		return tools.ToolResult{}, fmt.Errorf("execute_command: async command system not available for this sub-agent")
	}

	isReadOnlyExec := a.Policy.AllowedCapabilities[safety.CanExecuteReadOnly] && !a.Policy.AllowedCapabilities[safety.CanExecute]
	if !a.Yolo {
		sr := safety.CheckCommandSafetyWithPolicy(cmd, r.session.logger, a.CommandPolicy)
		if sr.Risk.IsAlwaysBlocked() {
			errMsg := fmt.Sprintf("Error: [SAFETY] %s\nSuggestion: %s", sr.Reason, sr.Suggestion)
			r.session.logger.Warn("SAFETY", "blocked", "command", cmd, "reason", sr.Reason)
			return tools.ToolResult{Content: errMsg}, fmt.Errorf("%s", errMsg)
		}
		if sr.NeedsConfirmation() {
			r.session.logger.Info("SAFETY", "risky", "command", cmd, "risk", sr.Risk.String())
		}
		if isReadOnlyExec && sr.Risk != safety.RiskSafe {
			return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Command %q is not safe for read-only execution (risk: %s, reason: %s). Only read-only commands like git status, git diff, git log, ls, cat, etc. are allowed", cmd, sr.Risk, sr.Reason)
		}
	}

	cwd := getStrParam(params, "cwd")
	if cwd == "" {
		cwd = r.session.WorkDir()
	}
	timeout := tools.GetIntArgAny(params, "timeout_seconds", -1)
	if timeout < 0 {
		timeout = 60
	}

	uc, err := mgr.StartUntracked(cmd, cwd, timeout)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("execute_command: %w", err)
	}
	r.session.logger.Info("EXEC", "subagent_auto_start", "agent_id", a.AgentID, "pid", uc.PID(), "cmd", cmd)
	return awaitCommandWithPromotion(ctx, mgr, uc, r.session.logger, string(a.AgentID))
}

func generateShortID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("session: generate agent ID: %v", err))
	}
	return hex.EncodeToString(b[:])
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
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
