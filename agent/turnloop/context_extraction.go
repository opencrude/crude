package turnloop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
)

const (
	// compactionAdaptations bounds the limit adaptations of one compaction.
	// Each one makes the next request strictly smaller, so extraction either
	// converges or stops with the provider's own evidence.
	compactionAdaptations = 16
	// minimumExtractionPart is the least source, in estimated tokens, one part
	// of a chained extraction carries.
	minimumExtractionPart = 256
	// compactionRefinements bounds how often a too-long result is shortened.
	compactionRefinements = 3
	// unexplainedFloor is the smallest input, in estimated tokens, tried
	// after rejections that name no cause.
	unexplainedFloor = 4096
)

// errExtractionAdapted reports that the limits narrowed and the request no
// longer fits them; the caller plans a smaller one.
var errExtractionAdapted = errors.New("extraction limits changed")

// extractionFailure is a failure Crude found in planning or checking an
// extraction, not a provider or transport error. It never means that the
// compaction connection is unavailable.
type extractionFailure string

func (e extractionFailure) Error() string { return string(e) }

// extractionLimits is the one authority for what an extraction request on
// this connection may carry. It starts from the advertised context window,
// the configured output limits and the limits learned for the connection,
// and narrows on provider evidence. Every stage of one compaction shares it.
type extractionLimits struct {
	agent *Agent
	// window is the context window in Crude's token estimate; 0 when unknown.
	window int
	// input is the largest estimated input one request may carry.
	input int
	// output is the proven output ceiling; 0 when none is known.
	output int
	// bytes is the largest estimated request body; 0 when none is known.
	bytes int
	// usageRatio is how far provider counts exceeded Crude's estimates.
	usageRatio float64
	sectioned  bool
	// adaptations counts narrowings.
	adaptations int
	// probe holds the limits from before smaller attempts after an
	// unexplained rejection; they are restored when even a small request is
	// refused the same way, and then size is known not to be the cause.
	probe       *[2]int
	probeErr    error
	unexplained bool
	// pending limits were inferred from a recovery; they are remembered only
	// when the smaller request succeeds.
	pending []pendingLimit
}

type pendingLimit struct {
	kind  provider.LearnedLimit
	value int
}

func (a *Agent) extractionLimits() *extractionLimits {
	learned := provider.LearnedRequestLimits(a.CompactionAgentProfile, a.Provider, a.Model)
	limits := &extractionLimits{agent: a, usageRatio: 1, output: learned.Output, bytes: learned.Bytes, sectioned: learned.Sectioned}
	limits.window = a.Provider.ContextWindow(a.Model)
	if learned.Window > 0 && (limits.window <= 0 || learned.Window < limits.window) {
		limits.window = learned.Window
	}
	limits.input = 128_000
	if limits.window > 0 {
		limits.input = limits.capacity()
	}
	if learned.Input > 0 {
		limits.input = min(limits.input, learned.Input)
	}
	return limits
}

func (l *extractionLimits) capacity() int {
	if l.window <= 0 {
		return l.input
	}
	return l.window - contextSafetyReserve(l.window) - 1
}

// admissible is the largest estimated input one request may carry: the
// adapted input limit, and the capacity scaled by how far the provider's
// counts exceeded Crude's estimates.
func (l *extractionLimits) admissible() int {
	if l.usageRatio > 1 {
		return min(l.input, int(float64(l.capacity())/l.usageRatio))
	}
	return l.input
}

func (l *extractionLimits) fits(tokens, bytes int) bool {
	return tokens <= l.admissible() && (l.bytes <= 0 || bytes <= l.bytes)
}

// response is the output allowance for a request of input estimated tokens.
// Zero means the provider default; ok is false when no output room remains.
func (l *extractionLimits) response(input int) (limit int, ok bool) {
	limit = l.agent.compactionOutputLimit(l.window, 0)
	if l.output > 0 && (limit <= 0 || l.output < limit) {
		limit = l.output
	}
	if l.window > 0 {
		predicted := int(math.Ceil(float64(input) * max(1, l.usageRatio)))
		available := l.window - predicted - contextSafetyReserve(l.window)
		if available <= 0 {
			return 0, false
		}
		if limit <= 0 || available < limit {
			limit = available
		}
	}
	return limit, true
}

// observe calibrates estimates against the provider's count of a request.
func (l *extractionLimits) observe(input, promptTokens int) {
	if l.window > 0 && input > 0 && promptTokens > input && promptTokens <= l.window-contextSafetyReserve(l.window) {
		l.usageRatio = max(l.usageRatio, float64(promptTokens)/float64(input))
	}
}

// adapt narrows the limits from the evidence in err about a request of input
// estimated tokens and bytes that asked for sent output tokens. It reports
// whether a limit changed; each change makes the next request smaller.
func (l *extractionLimits) adapt(err error, input, bytes, sent, attempt int) bool {
	if l.adaptations >= compactionAdaptations {
		return false
	}
	changed := false
	rejection, tooLarge := provider.RequestTooLarge(err, sent)
	switch {
	case tooLarge && rejection.Limit == provider.RejectedOutput:
		ceiling, lifetime := rejection.Maximum, provider.StatedLimitLifetime
		if ceiling <= 0 {
			ceiling, lifetime = sent/2, provider.InferredLimitLifetime
		}
		changed = l.lower(provider.LearnedOutput, &l.output, ceiling, lifetime, true)
	case tooLarge && rejection.Limit == provider.RejectedContext:
		if rejection.Stated() {
			// The provider counts tokens its own way; convert its ceiling into
			// Crude's estimate of the same request.
			window := rejection.Maximum
			if rejection.Counted > 0 && input > 0 {
				window = int(int64(rejection.Maximum) * int64(input) / int64(rejection.Counted))
			}
			if window > 0 && (l.window <= 0 || window < l.window) {
				l.window, changed = window, true
				l.record(provider.LearnedWindow, window, provider.StatedLimitLifetime)
				l.input = min(l.input, l.capacity())
			}
		}
		if !changed {
			changed = l.lower(provider.LearnedInput, &l.input, min(l.admissible(), input)*2/3, provider.InferredLimitLifetime, true)
		}
	case tooLarge && rejection.Limit == provider.RejectedBody:
		limit, lifetime := rejection.Maximum*9/10, provider.StatedLimitLifetime
		if limit <= 0 || limit >= bytes {
			limit, lifetime = bytes*2/3, provider.InferredLimitLifetime
		}
		changed = l.lower(provider.LearnedBytes, &l.bytes, limit, lifetime, true)
	case !tooLarge && !l.unexplained && provider.UnexplainedRejection(err):
		// A refusal without a cause may still be a size limit the route does
		// not name: halve the request until it is accepted or small.
		if l.probe == nil {
			l.probe, l.probeErr = &[2]int{l.input, l.output}, err
		}
		next := min(l.admissible(), input) / 2
		if next < unexplainedFloor {
			// Even a small request is refused: size is not the cause, and
			// the caller returns this refusal.
			_ = l.endProbe()
			return false
		}
		changed = l.lower(provider.LearnedInput, &l.input, next, provider.InferredLimitLifetime, false)
		if sent > 2*unexplainedFloor {
			changed = l.lower(provider.LearnedOutput, &l.output, sent/2, provider.InferredLimitLifetime, false) || changed
		}
	case !tooLarge && attempt > 0 && input > 8192 && provider.TimedOut(err):
		// The same large request timed out twice; a smaller one may finish.
		changed = l.lower(provider.LearnedInput, &l.input, min(l.admissible(), input)/2, provider.InferredLimitLifetime, false)
	}
	if changed {
		l.adaptations++
	}
	return changed
}

// endProbe restores the limits from before the smaller attempts and returns
// the refusal that started them, or nil when no attempt is in progress.
func (l *extractionLimits) endProbe() error {
	if l.probe == nil {
		return nil
	}
	refused := l.probeErr
	l.input, l.output = l.probe[0], l.probe[1]
	l.probe, l.probeErr, l.pending, l.unexplained = nil, nil, nil, true
	return refused
}

// truncated gives the next request more response room by carrying less
// source. A spent response allowance is not a provider limit to remember.
func (l *extractionLimits) truncated(input int) bool {
	if l.adaptations >= compactionAdaptations || input <= minimumExtractionPart {
		return false
	}
	previous := l.admissible()
	l.input = min(l.input, max(1, input/2))
	if l.admissible() >= previous {
		return false
	}
	l.adaptations++
	return true
}

func (l *extractionLimits) lower(kind provider.LearnedLimit, field *int, value int, lifetime time.Duration, now bool) bool {
	if value <= 0 || *field > 0 && value >= *field {
		return false
	}
	*field = value
	if now {
		l.record(kind, value, lifetime)
	} else {
		l.pending = append(l.pending, pendingLimit{kind: kind, value: value})
	}
	return true
}

// confirm remembers the inferred limits once a request within them succeeded.
func (l *extractionLimits) confirm() {
	for _, limit := range l.pending {
		l.record(limit.kind, limit.value, provider.InferredLimitLifetime)
	}
	l.pending, l.probe, l.probeErr = nil, nil, nil
}

func (l *extractionLimits) record(kind provider.LearnedLimit, value int, lifetime time.Duration) {
	a := l.agent
	if err := provider.RecordRequestLimit(a.CompactionAgentProfile, a.Provider, a.Model, kind, value, lifetime); err != nil && a.logger != nil {
		// The limit still applies to this compaction; only the next run loses it.
		a.logger.Warn("COMPACT", "learned_limit_not_saved", "kind", string(kind), "error", err.Error())
	}
}

func (l *extractionLimits) recordSectioned() {
	l.sectioned = true
	l.record(provider.LearnedSectioned, 1, provider.InferredLimitLifetime)
}

type compactionExtractor struct {
	agent        *Agent
	ctx          context.Context
	emit         func(AgentEvent)
	requestID    string
	trigger      string
	role         string
	stage        rootprompt.CompactionStage
	stageIndex   int
	beforeTokens int
	targetWindow int
	common       provider.ChatMessage
	system       provider.ChatMessage
	// tools are the conversation's tools, declared (but forbidden) only when
	// the writer is the serving model, so they keep its cached prefix.
	tools        []provider.ToolDef
	limits       *extractionLimits
	outputBudget int
	usage        provider.TokenUsage
	retryAttempt int
	refinements  int
}

// request extracts the stage from source: in one request when it fits the
// limits, otherwise as a chain of chronological parts.
func (x *compactionExtractor) request(source []provider.ChatMessage, directive string) (string, error) {
	messages := x.messages(nil, source, directive, x.outputBudget)
	if x.limits.fits(x.estimate(messages), x.bytes(messages)) {
		text, err := x.send(messages)
		if err == nil {
			return x.fit(text, directive)
		}
		if !errors.Is(err, errExtractionAdapted) {
			return "", err
		}
	}
	return x.chain(source, directive)
}

// chain reads source in chronological parts like a ledger: each request
// carries the checkpoint of every earlier part and returns it updated with
// the next part, so the result is one coherent checkpoint rather than a merge
// of separate summaries. When a limit narrows, the same part is planned again
// from the same running checkpoint; finished parts are never read again.
func (x *compactionExtractor) chain(source []provider.ChatMessage, directive string) (string, error) {
	cursor := newExtractionCursor(source)
	running, part, stalled := "", 0, 0
	for !cursor.done() {
		if err := x.ctx.Err(); err != nil {
			return "", err
		}
		var earlier []provider.ChatMessage
		if running != "" {
			checkpoint := provider.NewTextMessage("user", rootprompt.CompactionRunningCheckpoint(part, running))
			checkpoint.Origin, checkpoint.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
			earlier = []provider.ChatMessage{checkpoint}
		}
		// Whether this part is the last is known only after it is taken; the
		// longer directive bounds the envelope either way.
		longest := rootprompt.CompactionChainDirective(part+1, true, directive)
		if other := rootprompt.CompactionChainDirective(part+1, false, directive); len(other) > len(longest) {
			longest = other
		}
		envelope := x.messages(earlier, nil, longest, x.outputBudget)
		tokens := x.limits.admissible() - x.estimate(envelope)
		bytes := 0
		if x.limits.bytes > 0 {
			bytes = x.limits.bytes - x.bytes(envelope)
		}
		if tokens < minimumExtractionPart || x.limits.bytes > 0 && bytes < minimumExtractionPart*4 {
			if running == "" {
				if refused := x.limits.endProbe(); refused != nil {
					// Smaller attempts after a refusal without a cause reached
					// the size of the instructions: size is not the cause.
					return "", refused
				}
				return "", extractionFailure("the compaction model has no room for the history after the system prompt, tools and instructions")
			}
			shorter, err := x.shorten(running, directive)
			if err != nil {
				return "", err
			}
			running = shorter
			continue
		}
		var messages []provider.ChatMessage
		var next extractionCursor
		for fit := 0; ; fit++ {
			var block []provider.ChatMessage
			block, next = cursor.take(tokens, bytes)
			space := x.carry()
			if next.done() {
				space = x.outputBudget
			}
			messages = x.messages(earlier, block, rootprompt.CompactionChainDirective(part+1, next.done(), directive), space)
			// The estimate of the whole request is the authority; trim the
			// part by what it exceeds.
			overTokens := x.estimate(messages) - x.limits.admissible()
			overBytes := 0
			if x.limits.bytes > 0 {
				overBytes = x.bytes(messages) - x.limits.bytes
			}
			if overTokens <= 0 && overBytes <= 0 || fit == 2 {
				break
			}
			tokens -= max(0, overTokens) + 16
			if bytes > 0 {
				bytes -= max(0, overBytes) + 64
			}
		}
		x.progress("Reading part", part+1, part+1+next.parts(tokens), 0, nil)
		adaptations := x.limits.adaptations
		text, err := x.send(messages)
		if errors.Is(err, errExtractionAdapted) {
			// A narrower limit plans this part again. A part that did not fit
			// without new evidence would be planned the same way forever.
			if stalled++; x.limits.adaptations == adaptations && stalled > 2 {
				return "", extractionFailure("compaction could not plan a request within the model's limits")
			}
			continue
		}
		if err != nil {
			return "", err
		}
		running, cursor, part, stalled = text, next, part+1, 0
	}
	return x.fit(running, directive)
}

// carry is the space a running checkpoint may take between parts.
func (x *compactionExtractor) carry() int {
	return min(x.outputBudget, max(1024, x.limits.admissible()/4))
}

// shorten rewrites a running checkpoint that leaves no room for the next part.
func (x *compactionExtractor) shorten(running, directive string) (string, error) {
	if x.refinements >= compactionRefinements {
		return "", extractionFailure("the running checkpoint leaves no room for the rest of the history")
	}
	x.refinements++
	before := provider.EstimateTokens(running, x.agent.Model)
	x.progress("Shortening the checkpoint", 0, 0, 0, nil)
	source := provider.NewTextMessage("user", running)
	source.Origin, source.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
	messages := x.messages(nil, []provider.ChatMessage{source}, rootprompt.CompactionRefinementDirective(directive), x.carry())
	if !x.limits.fits(x.estimate(messages), x.bytes(messages)) {
		return "", extractionFailure("the running checkpoint no longer fits the compaction model")
	}
	text, err := x.send(messages)
	if errors.Is(err, errExtractionAdapted) {
		return "", extractionFailure("the running checkpoint no longer fits the compaction model")
	}
	if err != nil {
		return "", err
	}
	if provider.EstimateTokens(text, x.agent.Model) >= before {
		return "", extractionFailure("shortening the running checkpoint did not reduce it")
	}
	return text, nil
}

// fit shortens a result that exceeds the stage's checkpoint space.
func (x *compactionExtractor) fit(text, directive string) (string, error) {
	for provider.EstimateTokens(text, x.agent.Model) > x.outputBudget {
		if x.refinements >= compactionRefinements {
			return "", extractionFailure("extracted checkpoint cannot fit the continuation context")
		}
		x.refinements++
		before := provider.EstimateTokens(text, x.agent.Model)
		x.progress("Shortening the checkpoint", 0, 0, 0, nil)
		refined := provider.NewTextMessage("user", text)
		refined.Origin, refined.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
		shorter, err := x.request([]provider.ChatMessage{refined}, rootprompt.CompactionRefinementDirective(directive))
		if err != nil {
			return "", err
		}
		if provider.EstimateTokens(shorter, x.agent.Model) >= before {
			return "", extractionFailure("checkpoint refinement did not reduce its source")
		}
		text = shorter
	}
	return text, nil
}

// send performs one extraction request. Transient failures retry it; limit
// evidence narrows the limits and retries it while it still fits them, and
// otherwise returns errExtractionAdapted so the caller plans a smaller one.
func (x *compactionExtractor) send(messages []provider.ChatMessage) (string, error) {
	input, bytes := x.estimate(messages), x.bytes(messages)
	var lastErr error
	for attempt := 0; ; {
		if err := x.ctx.Err(); err != nil {
			return "", err
		}
		if !x.limits.fits(input, bytes) {
			return "", errExtractionAdapted
		}
		response, room := x.limits.response(input)
		if !room {
			if x.limits.truncated(input) {
				return "", errExtractionAdapted
			}
			return "", &provider.ContextWindowError{InputTokens: input, ContextWindow: x.limits.window}
		}
		completion, err := x.agent.completeStreamWithOptions(x.ctx, messages, x.tools, nil, nil, nil, nil, nil, nil, nil,
			completionRequestOptions{toolChoice: "none", maxOutputTokens: response, maxAttempts: 1, compaction: true, inputTokens: input})
		// Preparation, subsequent reduction and fallback share one operation,
		// but each billable request needs its own durable usage row.
		x.usage = x.usage.Add(completion.Usage)
		x.agent.emitCompactionUsage(x.emit, completion.Usage, provider.NewMessageID(), x.requestID, x.trigger, x.role, x.stage.Name, attempt > 0 || err != nil, err == nil)
		x.limits.observe(input, completion.Usage.PromptTokens)
		if err == nil {
			err = validateExtraction(completion)
		}
		if err == nil {
			x.limits.confirm()
			return strings.TrimSpace(completion.Content), nil
		}
		if x.ctx.Err() != nil {
			return "", x.ctx.Err()
		}
		if isTruncatedFinishReason(completion.FinishReason) {
			// Provider completion tokens include reasoning. Never repeat an
			// unchanged request after its full response allowance was spent.
			if x.limits.truncated(input) {
				x.progress("Making room for the reply", 0, 0, attempt+1, err)
				return "", errExtractionAdapted
			}
			return "", err
		}
		if x.limits.adapt(err, input, bytes, response, attempt) {
			x.progress("Adapting to the model's limits", 0, 0, attempt+1, err)
			continue
		}
		lastErr = err
		if !isRetryableCompactionError(err) || attempt >= compactionStageRecoveries {
			return "", lastErr
		}
		attempt++
		x.retryAttempt = attempt
		delay := x.agent.compactionRecoveryDelay(attempt, err)
		data := x.eventData()
		data["retry"], data["retry_limit"], data["backoff"], data["error"] = attempt, compactionStageRecoveries, delay.Seconds(), compactErrorText(err)
		x.emit(AgentEvent{Type: "context_compaction_retrying", Data: data})
		if err := waitCompactionRetry(x.ctx, delay); err != nil {
			return "", err
		}
	}
}

func validateExtraction(completion completionResult) error {
	if len(completion.ToolCalls) > 0 {
		return extractionFailure("compaction extraction returned tool calls")
	}
	if isTruncatedFinishReason(completion.FinishReason) {
		return extractionFailure("compaction extraction ended at its output limit; incomplete checkpoint rejected")
	}
	return validateCompactionStageText(completion.Content)
}

// messages is the exact request for one extraction: the system prompt, the
// running checkpoint of earlier parts, the source, the shared context and the
// stage instruction with the space its answer may take.
func (x *compactionExtractor) messages(earlier, source []provider.ChatMessage, directive string, space int) []provider.ChatMessage {
	instruction := provider.NewTextMessage("user", rootprompt.CompactionStageInstruction(x.role, x.stage, space, directive))
	instruction.Origin, instruction.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
	messages := make([]provider.ChatMessage, 0, len(earlier)+len(source)+3)
	messages = append(messages, x.system)
	messages = append(messages, earlier...)
	messages = append(messages, source...)
	return append(messages, x.common, instruction)
}

// estimate is the input of a request as sent, declared tools included.
func (x *compactionExtractor) estimate(messages []provider.ChatMessage) int {
	return estimateCanonicalRequest(provider.FilterReplayMessages(messages, x.agent.Provider), x.tools, x.agent.Model)
}

func (x *compactionExtractor) bytes(messages []provider.ChatMessage) int {
	return estimateRequestBytes(provider.FilterReplayMessages(messages, x.agent.Provider), x.tools)
}

func (x *compactionExtractor) eventData() map[string]any {
	data := x.agent.compactionEventData(x.requestID, x.trigger, x.role, x.stageIndex+1, x.stageIndex, x.beforeTokens, 0, x.targetWindow)
	data["stage_name"], data["stage_label"] = x.stage.Name, x.stage.Label
	return data
}

func (x *compactionExtractor) progress(label string, chunk, count, retry int, err error) {
	data := x.eventData()
	data["stage_label"] = x.stage.Label + " · " + label
	if count > 0 {
		data["stage_label"] = fmt.Sprintf("%s · %s %d/%d", x.stage.Label, label, chunk, count)
	}
	data["chunk"], data["chunk_count"], data["retry"] = chunk, count, retry
	if err != nil {
		data["error"] = compactErrorText(err)
	}
	x.emit(AgentEvent{Type: "context_compaction_stage", Data: data})
}

// extractionCursor reads the source front to back as attributed text. A part
// takes whole messages, or a piece of one, within its token and byte room;
// every piece repeats its message's role, origin and tool-result identity.
// Inline media is described, not sent again.
type extractionCursor struct {
	source  []provider.ChatMessage
	texts   []string
	runes   []int
	message int
	offset  int // byte offset into texts[message], on a rune boundary
	read    int // runes of texts[message] before offset
}

func newExtractionCursor(source []provider.ChatMessage) extractionCursor {
	cursor := extractionCursor{source: source, texts: make([]string, len(source)), runes: make([]int, len(source))}
	for index, message := range source {
		cursor.texts[index] = extractionSourceText(message, index)
		cursor.runes[index] = utf8.RuneCountInString(cursor.texts[index])
	}
	return cursor
}

func (c extractionCursor) done() bool { return c.message >= len(c.texts) }

// parts estimates how many more parts of tokens the unread source needs.
func (c extractionCursor) parts(tokens int) int {
	if c.done() || tokens <= 0 {
		return 0
	}
	remaining := c.runes[c.message] - c.read
	for _, count := range c.runes[c.message+1:] {
		remaining += count
	}
	return int(math.Ceil(float64(remaining) / 4 / float64(tokens)))
}

// take returns the next part within tokens estimated tokens and bytes bytes
// (0: no byte limit) and the cursor after it. A part always advances.
func (c extractionCursor) take(tokens, bytes int) ([]provider.ChatMessage, extractionCursor) {
	var part []provider.ChatMessage
	for !c.done() {
		message := c.source[c.message]
		text := c.texts[c.message][c.offset:]
		total := c.runes[c.message]
		header := rootprompt.CompactionSourceFragment(c.message, c.read, total, total, message.Role, string(message.EffectiveOrigin()), message.ToolCallID, "")
		roomTokens := tokens - provider.EstimateTokens(header, "") - 8
		roomBytes := math.MaxInt
		if bytes > 0 {
			roomBytes = bytes - len(header) - 64
		}
		pieceRunes := min(total-c.read, max(0, roomTokens)*4)
		piece := text
		if pieceRunes < total-c.read || len(piece) > roomBytes {
			piece = runePrefix(text, pieceRunes, max(0, roomBytes))
		}
		if piece == "" {
			if len(part) > 0 {
				break
			}
			// A part always advances, even when the room is smaller than one
			// header: carry at least one character.
			_, size := utf8.DecodeRuneInString(text)
			piece = text[:size]
		}
		count := utf8.RuneCountInString(piece)
		fragment := provider.NewTextMessage("user", rootprompt.CompactionSourceFragment(c.message, c.read, c.read+count, total, message.Role, string(message.EffectiveOrigin()), message.ToolCallID, piece))
		fragment.Origin, fragment.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
		part = append(part, fragment)
		tokens -= provider.EstimateMessageTokens(fragment, "")
		if bytes > 0 {
			bytes -= len(fragment.Content) + 32
		}
		if c.offset += len(piece); c.offset >= len(c.texts[c.message]) {
			c.message, c.offset, c.read = c.message+1, 0, 0
		} else {
			c.read += count
			break
		}
		if tokens < 64 || bytes > 0 && bytes < 256 {
			break
		}
	}
	return part, c
}

// runePrefix is the longest prefix of text with at most runes runes and at
// most bytes bytes, cut on a rune boundary.
func runePrefix(text string, runes, bytes int) string {
	end, count := len(text), 0
	for offset := range text {
		if count == runes {
			end = offset
			break
		}
		count++
	}
	if end > bytes {
		end = bytes
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
	}
	return text[:end]
}

func extractionSourceText(message provider.ChatMessage, index int) string {
	var text strings.Builder
	fmt.Fprintf(&text, "source_index=%d role=%q origin=%q tool_call_id=%q tool_error=%t\n", index, message.Role, message.EffectiveOrigin(), message.ToolCallID, message.ToolError)
	text.WriteString(message.Content.ContentDescription())
	if message.Refusal != "" {
		fmt.Fprintf(&text, "\nrefusal: %s", message.Refusal)
	}
	for _, call := range message.ToolCalls {
		fmt.Fprintf(&text, "\ntool_call id=%q name=%q arguments:\n%s", call.ID, call.Function.Name, call.Function.Arguments)
	}
	return text.String()
}

func compactionHumanManifest(messages []provider.ChatMessage, role, model string, budget int) string {
	selected := retainRecentUserContext(messages, role, budget, model)
	var entries []rootprompt.CompactionManifestEntry
	for _, message := range selected {
		if !message.IsGenuineUserInput() && message.EffectiveOrigin() != provider.OriginAssignment {
			continue
		}
		text := message.ContentString()
		if utf8.RuneCountInString(text) > budget*4 {
			entries = append(entries, rootprompt.CompactionManifestEntry{Origin: string(message.EffectiveOrigin()), Oversized: true})
			continue
		}
		entries = append(entries, rootprompt.CompactionManifestEntry{Origin: string(message.EffectiveOrigin()), Text: text})
	}
	return rootprompt.CompactionManifest(entries)
}
