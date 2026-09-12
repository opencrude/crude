package workspace

import (
	"strings"
	"time"

	"crude/sessiondb"
	ttypes "crude/types"
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
	if previous, exists := sess.usageRecords[record.RequestID]; exists && previous.UpdatedAt >= record.UpdatedAt {
		sess.usageMu.Unlock()
		return w.UsageSummary(agentID, ttypes.UsageThisAgent)
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
		return cached, true
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

	summary := ttypes.UsageSummary{Scope: scope}
	allCostKnown, allCacheReadKnown, allCacheWriteKnown, allReasoningKnown := true, true, true, true
	anyEstimated, anyPartial, anyUpper, anyCostEstimated := false, false, false, false
	costSource := ""
	var latest int64
	for id, record := range records {
		if !selected[id] {
			continue
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
		if record.AgentID == agentID && record.RequestKind != "compaction" && record.ContextUsed > 0 && record.UpdatedAt > latest {
			latest = record.UpdatedAt
			summary.ContextUsed = knownUsage(int64(record.ContextUsed), record.ContextUsed > 0, record.Estimated)
			summary.ContextLimit = knownUsage(int64(record.ContextWindow), record.ContextWindow > 0, false)
			summary.ModelID, summary.ProviderID = record.ModelID, record.ProviderID
			summary.MeasuredAt = time.Unix(0, record.UpdatedAt)
			summary.ReasoningInOut = record.ReasoningInOutput
		}
	}
	if summary.Requests == 0 {
		return summary, false
	}
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
	sess.usageMu.Lock()
	if sess.usageCache == nil {
		sess.usageCache = make(map[usageCacheKey]ttypes.UsageSummary)
	}
	if version == sess.usageVersion {
		sess.usageCache[cacheKey] = summary
	}
	sess.usageMu.Unlock()
	return summary, true
}

func knownUsage(value int64, known, estimated bool) ttypes.UsageValue {
	confidence := ttypes.ConfidenceMeasured
	if estimated {
		confidence = ttypes.ConfidenceEstimated
	}
	return ttypes.UsageValue{Value: value, Known: known, Confidence: confidence}
}
