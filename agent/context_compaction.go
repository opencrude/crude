package agent

import (
	"context"
	"crude/types/accounting"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	rand2 "math/rand/v2"
	"strings"
	"time"

	rootprompt "crude/prompt"
	"crude/provider"
)

const (
	compactionRecentUserMaxTokens = 20_000
	compactionStageRecoveries     = 3
)

type CompactResult struct {
	Messages      []provider.ChatMessage
	BeforeTokens  int
	AfterTokens   int
	ContextWindow int
	Usage         provider.TokenUsage
	RequestID     string
	Trigger       string
	Role          string
	StageName     string
	StageIndex    int
	Completed     int
	RetryAttempt  int
	ProviderID    string
	AccountID     string
	ModelID       string
}

type CompactionCommit struct {
	Messages      []provider.ChatMessage
	BeforeTokens  int
	AfterTokens   int
	ContextWindow int
	RequestID     string
	Trigger       string
	Role          string
	ProviderID    string
	AccountID     string
	ModelID       string
}

type CompactionSource struct {
	Messages      []provider.ChatMessage
	BeforeTokens  int
	ContextWindow int
	RequestID     string
	Trigger       string
	Role          string
	ProviderID    string
	AccountID     string
	ModelID       string
}

type compactOutcome struct {
	Messages      []provider.ChatMessage
	Usage         provider.TokenUsage
	AfterTokens   int
	ContextWindow int
	RequestID     string
	Trigger       string
	Role          string
	StageName     string
	StageIndex    int
	Completed     int
	RetryAttempt  int
	ProviderID    string
	AccountID     string
	ModelID       string
}

func compactionThreshold(ctxWindow, percent int) int {
	if ctxWindow <= 0 || percent <= 0 {
		return 0
	}
	threshold := ctxWindow * percent / 100
	threshold = min(threshold, ctxWindow-contextSafetyReserve(ctxWindow))
	if threshold < 1 {
		return 0
	}
	return threshold
}

func checkpointRole(depth int) string {
	if depth > 0 {
		return "child"
	}
	return "root"
}

func (a *Agent) contextCompactor(targetWindow int) *Agent {
	if a == nil || a.CompactionProvider == nil {
		return a
	}
	compactor := NewAgent(a.CompactionProvider, a.CompactionProvider.Model, a.logger)
	compactor.AgentID = a.AgentID
	compactor.TurnGeneration = a.TurnGeneration
	compactor.Depth = a.Depth
	compactor.ParentID = a.ParentID
	compactor.WorkDir = a.WorkDir
	compactor.AsyncMgr = a.AsyncMgr
	compactor.SubAgentMgr = a.SubAgentMgr
	compactor.ContinuationContext = a.ContinuationContext
	compactor.LatestHumanTurnState = a.LatestHumanTurnState
	compactor.guidance = a.guidance
	compactor.GuidanceCacheLoad = a.GuidanceCacheLoad
	compactor.GuidanceCacheStore = a.GuidanceCacheStore
	compactor.reportPublished = a.reportPublished
	compactor.MaxRetries = a.MaxRetries
	compactor.FilteredTools = a.FilteredTools
	compactor.PrepareCompaction = a.PrepareCompaction
	compactor.CommitCompaction = a.CommitCompaction
	compactor.Ctx = a.Ctx
	compactor.testHTTPClient = a.testHTTPClient
	compactor.compactionRetryDelay = a.compactionRetryDelay
	compactor.CompactionTargetModel = a.Model
	compactor.CompactionTargetWindow = targetWindow
	compactor.CompactionTargetTokens = a.contextThreshold(targetWindow)
	compactor.MaxOutputTokens = a.CompactionMaxOutput
	compactor.CompactionAgentProfile = a.CompactionAgentProfile
	return compactor
}

func validateContextCheckpoint(content, role string, maxTokens int) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("compaction returned an empty checkpoint")
	}
	decoder := xml.NewDecoder(strings.NewReader(content))
	stages := rootprompt.CompactionStages()
	seen := make(map[string]int, len(stages))
	depth := 0
	for {
		tok, err := decoder.Token()
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("invalid compaction checkpoint XML: %w", err)
		}
		switch node := tok.(type) {
		case xml.StartElement:
			switch depth {
			case 0:
				if node.Name.Local != "context_checkpoint" || attribute(node.Attr, "role") != role || attribute(node.Attr, "version") != "3" {
					return "", fmt.Errorf("compaction checkpoint has invalid root contract for role %q", role)
				}
			case 1:
				if !rootprompt.IsCompactionStage(node.Name.Local) {
					return "", fmt.Errorf("compaction checkpoint contains unknown section %q", node.Name.Local)
				}
				seen[node.Name.Local]++
			default:
				return "", fmt.Errorf("compaction checkpoint contains model-authored nested element %q", node.Name.Local)
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	for _, stage := range stages {
		if seen[stage.Name] != 1 {
			return "", fmt.Errorf("compaction checkpoint requires exactly one <%s> section", stage.Name)
		}
	}
	if provider.EstimateMessageTokens(provider.NewTextMessage("user", content), "") > maxTokens {
		return "", fmt.Errorf("compaction checkpoint exceeds %d available tokens", maxTokens)
	}
	return content, nil
}

func attribute(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func validateCompactionStageText(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("model returned an empty extraction")
	}
	for _, r := range text {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return fmt.Errorf("model returned invalid control character U+%04X", r)
		}
	}
	return nil
}

func (a *Agent) compactWithModel(
	ctx context.Context,
	msgs []provider.ChatMessage,
	tools []provider.ToolDef,
	requestID string,
	beforeTokens int,
	trigger string,
	emit func(AgentEvent),
) (compactOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(msgs) == 0 {
		return compactOutcome{}, errors.New("cannot compact empty context")
	}
	if requestID == "" {
		requestID = fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64())
	}
	if trigger == "" {
		trigger = "automatic"
	}
	role := checkpointRole(a.Depth)
	targetModel := a.CompactionTargetModel
	if targetModel == "" {
		targetModel = a.Model
	}
	if a.CompactionTargetWindow == 0 && targetModel != a.Model {
		return compactOutcome{}, errors.New("continuation model context window is unavailable for compaction")
	}
	ctxWindow := a.CompactionTargetWindow
	if ctxWindow == 0 && a.Provider != nil {
		ctxWindow = a.Provider.ContextWindow(targetModel)
	}
	if a.PrepareCompaction != nil {
		if err := a.PrepareCompaction(CompactionSource{
			Messages: append([]provider.ChatMessage(nil), msgs[1:]...), BeforeTokens: beforeTokens,
			ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
			AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
		}); err != nil {
			return compactOutcome{
				ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
				AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
			}, fmt.Errorf("persist compaction source: %w", err)
		}
	}
	if ctxWindow <= 0 {
		return compactOutcome{ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
				AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model},
			errors.New("continuation model context capacity is unavailable for compaction")
	}
	continuationLimit := ctxWindow - contextSafetyReserve(ctxWindow)
	if a.CompactionTargetTokens > 0 {
		continuationLimit = min(continuationLimit, a.CompactionTargetTokens)
	}
	if beforeTokens > 0 {
		continuationLimit = min(continuationLimit, beforeTokens)
	}
	fixed := estimateCanonicalRequest(msgs[:1], tools, targetModel)
	checkpointBudget := continuationLimit - fixed - 1
	if checkpointBudget < 1 {
		return compactOutcome{ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
				AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model},
			errors.New("target context has insufficient room for a checkpoint after system and tool instructions")
	}
	extractionWindow := a.Provider.ContextWindow(a.Model)
	learnedLimit, err := provider.LearnedOutputLimit(a.CompactionAgentProfile, a.Provider, a.Model)
	if err != nil {
		return compactOutcome{}, fmt.Errorf("load learned extraction output limit: %w", err)
	}
	manifestBudget := min(2048, max(128, extractionWindow/32))
	continuity, err := a.continuityMessage(ctx, true)
	if err != nil {
		return compactOutcome{}, err
	}
	checkpointBudget -= provider.EstimateMessageTokens(continuity, targetModel)
	if checkpointBudget < 1 {
		return compactOutcome{}, errors.New("target context has insufficient room for complete guidance, runtime continuity, and a checkpoint")
	}
	common := provider.NewTextMessage("user", rootprompt.CompactionCommon(role, compactionHumanManifest(msgs[1:], role, targetModel, manifestBudget), continuity.ContentString()))
	common.Origin = provider.OriginCompactionRequest
	common.Visibility = provider.VisibilityContextOnly
	inputBudget := extractionWindow - contextSafetyReserve(extractionWindow) - 1
	if extractionWindow <= 0 {
		inputBudget = 128_000
	}

	emit(AgentEvent{Type: "context_compaction_started", Data: a.compactionEventData(requestID, trigger, role, 0, 0, beforeTokens, 0, ctxWindow)})

	stages := rootprompt.CompactionStages()
	sections := make(map[string]string, len(stages))
	var aggregate provider.TokenUsage
	usageRatio := float64(1)
	for index, stage := range stages {
		remaining := checkpointBudget - 1
		for _, section := range sections {
			remaining -= provider.EstimateTokens(section, targetModel)
		}
		if remaining <= 0 {
			return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
					StageName: stage.Label, StageIndex: index + 1, Completed: index,
					AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model},
				errors.New("extracted checkpoint consumed the continuation context budget")
		}
		data := a.compactionEventData(requestID, trigger, role, index+1, index, beforeTokens, 0, ctxWindow)
		data["stage_name"] = stage.Name
		data["stage_label"] = stage.Label
		emit(AgentEvent{Type: "context_compaction_stage", Data: data})

		extractor := compactionExtractor{
			agent: a, ctx: ctx, emit: emit, requestID: requestID, trigger: trigger, role: role,
			stage: stage, stageIndex: index, beforeTokens: beforeTokens, targetWindow: ctxWindow,
			system: msgs[0], common: common, inputBudget: inputBudget, outputBudget: remaining,
			learnedOutputLimit: learnedLimit, usageRatio: usageRatio,
			capacityBudget: extractionWindow - contextSafetyReserve(extractionWindow) - 1,
		}
		stageText, stageErr := extractor.request(msgs[1:], rootprompt.InitialCompactionDirective(), 0)
		aggregate = addTokenUsage(aggregate, extractor.usage)
		inputBudget = extractor.inputBudget
		usageRatio = extractor.usageRatio
		if extractor.learnedOutputLimit > 0 {
			learnedLimit = extractor.learnedOutputLimit
		}
		if stageErr != nil {
			return compactOutcome{
					Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
					StageName: stage.Label, StageIndex: index + 1, Completed: index, RetryAttempt: extractor.retryAttempt,
					AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
				},
				fmt.Errorf("%s stage: %w", stage.Name, stageErr)
		}
		sections[stage.Name] = stageText
	}

	checkpoint, err := rootprompt.BuildContextCheckpoint(role, sections)
	if err != nil {
		return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role, StageIndex: len(stages), Completed: len(stages), AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model}, err
	}
	checkpoint, err = validateContextCheckpoint(checkpoint, role, checkpointBudget)
	if err != nil {
		return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role, StageIndex: len(stages), Completed: len(stages), AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model}, err
	}

	checkpointMsg := provider.NewTextMessage("user", checkpoint)
	checkpointMsg.Origin = provider.OriginCheckpoint
	checkpointMsg.Visibility = provider.VisibilityContextOnly
	// Jobs and plans may settle while extraction runs. Refresh their real owners
	// before commit; these facts must survive even if every extraction omits them.
	continuity, err = a.continuityMessage(ctx, true)
	if err != nil {
		return compactOutcome{}, err
	}
	retentionBudget := min(compactionRecentUserMaxTokens, max(0, continuationLimit-fixed-provider.EstimateMessageTokens(checkpointMsg, targetModel)-provider.EstimateMessageTokens(continuity, targetModel)-1))
	answered := latestHumanRequestAnswered(msgs[1:])
	if role == "root" && a.LatestHumanTurnState != nil {
		state, known, stateErr := a.LatestHumanTurnState(ctx)
		if stateErr != nil {
			return compactOutcome{}, fmt.Errorf("read durable human-turn state: %w", stateErr)
		}
		if known {
			answered = state == "completed"
		}
	}
	if role == "root" && answered {
		retentionBudget = 0
	}
	recentUsers := boundedRecentUserContext(msgs[1:], role, retentionBudget, targetModel)
	compacted := append([]provider.ChatMessage{checkpointMsg}, recentUsers...)
	compacted = append(compacted, continuity)
	afterTokens := estimateCanonicalRequest(append([]provider.ChatMessage{msgs[0]}, compacted...), tools, targetModel)
	if ctx.Err() != nil {
		return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
			AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model}, ctx.Err()
	}
	if afterTokens >= continuationLimit {
		return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
				AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model},
			fmt.Errorf("checkpoint exceeds continuation budget (%d/%d tokens)", afterTokens, continuationLimit)
	}
	if beforeTokens > 0 && afterTokens >= beforeTokens {
		return compactOutcome{Usage: aggregate, ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role, StageIndex: len(stages), Completed: len(stages), AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model},
			fmt.Errorf("compaction did not reduce active context (%d -> %d tokens)", beforeTokens, afterTokens)
	}

	return compactOutcome{
		Messages: compacted, Usage: aggregate, AfterTokens: afterTokens,
		ContextWindow: ctxWindow, RequestID: requestID, Trigger: trigger, Role: role,
		AccountID: a.Provider.AccountID, ProviderID: a.Provider.ID, ModelID: a.Model,
	}, nil
}

func compactionEventData(requestID, trigger, role string, stage, completed, before, after, ctxWindow int) map[string]any {
	return map[string]any{
		"request_id": requestID, "trigger": trigger, "role": role,
		"stage": stage, "stage_count": len(rootprompt.CompactionStages()), "completed_stages": completed,
		"before_tokens": before, "after_tokens": after, "context_window": ctxWindow,
	}
}

func (a *Agent) compactionEventData(requestID, trigger, role string, stage, completed, before, after, ctxWindow int) map[string]any {
	data := compactionEventData(requestID, trigger, role, stage, completed, before, after, ctxWindow)
	if a != nil && a.Provider != nil {
		data["account_id"] = a.Provider.AccountID
		data["provider_id"] = a.Provider.ID
		data["provider_name"] = a.Provider.Name
		data["model_id"] = a.Model
	}
	return data
}

func (a *Agent) emitCompactionUsage(emit func(AgentEvent), usage provider.TokenUsage, requestID, operationID, trigger, role, stage string, partial, succeeded bool) {
	if emit == nil || !succeeded && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		return
	}
	outcome := accounting.Interrupted
	if succeeded {
		outcome = accounting.Succeeded
	}
	price, costKnown, costSource := provider.LookupPriceKnown(a.Model)
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		costKnown = false
	}
	turnCost := price.CalculateCost(usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensCached)
	emit(AgentEvent{Type: "usage", Data: map[string]any{
		"request_outcome": string(outcome),
		"prompt":          usage.PromptTokens, "completion": usage.CompletionTokens,
		"prompt_cached": usage.PromptTokensCached, "prompt_written": usage.PromptTokensWritten,
		"cache_read_known": usage.CacheReadKnown, "cache_write_known": usage.CacheWriteKnown,
		"reasoning": usage.ReasoningTokens, "reasoning_known": usage.ReasoningKnown,
		"reasoning_in_output": usage.ReasoningInOutput,
		"cost":                turnCost, "cost_known": costKnown, "cost_source": costSource,
		"request_id": requestID, "operation_id": operationID, "request_kind": "compaction",
		"compaction_role": role, "compaction_stage": stage, "compaction_trigger": trigger,
		"account_id":  a.Provider.AccountID,
		"provider_id": a.Provider.ID, "provider_name": a.Provider.Name, "model_id": a.Model,
		"provider": a.Provider.ID, "model": a.Model,
		"context_window": a.Provider.ContextWindow(a.Model),
		"revision":       1, "reported_at": time.Now().UnixNano(), "estimated": false,
		"partial_or_retry": partial,
	}})
}

func addTokenUsage(a, b provider.TokenUsage) provider.TokenUsage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.PromptTokensCached += b.PromptTokensCached
	a.PromptTokensWritten += b.PromptTokensWritten
	a.CacheReadKnown = a.CacheReadKnown || b.CacheReadKnown
	a.CacheWriteKnown = a.CacheWriteKnown || b.CacheWriteKnown
	a.ReasoningTokens += b.ReasoningTokens
	a.ReasoningKnown = a.ReasoningKnown || b.ReasoningKnown
	a.ReasoningInOutput = a.ReasoningInOutput || b.ReasoningInOutput
	return a
}

func (a *Agent) compactionRecoveryDelay(attempt int) time.Duration {
	if a != nil && a.compactionRetryDelay != nil {
		return a.compactionRetryDelay(attempt)
	}
	return time.Duration(1<<max(0, attempt-1)) * time.Second
}

func waitCompactionRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableCompactionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if provider.IsContextWindowExceeded(err) {
		return false
	}
	var status *provider.StatusError
	var api *provider.APIError
	if errors.As(err, &status) || errors.As(err, &api) {
		return provider.Retryable(err)
	}
	return true
}

func compactErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	runes := []rune(text)
	if len(runes) > 160 {
		text = string(runes[:160]) + "…"
	}
	return text
}

func retainRecentUserContext(msgs []provider.ChatMessage, role string, budget int, model string) []provider.ChatMessage {
	var selected []provider.ChatMessage
	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		keep := m.IsGenuineUserInput()
		if role == "child" && m.EffectiveOrigin() == provider.OriginAssignment {
			keep = true
		}
		if !keep {
			continue
		}
		n := provider.EstimateMessageTokens(m, model)
		if used+n > budget && len(selected) > 0 && m.EffectiveOrigin() != provider.OriginAssignment {
			break
		}
		selected = append(selected, m)
		used += n
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

func boundedRecentUserContext(messages []provider.ChatMessage, role string, budget int, model string) []provider.ChatMessage {
	var selected []provider.ChatMessage
	used := 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if !message.IsGenuineUserInput() && (role != "child" || message.EffectiveOrigin() != provider.OriginAssignment) {
			continue
		}
		cost := provider.EstimateMessageTokens(message, model)
		if used+cost > budget {
			continue
		}
		selected = append(selected, message)
		used += cost
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func latestHumanRequestAnswered(messages []provider.ChatMessage) bool {
	latestHuman := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].IsGenuineUserInput() {
			latestHuman = index
			break
		}
	}
	if latestHuman < 0 {
		return false
	}
	for _, message := range messages[latestHuman+1:] {
		if message.Role == "assistant" && len(message.ToolCalls) == 0 &&
			(strings.TrimSpace(message.ContentString()) != "" || strings.TrimSpace(message.Refusal) != "") {
			return true
		}
	}
	return false
}

func (a *Agent) Compact(
	ctx context.Context,
	active []provider.ChatMessage,
	systemPrompt string,
	emit func(AgentEvent),
) (CompactResult, error) {
	return a.CompactWithRequestID(ctx, active, systemPrompt, "", emit)
}

func (a *Agent) CompactWithRequestID(
	ctx context.Context,
	active []provider.ChatMessage,
	systemPrompt string,
	requestID string,
	emit func(AgentEvent),
) (CompactResult, error) {
	if a == nil || a.Provider == nil {
		return CompactResult{}, errors.New("agent provider is nil")
	}
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var guidanceErr error
	systemPrompt, guidanceErr = a.loadProjectGuidance(ctx, systemPrompt)
	if guidanceErr != nil {
		return CompactResult{}, fmt.Errorf("cannot compact before reading complete project guidance: %w", guidanceErr)
	}
	msgs := []provider.ChatMessage{{
		Role: "system", Content: provider.TextContent(systemPrompt),
		CacheControl: &provider.CacheControl{Type: "ephemeral"},
	}}
	msgs = append(msgs, active...)
	tools := toolDefsToProviderDefs(a.FilteredTools)
	if len(tools) == 0 {
		tools = allToolDefs()
	}
	targetModel := a.CompactionTargetModel
	if targetModel == "" {
		targetModel = a.Model
	}
	before := estimateCanonicalRequest(msgs, tools, targetModel)
	outcome, err := a.compactWithModel(ctx, msgs, tools, requestID, before, "manual", emit)
	if err != nil {
		return CompactResult{
			BeforeTokens: before, ContextWindow: outcome.ContextWindow, Usage: outcome.Usage,
			RequestID: outcome.RequestID, Trigger: "manual", Role: checkpointRole(a.Depth),
			StageName: outcome.StageName, StageIndex: outcome.StageIndex, Completed: outcome.Completed, RetryAttempt: outcome.RetryAttempt,
			AccountID: outcome.AccountID, ProviderID: outcome.ProviderID, ModelID: outcome.ModelID,
		}, err
	}
	return CompactResult{
		Messages: outcome.Messages, BeforeTokens: before, AfterTokens: outcome.AfterTokens,
		ContextWindow: outcome.ContextWindow, Usage: outcome.Usage,
		RequestID: outcome.RequestID, Trigger: outcome.Trigger, Role: outcome.Role,
		AccountID: outcome.AccountID, ProviderID: outcome.ProviderID, ModelID: outcome.ModelID,
	}, nil
}
