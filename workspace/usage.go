package workspace

import (
	"sort"
	"strings"
	"time"

	"crude/sessiondb"
	ttypes "crude/types"
	"crude/types/accounting"
)

type usageCacheKey struct {
	agentID string
	scope   ttypes.UsageScope
}

func (w *Workspace) recordAgentUsage(agentID string, data any) (ttypes.UsageSummary, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return ttypes.UsageSummary{}, false
	}
	parentID := ""
	if node := w.AgentMgr.Get(agentID); node != nil && node.Parent != nil {
		parentID = node.Parent.ID
	}
	record, ok := sessiondb.UsageRecordFromEvent(data, sess.SessionID, agentID, parentID, "")
	if !ok {
		return ttypes.UsageSummary{}, false
	}
	sess.usageMu.Lock()
	if sess.usageRecords == nil {
		sess.usageRecords = make(map[string]sessiondb.UsageRecord)
	}
	previous := sess.usageRecords[record.RequestID]
	if previous.UpdatedAt >= record.UpdatedAt {
		sess.usageMu.Unlock()
		return w.UsageSummary(agentID, ttypes.UsageThisAgent)
	}
	if previous.Outcome.Known() {
		record.Outcome = previous.Outcome
	}
	sess.usageRecords[record.RequestID] = record
	sess.usageCache = make(map[usageCacheKey]ttypes.UsageSummary)
	sess.usageVersion++
	sess.usageMu.Unlock()
	if w.registryCostWriter != nil && sess.SessionID != "" {
		total, _ := w.UsageSummary(agentID, ttypes.UsageSession)
		w.registryCostWriter.Enqueue(sess.SessionID, total.Cost.USD)
	}
	return w.UsageSummary(agentID, ttypes.UsageThisAgent)
}

// UsageSummary returns one explicitly scoped, non-overlapping rollup.
func (w *Workspace) UsageSummary(agentID string, scope ttypes.UsageScope) (ttypes.UsageSummary, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return ttypes.UsageSummary{}, false
	}
	cacheKey := usageCacheKey{agentID: agentID, scope: scope}
	sess.usageMu.RLock()
	version := sess.usageVersion
	if cached, exists := sess.usageCache[cacheKey]; exists {
		sess.usageMu.RUnlock()
		return cached, cached.Requests > 0
	}
	records := make(map[string]sessiondb.UsageRecord, len(sess.usageRecords))
	for id, record := range sess.usageRecords {
		records[id] = record
	}
	sess.usageMu.RUnlock()
	selected := make(map[string]bool)
	switch scope {
	case ttypes.UsageSession:
		for id := range records {
			selected[id] = true
		}
	case ttypes.UsageSubtree:
		parents := make(map[string]string)
		for _, record := range records {
			parents[record.AgentID] = record.ParentAgentID
		}
		for id, record := range records {
			seen := make(map[string]bool)
			for owner := record.AgentID; owner != "" && !seen[owner]; owner = parents[owner] {
				if owner == agentID {
					selected[id] = true
					break
				}
				seen[owner] = true
			}
		}
	case ttypes.UsageAuxiliary:
		for id, record := range records {
			selected[id] = record.AgentID == "" || len(record.AgentID) >= 4 && record.AgentID[:4] == "aux:"
		}
	default:
		for id, record := range records {
			selected[id] = record.AgentID == agentID
		}
	}

	var scoped []sessiondb.UsageRecord
	type modelKey struct{ model, provider, account string }
	models := make(map[modelKey][]sessiondb.UsageRecord)
	for id, record := range records {
		if selected[id] {
			scoped = append(scoped, record)
		}
	}
	// Stable accumulation order keeps live and rehydrated monetary totals
	// identical even though their request maps have different iteration orders.
	sort.Slice(scoped, func(i, j int) bool { return scoped[i].RequestID < scoped[j].RequestID })
	for _, record := range scoped {
		key := modelKey{record.ModelID, record.ProviderID, record.AccountID}
		models[key] = append(models[key], record)
	}
	summary := summarizeUsage(scoped, agentID, scope)
	for key, records := range models {
		model := summarizeUsage(records, agentID, scope)
		summary.Models = append(summary.Models, ttypes.ModelUsage{ModelID: key.model, ProviderID: key.provider, AccountID: key.account, Turns: model.Turns, TurnsKnown: model.TurnsKnown, AgentTurns: model.AgentTurns, Requests: model.Requests, Cost: model.Cost})
	}
	sort.Slice(summary.Models, func(i, j int) bool {
		a, b := summary.Models[i], summary.Models[j]
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.AccountID < b.AccountID
	})
	sess.usageMu.Lock()
	if sess.usageCache == nil {
		sess.usageCache = make(map[usageCacheKey]ttypes.UsageSummary)
	}
	if version == sess.usageVersion {
		sess.usageCache[cacheKey] = summary
	}
	sess.usageMu.Unlock()
	return summary, summary.Requests > 0
}

func summarizeUsage(records []sessiondb.UsageRecord, agentID string, scope ttypes.UsageScope) ttypes.UsageSummary {
	summary := ttypes.UsageSummary{Scope: scope, TurnsKnown: true}
	type turnKey struct {
		agent      string
		generation uint64
	}
	turns := make(map[turnKey]bool)
	allCostKnown, allCacheReadKnown, allCacheWriteKnown, allReasoningKnown := true, true, true, true
	anyEstimated, anyPartial, anyUpper, anyCostEstimated := false, false, false, false
	costSource := ""
	var latest int64
	for _, record := range records {
		if record.Outcome == accounting.Succeeded {
			summary.Turns++
		}
		summary.TurnsKnown = summary.TurnsKnown && record.Outcome.Known()
		if record.TurnGeneration != 0 && (record.RequestKind == "normal" || record.RequestKind == "") && record.AgentID != "" && !strings.HasPrefix(record.AgentID, "aux:") {
			turns[turnKey{record.AgentID, record.TurnGeneration}] = true
		}
		summary.Requests++
		summary.InputTotal.Value += int64(record.PromptTokens)
		summary.CacheRead.Value += int64(record.CachedTokens)
		summary.CacheWrite.Value += int64(record.CacheWriteTokens)
		summary.Output.Value += int64(record.CompletionTokens)
		summary.Reasoning.Value += int64(record.ReasoningTokens)
		if record.CostKnown {
			summary.Cost.USD += record.CostUSD
		}
		allCostKnown = allCostKnown && record.CostKnown
		allCacheReadKnown = allCacheReadKnown && record.CacheReadKnown
		allCacheWriteKnown = allCacheWriteKnown && record.CacheWriteKnown
		allReasoningKnown = allReasoningKnown && record.ReasoningKnown
		anyEstimated = anyEstimated || record.Estimated
		anyPartial = anyPartial || record.Partial
		anyUpper = anyUpper || record.CostUpperBound
		anyCostEstimated = anyCostEstimated || strings.Contains(record.CostSource, "catalog")
		if costSource == "" {
			costSource = record.CostSource
		} else if record.CostSource != "" && record.CostSource != costSource {
			costSource = "mixed"
		}
		// Context is the selected agent's latest report, not whichever child
		// happened to finish last in a session/subtree aggregate.
		if record.AgentID == agentID && record.RequestKind != "compaction" && record.RequestKind != "permission_review" && record.ContextUsed > 0 && record.UpdatedAt > latest {
			latest = record.UpdatedAt
			summary.ContextUsed = knownUsage(int64(record.ContextUsed), record.ContextUsed > 0, record.Estimated)
			summary.ContextLimit = knownUsage(int64(record.ContextWindow), record.ContextWindow > 0, false)
			summary.ModelID, summary.ProviderID = record.ModelID, record.ProviderID
			summary.AccountID = record.AccountID
			summary.MeasuredAt = time.Unix(0, record.UpdatedAt)
			summary.ReasoningInOut = record.ReasoningInOutput
		}
	}
	summary.AgentTurns = len(turns)
	confidence := ttypes.ConfidenceMeasured
	if anyPartial {
		confidence = ttypes.ConfidencePartial
	} else if anyEstimated {
		confidence = ttypes.ConfidenceEstimated
	}
	summary.InputTotal = ttypes.UsageValue{Value: summary.InputTotal.Value, Known: true, Confidence: confidence}
	summary.Output = ttypes.UsageValue{Value: summary.Output.Value, Known: true, Confidence: confidence}
	summary.CacheRead.Known, summary.CacheWrite.Known, summary.Reasoning.Known = allCacheReadKnown, allCacheWriteKnown, allReasoningKnown
	summary.CacheRead.Confidence, summary.CacheWrite.Confidence, summary.Reasoning.Confidence = confidence, confidence, confidence
	if allCacheReadKnown && allCacheWriteKnown {
		summary.UncachedInput = ttypes.UsageValue{Value: summary.InputTotal.Value - summary.CacheRead.Value - summary.CacheWrite.Value, Known: true, Confidence: confidence}
		if summary.UncachedInput.Value < 0 {
			summary.UncachedInput.Known = false
		}
	}
	if summary.CacheRead.Value > summary.InputTotal.Value {
		summary.CacheRead.Known = false
	}
	summary.Cost.Known = allCostKnown
	summary.Cost.Source = costSource
	summary.Cost.Confidence = confidence
	if anyCostEstimated && summary.Cost.Confidence == ttypes.ConfidenceMeasured {
		summary.Cost.Confidence = ttypes.ConfidenceEstimated
	}
	if !allCostKnown {
		summary.Cost.Confidence = ttypes.ConfidencePartial
	}
	if anyUpper && allCostKnown {
		summary.Cost.Confidence = ttypes.ConfidenceUpperBound
	}
	return summary
}

func knownUsage(value int64, known, estimated bool) ttypes.UsageValue {
	confidence := ttypes.ConfidenceMeasured
	if estimated {
		confidence = ttypes.ConfidenceEstimated
	}
	return ttypes.UsageValue{Value: value, Known: known, Confidence: confidence}
}
