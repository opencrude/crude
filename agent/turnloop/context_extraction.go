package turnloop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/types/reasoning"
)

const (
	// compactionAdaptations bounds the limit adaptations of one compaction.
	// Each one makes the next request strictly smaller, so extraction either
	// converges or stops with the provider's own evidence.
	compactionAdaptations = 16
	// minimumExtractionPart is the least source, in estimated tokens, one part
	// of a long source carries.
	minimumExtractionPart = 256
	// minimumPartNotes is the least space, in estimated tokens, the notes of
	// one part may take, however many parts the source has.
	minimumPartNotes = 2048
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
// and narrows on provider evidence. Every request and stage of one
// compaction shares it, including requests that run at the same time.
type extractionLimits struct {
	mu    sync.Mutex
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
	// stopped is the refusal that ended smaller attempts: size is not its
	// cause, so no request of this shape is sent again.
	stopped error
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
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.admissibleLocked()
}

func (l *extractionLimits) admissibleLocked() int {
	if l.usageRatio > 1 {
		return min(l.input, int(float64(l.capacity())/l.usageRatio))
	}
	return l.input
}

// partCapacity reserves room for notes as well as source. A route's input
// limit is independent of its window: do not subtract the reserve twice.
func (l *extractionLimits) partCapacity(envelope int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	room := l.admissibleLocked()
	if l.window <= 0 {
		return room
	}
	// Reserve the normal response allowance when possible. In a small
	// window, divide the space remaining after fixed instructions between
	// source and response. The actual request may use all remaining output.
	available := int(float64(l.window-contextSafetyReserve(l.window)) / max(1, l.usageRatio))
	reserve := min(max(0, available-envelope)/2, l.agent.outputTokenLimit(l.window, 0))
	if l.output > 0 {
		reserve = min(reserve, l.output)
	}
	return min(room, available-reserve)
}

// windowSize is the context window in Crude's estimate; 0 when unknown.
func (l *extractionLimits) windowSize() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.window
}

// byteLimit is the largest estimated request body; 0 when none is known.
func (l *extractionLimits) byteLimit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bytes
}

func (l *extractionLimits) fits(tokens, bytes int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fitsLocked(tokens, bytes)
}

func (l *extractionLimits) fitsLocked(tokens, bytes int) bool {
	return tokens <= l.admissibleLocked() && (l.bytes <= 0 || bytes <= l.bytes)
}

// response is the output allowance for a request of input estimated tokens.
// Zero means the provider default; ok is false when no output room remains.
func (l *extractionLimits) response(input int) (limit int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window > 0 && input > 0 && promptTokens > input && promptTokens <= l.window-contextSafetyReserve(l.window) {
		l.usageRatio = max(l.usageRatio, float64(promptTokens)/float64(input))
	}
}

// adapt narrows the limits from the evidence in err about a request of input
// estimated tokens and bytes that asked for sent output tokens. It reports
// whether the request must be planned again; each narrowing makes the next
// request smaller.
func (l *extractionLimits) adapt(err error, input, bytes, sent, attempt int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.fitsLocked(input, bytes) || l.output > 0 && sent > l.output {
		// A request that ran at the same time already narrowed the limits
		// below this one. Plan it again within them; one cause must not
		// narrow them twice.
		return true
	}
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
			changed = l.lower(provider.LearnedInput, &l.input, min(l.admissibleLocked(), input)*2/3, provider.InferredLimitLifetime, true)
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
		next := min(l.admissibleLocked(), input) / 2
		if next < unexplainedFloor {
			// Even a small request is refused: size is not the cause, and
			// the caller returns this refusal.
			_ = l.endProbeLocked()
			return false
		}
		changed = l.lower(provider.LearnedInput, &l.input, next, provider.InferredLimitLifetime, false)
		if sent > 2*unexplainedFloor {
			changed = l.lower(provider.LearnedOutput, &l.output, sent/2, provider.InferredLimitLifetime, false) || changed
		}
	case !tooLarge && attempt > 0 && input > 8192 && provider.TimedOut(err):
		// The same large request timed out twice; a smaller one may finish.
		changed = l.lower(provider.LearnedInput, &l.input, min(l.admissibleLocked(), input)/2, provider.InferredLimitLifetime, false)
	}
	if changed {
		l.adaptations++
	}
	return changed
}

// endProbe restores the limits from before the smaller attempts and returns
// the refusal that started them, or nil when no attempt is in progress.
func (l *extractionLimits) endProbe() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.endProbeLocked()
}

func (l *extractionLimits) endProbeLocked() error {
	if l.probe == nil {
		return nil
	}
	refused := l.probeErr
	l.input, l.output = l.probe[0], l.probe[1]
	l.probe, l.probeErr, l.pending, l.unexplained = nil, nil, nil, true
	l.stopped = refused
	return refused
}

// probing reports smaller attempts after a refusal that named no cause.
func (l *extractionLimits) probing() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.probe != nil
}

// stoppedBy is the refusal that ended smaller attempts, or nil.
func (l *extractionLimits) stoppedBy() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}

// resume allows requests of another shape after a refusal ended smaller
// attempts, within the limits from before them.
func (l *extractionLimits) resume() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = nil
}

// truncated gives the next request more response room by carrying less
// source. A spent response allowance is not a provider limit to remember.
func (l *extractionLimits) truncated(input int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if input > l.admissibleLocked() {
		// Another request already made room.
		return true
	}
	if l.adaptations >= compactionAdaptations || input <= minimumExtractionPart {
		return false
	}
	previous := l.admissibleLocked()
	l.input = min(l.input, max(1, input/2))
	if l.admissibleLocked() >= previous {
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
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sectioned = true
	l.record(provider.LearnedSectioned, 1, provider.InferredLimitLifetime)
}

// extractionShared is the state that every request of one compaction shares,
// including requests that run at the same time: usage, retries, the notes of
// each long source and the connection's gate, which admits as many requests
// at once as the connection accepts.
type extractionShared struct {
	mu        sync.Mutex
	usage     provider.TokenUsage
	retry     int
	completed int
	emitMu    sync.Mutex
	notes     map[sourceKey]*sourceNotes
	turns     map[sourceKey]*prefixTurn
}

// resetTurns forgets the order of earlier requests: the next requests over
// the same prefix have another shape.
func (s *extractionShared) resetTurns() {
	s.mu.Lock()
	s.turns = make(map[sourceKey]*prefixTurn)
	s.mu.Unlock()
}

// The first stage over a shared prefix establishes its measured input and
// any route limits. The others can then use those measurements concurrently.
type prefixTurn struct {
	open chan struct{}
	// err is a failure of the connection itself in the first request; the
	// others return it instead of repeating it.
	err error
}

// partCount is the progress of reading one long source.
type partCount struct {
	mu          sync.Mutex
	read, parts int
}

// sourceKey names one source slice of the compaction, or (notes) the notes
// of one.
type sourceKey struct {
	first *provider.ChatMessage
	count int
	notes bool
}

// sourceNotes holds the notes of one long source. Every stage of the
// compaction merges the same notes, so the source is read once.
type sourceNotes struct {
	mu    sync.Mutex
	done  bool
	notes []extractionNote
	err   error
}

// extractionNote is the notes of source messages first to last.
type extractionNote struct {
	first, last int
	text        string
}

func newExtractionShared(a *Agent) *extractionShared {
	return &extractionShared{notes: make(map[sourceKey]*sourceNotes), turns: make(map[sourceKey]*prefixTurn)}
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
	refinements  int
	// shared is the state of the whole compaction; every copy of this
	// extractor for another stage or request points to the same one.
	shared      *extractionShared
	firstOutput func()
}

func (x *compactionExtractor) state() *extractionShared {
	if x.shared == nil {
		x.shared = newExtractionShared(x.agent)
	}
	return x.shared
}

// retryAttempt is the highest retry of one request in the compaction.
func (x *compactionExtractor) retryAttempt() int {
	s := x.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retry
}

// event sends one event; requests that run at the same time send in turn.
func (x *compactionExtractor) event(evt AgentEvent) {
	s := x.state()
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	x.emit(evt)
}

// fork is a copy of this extractor for another stage or request of the same
// compaction. It shares the limits, usage, progress and notes; a separate ctx
// lets the requests of one source stop together.
func (x *compactionExtractor) fork(ctx context.Context, stage rootprompt.CompactionStage, outputBudget int) *compactionExtractor {
	shared := x.state()
	f := *x
	f.shared = shared
	f.ctx, f.stage, f.outputBudget, f.refinements = ctx, stage, outputBudget, 0
	return &f
}

// request extracts the stage from source in one request when it fits the
// limits. A longer source is read in parts at the same time, and the stage
// is written from their notes.
func (x *compactionExtractor) request(source []provider.ChatMessage, directive string) (string, error) {
	x.state()
	messages := x.messages(nil, source, directive, x.outputBudget)
	if x.limits.fits(x.estimate(messages), x.bytes(messages)) {
		text, err := x.inTurn(sourceKeyOf(source, false), func(w *compactionExtractor) (string, error) {
			messages := w.messages(nil, source, directive, w.outputBudget)
			if !w.limits.fits(w.estimate(messages), w.bytes(messages)) {
				// Another stage learned a smaller limit meanwhile.
				return "", errExtractionAdapted
			}
			return w.send(messages)
		})
		if err == nil {
			return x.fit(text, directive)
		}
		if !errors.Is(err, errExtractionAdapted) {
			return "", err
		}
	}
	notes, err := x.sourceNotes(source, directive)
	if err != nil {
		return "", err
	}
	text, err := x.inTurn(sourceKeyOf(source, true), func(w *compactionExtractor) (string, error) {
		return w.merge(notes, directive)
	})
	if err != nil {
		return "", err
	}
	return x.fit(text, directive)
}

func sourceKeyOf(source []provider.ChatMessage, notes bool) sourceKey {
	if len(source) == 0 {
		return sourceKey{notes: notes}
	}
	return sourceKey{first: &source[0], count: len(source), notes: notes}
}

// inTurn runs work after the first request over the same prefix began its
// answer; the first runs at once.
func (x *compactionExtractor) inTurn(key sourceKey, work func(w *compactionExtractor) (string, error)) (string, error) {
	s := x.state()
	s.mu.Lock()
	turn := s.turns[key]
	first := turn == nil
	if first {
		turn = &prefixTurn{open: make(chan struct{})}
		s.turns[key] = turn
	}
	s.mu.Unlock()
	if !first {
		select {
		case <-turn.open:
		case <-x.ctx.Done():
			return "", x.ctx.Err()
		}
		s.mu.Lock()
		failed := turn.err
		s.mu.Unlock()
		if failed != nil {
			return "", failed
		}
		return work(x)
	}
	w := *x
	release := sync.OnceFunc(func() { close(turn.open) })
	w.firstOutput = release
	text, err := work(&w)
	if compactionModelUnavailable(err) {
		// The connection itself failed: the others would fail the same way.
		s.mu.Lock()
		turn.err = err
		s.mu.Unlock()
	}
	release()
	return text, err
}

// sourceNotes reads a long source once for the whole compaction; every stage
// that needs it waits for and merges the same notes.
func (x *compactionExtractor) sourceNotes(source []provider.ChatMessage, directive string) ([]extractionNote, error) {
	if len(source) == 0 {
		return nil, nil
	}
	s := x.state()
	key := sourceKey{first: &source[0], count: len(source)}
	s.mu.Lock()
	entry := s.notes[key]
	if entry == nil {
		entry = &sourceNotes{}
		s.notes[key] = entry
	}
	s.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.done {
		reader := x.fork(x.ctx, rootprompt.CompactionNotesStage(), x.outputBudget)
		entry.notes, entry.err = reader.readParts(newExtractionCursor(source), directive, nil)
		// A cancelled read is not a result: a later stage reads again.
		entry.done = x.ctx.Err() == nil
	}
	return entry.notes, entry.err
}

// extractSections writes the missing checkpoint sections of source at the
// same time; a long source is read once for all of them. Together they share
// the space that remains: when they exceed it, each is shortened in
// proportion to its length. It returns the stage index of the first failure.
func extractSections(ctx context.Context, source []provider.ChatMessage, missing []int, extractor func(ctx context.Context, index, budget int) *compactionExtractor, remaining int, model string) ([]string, int, error) {
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	first := extractor(stageCtx, missing[0], remaining)
	first.state().resetTurns()
	first.limits.resume()
	texts := make(map[int]string, len(missing))
	var mu sync.Mutex
	run := func(work func(x *compactionExtractor, index int) (string, error), budget func(index int) int) (int, error) {
		errs := make([]error, len(missing))
		var wg sync.WaitGroup
		for i, index := range missing {
			x := extractor(stageCtx, index, budget(index))
			wg.Add(1)
			go func() {
				defer wg.Done()
				text, err := work(x, index)
				mu.Lock()
				texts[index] = text
				mu.Unlock()
				if errs[i] = err; err != nil {
					cancel()
				}
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return missing[0], err
		}
		for _, canceled := range []bool{false, true} {
			for i, err := range errs {
				if err != nil && errors.Is(err, context.Canceled) == canceled {
					return missing[i], err
				}
			}
		}
		return 0, nil
	}
	first.progress("Writing", 0, nil)
	if at, err := run(func(x *compactionExtractor, _ int) (string, error) {
		text, err := x.request(source, rootprompt.InitialCompactionDirective())
		if err == nil {
			s := x.state()
			s.mu.Lock()
			s.completed++
			s.mu.Unlock()
		}
		return text, err
	}, func(int) int { return remaining }); err != nil {
		return nil, at, err
	}
	total := 0
	for _, index := range missing {
		total += provider.EstimateTokens(texts[index], model)
	}
	if total > remaining {
		lengths := make(map[int]int, len(missing))
		for _, index := range missing {
			lengths[index] = provider.EstimateTokens(texts[index], model)
		}
		if at, err := run(func(x *compactionExtractor, index int) (string, error) {
			mu.Lock()
			text := texts[index]
			mu.Unlock()
			return x.fit(text, rootprompt.InitialCompactionDirective())
		}, func(index int) int {
			return max(1, int(int64(remaining)*int64(lengths[index])/int64(total)))
		}); err != nil {
			return nil, at, err
		}
	}
	out := make([]string, 0)
	for index, text := range texts {
		for len(out) <= index {
			out = append(out, "")
		}
		out[index] = text
	}
	return out, 0, nil
}

// extractionPart is one part of a long source: the source between two
// cursor positions, as attributed fragments.
type extractionPart struct {
	from, end extractionCursor
	block     []provider.ChatMessage
}

// span is the first and last source message the part carries.
func (p extractionPart) span() (first, last int) {
	last = p.end.message - 1
	if p.end.offset > 0 {
		last = p.end.message
	}
	return p.from.message, max(p.from.message, last)
}

// readParts reads the source from cursor in parts at the same time; each
// request carries one part and writes its notes. A part that no longer fits
// narrowed limits is read again in smaller parts (count is then the progress
// of the whole source); finished parts are never read again. The notes come
// back in source order.
func (x *compactionExtractor) readParts(cursor extractionCursor, directive string, count *partCount) ([]extractionNote, error) {
	parts, err := x.planParts(cursor, directive)
	if err != nil {
		return nil, err
	}
	if count == nil {
		// The notes of all parts must fit one merge with room to answer.
		x.outputBudget = max(minimumPartNotes, min(x.carry(), x.limits.admissible()/(2*len(parts))))
		count = &partCount{parts: len(parts)}
	} else {
		count.mu.Lock()
		count.parts += len(parts) - 1
		count.mu.Unlock()
	}
	x.partProgress(count)

	ctx, cancel := context.WithCancel(x.ctx)
	defer cancel()
	results := make([][]extractionNote, len(parts))
	errs := make([]error, len(parts))
	var wg sync.WaitGroup
	for i, part := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reader := x.fork(ctx, x.stage, x.outputBudget)
			first, last := part.span()
			text, err := reader.send(reader.messages(nil, part.block, rootprompt.CompactionPartDirective(first, last, directive), reader.outputBudget))
			switch {
			case err == nil:
				results[i] = []extractionNote{{first: first, last: last, text: text}}
				count.mu.Lock()
				count.read++
				count.mu.Unlock()
				x.partProgress(count)
			case errors.Is(err, errExtractionAdapted):
				results[i], err = reader.readParts(part.from.until(part.end), directive, count)
			}
			if err != nil {
				errs[i] = err
				cancel()
			}
		}()
		if i == 0 && x.limits.probing() {
			// Smaller attempts after a refusal without a cause go one at a
			// time until one is accepted; the others would be refused too.
			wg.Wait()
		}
	}
	wg.Wait()
	if err := x.ctx.Err(); err != nil {
		return nil, err
	}
	// The first real failure, not the cancellation it caused in the others.
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return nil, err
		}
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	var notes []extractionNote
	for _, result := range results {
		notes = append(notes, result...)
	}
	return notes, nil
}

// planParts divides the source from cursor into parts that each fit one
// request within the current limits.
func (x *compactionExtractor) planParts(cursor extractionCursor, directive string) ([]extractionPart, error) {
	longest := rootprompt.CompactionPartDirective(math.MaxInt32, math.MaxInt32, directive)
	envelope := x.messages(nil, nil, longest, x.outputBudget)
	admissible, byteLimit := x.limits.partCapacity(x.estimate(envelope)), x.limits.byteLimit()
	tokens := admissible - x.estimate(envelope)
	bytes := 0
	if byteLimit > 0 {
		bytes = byteLimit - x.bytes(envelope)
	}
	if tokens < minimumExtractionPart || byteLimit > 0 && bytes < minimumExtractionPart*4 {
		if refused := x.limits.endProbe(); refused != nil {
			// Smaller attempts after a refusal without a cause reached the
			// size of the instructions: size is not the cause.
			return nil, refused
		}
		return nil, extractionFailure("the compaction model has no room for the history after the system prompt, tools and instructions")
	}
	var parts []extractionPart
	for !cursor.done() {
		part := extractionPart{from: cursor}
		room, roomBytes := tokens, bytes
		for fit := 0; ; fit++ {
			part.block, part.end = cursor.take(room, roomBytes)
			first, last := part.span()
			messages := x.messages(nil, part.block, rootprompt.CompactionPartDirective(first, last, directive), x.outputBudget)
			// The estimate of the whole request is the authority; trim the
			// part by what it exceeds.
			overTokens := x.estimate(messages) - admissible
			overBytes := 0
			if byteLimit > 0 {
				overBytes = x.bytes(messages) - byteLimit
			}
			if overTokens <= 0 && overBytes <= 0 || fit == 2 {
				break
			}
			room -= max(0, overTokens) + 16
			if roomBytes > 0 {
				roomBytes -= max(0, overBytes) + 64
			}
		}
		parts = append(parts, part)
		cursor = part.end
	}
	return parts, nil
}

// merge writes the stage from notes in source order. Notes that do not fit
// one request are first merged in consecutive groups at the same time, so
// every request reads whole notes in order.
func (x *compactionExtractor) merge(notes []extractionNote, directive string) (string, error) {
	for {
		if err := x.ctx.Err(); err != nil {
			return "", err
		}
		x.progress("Merging", 0, nil)
		messages := x.mergeMessages(notes, directive, x.outputBudget)
		if x.limits.fits(x.estimate(messages), x.bytes(messages)) {
			text, err := x.send(messages)
			if !errors.Is(err, errExtractionAdapted) {
				return text, err
			}
			continue
		}
		fewer, err := x.mergeGroups(notes, directive)
		if err != nil {
			return "", err
		}
		notes = fewer
	}
}

// mergeGroups merges consecutive notes in groups that fit one request. A note
// too long to share a request with its neighbour is shortened first, so each
// round leaves fewer notes.
func (x *compactionExtractor) mergeGroups(notes []extractionNote, directive string) ([]extractionNote, error) {
	stage := rootprompt.CompactionNotesStage()
	room := x.limits.admissible() - x.estimate(x.fork(x.ctx, stage, 0).mergeMessages(nil, directive, 0))
	if room < 2*minimumExtractionPart {
		return nil, extractionFailure("the compaction model has no room to merge the notes of the history")
	}
	ctx, cancel := context.WithCancel(x.ctx)
	defer cancel()
	run := func(count int, work func(i int) error) error {
		errs := make([]error, count)
		var wg sync.WaitGroup
		for i := range count {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if errs[i] = work(i); errs[i] != nil {
					cancel()
				}
			}()
		}
		wg.Wait()
		if err := x.ctx.Err(); err != nil {
			return err
		}
		for _, err := range errs {
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		}
		return errors.Join(errs...)
	}
	notes = append([]extractionNote(nil), notes...)
	var shortened sync.Map
	if err := run(len(notes), func(i int) error {
		if provider.EstimateTokens(notes[i].text, x.agent.Model) <= room/2 {
			return nil
		}
		shorter, err := x.fork(ctx, stage, room/3).fit(notes[i].text, directive)
		notes[i].text = shorter
		shortened.Store(i, true)
		return err
	}); err != nil {
		return nil, err
	}
	var groups [][]extractionNote
	for _, note := range notes {
		if n := len(groups); n > 0 {
			candidate := append(append([]extractionNote(nil), groups[n-1]...), note)
			messages := x.fork(ctx, stage, room/2).mergeMessages(candidate, directive, room/2)
			if x.limits.fits(x.estimate(messages), x.bytes(messages)) {
				groups[n-1] = candidate
				continue
			}
		}
		groups = append(groups, []extractionNote{note})
	}
	if len(groups) == len(notes) {
		some := false
		shortened.Range(func(any, any) bool { some = true; return false })
		if some {
			// Shorter notes may now fit one request together.
			return notes, nil
		}
		return nil, extractionFailure("the notes of the history do not fit the compaction model")
	}
	merged := make([]extractionNote, len(groups))
	err := run(len(groups), func(i int) error {
		group := groups[i]
		merged[i] = extractionNote{first: group[0].first, last: group[len(group)-1].last, text: group[0].text}
		if len(group) == 1 {
			return nil
		}
		writer := x.fork(ctx, stage, room/2)
		text, err := writer.send(writer.mergeMessages(group, directive, room/2))
		if errors.Is(err, errExtractionAdapted) {
			// The limits narrowed: merge this group in the next round.
			text, err = writer.merge(group, directive)
		}
		merged[i].text = text
		return err
	})
	return merged, err
}

// mergeMessages is the request that writes the stage from notes.
func (x *compactionExtractor) mergeMessages(notes []extractionNote, directive string, space int) []provider.ChatMessage {
	earlier := make([]provider.ChatMessage, 0, len(notes))
	for _, note := range notes {
		message := provider.NewTextMessage("user", rootprompt.CompactionPartNotes(note.first, note.last, note.text))
		message.Origin, message.Visibility = provider.OriginCompactionRequest, provider.VisibilityContextOnly
		earlier = append(earlier, message)
	}
	return x.messages(earlier, nil, rootprompt.CompactionMergeDirective(directive), space)
}

// carry is the most space the notes of one part may take.
func (x *compactionExtractor) carry() int {
	return min(x.outputBudget, max(1024, x.limits.admissible()/4))
}

// fit shortens a result that exceeds the stage's checkpoint space.
func (x *compactionExtractor) fit(text, directive string) (string, error) {
	for provider.EstimateTokens(text, x.agent.Model) > x.outputBudget {
		if x.refinements >= compactionRefinements {
			return "", extractionFailure("extracted checkpoint cannot fit the continuation context")
		}
		x.refinements++
		before := provider.EstimateTokens(text, x.agent.Model)
		x.progress("Shortening", 0, nil)
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
	s := x.state()
	var lastErr error
	for attempt := 0; ; {
		if err := x.ctx.Err(); err != nil {
			return "", err
		}
		if err := x.limits.stoppedBy(); err != nil {
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
			return "", &provider.ContextWindowError{InputTokens: input, ContextWindow: x.limits.windowSize()}
		}
		var text func(string)
		var thinking func(reasoning.Delta)
		if start := x.firstOutput; start != nil {
			text = func(string) { start() }
			thinking = func(reasoning.Delta) { start() }
		}
		completion, err := x.agent.completeStreamWithOptions(x.ctx, messages, x.tools, text, thinking, nil, nil, nil, nil, nil,
			completionRequestOptions{toolChoice: "none", maxOutputTokens: response, maxAttempts: 1, compaction: true, inputTokens: input})
		// Preparation, subsequent reduction and fallback share one operation,
		// but each billable request needs its own durable usage row.
		s.mu.Lock()
		s.usage = s.usage.Add(completion.Usage)
		s.mu.Unlock()
		x.agent.emitCompactionUsage(x.event, completion.Usage, provider.NewMessageID(), x.requestID, x.trigger, x.role, x.stage.Name, attempt > 0 || err != nil, err == nil)
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
		if provider.ConcurrencyAdjusted(err) {
			// Sent again when the gate has a free place; not a failed attempt.
			continue
		}
		if isTruncatedFinishReason(completion.FinishReason) {
			// Provider completion tokens include reasoning. Never repeat an
			// unchanged request after its full response allowance was spent.
			if x.limits.truncated(input) {
				x.progress("Adapting", attempt+1, err)
				return "", errExtractionAdapted
			}
			return "", err
		}
		if x.limits.adapt(err, input, bytes, response, attempt) {
			x.progress("Adapting", attempt+1, err)
			continue
		}
		lastErr = err
		if !isRetryableCompactionError(err) || attempt >= compactionStageRecoveries {
			return "", lastErr
		}
		attempt++
		s.mu.Lock()
		s.retry = max(s.retry, attempt)
		s.mu.Unlock()
		delay := x.agent.compactionRecoveryDelay(attempt, err)
		data := x.eventData()
		data["retry"], data["retry_limit"], data["backoff"], data["error"] = attempt, compactionStageRecoveries, delay.Seconds(), compactErrorText(err)
		x.event(AgentEvent{Type: "context_compaction_retrying", Data: data})
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
// notes of earlier parts, the source, the shared context and the stage
// instruction with the space its answer may take.
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
	s := x.state()
	s.mu.Lock()
	completed := s.completed
	s.mu.Unlock()
	data := x.agent.compactionEventData(x.requestID, x.trigger, x.role, x.stageIndex+1, completed, x.beforeTokens, 0, x.targetWindow)
	data["stage_name"], data["stage_label"] = x.stage.Name, x.stage.Label
	return data
}

// progress reports a short step label, such as "Merging", in the row.
func (x *compactionExtractor) progress(label string, retry int, err error) {
	data := x.eventData()
	data["stage_label"], data["retry"] = label, retry
	if err != nil {
		data["error"] = compactErrorText(err)
	}
	x.event(AgentEvent{Type: "context_compaction_stage", Data: data})
}

// partProgress reports how many parts of the long source were read.
func (x *compactionExtractor) partProgress(count *partCount) {
	count.mu.Lock()
	read, parts := count.read, count.parts
	count.mu.Unlock()
	data := x.eventData()
	data["stage_label"] = fmt.Sprintf("Read %d%%", 100*read/max(1, parts))
	data["chunk"], data["chunk_count"] = read, parts
	x.event(AgentEvent{Type: "context_compaction_stage", Data: data})
}

// extractionCursor reads the source front to back as attributed text, up to
// an end position. A part takes whole messages, or a piece of one, within its
// token and byte room; every piece repeats its message's role, origin and
// tool-result identity. Inline media is described, not sent again.
type extractionCursor struct {
	source  []provider.ChatMessage
	texts   []string
	runes   []int
	message int
	offset  int // byte offset into texts[message], on a rune boundary
	read    int // runes of texts[message] before offset
	// stopMessage and stopOffset are the end position: the cursor is done
	// there. A new cursor ends after the last message.
	stopMessage, stopOffset int
}

func newExtractionCursor(source []provider.ChatMessage) extractionCursor {
	cursor := extractionCursor{source: source, texts: make([]string, len(source)), runes: make([]int, len(source)), stopMessage: len(source)}
	for index, message := range source {
		cursor.texts[index] = extractionSourceText(message, index)
		cursor.runes[index] = utf8.RuneCountInString(cursor.texts[index])
	}
	return cursor
}

func (c extractionCursor) done() bool {
	return c.message > c.stopMessage || c.message == c.stopMessage && (c.message >= len(c.texts) || c.offset >= c.stopOffset)
}

// until is the cursor that reads from c up to end, the position of another
// cursor over the same source.
func (c extractionCursor) until(end extractionCursor) extractionCursor {
	c.stopMessage, c.stopOffset = end.message, end.offset
	return c
}

// stop is the byte offset in the current message where this cursor ends.
func (c extractionCursor) stop() int {
	if c.message == c.stopMessage {
		return c.stopOffset
	}
	return len(c.texts[c.message])
}

// take returns the next part within tokens estimated tokens and bytes bytes
// (0: no byte limit) and the cursor after it. A part always advances.
func (c extractionCursor) take(tokens, bytes int) ([]provider.ChatMessage, extractionCursor) {
	var part []provider.ChatMessage
	for !c.done() {
		message := c.source[c.message]
		text := c.texts[c.message][c.offset:c.stop()]
		left := utf8.RuneCountInString(text)
		total := c.runes[c.message]
		header := rootprompt.CompactionSourceFragment(c.message, c.read, total, total, message.Role, string(message.EffectiveOrigin()), message.ToolCallID, "")
		roomTokens := tokens - provider.EstimateTokens(header, "") - 8
		roomBytes := math.MaxInt
		if bytes > 0 {
			roomBytes = bytes - len(header) - 64
		}
		pieceRunes := min(left, max(0, roomTokens)*4)
		piece := text
		if pieceRunes < left || len(piece) > roomBytes {
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
			if c.offset < c.stop() {
				break
			}
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
