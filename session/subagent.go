package session

import (
	"context"
	captypes "crude/types/capability"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"slices"
	"strings"

	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"crude/agent"
	"crude/codexio"
	"crude/collaboration"
	"crude/fileio"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
	"crude/worktree"
)

type SubAgentRunner struct {
	session        *Session
	orchestratorID string
	effectiveCfg   *Config // per-orchestrator effective config (nil = use base)
	mgr            *agent.SubAgentManager
	fileOwnership  *agent.FileOwnership
	rateLimiter    *agent.RateLimiter
	sender         EventSender
	parentToolMu   sync.RWMutex
	parentToolFn   func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error)
	wg             sync.WaitGroup
	sessionCtx     context.Context
	sessionCancel  context.CancelFunc
	parentTurn     atomic.Uint64
	running        atomic.Int32
	turnMu         sync.Mutex
	turnCounts     map[uint64]int
	turnDone       map[uint64]chan struct{}
	// connMu guards each live child's current connection and a parent's
	// pending change, applied at the child's next request boundary.
	connMu             sync.Mutex
	activeConnections  map[agent.AgentID]childConnection
	pendingConnections map[agent.AgentID]childConnection
}

type childRecoveryOwner struct {
	Spec                agent.SubAgentSpec `json:"spec"`
	ProfileInstructions string             `json:"profile_instructions,omitempty"`
}

// childAssignmentDisplay is the chat projection of a typed assignment. The
// provider receives prompt.BuildChild's escaped XML envelope; people read the
// parent's authored description and instructions verbatim as Markdown. Both
// derive from the same spec fields, so nothing ever parses the envelope or
// model-authored structure back out of text.
func childAssignmentDisplay(spec agent.SubAgentSpec) string {
	description := strings.TrimSpace(spec.Description)
	instructions := strings.TrimSpace(spec.Instructions)
	switch {
	case description == "" || description == instructions:
		return instructions
	case instructions == "":
		return description
	}
	if !strings.ContainsAny(description, "\r\n*") {
		description = "**" + description + "**"
	}
	return description + "\n\n" + instructions
}

func NewSubAgentRunner(s *Session, maxConcurrent int) *SubAgentRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &SubAgentRunner{
		session:       s,
		mgr:           agent.NewSubAgentManager(maxConcurrent),
		fileOwnership: s.FileOwnership(),
		rateLimiter:   agent.NewRateLimiter(),
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
	for _, handle := range r.mgr.ActiveHandles() {
		if matches(handle) {
			handle.Cancel()
		}
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
	r.parentToolMu.Lock()
	r.parentToolFn = fn
	r.parentToolMu.Unlock()
}

func (r *SubAgentRunner) StartAgent(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	// Serialize launch admission, not the child's work. This also
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
		r.SetParentToolFn(r.session.buildToolFn(false, parentProvider, cat.ModelModalities))
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "Turn cancelled before sub-agent launch."}, err
	}
	description := getStrParam(params, "description")
	if description == "" {
		return tools.ToolResult{}, fmt.Errorf("agent_start: 'description' is required")
	}

	category := getStrParam(params, "category")
	if category == "" {
		category = agent.CatCrafter
	}
	profileName := getStrParam(params, "profile")
	if profileName != "" {
		active := r.effectiveCfg
		if active == nil {
			r.session.mu.Lock()
			active = copyConfig(r.session.cfg)
			r.session.mu.Unlock()
		}
		profile, ok := active.Agents.AgentProfiles[profileName]
		if !ok || profile.Enabled != nil && !*profile.Enabled {
			return tools.ToolResult{}, fmt.Errorf("agent profile %q is unknown or disabled", profileName)
		}
		if profile.Category != category {
			return tools.ToolResult{}, fmt.Errorf("agent profile %q requires category %q, not %q", profileName, profile.Category, category)
		}
	}
	name := deriveSubAgentName(category, description)

	spec := agent.SubAgentSpec{
		ID:            agent.AgentID("agent-" + generateShortID()),
		Name:          name,
		Category:      category,
		Description:   description,
		Instructions:  getStrParam(params, "instructions"),
		PolicyName:    getStrParam(params, "policy"),
		Depth:         1,
		ParentID:      agent.AgentID(r.orchestratorID),
		ParentTurn:    r.parentTurn.Load(),
		Isolation:     getStrParam(params, "isolation"),
		ControlTarget: controlTargetFromContext(ctx),
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
		_, validateErr := validateOwnershipPaths(r.session.WorkDir(), spec.FileOwnership)
		if validateErr != nil {
			return tools.ToolResult{Content: "Error: " + validateErr.Error()}, validateErr
		}
	}

	profileInstructions := r.resolveProfile(profileName, category, &spec)
	connection, err := parseConnectionRequest(params)
	if err != nil {
		return tools.ToolResult{}, err
	}
	if !connection.empty() {
		roleEffort := r.currentConfig().GetCategoryConfig(category).ThinkingEffort
		chosen := connection.resolve(connectionOf(spec), roleEffort)
		if chosen.Model == "" {
			chosen.Model = r.currentConfig().GetCategoryConfig(category).Model
		}
		if chosen.AccountID == "" {
			chosen.AccountID = r.currentConfig().GetCategoryConfig(category).AccountID
		}
		applyConnection(&spec, chosen)
		if err := r.validateConnection(spec); err != nil {
			return tools.ToolResult{}, err
		}
	}
	if err := validateChildReportPermission(spec); err != nil {
		return tools.ToolResult{}, err
	}
	activeConfig := r.effectiveCfg
	if activeConfig == nil {
		r.session.mu.Lock()
		activeConfig = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}
	if categoryCfg := activeConfig.GetCategoryConfig(spec.Category); categoryCfg.Enabled != nil && !*categoryCfg.Enabled {
		return tools.ToolResult{Content: "Agent role is disabled in settings."}, fmt.Errorf("%s role is disabled", spec.Category)
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

	if err := r.mgr.Validate(spec); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("%s", err)
	}

	handle, err := r.launchSubAgent(ctx, spec, profileInstructions)
	if err != nil {
		r.fileOwnership.Release(spec.ID)
		return tools.ToolResult{Content: fmt.Sprintf("Error launching sub-agent: %s", err)}, fmt.Errorf("launch: %w", err)
	}

	r.session.delegationMu.Unlock()
	locked = false

	go func() {
		select {
		case <-r.sessionCtx.Done():
			handle.Cancel()
		case <-handle.ResultCh:
		}
	}()
	runID, runVersion := handle.RunIdentity()
	resultJSON, _ := json.Marshal(map[string]any{
		"agent_id": string(spec.ID), "name": spec.Name, "category": spec.Category,
		"status": "running", "session_id": handle.SessionID,
		"run_id": runID, "run_version": runVersion,
		"execution_id": handle.Spec.ExecutionID, "execution_root": handle.Spec.ExecutionRoot, "isolation": handle.Spec.Isolation,
	})
	return tools.ToolResult{Content: string(resultJSON)}, nil
}

func validateOwnershipPaths(workDir string, paths []string) ([]string, error) {
	validated := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			return nil, errors.New("file_ownership entries must be concrete non-empty files or directories")
		}
		if strings.ContainsAny(path, "*?") {
			return nil, fmt.Errorf("file_ownership path %q contains wildcard syntax; globs are unsupported, pass concrete files or directories", raw)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workDir, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve file_ownership path %q: %w", raw, err)
		}
		validated = append(validated, filepath.Clean(absolute))
	}
	return validated, nil
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
	p, err := cfg.providerForCategory(childProviderConfig(spec))
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
		return nil, fmt.Errorf("sub-agent database path: %w", pathErr)
	} else if dbPath != "" {
		var err error
		subDB, err = sessiondb.OpenAndMigrate(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open sub-agent database: %w", err)
		} else {
			wd, _ := filepath.Abs(".")
			projectName := "unknown"
			if wd != "" {
				projectName = filepath.Base(wd)
			}
			if err := sessiondb.Create(subDB, sessiondb.SessionMeta{
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
			}); err != nil {
				_ = subDB.Close()
				return nil, fmt.Errorf("create sub-agent session: %w", err)
			}
		}
	}
	if subDB == nil {
		return nil, errors.New("sub-agent database is unavailable")
	}
	if err := r.session.bindChildPlanStore(string(spec.ID), subSessionID, subDB); err != nil {
		_ = subDB.Close()
		return nil, fmt.Errorf("restore child plan: %w", err)
	}
	if r.session.db == nil {
		_ = subDB.Close()
		return nil, errors.New("parent session database is unavailable")
	}

	r.session.logger.Info("SUBAGENT", "launch", "agent_id", spec.ID, "category", spec.Category, "template", p.ID, "account", spec.AccountID, "model", resolvedModelName, "effort", spec.ThinkingEffort)
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
	a.MediaReadPolicy = &tools.MediaReadPolicy{Model: p.Model, Standard: p.StandardID(), Modalities: slices.Clone(spec.ModelModalities)}
	a.WorkDir = r.session.WorkDir()
	a.GuidancePaths = append([]string(nil), spec.FileOwnership...)
	r.session.bindContinuity(a, subDB, subSessionID)
	r.session.bindGuidanceCache(a)
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
	if spec.ContextRetentionTokens != nil {
		a.CompactionRetentionTokens = *spec.ContextRetentionTokens
	}
	a.AgentID = spec.ID
	a.TurnGeneration = 1
	a.Depth = spec.Depth
	a.ParentID = spec.ParentID
	a.Policy = spec.Policy
	r.session.bindAgentPermissionPolicy(a)
	childTempDir := r.childTempDir(spec.ID)
	a.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
	r.session.observeCommandOutcomes(a.AsyncMgr, string(spec.ID))
	if err := a.AsyncMgr.SetTempDir(childTempDir); err != nil {
		if subDB != nil {
			_ = sessiondb.CloseSession(subDB, subSessionID)
			_ = subDB.Close()
		}
		return nil, fmt.Errorf("create child temp directory: %w", err)
	}
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
	registered, err := r.session.registerCollaborator(subCtx, string(spec.ID), spec.Name, subSessionID, string(spec.ParentID), spec.Category, subDB)
	if err != nil {
		r.fileOwnership.Release(spec.ID)
		if subDB != nil {
			_ = sessiondb.CloseSession(subDB, subSessionID)
			_ = subDB.Close()
		}
		subCancel()
		return nil, fmt.Errorf("register workspace child: %w", err)
	}
	handle.RuntimeEpoch = registered.RuntimeEpoch
	launched := false
	defer func() {
		if launched {
			return
		}
		subCancel()
		a.AsyncMgr.CloseAndWait()
		if store := r.session.CollaborationStore(); store != nil {
			if runID, runVersion := handle.RunIdentity(); runID != "" {
				_, _ = store.FinishRun(context.Background(), collaboration.FinishRunRequest{
					RunID: runID, AgentID: string(spec.ID), RuntimeEpoch: handle.RuntimeEpoch,
					ExpectedVersion: runVersion, State: collaboration.RunCancelled, Outcome: "launch aborted",
				})
			}
		}
		r.releaseExecutionPin(spec, handle)
		r.fileOwnership.Release(spec.ID)
		_ = subDB.Close()
		r.session.detachCollaborator(context.Background(), string(spec.ID))
	}()
	if store := r.session.CollaborationStore(); store != nil && handle.RuntimeEpoch > 0 {
		run, runErr := store.StartRun(ctx, collaboration.StartRunRequest{
			AgentID: string(spec.ID), RuntimeEpoch: handle.RuntimeEpoch,
			RequestID: spec.ParentToolCallID, Origin: collaboration.OriginPeer,
		})
		if runErr != nil {
			r.session.detachCollaborator(context.Background(), string(spec.ID))
			r.fileOwnership.Release(spec.ID)
			if subDB != nil {
				_ = sessiondb.CloseSession(subDB, subSessionID)
				_ = subDB.Close()
			}
			subCancel()
			return nil, fmt.Errorf("start workspace child run: %w", runErr)
		}
		handle.SetRunIdentity(run.ID, run.Version)
		a.RunID = run.ID
		if err := r.prepareChildExecution(ctx, &spec, run.ID); err != nil {
			return nil, fmt.Errorf("prepare child execution: %w", err)
		}
		handle.Spec = spec
		a.WorkDir = spec.ExecutionRoot
		a.GuidancePaths = slices.Clone(spec.FileOwnership)
		if err := r.fileOwnership.PreClaim(spec.ID, spec.Name, spec.FileOwnership); err != nil {
			return nil, err
		}

		if len(spec.FileOwnership) > 0 {
			if _, err := store.AcquireClaims(ctx, collaboration.AcquireClaimsRequest{
				AgentID: string(spec.ID), RunID: run.ID, RequestID: spec.ParentToolCallID, Paths: spec.FileOwnership,
			}); err != nil {
				r.fileOwnership.Release(spec.ID)
				if subDB != nil {
					_ = subDB.Close()
				}
				return nil, fmt.Errorf("reserve workspace child ownership: %w", err)
			}
		}
	} else {
		// Standalone explicit shared sessions create no managed artifact or
		// setup effects. Managed execution always requires a durable run owner.
		if spec.Isolation != string(worktree.Shared) {
			return nil, errors.New("child execution requires a durable collaboration run")
		}
		spec.ExecutionRoot = r.session.WorkDir()
		spec.FileOwnership, err = validateOwnershipPaths(spec.ExecutionRoot, spec.FileOwnership)
		if err != nil {
			return nil, err
		}
		handle.Spec = spec
	}

	promptAllowed, promptDenied := agent.ModelToolSelection(spec.AllowedTools, spec.DeniedTools, spec.Model)
	systemPrompt, userPrompt := rootprompt.BuildChild(rootprompt.ChildOptions{
		Category: spec.Category, Name: spec.Name, ID: string(spec.ID), ParentID: string(spec.ParentID), ParentTurn: spec.ParentTurn,
		Description: spec.Description, Instructions: spec.Instructions, PolicyName: spec.PolicyName,
		AllowedTools: promptAllowed, DeniedTools: promptDenied, FileOwnership: spec.FileOwnership,
		ProfileInstructions: profileInstructions, WorkDir: spec.ExecutionRoot,
		TempDir: childTempDir, Model: spec.Model,
	})
	ownerData, err := json.Marshal(childRecoveryOwner{Spec: spec, ProfileInstructions: profileInstructions})
	if err != nil {
		_ = subDB.Close()
		subCancel()
		return nil, fmt.Errorf("encode child recovery owner: %w", err)
	}
	assignmentInput := provider.UserInput{
		InternalID:  "assignment-" + string(spec.ID),
		Text:        userPrompt,
		DisplayText: childAssignmentDisplay(spec),
		Origin:      provider.OriginAssignment,
	}
	assignment := provider.NewTextMessage("user", userPrompt)
	assignment.InternalID = assignmentInput.InternalID
	assignment.DisplayContent = assignmentInput.DisplayText
	assignment.Origin = provider.OriginAssignment
	recoveryState := newRecoveryRunState(assignmentInput, nil)
	recoveryState.inputAdmitted = true
	recoveryState.archive = []provider.ChatMessage{assignment}
	recoveryState.active = []provider.ChatMessage{assignment}
	recoveryState.contextOwnerID = subSessionID
	recoveryState.contextGeneration = 1
	recoveryState.contextReason = "child_assignment"
	recoveryState.ownerData = ownerData
	supervisor, err := r.session.newRecoverySupervisor(
		subDB, subSessionID, string(spec.ID), 1, recoveryState, nil, r.sender,
	)
	if err != nil {
		_ = subDB.Close()
		subCancel()
		return nil, fmt.Errorf("create child recovery supervisor: %w", err)
	}
	childPersist := supervisor.Persist
	childDiscard := supervisor.Discard
	supervisor.Persist = func(status recoverytypes.Status) error {
		if err := childPersist(status); err != nil {
			return err
		}
		parentCheckpoint := recoveryState.checkpoint(status)
		parentCheckpoint.ContextOwnerID = ""
		if err := sessiondb.SaveRecoveryCheckpoint(r.session.db, r.session.sessionID, parentCheckpoint); err != nil {
			return err
		}
		return nil
	}
	supervisor.Discard = func(id string) error {
		childErr := childDiscard(id)
		parentErr := sessiondb.DeleteRecoveryCheckpoint(r.session.db, id)
		if childErr != nil {
			return childErr
		}
		return parentErr
	}
	if err := supervisor.Persist(supervisor.CheckpointStatus()); err != nil {
		_ = supervisor.Cancel()
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), 1)
		_ = subDB.Close()
		subCancel()
		return nil, fmt.Errorf("persist child assignment: %w", err)
	}
	defer func() {
		if !launched {
			_ = supervisor.Cancel()
			r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), 1)
		}
	}()

	a.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)
	a.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		return tools.ForModel(agent.FilterToolsForModel(allToolDefs, spec.AllowedTools, spec.DeniedTools, spec.Category, true, a.Model), a.Model), nil
	}
	a.RequestBoundary = r.requestBoundary(handle, recoveryState, supervisor)
	r.setActiveConnection(spec.ID, connectionOf(spec))
	r.session.bindAgentControl(subCtx, a, spec.ControlTarget)
	r.session.bindAgentRuntime(subCtx, a, captypes.RuntimeActor{ID: string(spec.ID), ParentID: string(spec.ParentID), SessionID: subSessionID, AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools, SkillModes: spec.SkillModes}, spec.Name)

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
	if err := r.persistResult(handle, agent.SubAgentResult{
		AgentID: spec.ID, SessionID: subSessionID, Name: spec.Name,
		Category: spec.Category, Status: agent.SubAgentRunning,
	}); err != nil {
		r.mgr.MarkInterrupted(spec.ID)
		return nil, fmt.Errorf("persist running child: %w", err)
	}

	r.wg.Add(1)
	r.beginTurnChild(spec.ParentTurn)
	go func() {
		defer r.wg.Done()
		defer r.finishTurnChild(spec.ParentTurn)
		r.runSubAgentLoop(subCtx, handle, systemPrompt, userPrompt, supervisor, recoveryState, false)
	}()
	launched = true
	return handle, nil
}

// childTempDir is the child's private scratch directory, exported to its
// commands as CRUDE_TEMP_DIR and published in its prompt.
func (r *SubAgentRunner) childTempDir(id agent.AgentID) string {
	base := r.session.TempDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "agents", string(id))
}

// notifyParentOfRejectedWrite tells the parent that its running child was
// denied a write, so the parent can extend the child's scope or redirect it.
// Identical rejections within one run are published once.
func (r *SubAgentRunner) notifyParentOfRejectedWrite(ctx context.Context, spec agent.SubAgentSpec, handle *agent.SubAgentHandle, target, reason string) {
	store := r.session.CollaborationStore()
	if store == nil || spec.ParentID == "" {
		return
	}
	runID := ""
	if handle != nil {
		runID, _ = handle.RunIdentity()
	}
	digest := sha256.Sum256([]byte(target + "|" + reason))
	body := fmt.Sprintf("%s (%s, %s) was denied a write to %s: %s. Nothing was changed and it is still running; it was told to stay in scope or ask you. To extend its scope, read its claims and run with agent_inspect, then call agent_update_claims.",
		spec.Name, spec.Category, spec.ID, target, reason)
	_, err := store.Publish(context.WithoutCancel(ctx), collaboration.PublishRequest{
		SenderID: string(spec.ID), SenderName: spec.Name,
		IdempotencyKey: fmt.Sprintf("write-rejected:%s:%x", runID, digest[:12]),
		RecipientIDs:   []string{string(spec.ParentID)}, Origin: collaboration.OriginRuntime,
		Kind: collaboration.EventPeerNote, Body: body,
	})
	if err != nil {
		r.session.logger.Warn("COLLAB", "write_rejection_publish_failed", "agent_id", spec.ID, "error", err.Error())
	}
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

// ResumeRecovery creates a fresh volatile attempt for one durable child task.
// It is called only by the exact incident/generation-fenced session API. Every
// refusal returns its reason, so neither the human nor the parent is left with
// a Continue that silently does nothing.
func (r *SubAgentRunner) ResumeRecovery(parentCheckpoint sessiondb.RecoveryCheckpoint) error {
	if r == nil || r.session == nil || len(parentCheckpoint.OwnerData) == 0 {
		return errors.New("the child task has no recoverable owner record")
	}
	var owner childRecoveryOwner
	if err := json.Unmarshal(parentCheckpoint.OwnerData, &owner); err != nil ||
		string(owner.Spec.ID) != parentCheckpoint.Status.AgentID {
		return errors.New("the child task's owner record is unreadable or belongs to another agent")
	}
	spec := owner.Spec
	if err := validateChildReportPermission(spec); err != nil {
		r.session.logger.Warn("SUBAGENT", "report_permission_invalid", "agent_id", spec.ID, "error", err.Error())
		return fmt.Errorf("the child cannot report its result (%w); cancel it and launch a replacement", err)
	}
	spec.Policy = agent.ComposePolicy(safety.DefaultPolicy(), spec.PolicyName, spec.Category)
	// Child session identity is the stable agent ID in the current format.
	dbPath, err := r.subAgentDBPath(string(spec.ID))
	if err != nil || dbPath == "" {
		return fmt.Errorf("the child session database path is unavailable: %v", err)
	}
	subDB, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return fmt.Errorf("open the child session database: %w", err)
	}
	var recoveryAsync *fileio.AsyncCommandManager
	fail := func(err error) error {
		if recoveryAsync != nil {
			recoveryAsync.CloseAndWait()
		}
		_ = subDB.Close()
		return err
	}
	if err := sessiondb.InterruptActiveRecoveries(subDB, string(spec.ID)); err != nil {
		return fail(fmt.Errorf("settle the interrupted attempt: %w", err))
	}
	childCheckpoint, ok, err := sessiondb.LoadRecoveryCheckpoint(subDB, string(spec.ID), string(spec.ID))
	if err != nil {
		return fail(fmt.Errorf("load the child recovery checkpoint: %w", err))
	}
	if !ok || childCheckpoint.Status.ID != parentCheckpoint.Status.ID ||
		childCheckpoint.Status.Generation != parentCheckpoint.Status.Generation {
		return fail(errors.New("the child's durable checkpoint no longer matches this incident; inspect it again"))
	}
	childCheckpoint.Status.AgentID = string(spec.ID)
	if err := r.session.bindChildPlanStore(string(spec.ID), string(spec.ID), subDB); err != nil {
		return fail(fmt.Errorf("bind the child plan: %w", err))
	}
	if spec.ExecutionID != "" {
		e, err := r.session.executionService()
		if err != nil {
			return fail(fmt.Errorf("execution service: %w", err))
		}
		retained, err := e.Inspect(spec.ExecutionID)
		if err != nil {
			return fail(fmt.Errorf("inspect the child's execution root: %w", err))
		}
		if !retained.Ready || retained.Removed || retained.Root != spec.ExecutionRoot || retained.Owner.AgentID != string(spec.ID) {
			return fail(errors.New("the child's isolated execution root was removed or reassigned; cancel it and launch a replacement"))
		}
		for _, pin := range retained.Pins {
			if pin.AgentID == string(spec.ID) && pin.RunID == spec.ExecutionRunID {
				if err := r.session.ReleaseExecution(context.Background(), spec.ExecutionID, pin); err != nil {
					return fail(fmt.Errorf("release the previous execution pin: %w", err))
				}
			}
		}
	}
	childCheckpoint.ContextOwnerID = string(spec.ID)
	childCheckpoint.Archive = reconcileInterruptedTools(childCheckpoint.Archive, childCheckpoint.InFlightTools...)
	childCheckpoint.ActiveContext = reconcileInterruptedTools(childCheckpoint.ActiveContext, childCheckpoint.InFlightTools...)
	if store := r.session.CollaborationStore(); store != nil {
		claims, claimErr := store.Claims(context.Background(), string(spec.ID), true)
		if claimErr != nil {
			return fail(fmt.Errorf("read the child's claims: %w", claimErr))
		}
		// Parent-updated declarations (no run) replace the launch scope. Claims
		// still bound to the interrupted attempt are released by the runtime
		// takeover when this attempt registers; they never block continuation.
		declared := make([]string, 0, len(claims))
		for _, claim := range claims {
			if claim.RunID == "" {
				declared = append(declared, claim.Path)
			}
		}
		if len(declared) > 0 {
			spec.FileOwnership = declared
		}
	}
	if err := r.fileOwnership.ReplaceClaims(spec.ID, spec.Name, spec.FileOwnership); err != nil {
		return fail(fmt.Errorf("restore the child's file scope: %w", err))
	}
	updatedOwnerData, err := json.Marshal(childRecoveryOwner{Spec: spec, ProfileInstructions: owner.ProfileInstructions})
	if err != nil {
		return fail(err)
	}
	childCheckpoint.OwnerData = updatedOwnerData
	recoveryState := newRecoveryRunState(parentCheckpoint.PendingInput, &childCheckpoint)
	supervisor, err := r.session.newRecoverySupervisor(
		subDB, string(spec.ID), string(spec.ID), parentCheckpoint.Status.Generation,
		recoveryState, &parentCheckpoint.Status, r.sender,
	)
	if err != nil {
		return fail(errors.New("another attempt of this child is already active"))
	}
	childPersist, childDiscard := supervisor.Persist, supervisor.Discard
	supervisor.Persist = func(status recoverytypes.Status) error {
		if err := childPersist(status); err != nil {
			return err
		}
		parent := recoveryState.checkpoint(status)
		parent.ContextOwnerID = ""
		return sessiondb.SaveRecoveryCheckpoint(r.session.db, r.session.sessionID, parent)
	}
	supervisor.Discard = func(id string) error {
		childErr := childDiscard(id)
		parentErr := sessiondb.DeleteRecoveryCheckpoint(r.session.db, id)
		if childErr != nil {
			return childErr
		}
		return parentErr
	}
	parkResume := func(kind agent.RecoveryFailureKind, cause error) error {
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), parentCheckpoint.Status.Generation)
		status := supervisor.Park(agent.AttentionRecovery(kind, cause))
		parent := recoveryState.checkpoint(status)
		parent.ContextOwnerID = ""
		r.session.parkRecovery(parent)
		return fail(fmt.Errorf("the continuation could not start and the task is parked again (recovery_id %s, generation %d): %w", status.ID, status.Generation, cause))
	}

	var cfg *Config
	if r.session.runtimeProfile != nil {
		cfg = r.session.runtimeProfile.GetEffectiveConfig(r.orchestratorID)
	}
	if cfg == nil {
		cfg = r.effectiveCfg
	}
	if cfg == nil {
		r.session.mu.Lock()
		cfg = copyConfig(r.session.cfg)
		r.session.mu.Unlock()
	}
	if cfg == nil {
		return parkResume(agent.RecoveryFailureConfiguration, errors.New("child session configuration is unavailable"))
	}
	p, err := cfg.providerForCategory(childProviderConfig(spec))
	if err != nil {
		return parkResume(agent.RecoveryFailureConfiguration, err)
	}
	spec.Provider, spec.ProviderURL, spec.APIStandard = p.ID, p.BaseURL, p.StandardID()
	filteredTools := agent.FilterToolsForModel(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true, p.Model)
	live := agent.NewAgent(p, p.Model, r.session.logger)
	live.MediaReadPolicy = &tools.MediaReadPolicy{Model: p.Model, Standard: p.StandardID(), Modalities: slices.Clone(spec.ModelModalities)}
	live.WorkDir = childExecutionRoot(spec, r.session.WorkDir())
	live.GuidancePaths = append([]string(nil), spec.FileOwnership...)
	r.session.bindContinuity(live, subDB, string(spec.ID))
	r.session.bindGuidanceCache(live)
	live.MaxOutputTokens = spec.ModelMaxOutput
	if compactionCfg := cfg.GetCategoryConfig(CategoryAeron); compactionCfg.Model != "" {
		compactionProvider, compactionErr := cfg.providerForCategory(compactionCfg)
		if compactionErr != nil {
			return parkResume(agent.RecoveryFailureConfiguration, compactionErr)
		}
		live.CompactionProvider = compactionProvider
		live.CompactionMaxOutput = compactionCfg.ModelMaxOutput
	}
	live.CommandPolicy = safety.CommandPolicyFromSettings(cfg.Safety)
	if spec.ContextCompactionPercent != nil {
		live.ContextCompactionPercent = *spec.ContextCompactionPercent
	}
	if spec.ContextRetentionTokens != nil {
		live.CompactionRetentionTokens = *spec.ContextRetentionTokens
	}
	live.AgentID, live.TurnGeneration = spec.ID, parentCheckpoint.Status.Generation
	live.Depth, live.ParentID, live.Policy = spec.Depth, spec.ParentID, spec.Policy
	r.session.bindAgentPermissionPolicy(live)
	childTempDir := r.childTempDir(spec.ID)
	live.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
	r.session.observeCommandOutcomes(live.AsyncMgr, string(spec.ID))
	recoveryAsync = live.AsyncMgr
	if err := live.AsyncMgr.SetTempDir(childTempDir); err != nil {
		return parkResume(agent.RecoveryFailureStorage, err)
	}
	live.FilteredTools = tools.ForModel(filteredTools, p.Model)
	baseCtx := r.sessionCtx
	if spec.HistoryMessageID != "" {
		baseCtx = withHistoryScope(baseCtx, r.session.sessionID, spec.HistoryMessageID, string(spec.ID))
	}
	subCtx, subCancel := context.WithCancel(baseCtx)
	live.Ctx = subCtx
	blockCh := make(chan ttypes.BlockResponse, 8)
	handle := &agent.SubAgentHandle{
		Spec: spec, Agent: live, Cancel: subCancel, ResultCh: make(chan agent.SubAgentResult, 1),
		BlockCh: blockCh, StartedAt: parentCheckpoint.Status.StartedAt,
		SessionID: string(spec.ID), DB: subDB,
	}
	if handle.StartedAt.IsZero() {
		handle.StartedAt = time.Now()
	}
	registered, err := r.session.registerCollaborator(
		subCtx, string(spec.ID), spec.Name, handle.SessionID,
		string(spec.ParentID), spec.Category, subDB,
	)
	if err != nil {
		subCancel()
		return parkResume(agent.RecoveryFailureStorage, err)
	}
	handle.RuntimeEpoch = registered.RuntimeEpoch
	if store := r.session.CollaborationStore(); store != nil && handle.RuntimeEpoch > 0 {
		run, runErr := store.StartRun(context.Background(), collaboration.StartRunRequest{
			AgentID: string(spec.ID), RuntimeEpoch: handle.RuntimeEpoch,
			RequestID: spec.ParentToolCallID, Origin: collaboration.OriginPeer,
		})
		if runErr != nil {
			r.session.detachCollaborator(context.Background(), string(spec.ID))
			subCancel()
			return parkResume(agent.RecoveryFailureStorage, runErr)
		}
		handle.SetRunIdentity(run.ID, run.Version)
		live.RunID = run.ID
		if spec.ExecutionID != "" {
			service, err := r.session.executionService()
			if err != nil {
				return parkResume(agent.RecoveryFailureConfiguration, err)
			}
			if err := service.Pin(spec.ExecutionID, worktree.Owner{AgentID: string(spec.ID), RunID: run.ID}); err != nil {
				return parkResume(agent.RecoveryFailureStorage, err)
			}
			spec.ExecutionRunID = run.ID
			handle.Spec = spec
			data, err := json.Marshal(childRecoveryOwner{Spec: spec, ProfileInstructions: owner.ProfileInstructions})
			if err != nil {
				return parkResume(agent.RecoveryFailureStorage, err)
			}
			recoveryState.ownerData = data
			if err := supervisor.Persist(supervisor.CheckpointStatus()); err != nil {
				return parkResume(agent.RecoveryFailureStorage, err)
			}
		}

		if len(spec.FileOwnership) > 0 {
			if _, err := store.AcquireClaims(context.Background(), collaboration.AcquireClaimsRequest{
				AgentID: string(spec.ID), RunID: run.ID,
				RequestID: spec.ParentToolCallID, Paths: spec.FileOwnership,
			}); err != nil {
				r.session.detachCollaborator(context.Background(), string(spec.ID))
				subCancel()
				r.interruptSubAgentAttempt(handle)
				return parkResume(agent.RecoveryFailureStorage, err)
			}
		}
	}
	restoreChildReportArtifact(handle, recoveryState.archive)
	r.SetParentToolFn(r.session.buildToolFn(false, p, spec.ModelModalities))
	live.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		return tools.ForModel(agent.FilterToolsForModel(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true, live.Model), live.Model), nil
	}
	live.RequestBoundary = r.requestBoundary(handle, recoveryState, supervisor)
	r.setActiveConnection(spec.ID, connectionOf(spec))
	r.session.bindAgentControl(subCtx, live, spec.ControlTarget)
	r.session.bindAgentRuntime(subCtx, live, captypes.RuntimeActor{
		ID: string(spec.ID), ParentID: string(spec.ParentID), SessionID: handle.SessionID,
		AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools, SkillModes: spec.SkillModes,
	}, spec.Name)
	promptAllowed, promptDenied := agent.ModelToolSelection(spec.AllowedTools, spec.DeniedTools, spec.Model)
	systemPrompt, userPrompt := rootprompt.BuildChild(rootprompt.ChildOptions{
		Category: spec.Category, Name: spec.Name, ID: string(spec.ID), ParentID: string(spec.ParentID), ParentTurn: spec.ParentTurn,
		Description: spec.Description, Instructions: spec.Instructions, PolicyName: spec.PolicyName,
		AllowedTools: promptAllowed, DeniedTools: promptDenied, FileOwnership: spec.FileOwnership,
		ProfileInstructions: owner.ProfileInstructions, WorkDir: live.WorkDir,
		TempDir: childTempDir, Model: spec.Model,
	})
	if err := r.mgr.Register(handle); err != nil {
		r.interruptSubAgentAttempt(handle)
		r.session.detachCollaborator(context.Background(), string(spec.ID))
		subCancel()
		return parkResume(agent.RecoveryFailureConfiguration, err)
	}
	if err := r.persistResult(handle, agent.SubAgentResult{
		AgentID: spec.ID, SessionID: handle.SessionID, Name: spec.Name,
		Category: spec.Category, Status: agent.SubAgentRunning,
	}); err != nil {
		r.mgr.MarkInterrupted(spec.ID)
		r.interruptSubAgentAttempt(handle)
		r.session.detachCollaborator(context.Background(), string(spec.ID))
		subCancel()
		return parkResume(agent.RecoveryFailureStorage, err)
	}
	r.wg.Add(1)
	r.beginTurnChild(spec.ParentTurn)
	go func() {
		defer r.wg.Done()
		defer r.finishTurnChild(spec.ParentTurn)
		r.runSubAgentLoop(subCtx, handle, systemPrompt, userPrompt, supervisor, recoveryState, true)
	}()
	go func() {
		select {
		case <-r.sessionCtx.Done():
			handle.Cancel()
		case <-handle.ResultCh:
		}
	}()
	return nil
}

func restoreChildReportArtifact(handle *agent.SubAgentHandle, messages []provider.ChatMessage) {
	if handle == nil || handle.Agent == nil {
		return
	}
	reportCalls := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == "publish_report" {
				reportCalls[call.ID] = true
			}
		}
		if message.Role != "tool" || message.ToolError || !reportCalls[message.ToolCallID] {
			continue
		}
		var published collaboration.PublishReportResult
		if json.Unmarshal([]byte(message.ContentString()), &published) != nil ||
			published.Artifact.Kind != collaboration.ReportFinal ||
			published.Artifact.AuthorID != string(handle.Spec.ID) {
			continue
		}
		runID, _ := handle.RunIdentity()
		if runID == "" || published.Artifact.RunID != runID {
			continue // A previous attempt's final remains evidence, not this run's deliverable.
		}
		handle.SetReportArtifact(published.Artifact.ID, published.Artifact.Revision)
		handle.Agent.RestorePublishedReport()
		return
	}
}

func (r *SubAgentRunner) runSubAgentLoop(
	ctx context.Context,
	handle *agent.SubAgentHandle,
	systemPrompt, userPrompt string,
	supervisor *agent.Supervisor,
	recoveryState *recoveryRunState,
	resumed bool,
) {
	defer func() {
		if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
			handle.Agent.AsyncMgr.CloseAndWait()
		}
		r.releaseExecutionPin(handle.Spec, handle)
	}()
	done := false
	var terminalDone *agent.AgentEvent
	var terminalPublicationErr error
	defer r.session.detachCollaborator(context.Background(), string(handle.Spec.ID))
	defer r.forgetConnection(handle.Spec.ID)
	r.session.reconcileCollaborationAdmissions(handle.DB, string(handle.Spec.ID))

	// Send agent identity to TUI for tab labeling
	if r.sender != nil {
		r.sender.SendAgentEventWithID("sub_agent_started", map[string]string{
			"agent_id":       string(handle.Spec.ID),
			"agent_name":     handle.Spec.Name,
			"category":       handle.Spec.Category,
			"model":          handle.Spec.Model,
			"provider":       handle.Spec.Provider,
			"task":           handle.Spec.Description,
			"prompt":         childAssignmentDisplay(handle.Spec),
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
			stack := debug.Stack()
			r.session.logger.Error("SUBAGENT", "crash", "agent_id", handle.Spec.ID, "error", fmt.Sprintf("%v", rec))
			r.session.finishRecovery(string(handle.Spec.ID), supervisor.IncidentID(), supervisor.CheckpointStatus().Generation)
			status := supervisor.Park(agent.AttentionRecovery(
				agent.RecoveryFailurePanic,
				fmt.Errorf("child panic: %v\n\n%s", rec, stack),
			))
			checkpoint := recoveryState.checkpoint(status)
			checkpoint.ContextOwnerID = ""
			r.session.parkRecovery(checkpoint)
			r.interruptSubAgentAttempt(handle)
			r.mgr.MarkInterrupted(handle.Spec.ID)
			if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
				handle.Agent.AsyncMgr.CloseAndWait()
			}
			if handle.DB != nil {
				_ = handle.DB.Close()
			}
			return
		}

		if !done && ctx.Err() != nil {
			err := terminalPublicationErr
			if err == nil {
				err = r.finalizeSubAgent(handle, agent.SubAgentResult{
					AgentID: handle.Spec.ID, Name: handle.Spec.Name, Category: handle.Spec.Category,
					SessionID: handle.SessionID, Status: agent.SubAgentCancelled,
					Errors: []string{ctx.Err().Error()},
				}, supervisor.Cancel, 0)
			}
			if err != nil {
				r.session.finishRecovery(string(handle.Spec.ID), supervisor.IncidentID(), supervisor.CheckpointStatus().Generation)
				status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, err))
				checkpoint := recoveryState.checkpoint(status)
				checkpoint.ContextOwnerID = ""
				r.session.parkRecovery(checkpoint)
				r.interruptSubAgentAttempt(handle)
				r.mgr.MarkInterrupted(handle.Spec.ID)
				if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
					handle.Agent.AsyncMgr.CloseAndWait()
				}
				if handle.DB != nil {
					_ = handle.DB.Close()
				}
				return
			}
			if r.sender != nil {
				if terminalDone != nil {
					sendRuntimeEvent(r.sender, *terminalDone)
				} else {
					r.sender.SendAgentEventWithID("done", map[string]any{
						"content": "Cancelled.", "reason": "cancelled",
					}, string(handle.Spec.ID))
				}
			}
			done = true
			return
		}
		if !done {
			r.session.logger.Error("SUBAGENT", "unexpected_exit", "agent_id", handle.Spec.ID)
			r.session.finishRecovery(string(handle.Spec.ID), supervisor.IncidentID(), supervisor.CheckpointStatus().Generation)
			status := supervisor.Park(agent.AttentionRecovery(
				agent.RecoveryFailurePanic,
				errors.New("child exited unexpectedly before a natural finish"),
			))
			checkpoint := recoveryState.checkpoint(status)
			checkpoint.ContextOwnerID = ""
			r.session.parkRecovery(checkpoint)
			r.interruptSubAgentAttempt(handle)
			r.mgr.MarkInterrupted(handle.Spec.ID)
			if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
				handle.Agent.AsyncMgr.CloseAndWait()
			}
			if handle.DB != nil {
				_ = handle.DB.Close()
			}
			return
		}
	}()

	userMsgs := append([]provider.ChatMessage(nil), recoveryState.active...)
	if len(userMsgs) == 0 {
		assignment := provider.NewTextMessage("user", userPrompt)
		assignment.InternalID = recoveryState.pending.InternalID
		assignment.DisplayContent = childAssignmentDisplay(handle.Spec)
		assignment.Origin = provider.OriginAssignment
		userMsgs = []provider.ChatMessage{assignment}
	}
	handle.Agent.ContextArchive = recoveryArchiveMessages(recoveryState.archive)
	handle.Agent.FrozenPrefix = append([]provider.ChatMessage(nil), userMsgs...)
	contextGeneration := max(uint64(1), recoveryState.contextGeneration)
	turnGeneration := max(uint64(1), handle.Agent.TurnGeneration)
	if resumed {
		if err := supervisor.BeginRetry(); err != nil &&
			!supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
			_ = supervisor.Cancel()
			return
		}
		recoveryState.appendContinuation(rootprompt.RecoveryInterrupted)
		archive, active := recoveryState.contexts()
		userMsgs = active
		handle.Agent.ContextArchive = recoveryArchiveMessages(archive)
		handle.Agent.FrozenPrefix = append([]provider.ChatMessage(nil), userMsgs...)
		for {
			if err := supervisor.Persist(supervisor.CheckpointStatus()); err == nil {
				break
			} else if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
				return
			}
		}
	}
	var pendingCollaboration []collaboration.PendingDelivery
	defer func() {
		r.session.releaseCollaborationReservations(
			string(handle.Spec.ID), handle.RuntimeEpoch, pendingCollaboration,
			"child turn ended before context admission",
		)
	}()
	handle.Agent.PrepareRuntimeNotices = func() []provider.ChatMessage {
		if len(pendingCollaboration) == 0 {
			reserved, err := r.session.reserveCollaboration(ctx, string(handle.Spec.ID), handle.RuntimeEpoch, 20)
			if err != nil && !errors.Is(err, collaboration.ErrUnavailable) {
				r.session.logger.Warn("COLLAB", "child_reserve_failed", "agent_id", handle.Spec.ID, "error", err.Error())
			} else {
				pendingCollaboration = reserved
			}
		}
		messages := make([]provider.ChatMessage, 0, len(pendingCollaboration))
		for _, delivery := range pendingCollaboration {
			messages = append(messages, collaborationMessage(delivery))
		}
		return messages
	}
	handle.Agent.CommitRuntimeNotices = func(messages []provider.ChatMessage) error {
		if len(pendingCollaboration) == 0 {
			return nil
		}
		contextMessages := messages
		if len(contextMessages) > 0 && contextMessages[0].Role == "system" {
			contextMessages = contextMessages[1:]
		}
		runID, _ := handle.RunIdentity()
		committed, err := r.session.commitCollaborationAdmissions(
			handle.DB, handle.SessionID, string(handle.Spec.ID), string(handle.Spec.ID), runID,
			handle.RuntimeEpoch, recoveryState.contextRevision,
			collaborationDeltas(pendingCollaboration, contextMessages, string(handle.Spec.ID)), pendingCollaboration,
		)
		if err != nil {
			r.session.releaseCollaborationReservations(
				string(handle.Spec.ID), handle.RuntimeEpoch, pendingCollaboration,
				"child context admission failed",
			)
			pendingCollaboration = nil
			return err
		}
		if err := reconcileCommittedRecoveryContext(recoveryState, committed); err != nil {
			return err
		}
		contextGeneration = committed.Generation
		if r.sender != nil {
			for _, delivery := range pendingCollaboration {
				r.sender.SendAgentEventWithID("collaboration_message", delivery.Event, string(handle.Spec.ID))
			}
		}
		pendingCollaboration = nil
		return nil
	}
	handle.Agent.PrepareCompaction = func(source agent.CompactionSource) error {
		data := compactionLifecycleData(source.RequestID, source.Trigger, source.Role, 0, 0, source.BeforeTokens, 0)
		data["context_window"] = source.ContextWindow
		data["provider_id"] = source.ProviderID
		data["account_id"] = source.AccountID
		data["model_id"] = source.ModelID
		record := compactionRecordFromData(handle.SessionID, string(handle.Spec.ID), sessiondb.CompactionPreparing, data)
		archive, _ := recoveryState.contexts()
		recoveryState.snapshot(archive, source.Messages)
		_, active := recoveryState.contexts()
		state, err := sessiondb.PublishContext(handle.DB, sessiondb.ContextPublication{
			SessionID: handle.SessionID, AgentID: string(handle.Spec.ID), RuntimeAgentID: string(handle.Spec.ID), ExpectedRevision: recoveryState.contextRevisionValue(),
			Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: contextGeneration, Reason: "compaction_source", Messages: active}}, Compaction: &record,
		})
		if err == nil {
			err = reconcileCommittedRecoveryContext(recoveryState, state)
		}
		return err
	}
	handle.Agent.CommitCompaction = func(commit agent.CompactionCommit) error {
		data := compactionLifecycleData(commit.RequestID, commit.Trigger, commit.Role, compactionStageCount, compactionStageCount, commit.BeforeTokens, commit.AfterTokens)
		data["context_window"] = commit.ContextWindow
		data["provider_id"] = commit.ProviderID
		data["account_id"] = commit.AccountID
		data["model_id"] = commit.ModelID
		record := compactionRecordFromData(handle.SessionID, string(handle.Spec.ID), sessiondb.CompactionCompleted, data)
		state, err := sessiondb.PublishContext(handle.DB, sessiondb.ContextPublication{
			SessionID: handle.SessionID, AgentID: string(handle.Spec.ID), RuntimeAgentID: string(handle.Spec.ID), ExpectedRevision: recoveryState.contextRevisionValue(),
			Mutation: sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: contextGeneration + 1, Reason: "automatic_compaction", Messages: canonicalContextMessages(commit.Messages)}}, Compaction: &record, ArchivePrevious: true,
		})
		if err == nil {
			archive, _ := recoveryState.contexts()
			recoveryState.snapshot(archive, state.Messages)
			recoveryState.setContext(handle.SessionID, state.Revision, state.Generation, state.Reason)
			contextGeneration = state.Generation
		}
		return err
	}
	for {
		if handle.DB == nil {
			if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, errors.New("child database is unavailable"))) {
				return
			}
			continue
		}
		if err := sessiondb.BeginAgentTurn(handle.DB, handle.SessionID, string(handle.Spec.ID), turnGeneration, string(provider.OriginAssignment)); err != nil {
			if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
				return
			}
			continue
		}
		break
	}
	runID, _ := handle.RunIdentity()
	// Stop execution independently of the retained-input drain. The explicit
	// post-run join below owns the worker's final publication boundary.
	steeringCtx, stopSteering := context.WithCancel(context.WithoutCancel(ctx))
	runtimeMessages, runtimeMessagesDone, flushRuntime := r.session.humanSteeringMessages(
		steeringCtx, handle.DB, handle.SessionID, string(handle.Spec.ID), runID,
		handle.RuntimeEpoch, func() uint64 { return contextGeneration },
		handle.Spec.Model, r.sender,
	)
	defer func() { stopSteering(); <-runtimeMessagesDone }()
	handle.Agent.FlushRuntimeMessages = flushRuntime
	handle.Agent.Checkpoint = func(archive, active []provider.ChatMessage) error {
		current, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, string(handle.Spec.ID))
		if err != nil {
			return err
		}
		if found {
			if err := retainChildCheckpointSource(recoveryState, archive, active, current); err != nil {
				return err
			}
		} else {
			recoveryState.snapshot(archive, active)
		}
		recoveryState.mu.Lock()
		recoveryState.contextRevision = loadContextRevision(current, found)
		recoveryState.contextGeneration, recoveryState.contextReason = contextGeneration, "run_checkpoint"
		recoveryState.mu.Unlock()
		return persistRecoveryBoundary(ctx, supervisor)
	}
	handle.Agent.CheckpointTools = func(archive, active, inFlight []provider.ChatMessage) error {
		recoveryState.mu.Lock()
		recoveryState.inFlightTools = append([]provider.ChatMessage(nil), inFlight...)
		recoveryState.mu.Unlock()
		return handle.Agent.Checkpoint(archive, active)
	}
	childRunID, _ := handle.RunIdentity()
	eventSource := &runtimeEventSource{ownerID: handle.SessionID, agentID: string(handle.Spec.ID), runID: childRunID, runtimeEpoch: handle.RuntimeEpoch, generation: turnGeneration}
	childEmit := func(evt agent.AgentEvent) {
		evt = eventSource.bind(evt)
		if err := r.session.persistCompactionEvent(handle.DB, handle.SessionID, evt.AgentID, evt); err != nil {
			r.session.logger.Warn("COMPACT", "child_lifecycle_persist_failed", "agent_id", evt.AgentID, "error", err.Error())
		}
		if evt.Type == "done" {
			copy := evt
			terminalDone = &copy
		} else if r.sender != nil {
			sendRuntimeEvent(r.sender, evt)
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
		if evt.Type == "done" {
			if data, ok := evt.Data.(map[string]any); ok {
				if reason, ok := data["reason"].(string); ok {
					handle.SetTermReason(reason)
				}
			}
		}
	}
	prepareAttempt := func() (string, error) {
		var cfg *Config
		if r.session.runtimeProfile != nil {
			cfg = r.session.runtimeProfile.GetEffectiveConfig(r.orchestratorID)
		}
		if cfg == nil {
			cfg = r.effectiveCfg
		}
		if cfg == nil {
			r.session.mu.Lock()
			cfg = copyConfig(r.session.cfg)
			r.session.mu.Unlock()
		}
		if cfg == nil {
			return "", errors.New("child session configuration is unavailable")
		}
		currentProvider, err := cfg.providerForCategory(childProviderConfig(handle.Spec))
		if err != nil {
			return "", err
		}
		handle.Agent.Provider, handle.Agent.Model = currentProvider, currentProvider.Model
		handle.Spec.Provider = currentProvider.ID
		if compactionCfg := cfg.GetCategoryConfig(CategoryAeron); compactionCfg.Model != "" {
			compactionProvider, compactionErr := cfg.providerForCategory(compactionCfg)
			if compactionErr != nil {
				return "", compactionErr
			}
			handle.Agent.CompactionProvider = compactionProvider
			handle.Agent.CompactionMaxOutput = compactionCfg.ModelMaxOutput
		}
		return systemPrompt, nil
	}
	runResult := handle.Agent.RunSupervised(
		ctx, userMsgs, systemPrompt, childEmit, handle.BlockCh, handle.SessionID, runtimeMessages,
		agent.SupervisedRunOptions{
			Supervisor: supervisor,
			Prepare:    prepareAttempt,
			Snapshot:   recoveryState.snapshot,
			OnAttention: func(failure agent.RecoveryFailure) agent.AttentionDisposition {
				switch failure.Kind {
				case agent.RecoveryFailureConfiguration, agent.RecoveryFailureGuidance:
					return agent.AttentionParkAttempt
				default:
					return agent.AttentionWait
				}
			},
			DeferSettlement: true,
		},
	)
	agentMsgs := runResult.Archive
	stopSteering()
	<-runtimeMessagesDone
	active := runResult.Active
	terminalStatus := supervisor.CheckpointStatus()
	if runResult.State == agent.SupervisedRunParked {
		terminalStatus = runResult.Incident
	}
	var terminalCheckpoint sessiondb.RecoveryCheckpoint
	var generation uint64
	var reason string
	for {
		recoveryState.snapshot(agentMsgs, active)
		current, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, string(handle.Spec.ID))
		if err == nil && !found {
			err = fmt.Errorf("child terminal context is unavailable")
		}
		if err == nil {
			err = reconcileCommittedRecoveryContext(recoveryState, current)
		}
		var transcriptAppend []sessiondb.ContextMessageDelta
		if err == nil {
			agentMsgs, active = recoveryState.contexts()
			var archive, context []provider.ChatMessage
			archive, context, transcriptAppend, err = childTerminalPublication(handle, agentMsgs, active, time.Now())
			if err == nil {
				agentMsgs, active = archive, context
				recoveryState.snapshot(agentMsgs, active)
			}
		}
		var state sessiondb.ContextState
		if err == nil {
			generation, reason = current.Generation, "session_start"
			if contextCheckpointContent(active) != "" {
				generation, reason = max(uint64(2), generation), "automatic_compaction"
			}
			terminalCheckpoint = recoveryState.checkpoint(terminalStatus)
			state, err = sessiondb.PublishContext(handle.DB, sessiondb.ContextPublication{
				SessionID: handle.SessionID, AgentID: string(handle.Spec.ID), RuntimeAgentID: string(handle.Spec.ID), ExpectedRevision: current.Revision,
				Mutation:         sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: generation, Reason: reason, Messages: active}},
				TranscriptAppend: transcriptAppend, Recovery: &terminalCheckpoint,
			})
		}
		if err == nil {
			recoveryState.setContext(handle.SessionID, state.Revision, state.Generation, state.Reason)
			terminalPublicationErr = nil
			break
		}
		terminalPublicationErr = err
		var conflict *sessiondb.ContextRevisionConflictError
		if errors.As(err, &conflict) {
			continue // Reconcile the actual new source; do not park stale bytes.
		}
		if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
			return
		}
	}
	if runResult.State == agent.SupervisedRunParked {
		committedCheckpoint := terminalCheckpoint
		committedCheckpoint.ContextOwnerID = handle.SessionID
		committedCheckpoint.ContextRevision = recoveryState.contextRevision
		committedCheckpoint.ContextGeneration = generation
		committedCheckpoint.ContextReason = reason
		committedCheckpoint.ActiveContext = append([]provider.ChatMessage(nil), active...)
		r.session.parkRecovery(committedCheckpoint)
		if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
			handle.Agent.AsyncMgr.CloseAndWait()
		}
		r.interruptSubAgentAttempt(handle)
		r.mgr.MarkInterrupted(handle.Spec.ID)
		done = true
		if handle.DB != nil {
			_ = handle.DB.Close()
		}
		return
	}
	result := r.buildSubAgentResult(handle, agentMsgs)

	if result.Status != agent.SubAgentCancelled && !handle.ReportDone() {
		result.Errors = append(result.Errors, "required final report was not published")
		if result.Status == agent.SubAgentCompleted {
			result.Status = agent.SubAgentFailedPolicy
		}
		r.session.logger.Warn("SUBAGENT", "missing_final_report", "agent_id", handle.Spec.ID, "name", handle.Spec.Name)
	}

	result.HistoryMessageID = handle.Spec.HistoryMessageID
	if id, revision, ok := handle.ReportArtifact(); ok {
		result.ReportID, result.ReportRevision = id, revision
	}
	runID, runVersion := handle.RunIdentity()
	terminalState, runState := sessiondb.TurnFailed, collaboration.RunFailed
	if result.Status == agent.SubAgentCompleted {
		terminalState, runState = sessiondb.TurnCompleted, collaboration.RunSucceeded
	}
	if result.Status == agent.SubAgentCancelled {
		terminalState, runState = sessiondb.TurnInterrupted, collaboration.RunCancelled
	}
	intent := terminalSettlementIntent{SessionID: handle.SessionID, AgentID: string(handle.Spec.ID), OwnerID: string(handle.Spec.ID), Generation: handle.Agent.TurnGeneration, TurnState: terminalState, Child: &result,
		Run: collaboration.FinishRunRequest{RunID: runID, AgentID: string(handle.Spec.ID), RuntimeEpoch: handle.RuntimeEpoch, ExpectedVersion: runVersion, State: runState, Outcome: string(result.Status), ContextCommitID: result.HistoryMessageID}}
	settle := func() error {
		if result.Status == agent.SubAgentCancelled || ctx.Err() != nil {
			return supervisor.Cancel()
		}
		return supervisor.Resolve()
	}
	terminalStopPublished := result.Status == agent.SubAgentCancelled
	for {
		intent.Stopped = ctx.Err() != nil
		err := recoveryState.captureTerminalSettlement(intent)
		if err == nil {
			err = supervisor.Persist(supervisor.CheckpointStatus())
		}
		if ctx.Err() != nil && !terminalStopPublished {
			if err == nil {
				err = publishChildTerminalStop(handle, recoveryState, supervisor)
			}
			if err == nil {
				terminalStopPublished = true
			}
		}
		if err == nil {
			err = r.finalizeSubAgent(handle, result, settle, 0)
		}
		if err == nil {
			done = true
			r.session.finishRecovery(string(handle.Spec.ID), supervisor.IncidentID(), supervisor.CheckpointStatus().Generation)
			if terminalDone != nil && r.sender != nil {
				sendRuntimeEvent(r.sender, *terminalDone)
			}
			return
		}
		terminalPublicationErr = err
		waitCtx := ctx
		if ctx.Err() != nil {
			// Stop execution, not ownership of the exact terminal publication.
			waitCtx = r.sessionCtx
		}
		if !supervisor.Await(waitCtx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) && r.sessionCtx.Err() != nil {
			return
		}
	}
}

func publishChildTerminalStop(handle *agent.SubAgentHandle, run *recoveryRunState, supervisor *agent.Supervisor) error {
	current, found, err := sessiondb.LoadContextState(handle.DB, handle.SessionID, string(handle.Spec.ID))
	if err != nil {
		return err
	}
	if !found {
		return errors.New("child terminal source is unavailable")
	}
	if err := reconcileCommittedRecoveryContext(run, current); err != nil {
		return err
	}
	archive, active := run.contexts()
	boundaryID := "turn-stopped:" + supervisor.IncidentID()
	if !slices.ContainsFunc(active, func(message provider.ChatMessage) bool { return message.InternalID == boundaryID }) {
		boundary := provider.NewTextMessage("user", rootprompt.TurnStoppedBoundary())
		boundary.InternalID, boundary.Origin, boundary.Visibility = boundaryID, provider.OriginTurnStopped, provider.VisibilityContextOnly
		archive, active = append(archive, boundary), append(active, boundary)
	}
	archive, active, deltas, err := childTerminalPublication(handle, archive, active, time.Now())
	if err != nil {
		return err
	}
	run.snapshot(archive, active)
	checkpoint := run.checkpoint(supervisor.CheckpointStatus())
	state, err := sessiondb.PublishContext(handle.DB, sessiondb.ContextPublication{
		SessionID: handle.SessionID, AgentID: string(handle.Spec.ID), RuntimeAgentID: string(handle.Spec.ID), ExpectedRevision: current.Revision,
		Mutation:         sessiondb.ContextMutation{Replace: &sessiondb.ContextReplacement{Generation: current.Generation, Reason: "explicit_terminal_stop", Messages: active}},
		TranscriptAppend: deltas, Recovery: &checkpoint,
	})
	if err != nil {
		return err
	}
	run.setContext(handle.SessionID, state.Revision, state.Generation, state.Reason)
	return supervisor.Persist(supervisor.CheckpointStatus())
}

func childTerminalPublication(
	handle *agent.SubAgentHandle,
	archive, active []provider.ChatMessage,
	createdAt time.Time,
) ([]provider.ChatMessage, []provider.ChatMessage, []sessiondb.ContextMessageDelta, error) {
	archive = append([]provider.ChatMessage(nil), archive...)
	active = append([]provider.ChatMessage(nil), active...)
	existing := make(map[string]sessiondb.MessageWithMeta)
	if handle != nil && handle.DB != nil {
		rows, err := sessiondb.LoadMessagesWithMeta(handle.DB)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, row := range rows {
			if row.Msg.InternalID != "" {
				existing[row.Msg.InternalID] = row
			}
		}
	}
	originalKeys := make(map[string][]int)
	for index, message := range archive {
		if message.InternalID == "" {
			encoded, _ := json.Marshal(canonicalContextMessage(message))
			originalKeys[string(encoded)] = append(originalKeys[string(encoded)], index)
		}
	}
	for index := range archive {
		if archive[index].InternalID == "" {
			archive[index].InternalID = newMessageID()
		}
	}
	for activeIndex := range active {
		if active[activeIndex].InternalID != "" {
			continue
		}
		encoded, _ := json.Marshal(canonicalContextMessage(active[activeIndex]))
		key := string(encoded)
		matches := originalKeys[key]
		if len(matches) > 0 {
			archiveIndex := matches[0]
			originalKeys[key] = matches[1:]
			active[activeIndex].InternalID = archive[archiveIndex].InternalID
		}
		if active[activeIndex].InternalID == "" {
			active[activeIndex].InternalID = newMessageID()
		}
	}

	rawToolArgs := toolCallArgsIndex(archive)
	toolNames := make(map[string]string)
	toolArgs := make(map[string]string)
	for _, message := range archive {
		for _, call := range message.ToolCalls {
			toolNames[call.ID] = call.Function.Name
			if params, err := provider.ToolCallParams(call); err == nil {
				toolArgs[call.ID] = agent.FormatToolArgs(params)
			}
		}
	}
	turnIdentity := ttypes.TurnIdentity{Generation: 1}
	agentID := ""
	if handle != nil {
		agentID = string(handle.Spec.ID)
		turnIdentity.ProviderID = handle.Spec.Provider
		turnIdentity.AccountID = handle.Spec.AccountID
		turnIdentity.ModelID = handle.Spec.Model
	}
	assignmentSeen := len(existing) > 0
	baseSystemSkipped := false
	deltas := make([]sessiondb.ContextMessageDelta, 0, len(archive))
	for index, message := range archive {
		if message.Role == "system" && !baseSystemSkipped {
			baseSystemSkipped = true
			continue
		}
		if message.Role == "system" || isProviderControlMessage(message) {
			continue
		}
		if _, ok := existing[message.InternalID]; ok {
			continue
		}
		displayRole := message.Role
		if message.Role == "user" && !assignmentSeen {
			displayRole, assignmentSeen = "assignment", true
		}
		meta := sessiondb.DisplayMeta{DisplayRole: displayRole}
		if message.Role == "tool" {
			meta.ToolName, meta.ToolArgs = toolNames[message.ToolCallID], toolArgs[message.ToolCallID]
			if message.ToolError {
				meta.ToolStatus = "error"
			} else {
				meta.ToolStatus = "success"
			}
		}
		if message.Role == "assistant" || message.Role == "tool" {
			meta.TurnGeneration = turnIdentity.Generation
			meta.ProviderID, meta.AccountID, meta.ModelID = turnIdentity.ProviderID, turnIdentity.AccountID, turnIdentity.ModelID
		}
		stamp := createdAt.Add(time.Duration(index) * time.Nanosecond)
		meta = canonicalizeDisplayMeta(message, meta, rawToolArgs[message.ToolCallID], index, stamp)
		deltas = append(deltas, contextDelta(message, meta, 1, stamp, agentID))
	}
	return canonicalContextMessages(archive), canonicalContextMessages(active), deltas, nil
}

func (r *SubAgentRunner) interruptSubAgentAttempt(handle *agent.SubAgentHandle) {
	if r == nil || r.session == nil || handle == nil {
		return
	}
	if checkpoint, found, err := sessiondb.LoadRecoveryCheckpoint(handle.DB, handle.SessionID, string(handle.Spec.ID)); err == nil && found && len(checkpoint.TerminalSettlement) != 0 {
		return
	}
	runID, runVersion := handle.RunIdentity()
	store := r.session.CollaborationStore()
	if store == nil || runID == "" {
		return
	}
	finished, err := finishCollaborationRun(store, collaboration.FinishRunRequest{
		RunID: runID, AgentID: string(handle.Spec.ID), RuntimeEpoch: handle.RuntimeEpoch,
		ExpectedVersion: runVersion, State: collaboration.RunInterrupted,
		Outcome: "recovery pending", ContextCommitID: handle.Spec.HistoryMessageID,
	})
	if err != nil {
		r.session.logger.Warn("COLLAB", "child_recovery_interrupt_failed", "run_id", runID, "error", err.Error())
		return
	}
	handle.SetRunVersion(finished.Version)
	if handle.Agent != nil {
		r.revokeChildCapabilityRun(handle, runID)
	}
}

// finalizeSubAgent is the sole terminal delivery path for successful, failed,
// cancelled, panicked, and unexpected child exits. Persistence alone is not a
// completion signal: both the UI event and parent wake are mandatory.
func (r *SubAgentRunner) finalizeSubAgent(
	handle *agent.SubAgentHandle,
	result agent.SubAgentResult,
	settle func() error,
	settlementEpoch uint64,
) error {
	if handle == nil || handle.DB == nil {
		return errors.New("child terminal handle or ledger is unavailable")
	}
	if runID, _ := handle.RunIdentity(); runID != "" && r.session.CollaborationStore() == nil {
		return errors.New("child terminal collaboration store is unavailable")
	}
	if handle.Agent != nil && handle.Agent.AsyncMgr != nil {
		handle.Agent.AsyncMgr.CloseAndWait()
	}
	if result.HistoryMessageID == "" {
		result.HistoryMessageID = handle.Spec.HistoryMessageID
	}
	if reportID, revision, ok := handle.ReportArtifact(); ok {
		result.ReportID, result.ReportRevision = reportID, revision
	}
	if handle.DB != nil {
		turnState := sessiondb.TurnFailed
		switch result.Status {
		case agent.SubAgentCompleted:
			turnState = sessiondb.TurnCompleted
		case agent.SubAgentCancelled:
			turnState = sessiondb.TurnInterrupted
		}
		generation := uint64(1)
		if handle.Agent != nil {
			generation = max(uint64(1), handle.Agent.TurnGeneration)
		}
		if err := sessiondb.FinishAgentTurn(handle.DB, handle.SessionID, string(handle.Spec.ID), generation, turnState); err != nil {
			r.session.logger.Warn("SUBAGENT", "turn_state_finish_failed", "agent_id", handle.Spec.ID, "error", err.Error())
			return err
		}
	}
	if err := r.persistResult(handle, result); err != nil {
		return err
	}
	runID, runVersion := handle.RunIdentity()
	if store := r.session.CollaborationStore(); store != nil && runID != "" {
		runState := collaboration.RunFailed
		outcome := string(result.Status)
		switch result.Status {
		case agent.SubAgentCompleted:
			runState = collaboration.RunSucceeded
		case agent.SubAgentCancelled:
			runState = collaboration.RunCancelled
		case agent.SubAgentCrashed:
			runState = collaboration.RunInterrupted
		}
		finished, err := finishCollaborationRun(store, collaboration.FinishRunRequest{
			RunID: runID, AgentID: string(handle.Spec.ID),
			RuntimeEpoch: handle.RuntimeEpoch, ExpectedVersion: runVersion,
			State: runState, Outcome: outcome, ContextCommitID: result.HistoryMessageID,
			SettlementEpoch: settlementEpoch,
		})
		if err != nil {
			r.session.logger.Warn("COLLAB", "child_run_finish_failed",
				"run_id", runID, "error", err.Error())
			return err
		} else {
			handle.SetRunVersion(finished.Version)
			if handle.Agent != nil {
				r.revokeChildCapabilityRun(handle, runID)
			}
			if handle.Spec.ParentID != "" {
				var references []collaboration.Reference
				if result.HistoryMessageID != "" {
					references = append(references, collaboration.Reference{Kind: "history_message", ID: result.HistoryMessageID})
				}
				if result.ReportID != "" {
					references = append(references, collaboration.Reference{Kind: "report", ID: result.ReportID, Revision: result.ReportRevision})
				}
				_, publishErr := store.Publish(context.Background(), collaboration.PublishRequest{
					SenderID: string(handle.Spec.ID), SenderName: handle.Spec.Name,
					IdempotencyKey: "run-outcome:" + runID,
					RecipientIDs:   []string{string(handle.Spec.ParentID)},
					Origin:         collaboration.OriginRuntime, Kind: collaboration.EventRunOutcome,
					Body:  fmt.Sprintf("%s · %s", handle.Spec.Name, outcome),
					RunID: runID, RequestID: handle.Spec.ParentToolCallID,
					References: references,
				})
				if publishErr != nil {
					r.session.logger.Warn("COLLAB", "child_outcome_publish_failed",
						"run_id", runID, "error", publishErr.Error())
					return publishErr
				}
			}
		}
	}
	if handle.DB != nil {
		if err := sessiondb.CloseSession(handle.DB, handle.SessionID); err != nil {
			return err
		}
	}
	if settle != nil {
		if err := settle(); err != nil {
			return err
		}
	}
	if handle.DB != nil {
		if err := handle.DB.Close(); err != nil {
			return err
		}
	}
	if r.sender != nil {
		r.sender.SendAgentEventWithID("sub_agent_completed", map[string]string{
			"agent_id":        string(handle.Spec.ID),
			"agent_name":      handle.Spec.Name,
			"category":        handle.Spec.Category,
			"status":          string(result.Status),
			"report_id":       result.ReportID,
			"report_revision": fmt.Sprint(result.ReportRevision),
		}, string(handle.Spec.ID))
	}
	handle.ResultCh <- result
	r.mgr.MarkFinished(handle.Spec.ID, result)
	return nil
}

func (r *SubAgentRunner) buildSubAgentResult(handle *agent.SubAgentHandle, msgs []provider.ChatMessage) agent.SubAgentResult {
	status := agent.SubAgentCompleted
	if ctx := handle.Agent.Ctx; ctx != nil && ctx.Err() != nil {
		status = agent.SubAgentCancelled
	}

	switch handle.TermReason() {
	case "error", "stop_condition_met", "context_limit_reached":
		if status == agent.SubAgentCompleted {
			status = agent.SubAgentFailedError
		}
	case "cancelled":
		status = agent.SubAgentCancelled
	case "missing_final_report":
		if status == agent.SubAgentCompleted {
			status = agent.SubAgentFailedPolicy
		}
	}
	if status == agent.SubAgentCompleted && !handle.ReportDone() {
		status = agent.SubAgentFailedPolicy
	}

	gist := ""
	logPath := handle.SessionID
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

func (r *SubAgentRunner) buildProxiedToolFn(spec agent.SubAgentSpec, blockCh chan ttypes.BlockResponse, handle *agent.SubAgentHandle) func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	root := childExecutionRoot(spec, r.session.WorkDir())
	privateDir := r.childTempDir(spec.ID)
	var turnProvider *provider.Provider
	if handle != nil && handle.Agent != nil {
		turnProvider = handle.Agent.Provider
	}
	nativeFn := r.session.buildToolFnAtRoot(false, turnProvider, spec.ModelModalities, root)
	return func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
		if !agent.PolicyAllows(spec.Policy, tool) {
			return tools.ToolResult{}, fmt.Errorf("[POLICY BLOCK] Tool %q is not allowed for this agent. Your policy is %q", tool, spec.PolicyName)
		}
		resolveRelativeParamsAtRoot(root, tool, params)
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
			// The child's own temporary directory is private scratch space: it is
			// never shared work, so it needs neither ownership scope nor claims.
			requested := writeToolTargets(tool, params)
			targets := sharedWriteTargets(requested, privateDir)
			if len(requested) == 0 || len(targets) > 0 {
				// A scoped child writes only inside its current committed scope,
				// which its parent may replace while it runs.
				if len(spec.FileOwnership) > 0 {
					for _, target := range targets {
						if !r.fileOwnership.WriteScopeAllows(spec.ID, target) {
							scope := r.fileOwnership.WriteClaimsByAgent(spec.ID)
							r.notifyParentOfRejectedWrite(ctx, spec, handle, target, fmt.Sprintf("outside its file scope (%s)", strings.Join(scope, ", ")))
							return tools.ToolResult{}, fmt.Errorf("write to %q was rejected: it is outside your file scope (%s). Nothing was changed and your run continues. Work within your scope, use your temporary directory (%s) for scratch files, or ask your parent with agent_send to extend your scope via agent_update_claims", target, strings.Join(scope, ", "), privateDir)
						}
					}
				}
				if err := r.fileOwnership.CommitClaimAll(spec.ID, spec.Name, targets, true, func() error {
					store := r.session.CollaborationStore()
					if store == nil {
						return errors.New("workspace collaboration runtime is unavailable")
					}
					participant, err := store.Agent(ctx, string(spec.ID))
					if err != nil {
						return err
					}
					identity, err := collaborationExecutionIdentity(ctx)
					if err != nil {
						return err
					}
					runID := identity.RunID
					if handle != nil {
						runID, _ = handle.RunIdentity()
					}
					if runID == "" || participant.CurrentRunID != runID {
						return collaboration.ErrStale
					}
					_, err = store.AcquireClaims(ctx, collaboration.AcquireClaimsRequest{
						AgentID: string(spec.ID), RunID: runID,
						RequestID: identity.ToolCallID, Paths: targets,
					})
					return err
				}); err != nil {
					// Claims are published only after every target has passed the
					// complete preflight, so a rejected effect leaves no claim behind.
					r.notifyParentOfRejectedWrite(ctx, spec, handle, strings.Join(targets, ", "), err.Error())
					return tools.ToolResult{}, fmt.Errorf("write was rejected: %w. Nothing was changed and your run continues. Coordinate with the owning agent or ask your parent with agent_send", err)
				}
				releaseWrite, err := r.fileOwnership.BeginWrite(spec.ID, targets)
				if err != nil {
					return tools.ToolResult{}, fmt.Errorf("write was rejected: %w (your scope changed during this call). Nothing was changed and your run continues", err)
				}
				defer releaseWrite()
				for _, target := range targets {
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
		}

		if tool == "exec_command" || tool == "write_stdin" {
			if handle == nil || handle.Agent == nil {
				return tools.ToolResult{}, fmt.Errorf("child command manager unavailable")
			}
			return executeCodexCommand(ctx, handle.Agent.AsyncMgr, root, tool, params, handle.Agent.CommandPolicy)
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

		return nativeFn(withHistoryActor(ctx, string(spec.ID)), tool, params)
	}
}

// sharedWriteTargets drops targets inside the agent's private temporary
// directory. Those paths belong to no shared scope, so they are neither checked
// against nor claimed in the workspace ownership ledger.
func sharedWriteTargets(targets []string, privateDir string) []string {
	if privateDir == "" {
		return targets
	}
	shared := make([]string, 0, len(targets))
	for _, target := range targets {
		if !pathInside(privateDir, target) {
			shared = append(shared, target)
		}
	}
	return shared
}

func pathInside(root, target string) bool {
	root, target = normalizeOwnershipTarget(root), normalizeOwnershipTarget(target)
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func normalizeOwnershipTarget(path string) string {
	path = strings.TrimSpace(path)
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
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

func (r *SubAgentRunner) persistResult(_ *agent.SubAgentHandle, result agent.SubAgentResult) error {
	if r.session.db == nil {
		return errors.New("parent session database is unavailable")
	}
	if err := sessiondb.InsertSubAgentResult(r.session.db, subAgentResultRow(r.session.sessionID, result)); err != nil {
		r.session.logger.Error("SUBAGENT", "persist_result_failed", "agent_id", result.AgentID, "error", err.Error())
		return err
	}
	return nil
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
		spec.SkillModes = maps.Clone(catCfg.SkillModes)
		if spec.ModelContextWindow == 0 && catCfg.ModelContextWindow > 0 {
			spec.ModelContextWindow = catCfg.ModelContextWindow
		}
		if spec.ModelMaxOutput == 0 && catCfg.ModelMaxOutput > 0 {
			spec.ModelMaxOutput = catCfg.ModelMaxOutput
		}
		if spec.ContextCompactionPercent == nil && catCfg.ContextCompactionPercent != nil {
			spec.ContextCompactionPercent = cloneIntPtr(catCfg.ContextCompactionPercent)
		}
		if spec.ContextRetentionTokens == nil && catCfg.ContextRetentionTokens != nil {
			spec.ContextRetentionTokens = cloneIntPtr(catCfg.ContextRetentionTokens)
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
			if len(ap.SkillModes) > 0 {
				if spec.SkillModes == nil {
					spec.SkillModes = make(map[string]string)
				}
				maps.Copy(spec.SkillModes, ap.SkillModes)
			}
			if len(spec.FileOwnership) == 0 && len(ap.FileOwnership) > 0 {
				spec.FileOwnership = slices.Clone(ap.FileOwnership)
			}
			if ap.ContextCompactionPercent != nil {
				spec.ContextCompactionPercent = cloneIntPtr(ap.ContextCompactionPercent)
			}
			if ap.ContextRetentionTokens != nil {
				spec.ContextRetentionTokens = cloneIntPtr(ap.ContextRetentionTokens)
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
		if spec.ContextRetentionTokens == nil && categoryCfg.ContextRetentionTokens != nil {
			spec.ContextRetentionTokens = cloneIntPtr(categoryCfg.ContextRetentionTokens)
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
	store, err := r.session.childPlanStore(string(handle.Spec.ID), handle.SessionID, handle.DB)
	if err != nil {
		return tools.ToolResult{}, err
	}
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
			cwd = handle.Agent.WorkDir
		}
		timeout := tools.GetIntArgAny(params, "timeout_seconds", 0)
		info, err := mgr.Start(handleName, command, cwd, timeout)
		if err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("async_execute_command: %w", err)
		}
		return tools.ToolResult{Content: FormatCommandStart(*info)}, nil

	case "check_async_command":
		handleName := getStrParam(params, "handle")
		if handleName == "" {
			return tools.ToolResult{}, fmt.Errorf("check_async_command: missing 'handle'")
		}
		info, ok := mgr.Check(handleName)
		if !ok {
			return tools.ToolResult{Content: fmt.Sprintf("Async command [%s] not found — it may have already been cleaned up or never started.", handleName)}, nil
		}
		return tools.ToolResult{Content: FormatCommandStatus(*info)}, nil

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
		maxBytes := tools.GetIntArgAny(params, "max_bytes", fileio.DefaultOutputChunkBytes)
		page, err := mgr.ReadOutputPage(handleName, sinceByte, maxBytes)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("read_async_output: %w", err)
		}
		return tools.ToolResult{Content: FormatCommandPage(page)}, nil

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
	if !r.session.YoloEnabled() {
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
		cwd = handle.Agent.WorkDir
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
	if arr, ok := val.([]string); ok {
		return append([]string(nil), arr...)
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
