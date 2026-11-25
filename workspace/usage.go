package workspace

import (
	"sort"
	"strings"
	"time"

	"crude/agent/sessiondb"
	"crude/types/accounting"
	"crude/types/statuscenter"
)

type usageCacheKey struct {
	agentID string
	scope   statuscenter.UsageScope
}

func (w *Workspace) recordAgentUsage(agentID string, data any) (statuscenter.UsageSummary, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return statuscenter.UsageSummary{}, false
	}
	parentID := ""
	if node := w.AgentMgr.Get(agentID); node != nil && node.Parent != nil {
		parentID = node.Parent.ID
	}
	record, ok := sessiondb.UsageRecordFromEvent(data, sess.SessionID, agentID, parentID, "")
	if !ok {
		return statuscenter.UsageSummary{}, false
	}
	sess.usageMu.Lock()
	if sess.usageRecords == nil {
		sess.usageRecords = make(map[string]sessiondb.UsageRecord)
	}
	previous := sess.usageRecords[record.RequestID]
	if previous.UpdatedAt >= record.UpdatedAt {
		sess.usageMu.Unlock()
		return w.UsageSummary(agentID, statuscenter.UsageThisAgent)
	}
	if previous.RequestID != "" {
		record.CreatedAt = previous.CreatedAt // order requests, not later revisions
	}
	if previous.Outcome.Known() {
		record.Outcome = previous.Outcome
	}
	sess.usageRecords[record.RequestID] = record
	sess.usageCache = make(map[usageCacheKey]statuscenter.UsageSummary)
	sess.usageVersion++
	sess.usageMu.Unlock()
	if w.registryCostWriter != nil && sess.SessionID != "" {
		total, _ := w.UsageSummary(agentID, statuscenter.UsageSession)
		w.registryCostWriter.Enqueue(sess.SessionID, total.Cost.USD)
	}
	return w.UsageSummary(agentID, statuscenter.UsageThisAgent)
}

// UsageSummary returns one explicitly scoped, non-overlapping rollup.
func (w *Workspace) UsageSummary(agentID string, scope statuscenter.UsageScope) (statuscenter.UsageSummary, bool) {
	sess := w.sessionForAgent(agentID)
	if sess == nil {
		return statuscenter.UsageSummary{}, false
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
	case statuscenter.UsageSession:
		for id := range records {
			selected[id] = true
		}
	case statuscenter.UsageSubtree:
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
	case statuscenter.UsageAuxiliary:
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
		summary.Models = append(summary.Models, statuscenter.ModelUsage{ModelID: key.model, ProviderID: key.provider, AccountID: key.account, Turns: model.Turns, TurnsKnown: model.TurnsKnown, AgentTurns: model.AgentTurns, Requests: model.Requests, Cost: model.Cost})
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
		sess.usageCache = make(map[usageCacheKey]statuscenter.UsageSummary)
	}
	if version == sess.usageVersion {
		sess.usageCache[cacheKey] = summary
	}
	sess.usageMu.Unlock()
	return summary, summary.Requests > 0
}

func summarizeUsage(records []sessiondb.UsageRecord, agentID string, scope statuscenter.UsageScope) statuscenter.UsageSummary {
	summary := statuscenter.UsageSummary{Scope: scope, TurnsKnown: true}
	type turnKey struct {
		agent      string
		generation uint64
	}
	turns := make(map[turnKey]bool)
	allCostKnown, allCacheReadKnown, allCacheWriteKnown, allReasoningKnown := true, true, true, true
	anyEstimated, anyPartial, anyUpper, anyCostEstimated := false, false, false, false
	costSource := ""
	var latest int64
	var latestID string
	var latestNormal sessiondb.UsageRecord
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
		readKnown := cacheReadKnown(record)
		if readKnown {
			summary.CacheRead.Value += int64(record.CachedTokens)
		}
		if record.CacheWriteKnown {
			summary.CacheWrite.Value += int64(record.CacheWriteTokens)
		}
		summary.Output.Value += int64(record.CompletionTokens)
		summary.Reasoning.Value += int64(record.ReasoningTokens)
		if record.CostKnown {
			summary.Cost.USD += record.CostUSD
		}
		allCostKnown = allCostKnown && record.CostKnown
		allCacheReadKnown = allCacheReadKnown && readKnown
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
		if record.AgentID == agentID && (record.RequestKind == "normal" || record.RequestKind == "") &&
			(latestNormal.RequestID == "" || record.CreatedAt > latestNormal.CreatedAt ||
				record.CreatedAt == latestNormal.CreatedAt && record.RequestID > latestNormal.RequestID) {
			latestNormal = record
		}
		// Context is the selected agent's latest report, not whichever child
		// happened to finish last in a session/subtree aggregate.
		if record.AgentID == agentID && record.RequestKind != "compaction" && record.RequestKind != "permission_review" && record.ContextUsed > 0 &&
			(record.UpdatedAt > latest || record.UpdatedAt == latest && record.RequestID > latestID) {
			latest = record.UpdatedAt
			latestID = record.RequestID
			summary.ContextUsed = knownUsage(int64(record.ContextUsed), record.ContextUsed > 0, record.Estimated)
			summary.ContextLimit = knownUsage(int64(record.ContextWindow), record.ContextWindow > 0, false)
			summary.ModelID, summary.ProviderID = record.ModelID, record.ProviderID
			summary.AccountID = record.AccountID
			summary.MeasuredAt = time.Unix(0, record.UpdatedAt)
			summary.ReasoningInOut = record.ReasoningInOutput
		}
	}
	summary.AgentTurns = len(turns)
	confidence := usageConfidence(anyEstimated, anyPartial)
	hasUsage := summary.Requests > 0
	summary.InputTotal = usageValue(summary.InputTotal.Value, hasUsage, confidence)
	summary.Output = usageValue(summary.Output.Value, hasUsage, confidence)
	summary.CacheRead = usageValue(summary.CacheRead.Value, hasUsage && allCacheReadKnown, confidence)
	summary.CacheWrite = usageValue(summary.CacheWrite.Value, hasUsage && allCacheWriteKnown, confidence)
	summary.Reasoning = usageValue(summary.Reasoning.Value, hasUsage && allReasoningKnown, confidence)
	if summary.CacheRead.Known {
		summary.UncachedInput = usageValue(summary.InputTotal.Value-summary.CacheRead.Value, true, confidence)
	}
	if latestNormal.RequestID != "" {
		confidence := usageConfidence(latestNormal.Estimated, latestNormal.Partial)
		summary.LatestCache = statuscenter.RequestCacheUsage{
			Present:    true,
			InputTotal: usageValue(int64(latestNormal.PromptTokens), true, confidence),
			Read:       usageValue(int64(latestNormal.CachedTokens), cacheReadKnown(latestNormal), confidence),
		}
	}
	summary.Cost.Known = allCostKnown
	summary.Cost.Source = costSource
	summary.Cost.Confidence = confidence
	if anyCostEstimated && summary.Cost.Confidence == statuscenter.ConfidenceMeasured {
		summary.Cost.Confidence = statuscenter.ConfidenceEstimated
	}
	if !allCostKnown {
		summary.Cost.Confidence = statuscenter.ConfidencePartial
	}
	if anyUpper && allCostKnown {
		summary.Cost.Confidence = statuscenter.ConfidenceUpperBound
	}
	return summary
}

// Validate each read against its own inclusive input total before adding it.
func cacheReadKnown(record sessiondb.UsageRecord) bool {
	return record.CacheReadKnown && record.CachedTokens >= 0 && record.CachedTokens <= record.PromptTokens
}

func usageConfidence(estimated, partial bool) statuscenter.ValueConfidence {
	switch {
	case partial:
		return statuscenter.ConfidencePartial
	case estimated:
		return statuscenter.ConfidenceEstimated
	default:
		return statuscenter.ConfidenceMeasured
	}
}

func usageValue(value int64, known bool, confidence statuscenter.ValueConfidence) statuscenter.UsageValue {
	if !known {
		confidence = statuscenter.ConfidenceUnknown
	}
	return statuscenter.UsageValue{Value: value, Known: known, Confidence: confidence}
}

func knownUsage(value int64, known, estimated bool) statuscenter.UsageValue {
	confidence := statuscenter.ConfidenceMeasured
	if estimated {
		confidence = statuscenter.ConfidenceEstimated
	}
	return statuscenter.UsageValue{Value: value, Known: known, Confidence: confidence}
}
