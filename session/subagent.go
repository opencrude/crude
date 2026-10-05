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
	"encoding/hex"

	"crude/agent"
	"crude/codexio"
	"crude/collaboration"
	"crude/fileio"
	"crude/plan"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	ttypes "crude/types"
	recoverytypes "crude/types/recovery"
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
}

type childRecoveryOwner struct {
	Spec                agent.SubAgentSpec `json:"spec"`
	ProfileInstructions string             `json:"profile_instructions,omitempty"`
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

	profileInstructions := r.resolveProfile(profileName, category, &spec)
	for index, path := range spec.FileOwnership {
		if path != "" && !filepath.IsAbs(path) {
			spec.FileOwnership[index] = filepath.Join(r.session.WorkDir(), path)
		}
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

	if len(spec.FileOwnership) > 0 {
		if err := r.fileOwnership.PreClaim(spec.ID, spec.Name, spec.FileOwnership); err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("Error: %s", err)}, fmt.Errorf("%s", err)
		}
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
	})
	return tools.ToolResult{Content: string(resultJSON)}, nil
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
	if r.session.db == nil {
		_ = subDB.Close()
		return nil, errors.New("parent session database is unavailable")
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
	a.AgentID = spec.ID
	a.TurnGeneration = 1
	a.Depth = spec.Depth
	a.ParentID = spec.ParentID
	a.Policy = spec.Policy
	r.session.bindAgentPermissionPolicy(a)
	childTempDir := filepath.Join(r.session.TempDir(), "agents", string(spec.ID))
	a.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
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
	systemPrompt, userPrompt := rootprompt.BuildChild(rootprompt.ChildOptions{
		Category: spec.Category, Name: spec.Name, ID: string(spec.ID), ParentID: string(spec.ParentID), ParentTurn: spec.ParentTurn,
		Description: spec.Description, Instructions: spec.Instructions, PolicyName: spec.PolicyName,
		AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools, FileOwnership: spec.FileOwnership,
		ProfileInstructions: profileInstructions, WorkDir: r.session.WorkDir(),
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
		DisplayText: userPrompt,
		Origin:      provider.OriginAssignment,
	}
	assignment := provider.NewTextMessage("user", userPrompt)
	assignment.InternalID = assignmentInput.InternalID
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
	registered, err := r.session.registerCollaborator(subCtx, string(spec.ID), spec.Name, subSessionID, string(spec.ParentID), spec.Category, subDB)
	if err != nil {
		_ = supervisor.Cancel()
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), 1)
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
		_ = supervisor.Cancel()
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), 1)
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
	}

	a.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)
	a.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		return tools.ForModel(filteredTools, p.Model), nil
	}
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
// It is called only by the exact incident/generation-fenced session API.
func (r *SubAgentRunner) ResumeRecovery(parentCheckpoint sessiondb.RecoveryCheckpoint) bool {
	if r == nil || r.session == nil || len(parentCheckpoint.OwnerData) == 0 {
		return false
	}
	var owner childRecoveryOwner
	if err := json.Unmarshal(parentCheckpoint.OwnerData, &owner); err != nil ||
		string(owner.Spec.ID) != parentCheckpoint.Status.AgentID {
		return false
	}
	spec := owner.Spec
	spec.Policy = agent.ComposePolicy(safety.DefaultPolicy(), spec.PolicyName, spec.Category)
	// Child session identity is the stable agent ID in the current format.
	dbPath, err := r.subAgentDBPath(string(spec.ID))
	if err != nil || dbPath == "" {
		return false
	}
	subDB, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		return false
	}
	var recoveryAsync *fileio.AsyncCommandManager
	fail := func() bool {
		if recoveryAsync != nil {
			recoveryAsync.CloseAndWait()
		}
		_ = subDB.Close()
		return false
	}
	if err := sessiondb.InterruptActiveRecoveries(subDB, string(spec.ID)); err != nil {
		return fail()
	}
	childCheckpoint, ok, err := sessiondb.LoadRecoveryCheckpoint(subDB, string(spec.ID), string(spec.ID))
	if err != nil || !ok || childCheckpoint.Status.ID != parentCheckpoint.Status.ID ||
		childCheckpoint.Status.Generation != parentCheckpoint.Status.Generation {
		return fail()
	}
	childCheckpoint.Status.AgentID = string(spec.ID)
	childCheckpoint.ContextOwnerID = string(spec.ID)
	recoveryState := newRecoveryRunState(parentCheckpoint.PendingInput, &childCheckpoint)
	supervisor, err := r.session.newRecoverySupervisor(
		subDB, string(spec.ID), string(spec.ID), parentCheckpoint.Status.Generation,
		recoveryState, &parentCheckpoint.Status, r.sender,
	)
	if err != nil {
		return fail()
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
	parkResume := func(kind agent.RecoveryFailureKind, cause error) bool {
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), parentCheckpoint.Status.Generation)
		status := supervisor.Park(agent.AttentionRecovery(kind, cause))
		parent := recoveryState.checkpoint(status)
		parent.ContextOwnerID = ""
		r.session.parkRecovery(parent)
		return fail()
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
	p, err := cfg.ProviderForAccount(spec.AccountID, spec.Model)
	if err != nil {
		return parkResume(agent.RecoveryFailureConfiguration, err)
	}
	spec.Provider, spec.ProviderURL, spec.APIStandard = p.ID, p.BaseURL, p.StandardID()
	filteredTools := agent.FilterToolsForModel(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true, p.Model)
	live := agent.NewAgent(p, p.Model, r.session.logger)
	live.WorkDir = r.session.WorkDir()
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
	live.AgentID, live.TurnGeneration = spec.ID, parentCheckpoint.Status.Generation
	live.Depth, live.ParentID, live.Policy = spec.Depth, spec.ParentID, spec.Policy
	r.session.bindAgentPermissionPolicy(live)
	childTempDir := filepath.Join(r.session.TempDir(), "agents", string(spec.ID))
	live.AsyncMgr = fileio.NewAsyncCommandManagerWithLogger(r.session.logger)
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
	restoreChildReportArtifact(handle, recoveryState.archive)
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
	r.SetParentToolFn(r.session.buildToolFn(false, p, spec.ModelModalities))
	live.ToolFn = r.buildProxiedToolFn(spec, blockCh, handle)
	live.PrepareStep = func() ([]tools.ToolDef, []provider.ChatMessage) {
		return tools.ForModel(filteredTools, live.Model), nil
	}
	r.session.bindAgentRuntime(subCtx, live, captypes.RuntimeActor{
		ID: string(spec.ID), ParentID: string(spec.ParentID), SessionID: handle.SessionID,
		AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools, SkillModes: spec.SkillModes,
	}, spec.Name)
	systemPrompt, userPrompt := rootprompt.BuildChild(rootprompt.ChildOptions{
		Category: spec.Category, Name: spec.Name, ID: string(spec.ID), ParentID: string(spec.ParentID), ParentTurn: spec.ParentTurn,
		Description: spec.Description, Instructions: spec.Instructions, PolicyName: spec.PolicyName,
		AllowedTools: spec.AllowedTools, DeniedTools: spec.DeniedTools, FileOwnership: spec.FileOwnership,
		ProfileInstructions: owner.ProfileInstructions, WorkDir: r.session.WorkDir(),
		TempDir: childTempDir, Model: spec.Model,
	})
	if err := r.mgr.Register(handle); err != nil {
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), parentCheckpoint.Status.Generation)
		status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureConfiguration, err))
		parent := recoveryState.checkpoint(status)
		parent.ContextOwnerID = ""
		r.session.parkRecovery(parent)
		r.interruptSubAgentAttempt(handle)
		r.session.detachCollaborator(context.Background(), string(spec.ID))
		subCancel()
		return fail()
	}
	if err := r.persistResult(handle, agent.SubAgentResult{
		AgentID: spec.ID, SessionID: handle.SessionID, Name: spec.Name,
		Category: spec.Category, Status: agent.SubAgentRunning,
	}); err != nil {
		r.mgr.MarkInterrupted(spec.ID)
		r.session.finishRecovery(string(spec.ID), supervisor.IncidentID(), parentCheckpoint.Status.Generation)
		status := supervisor.Park(agent.AttentionRecovery(agent.RecoveryFailureStorage, err))
		parent := recoveryState.checkpoint(status)
		parent.ContextOwnerID = ""
		r.session.parkRecovery(parent)
		r.interruptSubAgentAttempt(handle)
		r.session.detachCollaborator(context.Background(), string(spec.ID))
		subCancel()
		return fail()
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
	return true
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
	done := false
	var terminalDone *agent.AgentEvent
	defer r.session.detachCollaborator(context.Background(), string(handle.Spec.ID))
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
			if err := supervisor.Cancel(); err != nil {
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
			r.finalizeSubAgent(handle, agent.SubAgentResult{
				AgentID: handle.Spec.ID, Name: handle.Spec.Name, Category: handle.Spec.Category,
				SessionID: handle.SessionID, Status: agent.SubAgentCancelled,
				Errors: []string{ctx.Err().Error()},
			})
			if r.sender != nil {
				if terminalDone != nil {
					r.sender.SendAgentEventWithID(terminalDone.Type, terminalDone.Data, terminalDone.AgentID)
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
		if err := r.session.commitCollaborationAdmissions(
			handle.DB, handle.SessionID, string(handle.Spec.ID), runID,
			handle.RuntimeEpoch, contextGeneration, contextMessages, pendingCollaboration,
		); err != nil {
			r.session.releaseCollaborationReservations(
				string(handle.Spec.ID), handle.RuntimeEpoch, pendingCollaboration,
				"child context admission failed",
			)
			pendingCollaboration = nil
			return err
		}
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
	steeringCtx, stopSteering := context.WithCancel(ctx)
	runtimeMessages, runtimeMessagesDone, flushRuntime := r.session.humanSteeringMessages(
		steeringCtx, handle.DB, handle.SessionID, string(handle.Spec.ID), runID,
		handle.RuntimeEpoch, func() uint64 { return contextGeneration },
		handle.Spec.Model, r.sender,
	)
	handle.Agent.FlushRuntimeMessages = flushRuntime
	handle.Agent.Checkpoint = func(archive, active []provider.ChatMessage) error {
		recoveryState.snapshot(archive, active)
		recoveryState.setContext(handle.SessionID, contextGeneration, "run_checkpoint")
		return supervisor.Persist(supervisor.CheckpointStatus())
	}
	childEmit := func(evt agent.AgentEvent) {
		evt.AgentID = string(handle.Spec.ID)
		if err := r.session.persistCompactionEvent(handle.DB, handle.SessionID, evt.AgentID, evt); err != nil {
			r.session.logger.Warn("COMPACT", "child_lifecycle_persist_failed", "agent_id", evt.AgentID, "error", err.Error())
		}
		if evt.Type == "done" {
			copy := evt
			terminalDone = &copy
		} else if r.sender != nil {
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
		currentProvider, err := cfg.ProviderForAccount(handle.Spec.AccountID, handle.Spec.Model)
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
	agentMsgs := handle.Agent.RunSupervised(
		ctx, userMsgs, systemPrompt, childEmit, handle.BlockCh, handle.SessionID, runtimeMessages,
		agent.SupervisedRunOptions{
			Supervisor:      supervisor,
			Prepare:         prepareAttempt,
			Snapshot:        recoveryState.snapshot,
			DeferSettlement: true,
		},
	)
	stopSteering()
	<-runtimeMessagesDone
	controlMessages := r.session.takeCollaborationContext(string(handle.Spec.ID))
	if len(controlMessages) > 0 {
		agentMsgs = append(agentMsgs, controlMessages...)
	}
	active := handle.Agent.ActiveContext()
	active = append(active, controlMessages...)
	recoveryState.snapshot(agentMsgs, active)
	generation := contextGeneration
	reason := "session_start"
	if contextCheckpointContent(active) != "" && generation == 1 {
		generation = 2
		contextGeneration = generation
	}
	if contextCheckpointContent(active) != "" {
		reason = "automatic_compaction"
	}
	for {
		err := sessiondb.SaveContextState(handle.DB, handle.SessionID, string(handle.Spec.ID), sessiondb.ContextState{
			AgentID: string(handle.Spec.ID), Generation: generation, Reason: reason, Messages: active,
		})
		if err == nil {
			break
		}
		if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
			return
		}
	}

	for {
		err := persistSubAgentTranscript(handle, agentMsgs)
		if err == nil {
			break
		}
		if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
			return
		}
	}
	result := r.buildSubAgentResult(handle, agentMsgs)

	if result.Status != agent.SubAgentCancelled && !handle.ReportDone() {
		result.Errors = append(result.Errors, "required final report was not published")
		if result.Status == agent.SubAgentCompleted {
			result.Status = agent.SubAgentFailedPolicy
		}
		r.session.logger.Warn("SUBAGENT", "missing_final_report", "agent_id", handle.Spec.ID, "name", handle.Spec.Name)
	}

	settle := func() error {
		if result.Status == agent.SubAgentCancelled {
			return supervisor.Cancel()
		}
		return supervisor.Resolve()
	}
	for {
		err := r.finalizeSubAgentWithSettlement(handle, result, settle)
		if err == nil {
			done = true
			r.session.finishRecovery(string(handle.Spec.ID), supervisor.IncidentID(), supervisor.CheckpointStatus().Generation)
			if terminalDone != nil && r.sender != nil {
				r.sender.SendAgentEventWithID(terminalDone.Type, terminalDone.Data, terminalDone.AgentID)
			}
			return
		}
		if !supervisor.Await(ctx, agent.AttentionRecovery(agent.RecoveryFailureStorage, err)) {
			return
		}
	}
}

func (r *SubAgentRunner) interruptSubAgentAttempt(handle *agent.SubAgentHandle) {
	if r == nil || r.session == nil || handle == nil {
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
}

// finalizeSubAgent is the sole terminal delivery path for successful, failed,
// cancelled, panicked, and unexpected child exits. Persistence alone is not a
// completion signal: both the UI event and parent wake are mandatory.
func (r *SubAgentRunner) finalizeSubAgent(handle *agent.SubAgentHandle, result agent.SubAgentResult) {
	_ = r.finalizeSubAgentWithSettlement(handle, result, nil)
}

func (r *SubAgentRunner) finalizeSubAgentWithSettlement(
	handle *agent.SubAgentHandle,
	result agent.SubAgentResult,
	settle func() error,
) error {
	if handle == nil {
		return nil
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
			if settle != nil {
				return err
			}
		}
	}
	if err := r.persistResult(handle, result); err != nil && settle != nil {
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
		})
		if err != nil {
			r.session.logger.Warn("COLLAB", "child_run_finish_failed",
				"run_id", runID, "error", err.Error())
			if settle != nil {
				return err
			}
		} else {
			handle.SetRunVersion(finished.Version)
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
					if settle != nil {
						return publishErr
					}
				}
			}
		}
	}
	if handle.DB != nil {
		if err := sessiondb.CloseSession(handle.DB, handle.SessionID); err != nil && settle != nil {
			return err
		}
	}
	if settle != nil {
		if err := settle(); err != nil {
			return err
		}
	}
	if handle.DB != nil {
		if err := handle.DB.Close(); err != nil && settle != nil {
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
	r.parentToolMu.RLock()
	parentFn := r.parentToolFn
	r.parentToolMu.RUnlock()
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
		maxBytes := tools.GetIntArgAny(params, "max_bytes", fileio.DefaultOutputChunkBytes)
		out, nextByte, totalBytes, err := mgr.ReadOutputLimit(handleName, sinceByte, maxBytes)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("read_async_output: %w", err)
		}
		if out == "" {
			return tools.ToolResult{Content: fmt.Sprintf("[%s] next_byte=%d total_bytes=%d (no new output since byte %d)", handleName, nextByte, totalBytes, sinceByte)}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf(
			"[%s output bytes %d..%d of %d]\n%s\n[next_byte=%d total_bytes=%d has_more=%t]",
			handleName, sinceByte, nextByte, totalBytes, out, nextByte, totalBytes, nextByte < totalBytes,
		)}, nil

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
