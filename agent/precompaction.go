package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	rand2 "math/rand/v2"
	"strings"
	"sync"
	"time"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/retry"
	"crude/tools"
)

// Automatic compaction prepares checkpoints in the background and adopts them
// late. The decision is adaptive rather than a fixed fullness:
//
//   - Physical: the next request must fit the window with its full output
//     allowance. That is the only hard trigger.
//   - Economic: carrying old context costs money on every request even when
//     cached. When prices are known, Crude compacts once the extra spend paid
//     for context a checkpoint would remove has reached what compacting costs
//     (the ski-rental rule: never worse than twice the best choice made with
//     hindsight). Unknown prices count only uncached tokens, so Crude keeps
//     context rather than guess.
//   - Explicit: a user-set percent always wins; "off" disables compaction.
//
// Preparation starts halfway to the trigger, so a slow or flaky Aeron model has
// the second half to finish while the agent keeps working; a ready checkpoint
// waits for the trigger. The adopted context is the fresh system prompt and
// tools, the checkpoint of older context, and the recent turns verbatim.

// compactionPlan is one step's decision.
type compactionPlan struct {
	prepare bool // start preparing a checkpoint in the background
	swap    bool // adopt a ready checkpoint now
	force   bool // the next request must not be sent without a checkpoint
	reason  string
}

// compactionPlanner learns, within a run, what context costs and what a
// checkpoint costs and yields.
type compactionPlanner struct {
	excess        float64 // spend on context a checkpoint would have removed
	units         string  // "usd" or "tokens"; a change resets accounting
	floor         int     // context size right after the last checkpoint
	checkpoint    int     // learned checkpoint size, tokens
	costPerSource float64 // learned compaction spend per source token, in units
	maxGrowth     int     // largest single-step growth since the last checkpoint
	previous      int     // context size at the previous evaluation
}

func newCompactionPlanner(priorCheckpoint int) *compactionPlanner {
	return &compactionPlanner{checkpoint: max(0, priorCheckpoint)}
}

// prices returns the main and compaction prices in one unit system.
func (p *compactionPlanner) prices(a *Agent) (main, compaction provider.ModelPrice, units string) {
	main, mainKnown, _ := provider.LookupPriceKnown(a.Model)
	compactionModel := a.Model
	if a.CompactionProvider != nil {
		compactionModel = a.CompactionProvider.Model
	}
	compaction, compactionKnown, _ := provider.LookupPriceKnown(compactionModel)
	if mainKnown && compactionKnown && main.InputPricePerToken > 0 {
		return main, compaction, "usd"
	}
	// Token units: an uncached token costs one, a cached token is treated as
	// free because its real price is unknown.
	one := provider.ModelPrice{InputPricePerToken: 1, OutputPricePerToken: 1}
	return one, one, "tokens"
}

// observe records one accepted main request. contextTokens is the request's
// context; postCompaction is what would remain after a checkpoint now. When the
// provider does not report cache reads, Crude assumes the prefix was cached
// (the cheaper reading), so missing data never makes it compact sooner.
func (p *compactionPlanner) observe(a *Agent, usage provider.TokenUsage, contextTokens, postCompaction int, estimated bool) {
	main, _, units := p.prices(a)
	if units != p.units {
		p.units, p.excess, p.costPerSource = units, 0, 0
	}
	if estimated || contextTokens <= 0 || contextTokens <= postCompaction {
		return
	}
	cached := usage.PromptTokensCached
	if !usage.CacheReadKnown && a.Provider.ProtocolID() != "ollama" {
		cached = usage.PromptTokens
	}
	cost := main.CalculateCost(usage.PromptTokens, 0, cached)
	if units == "tokens" {
		cost = float64(max(0, usage.PromptTokens-cached))
	}
	p.excess += cost * float64(contextTokens-postCompaction) / float64(contextTokens)
}

// growth tracks the largest step-to-step context increase, the margin the
// physical trigger keeps so the next step still fits.
func (p *compactionPlanner) growth(active int) {
	if p.previous > 0 && active > p.previous {
		p.maxGrowth = max(p.maxGrowth, active-p.previous)
	}
	p.previous = active
}

// buyCost estimates the price of adopting a checkpoint for a source of this
// size: extraction plus the cold cache of the first request afterwards.
func (p *compactionPlanner) buyCost(a *Agent, source, post int) float64 {
	main, compaction, units := p.prices(a)
	extraction := p.costPerSource * float64(source)
	if p.costPerSource <= 0 {
		// Before the first observed checkpoint: one combined extraction reads
		// the source once; a connection known to need staged extraction reads
		// it once per section.
		reads := 1
		connection, model := a.Provider, a.Model
		if a.CompactionProvider != nil {
			connection, model = a.CompactionProvider, a.CompactionProvider.Model
		}
		if !combinedCompactionWorks(connection, model) {
			reads = len(rootprompt.CompactionStages())
		}
		extraction = float64(reads) * compaction.CalculateCost(source, 0, 0)
		extraction += compaction.CalculateCost(0, p.checkpoint, 0)
	}
	cached := main.InputCachedPricePerToken
	if units == "tokens" || cached <= 0 {
		cached = 0
	}
	return extraction + (main.InputPricePerToken-cached)*float64(post)
}

// adoptPrepared reports whether a checkpoint that is already prepared is worth
// adopting now. Its extraction is paid for, so the only remaining price is the
// cold cache of the next request; once the excess spend has covered that,
// carrying the old context any longer is pure waste. An explicit percent keeps
// its exact boundary instead, and the economic floor still applies.
func (p *compactionPlanner) adoptPrepared(a *Agent, active, post, hardLimit int) bool {
	if a.ContextCompactionPercent != 0 || p.units == "" || active < economicFloor(max(p.floor, 1), hardLimit) {
		return false
	}
	main, _, units := p.prices(a)
	cached := main.InputCachedPricePerToken
	if units == "tokens" || cached <= 0 {
		cached = 0
	}
	return p.excess >= (main.InputPricePerToken-cached)*float64(post)
}

// learn records an adopted checkpoint and resets the accumulated excess.
func (p *compactionPlanner) learn(a *Agent, source, checkpoint, after int, usage provider.TokenUsage) {
	_, compaction, units := p.prices(a)
	cost := compaction.CalculateCost(usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensCached)
	if units == "tokens" {
		cost = float64(max(0, usage.PromptTokens-usage.PromptTokensCached) + usage.CompletionTokens)
	}
	if source > 0 && cost > 0 {
		p.costPerSource = cost / float64(source)
	}
	if checkpoint > 0 {
		p.checkpoint = checkpoint
	}
	// The capacity margin is the largest growth since this checkpoint: one
	// past jump (a large paste) must not keep the agent near capacity forever.
	p.excess, p.floor, p.previous, p.maxGrowth = 0, after, after, 0
}

// economicFloor is the smallest context the economic rule may compact. A
// checkpoint is lossy: it trades exact history the agent can still reason
// over for a summary, a cost the spend accounting cannot see. Below half of
// the model's capacity that history is cheap to keep in quality terms, so a
// cheap extraction model next to an expensive main model never compacts a
// large window after a few steps; only fullness or an explicit percent can.
func economicFloor(floor, hardLimit int) int {
	return max(floor, hardLimit/2)
}

// checkpointEstimate is the expected checkpoint size: the last one observed in
// this run, else the agent's last checkpoint from earlier runs, else unknown
// (0). The first real checkpoint calibrates every later decision.
func (p *compactionPlanner) checkpointEstimate() int {
	return p.checkpoint
}

// evaluate decides this step. active is the projected next request, fixed the
// system prompt and tools, tail the verbatim recent context a checkpoint keeps.
func (p *compactionPlanner) evaluate(a *Agent, active, fixed, tail, hardLimit, window int, overflow bool) compactionPlan {
	p.growth(active)
	if overflow || active >= hardLimit {
		return compactionPlan{prepare: true, swap: true, force: true, reason: "context reached the model's capacity"}
	}
	checkpoint := p.checkpointEstimate()
	post := fixed + checkpoint + tail
	source := active - tail
	worthwhile := source-fixed > checkpoint
	floor := max(fixed, p.floor)
	near := hardLimit - max(p.maxGrowth, 1)
	plan := compactionPlan{}
	if percent := a.ContextCompactionPercent; percent > 0 {
		// An explicit fullness is a promise to the user: past it the next
		// request waits for a checkpoint rather than carrying the context.
		threshold := min(hardLimit, max(1, window*percent/100))
		plan.swap = active >= threshold
		plan.force = plan.swap
		plan.prepare = active >= floor+(threshold-floor)/2
		plan.reason = fmt.Sprintf("context reached the configured %d%%", percent)
	} else if quality := economicFloor(floor, hardLimit); active >= quality*3/4 {
		buy := p.buyCost(a, source, post)
		// Excess grows roughly quadratically with steps, so a quarter of the
		// price is halfway in steps.
		plan.swap = active >= quality && buy > 0 && p.excess >= buy
		plan.prepare = buy > 0 && p.excess >= buy/4
		plan.reason = "carrying the older context now costs more than a checkpoint"
	}
	if active >= near {
		plan.swap, plan.reason = true, "context is approaching the model's capacity"
	}
	if active >= floor+(hardLimit-floor)/2 {
		plan.prepare = true
	}
	if !worthwhile && !plan.force {
		// A checkpoint would not be smaller than what it replaces.
		return compactionPlan{}
	}
	plan.prepare = plan.prepare || plan.swap
	return plan
}

// verbatimTailCut chooses where a checkpoint ends and verbatim recent context
// begins in msgs (msgs[0] is the system prompt). It prefers keeping the
// current and previous human turns whole, then recent tool rounds, and never
// keeps a tail larger than the part being summarized. It returns len(msgs)
// when nothing can stay verbatim, and whether the tail holds human input.
func verbatimTailCut(msgs []provider.ChatMessage, role, model string) (int, bool) {
	if len(msgs) < 3 {
		return len(msgs), false
	}
	suffix := make([]int, len(msgs)+1)
	for index := len(msgs) - 1; index >= 1; index-- {
		suffix[index] = suffix[index+1] + provider.EstimateMessageTokens(msgs[index], model)
	}
	total := suffix[1]
	human := func(message provider.ChatMessage) bool {
		return message.IsGenuineUserInput() || role == "child" && message.EffectiveOrigin() == provider.OriginAssignment
	}
	var turns, rounds []int
	for index := 1; index < len(msgs); index++ {
		switch {
		case human(msgs[index]):
			turns, rounds = append(turns, index), nil
		case msgs[index].Role == "assistant" && len(turns) > 0:
			rounds = append(rounds, index)
		}
	}
	var candidates []int
	for _, turn := range turns[max(0, len(turns)-2):] {
		if turn >= 2 {
			candidates = append(candidates, turn)
		}
	}
	// Then tool rounds of the current turn, oldest first (largest tail).
	candidates = append(candidates, rounds...)
	for _, cut := range candidates {
		if msgs[cut].Role == "tool" || suffix[cut] > total-suffix[cut] {
			continue
		}
		tailHuman := false
		for _, message := range msgs[cut:] {
			tailHuman = tailHuman || human(message)
		}
		return cut, tailHuman
	}
	return len(msgs), false
}

// contextDigest identifies a context prefix by its messages' durable IDs
// (their content where a message has none), so a checkpoint prepared in one
// run matches the same history resumed in the next.
func contextDigest(messages []provider.ChatMessage) [32]byte {
	hash := sha256.New()
	for _, message := range messages {
		if message.InternalID != "" {
			hash.Write([]byte("id:" + message.InternalID))
		} else {
			encoded, _ := json.Marshal(message)
			hash.Write(encoded)
		}
		hash.Write([]byte{0})
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

// PrecompactionSlot owns at most one background checkpoint preparation for one
// agent. It outlives individual runs, so ending a short human turn does not
// discard a checkpoint that is already paid for; the next run adopts it when
// its source is still the start of the context. The owner closes the slot
// with the agent's lifetime.
type PrecompactionSlot struct {
	mu       sync.Mutex
	lifetime context.Context
	emit     func(AgentEvent)
	job      *precompactionJob
	tasks    sync.WaitGroup
	retryAt  time.Time
	failures int
	closed   bool
	// planner carries what the agent has learned about context cost across
	// runs: a chatty session of short turns still accumulates its spend.
	planner *compactionPlanner
}

// plannerFor returns the slot's planner, creating it on first use.
func (slot *PrecompactionSlot) plannerFor(priorCheckpoint int) *compactionPlanner {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.planner == nil {
		slot.planner = newCompactionPlanner(priorCheckpoint)
	}
	return slot.planner
}

type precompactionJob struct {
	requestID    string
	cut          int
	digest       [32]byte
	sourceTokens int
	tailHasHuman bool
	model        string
	cancel       context.CancelFunc
	done         chan struct{}
	outcome      compactOutcome
	err          error
}

// NewPrecompactionSlot creates a slot whose background work stops when
// lifetime ends. emit receives background progress and usage when no run is
// attached.
func NewPrecompactionSlot(lifetime context.Context, emit func(AgentEvent)) *PrecompactionSlot {
	if emit == nil {
		emit = func(AgentEvent) {}
	}
	return &PrecompactionSlot{lifetime: lifetime, emit: emit}
}

// Close cancels background preparation and waits for it to stop.
func (slot *PrecompactionSlot) Close() {
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.closed = true
	if slot.job != nil {
		slot.job.cancel()
	}
	slot.mu.Unlock()
	slot.tasks.Wait()
}

func (slot *PrecompactionSlot) current() *precompactionJob {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.job
}

func (slot *PrecompactionSlot) discard(job *precompactionJob) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.job == job {
		job.cancel()
		slot.job = nil
	}
}

func (job *precompactionJob) ready() bool {
	select {
	case <-job.done:
		return true
	default:
		return false
	}
}

// matches reports whether the job summarized exactly the start of msgs.
func (job *precompactionJob) matches(msgs []provider.ChatMessage, model string) bool {
	return job.cut <= len(msgs) && job.model == model && contextDigest(msgs[1:job.cut]) == job.digest
}

// start begins preparing a checkpoint of msgs[1:cut] unless one is already in
// flight or recent failures are backing off.
func (slot *PrecompactionSlot) start(a *Agent, msgs []provider.ChatMessage, tools []provider.ToolDef, cut int, tailHasHuman bool, targetTokens, window, sourceTokens int) bool {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.closed || slot.job != nil || time.Now().Before(slot.retryAt) || cut < 2 {
		return false
	}
	compactor := a.backgroundCompactor(window, targetTokens, tailHasHuman)
	source := append([]provider.ChatMessage(nil), msgs[:cut]...)
	ctx, cancel := context.WithCancel(slot.lifetime)
	job := &precompactionJob{
		requestID: fmt.Sprintf("%016x%016x", rand2.Uint64(), rand2.Uint64()),
		cut:       cut, digest: contextDigest(source[1:]), tailHasHuman: tailHasHuman,
		sourceTokens: max(sourceTokens, estimateCanonicalRequest(source, tools, a.Model)),
		model:        a.Model, cancel: cancel, done: make(chan struct{}),
	}
	slot.job = job
	slot.tasks.Add(1)
	emit := slot.backgroundEmit(job.requestID)
	go func() {
		defer slot.tasks.Done()
		defer close(job.done)
		defer cancel()
		emit(AgentEvent{Type: "context_precompaction", Data: map[string]any{"phase": "started", "request_id": job.requestID, "before_tokens": job.sourceTokens}})
		job.outcome, job.err = compactor.compactWithModel(ctx, source, tools, job.requestID, job.sourceTokens, "background", emit)

		slot.mu.Lock()
		defer slot.mu.Unlock()
		if job.err == nil {
			slot.failures, slot.retryAt = 0, time.Time{}
			emit(AgentEvent{Type: "context_precompaction", Data: map[string]any{"phase": "ready", "request_id": job.requestID, "before_tokens": job.sourceTokens, "after_tokens": job.outcome.AfterTokens}})
			return
		}
		if slot.job == job {
			slot.job = nil
		}
		if ctx.Err() != nil {
			return
		}
		slot.failures++
		slot.retryAt = time.Now().Add(retry.Exponential(slot.failures, time.Second, retry.MaxCooldown))
		if concurrencyRefusal(job.err) && provider.SameConnection(compactor.Provider, a.Provider) {
			_ = provider.RecordConcurrencyRefused(compactor.Provider, compactor.Model)
		}
		emit(AgentEvent{Type: "context_precompaction", Data: map[string]any{"phase": "failed", "request_id": job.requestID, "error": compactErrorText(job.err), "retry_in_seconds": time.Until(slot.retryAt).Seconds()}})
	}()
	return true
}

// backgroundEmit forwards spend and progress without presenting background
// work as a blocking compaction of the agent.
func (slot *PrecompactionSlot) backgroundEmit(requestID string) func(AgentEvent) {
	return func(event AgentEvent) {
		switch event.Type {
		case "usage":
			slot.emit(event)
		case "context_compaction_stage", "context_compaction_retrying":
			data := map[string]any{"phase": "progress", "request_id": requestID}
			if values, ok := event.Data.(map[string]any); ok {
				data["stage_label"] = values["stage_label"]
				if retry, ok := values["retry"]; ok {
					data["retry"] = retry
				}
			}
			slot.emit(AgentEvent{Type: "context_precompaction", Data: data})
		}
	}
}

// wait blocks until the job finishes or ctx ends.
func (job *precompactionJob) wait(ctx context.Context) error {
	select {
	case <-job.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func concurrencyRefusal(err error) bool {
	var status *provider.StatusError
	if errors.As(err, &status) && (status.StatusCode == 429 || status.StatusCode == 503 || status.StatusCode == 529) {
		return true
	}
	text := strings.ToLower(fmt.Sprint(err))
	return strings.Contains(text, "concurren") || strings.Contains(text, "too many requests") || strings.Contains(text, "overloaded")
}

// backgroundCompactor is an independent agent instance for extraction. It
// shares no mutable request state with the running agent and never persists:
// adoption commits through the running agent's owners.
func (a *Agent) backgroundCompactor(window, targetTokens int, tailHasHuman bool) *Agent {
	return a.compactorFor(false, window, targetTokens, tailHasHuman)
}

// compactorFor builds the extraction agent; useMain selects the agent's own
// connection, the fallback when the configured compaction model is failing.
func (a *Agent) compactorFor(useMain bool, window, targetTokens int, tailHasHuman bool) *Agent {
	compactor := a
	if !useMain {
		compactor = a.contextCompactor(window)
	}
	if compactor == a {
		compactor = NewAgent(a.Provider, a.Model, a.logger)
		compactor.AgentID, compactor.TurnGeneration, compactor.Depth, compactor.ParentID = a.AgentID, a.TurnGeneration, a.Depth, a.ParentID
		compactor.WorkDir, compactor.AsyncMgr, compactor.SubAgentMgr = a.WorkDir, a.AsyncMgr, a.SubAgentMgr
		compactor.ContinuationContext, compactor.LatestHumanTurnState = a.ContinuationContext, a.LatestHumanTurnState
		compactor.guidance, compactor.MaxRetries, compactor.FilteredTools = a.guidance, a.MaxRetries, a.FilteredTools
		compactor.testHTTPClient, compactor.compactionRetryDelay = a.testHTTPClient, a.compactionRetryDelay
		compactor.CompactionTargetModel, compactor.CompactionTargetWindow = a.Model, window
		compactor.CompactionRetentionTokens, compactor.MaxOutputTokens = a.CompactionRetentionTokens, a.MaxOutputTokens
		compactor.CompactionAgentProfile = a.CompactionAgentProfile
	}
	compactor.PrepareCompaction, compactor.CommitCompaction = nil, nil
	compactor.CompactionTargetTokens = max(1, targetTokens)
	compactor.compactionTailHasHuman = tailHasHuman
	return compactor
}

// compactionStorageError marks a failure to persist a checkpoint, which stops
// the run as a storage fault rather than a deferrable compaction failure.
type compactionStorageError struct{ err error }

func (e *compactionStorageError) Error() string { return e.err.Error() }
func (e *compactionStorageError) Unwrap() error { return e.err }

func tailTokensOf(messages []provider.ChatMessage, model string) int {
	return provider.EstimateMessagesTotal(messages, model)
}

// reobtainableTools return data that can be fetched again on demand; their
// old results carry no information the agent cannot recover.
var reobtainableTools = map[string]bool{
	"read_file": true, "grep_files": true, "glob_files": true, "list_directory": true,
	"web_fetch": true, "web_search": true, "arxiv_search": true,
	"read_async_output": true, "check_async_command": true, "recall_memory": true, "search_history": true,
	"get_plan": true, "board_read": true, "artifact_read": true, "agent_inspect": true, "agent_list": true,
}

// decayVerbatimTail replaces re-obtainable tool results older than the latest
// tool round with one-line stubs. It runs only at a checkpoint, where the
// cached prefix is rebuilt anyway, so it never costs an extra cold request.
// The latest round stays whole because the agent may still be reading it;
// command output, edits and external results always stay verbatim.
func decayVerbatimTail(tail []provider.ChatMessage, model string) []provider.ChatMessage {
	latest := -1
	calls := make(map[string]provider.ToolCall)
	for index, message := range tail {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			latest = index
			for _, call := range message.ToolCalls {
				calls[call.ID] = call
			}
		}
	}
	out := make([]provider.ChatMessage, len(tail))
	copy(out, tail)
	for index := 0; index < latest; index++ {
		message := out[index]
		call, known := calls[message.ToolCallID]
		if message.Role != "tool" || !known || !reobtainableTools[call.Function.Name] {
			continue
		}
		original := message.ContentString()
		stub := rootprompt.ElidedToolResult(call.Function.Name, tools.SummarizeArgs(call.Function.Name, call.Function.Arguments), len([]rune(original)))
		elided := message
		elided.Content = provider.TextContent(stub)
		if provider.EstimateMessageTokens(elided, model) >= provider.EstimateMessageTokens(message, model) {
			continue
		}
		if message.InternalID != "" {
			elided.InternalID = message.InternalID + "-elided"
		}
		out[index] = elided
	}
	return out
}

// compactionModelUnavailable reports a compaction failure caused by the
// connection itself (credentials, a missing model, an outage or rate limit
// that outlasted its retries) rather than by the request. Limit and shape
// rejections (context, output caps a provider over-advertises, invalid
// request sizes) are the extractor's to adapt: smaller chunks, staged
// sections, learned ceilings.
func compactionModelUnavailable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || provider.IsContextWindowExceeded(err) {
		return false
	}
	var status *provider.StatusError
	if errors.As(err, &status) {
		switch {
		case status.StatusCode == 401, status.StatusCode == 403, status.StatusCode == 404,
			status.StatusCode == 408, status.StatusCode == 429, status.StatusCode >= 500:
			return true
		}
		return false
	}
	var api *provider.APIError
	if errors.As(err, &api) {
		kind := strings.ToLower(api.Type + " " + api.Code)
		return strings.Contains(kind, "auth") || strings.Contains(kind, "permission") || strings.Contains(kind, "not_found") ||
			strings.Contains(kind, "overload") || strings.Contains(kind, "rate") || strings.Contains(kind, "unavailable")
	}
	// Transport failures (refused, reset, timed out) carry no status.
	return provider.Retryable(err)
}
