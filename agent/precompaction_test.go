package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/tools"
)

func TestVerbatimTailKeepsRecentTurnsAndNeverOutweighsTheSummary(t *testing.T) {
	msgs := []provider.ChatMessage{provider.NewTextMessage("system", "system")}
	for turn := 0; turn < 8; turn++ {
		msgs = append(msgs,
			provider.NewTextMessage("user", fmt.Sprintf("request %d", turn)),
			provider.NewTextMessage("assistant", strings.Repeat("analysis ", 2_000)),
		)
	}
	cut, tailHuman := verbatimTailCut(msgs, "root", "model")
	if cut != len(msgs)-4 || !tailHuman {
		t.Fatalf("cut=%d tailHuman=%t; want the previous and current turns kept (cut %d)", cut, tailHuman, len(msgs)-4)
	}

	// One huge current turn: whole turns cannot stay, so a later tool round
	// is the boundary, never a tool result.
	long := []provider.ChatMessage{provider.NewTextMessage("system", "system"), provider.NewTextMessage("user", "do the long task")}
	for round := 0; round < 6; round++ {
		call := provider.NewTextMessage("assistant", "")
		call.ToolCalls = []provider.ToolCall{{ID: fmt.Sprintf("call-%d", round), Type: "function", Function: provider.FunctionCall{Name: "read_file", Arguments: "{}"}}}
		result := provider.NewTextMessage("tool", strings.Repeat("evidence ", 3_000))
		result.ToolCallID = call.ToolCalls[0].ID
		long = append(long, call, result)
	}
	cut, tailHuman = verbatimTailCut(long, "root", "model")
	if cut <= 2 || cut >= len(long) || long[cut].Role != "assistant" || tailHuman {
		t.Fatalf("long-turn cut=%d role=%q tailHuman=%t", cut, long[cut].Role, tailHuman)
	}
	tail := provider.EstimateMessagesTotal(long[cut:], "model")
	summarized := provider.EstimateMessagesTotal(long[1:cut], "model")
	if tail > summarized {
		t.Fatalf("verbatim tail %d outweighs the summarized part %d", tail, summarized)
	}

	if cut, _ := verbatimTailCut(msgs[:2], "root", "model"); cut != 2 {
		t.Fatalf("tiny context cut=%d, want everything summarized", cut)
	}
}

func economicAgent(t *testing.T, model string) *Agent {
	t.Helper()
	p := provider.FromPreset("openai-chat", "key", "http://127.0.0.1:0")
	p.Model = model
	provider.SetModelContext(model, 1_000_000)
	provider.SetModelPrice(model, provider.ModelPrice{InputPricePerToken: 2e-6, OutputPricePerToken: 8e-6, InputCachedPricePerToken: 0.2e-6})
	return NewAgent(p, model, nil)
}

// The economic rule used to adopt a checkpoint as soon as a cheap extraction
// paid for itself, so a 1M-token window was compacted at ~90K tokens after a
// handful of steps (sessions 2026-10-16_064433 and _113625). A checkpoint is
// lossy; below half of capacity the exact history is kept, and past it the
// spend rule decides with preparation leading adoption.
func TestAdaptiveCompactionTriggersWhenContextCostsMoreThanACheckpoint(t *testing.T) {
	a := economicAgent(t, "economic-main-model")
	planner := newCompactionPlanner(30_000)
	const fixed, tail, hardLimit, window = 20_000, 10_000, 900_000, 1_000_000
	prepareAt, swapAt := 0, 0
	for active := 60_000; active < hardLimit && swapAt == 0; active += 6_000 {
		plan := planner.evaluate(a, active, fixed, tail, hardLimit, window, false)
		if plan.force {
			t.Fatalf("economic plan forced at %d tokens", active)
		}
		if plan.prepare && prepareAt == 0 {
			prepareAt = active
		}
		if plan.swap {
			swapAt = active
		}
		usage := provider.TokenUsage{PromptTokens: active, PromptTokensCached: active - 6_000, CacheReadKnown: true}
		planner.observe(a, usage, active, fixed+planner.checkpointEstimate()+tail)
	}
	if swapAt < hardLimit/2 || swapAt > hardLimit*6/10 {
		t.Fatalf("checkpoint adopted at %d tokens; want just past half of the %d capacity", swapAt, hardLimit)
	}
	if prepareAt == 0 || prepareAt >= swapAt {
		t.Fatalf("background preparation started at %d, adoption at %d; preparation must lead", prepareAt, swapAt)
	}

	// Without cache reporting Crude assumes the cheaper (cached) reading, so
	// missing data never makes it compact sooner.
	unreported := newCompactionPlanner(30_000)
	unreportedAt := 0
	for active := 60_000; active < hardLimit && unreportedAt == 0; active += 6_000 {
		if plan := unreported.evaluate(a, active, fixed, tail, hardLimit, window, false); plan.swap {
			unreportedAt = active
		}
		unreported.observe(a, provider.TokenUsage{PromptTokens: active}, active, fixed+30_000+tail)
	}
	if unreportedAt == 0 || unreportedAt < swapAt {
		t.Fatalf("unreported cache usage compacted at %d, reported usage at %d; missing data must neither compact sooner nor switch the rule off", unreportedAt, swapAt)
	}

	// Unknown prices use the common relative prices: cached context is cheap
	// but never free, so an unpriced model still compacts past the quality
	// floor instead of riding to the capacity cliff.
	unpriced := economicAgent(t, "unpriced-main-model")
	provider.SetModelPrice("unpriced-main-model", provider.ModelPrice{})
	tokens := newCompactionPlanner(30_000)
	tokensAt := 0
	for active := 60_000; active < hardLimit && tokensAt == 0; active += 6_000 {
		if plan := tokens.evaluate(unpriced, active, fixed, tail, hardLimit, window, false); plan.swap {
			tokensAt = active
		}
		tokens.observe(unpriced, provider.TokenUsage{PromptTokens: active, PromptTokensCached: active - 6_000, CacheReadKnown: true}, active, fixed+30_000+tail)
	}
	if tokensAt < hardLimit/2 || tokensAt >= hardLimit-12_000 {
		t.Fatalf("unpriced context compacted at %d tokens; want past the floor %d and before the capacity margin", tokensAt, hardLimit/2)
	}

	// No price and no usage at all: the general rule still runs. New context
	// cannot have been cached, so the reading matches the reported one above:
	// never sooner, and never switched off.
	blind := newCompactionPlanner(30_000)
	blindAt := 0
	for active := 60_000; active < hardLimit && blindAt == 0; active += 6_000 {
		if plan := blind.evaluate(unpriced, active, fixed, tail, hardLimit, window, false); plan.swap {
			blindAt = active
		}
		blind.observe(unpriced, provider.TokenUsage{}, active, fixed+30_000+tail)
	}
	if blindAt == 0 || blindAt < tokensAt {
		t.Fatalf("without price or usage data a checkpoint was adopted at %d tokens, with usage at %d", blindAt, tokensAt)
	}
}

// A premium main model with a cheap compaction model, the exact pairing of
// the reported sessions: a checkpoint is never adopted below half of
// capacity, and background preparation (which leaves the context untouched)
// does not even start before three quarters of that.
func TestCheapCompactorNeverCompactsAPremiumWindowEarly(t *testing.T) {
	a := economicAgent(t, "premium-main-model")
	provider.SetModelPrice("premium-main-model", provider.ModelPrice{InputPricePerToken: 2e-6, OutputPricePerToken: 1e-5, InputCachedPricePerToken: 1e-7})
	compactor := provider.FromPreset("openai-chat", "key", "http://127.0.0.1:0")
	compactor.Model = "cheap-compaction-model"
	provider.SetModelPrice("cheap-compaction-model", provider.ModelPrice{InputPricePerToken: 1.5e-7, OutputPricePerToken: 5e-7, InputCachedPricePerToken: 3e-8})
	a.CompactionProvider = compactor
	planner := newCompactionPlanner(40_000)
	const fixed, hardLimit, window = 25_000, 900_000, 1_050_000
	previous := 0
	for active := 30_000; active < hardLimit/2; active += 12_000 {
		tail := active / 4
		plan := planner.evaluate(a, active, fixed, tail, hardLimit, window, false)
		if plan.swap || plan.force || plan.prepare && active < hardLimit*3/8 {
			t.Fatalf("checkpoint planned at %d of %d tokens: %+v", active, hardLimit, plan)
		}
		planner.observe(a, provider.TokenUsage{PromptTokens: active, PromptTokensCached: previous, CacheReadKnown: true}, active, fixed+40_000+tail)
		previous = active
	}
	if plan := planner.evaluate(a, hardLimit-1_000, fixed, 100_000, hardLimit, window, false); !plan.swap {
		t.Fatalf("near capacity the checkpoint must be adopted: %+v", plan)
	}
}

func TestExplicitPercentIsABoundaryAndPreparationStartsHalfway(t *testing.T) {
	a := economicAgent(t, "explicit-percent-model")
	a.ContextCompactionPercent = 50
	planner := newCompactionPlanner(0)
	if plan := planner.evaluate(a, 200_000, 10_000, 0, 900_000, 1_000_000, false); plan.prepare || plan.swap {
		t.Fatalf("plan below halfway=%+v", plan)
	}
	if plan := planner.evaluate(a, 300_000, 10_000, 0, 900_000, 1_000_000, false); !plan.prepare || plan.swap {
		t.Fatalf("plan past halfway=%+v", plan)
	}
	if plan := planner.evaluate(a, 500_000, 10_000, 0, 900_000, 1_000_000, false); !plan.swap || !plan.force {
		t.Fatalf("plan at the configured percent=%+v", plan)
	}
	if plan := planner.evaluate(a, 900_000, 10_000, 0, 900_000, 1_000_000, false); !plan.force {
		t.Fatalf("plan at capacity=%+v", plan)
	}
}

// precompactionServer answers compaction extractions with a combined
// checkpoint and main requests with tool rounds, recording the order.
type precompactionServer struct {
	mu          sync.Mutex
	order       []string
	main        atomic.Int32
	compactions atomic.Int32
	rounds      int32
	finals      []provider.ChatRequest
	// usage reports provider token usage on main requests, with the previous
	// request's prompt as the cached prefix, so spend accounting sees real cost.
	usage  bool
	cached int
}

func (server *precompactionServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var text strings.Builder
		for _, message := range request.Messages {
			text.WriteString(message.ContentString())
		}
		if strings.Contains(text.String(), "Context compaction extraction contract") {
			server.compactions.Add(1)
			server.record("compaction")
			writeChatSSE(w, "<<<intent>>>\n- finish the long task\n<<<work>>>\n- rounds summarized\n<<<state>>>\n- workspace clean\n<<<continuation>>>\n- continue with the next round", "stop")
			return
		}
		call := server.main.Add(1)
		server.record("main")
		if call > server.rounds {
			server.mu.Lock()
			server.finals = append(server.finals, request)
			server.mu.Unlock()
			writeChatSSE(w, "finished", "stop")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		usage := ""
		if server.usage {
			prompt := estimateCanonicalRequest(request.Messages, request.Tools, request.Model)
			server.mu.Lock()
			cached := min(server.cached, prompt)
			server.cached = prompt
			server.mu.Unlock()
			usage = fmt.Sprintf(",\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":%d}}", prompt, cached)
		}
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"round-%d\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"f%d\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]%s}\n\ndata: [DONE]\n\n", call, call, usage)
	}
}

func (server *precompactionServer) record(kind string) {
	server.mu.Lock()
	server.order = append(server.order, kind)
	server.mu.Unlock()
}

func precompactionAgent(t *testing.T, url, model string) *Agent {
	t.Helper()
	p := provider.FromPreset("openai-chat", "key", url)
	p.Model = model
	provider.SetModelContext(model, 100_000)
	a := newTestAgent(t, p, p.Model)
	combinedCompactionFailures.Delete(combinedCompactionKey(p, model))
	a.Yolo = true
	a.ContextCompactionPercent = 60
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		time.Sleep(20 * time.Millisecond)
		return tools.ToolResult{Content: strings.Repeat("evidence ", 3_000)}, nil
	}
	return a
}

func TestBackgroundCheckpointIsPreparedWhileTheAgentWorksAndAdoptedWithRecentContextVerbatim(t *testing.T) {
	server := &precompactionServer{rounds: 14}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "precompaction-model")
	var mu sync.Mutex
	var events []AgentEvent
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task exactly as specified")}, "system", func(event AgentEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}, nil, "", nil)

	mu.Lock()
	defer mu.Unlock()
	if server.compactions.Load() == 0 {
		t.Fatal("no checkpoint was prepared")
	}
	var adopted map[string]any
	for _, event := range events {
		if event.Type == "context_compacted" {
			adopted, _ = event.Data.(map[string]any)
		}
		if event.Type == "context_compaction_started" {
			if data, _ := event.Data.(map[string]any); data["stage_label"] == "Finishing the checkpoint prepared in the background" {
				t.Fatal("the agent waited for a checkpoint that should have been ready")
			}
		}
	}
	if adopted == nil || adopted["trigger"] != "background" {
		t.Fatalf("checkpoint was not adopted from the background: %+v", adopted)
	}
	first := -1
	for index, kind := range server.order {
		if kind == "compaction" && first < 0 {
			first = index
		}
	}
	workedMeanwhile := false
	for _, kind := range server.order[first+1:] {
		workedMeanwhile = workedMeanwhile || kind == "main"
	}
	if !workedMeanwhile {
		t.Fatalf("the agent stopped working while the checkpoint was prepared: %v", server.order)
	}
	if len(server.finals) != 1 {
		t.Fatalf("final requests=%d", len(server.finals))
	}
	var final strings.Builder
	for _, message := range server.finals[0].Messages {
		final.WriteString(message.ContentString())
	}
	for _, want := range []string{"context_checkpoint", "Do the long task exactly as specified", "round-14"} {
		if !strings.Contains(final.String(), want) && !strings.Contains(fmt.Sprint(server.finals[0].Messages), want) {
			t.Fatalf("continuation lacks %q", want)
		}
	}
}

func TestPreparedCheckpointSurvivesTheEndOfARun(t *testing.T) {
	server := &precompactionServer{rounds: 0}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	slot := NewPrecompactionSlot(t.Context(), nil)
	defer slot.Close()
	a := precompactionAgent(t, endpoint.URL, "precompaction-slot-model")
	a.Precompaction = slot
	history := []provider.ChatMessage{}
	for turn := 0; turn < 8; turn++ {
		history = append(history,
			provider.NewTextMessage("user", fmt.Sprintf("request %d", turn)),
			provider.NewTextMessage("assistant", strings.Repeat("analysis ", 1_500)),
		)
	}
	history = append(history, provider.NewTextMessage("user", "short question"))
	archive := a.Run(t.Context(), history, "system", nil, nil, "", nil)
	job := slot.current()
	if job == nil {
		t.Fatal("no background preparation was started past halfway")
	}
	if err := job.wait(t.Context()); err != nil || job.err != nil {
		t.Fatalf("preparation outlived its run but failed: wait=%v job=%v", err, job.err)
	}
	prepared := server.compactions.Load()

	next := a
	next.Precompaction = slot
	followUp := append(append([]provider.ChatMessage(nil), archive[1:]...), provider.NewTextMessage("user", strings.Repeat("more detail ", 11_500)))
	var compacted bool
	next.Run(t.Context(), followUp, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			data, _ := event.Data.(map[string]any)
			compacted = data["trigger"] == "background"
		}
	}, nil, "", nil)
	if !compacted || server.compactions.Load() != prepared {
		t.Fatalf("next run did not adopt the prepared checkpoint: compacted=%t extractions %d -> %d", compacted, prepared, server.compactions.Load())
	}
}

// A run that must wait for an in-flight background checkpoint announces that
// wait as a compaction boundary. When the job then fails, the run compacts
// synchronously; the announced boundary must settle instead of staying active
// in the transcript (and resuming as an interrupted compaction).
func TestUnusableBackgroundCheckpointSettlesItsAnnouncedBoundary(t *testing.T) {
	server := &precompactionServer{rounds: 0}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "unusable-background-model")
	history := []provider.ChatMessage{}
	for turn := 0; turn < 8; turn++ {
		user := provider.NewTextMessage("user", fmt.Sprintf("request %d", turn))
		user.InternalID = fmt.Sprintf("user-%d", turn)
		reply := provider.NewTextMessage("assistant", strings.Repeat("analysis ", 1_500))
		reply.InternalID = fmt.Sprintf("reply-%d", turn)
		history = append(history, user, reply)
	}
	history = append(history, provider.NewTextMessage("user", strings.Repeat("more detail ", 25_000)))
	slot := NewPrecompactionSlot(t.Context(), nil)
	defer slot.Close()
	job := &precompactionJob{
		requestID: "background-job", cut: 3, digest: contextDigest(history[:2]), model: a.Model,
		cancel: func() {}, done: make(chan struct{}), err: fmt.Errorf("compaction model unavailable"),
	}
	slot.job = job
	a.Precompaction = slot

	settled := map[string]string{}
	var announced bool
	a.Run(t.Context(), history, "system", func(event AgentEvent) {
		data, _ := event.Data.(map[string]any)
		id, _ := data["request_id"].(string)
		switch event.Type {
		case "context_compaction_started":
			if data["stage_label"] == "Finishing the checkpoint prepared in the background" {
				announced = true
				close(job.done)
			}
			settled[id] = ""
		case "context_compacted", "context_compaction_cancelled", "context_compaction_failed":
			settled[id] = event.Type
		}
	}, nil, "", nil)

	if !announced {
		t.Fatal("the run never waited for the in-flight background checkpoint")
	}
	if settled["background-job"] != "context_compaction_failed" {
		t.Fatalf("announced background boundary settled as %q; boundaries=%v", settled["background-job"], settled)
	}
	compacted := false
	for id, terminal := range settled {
		if terminal == "" {
			t.Fatalf("compaction boundary %q never settled: %v", id, settled)
		}
		compacted = compacted || id != "background-job" && terminal == "context_compacted"
	}
	if !compacted {
		t.Fatalf("the run did not compact synchronously after the unusable checkpoint: %v", settled)
	}
}

// When the system prompt and tools nearly fill an explicit percentage, every
// checkpoint lands just below it and one more step crosses it again. The next
// attempt must wait for real growth instead of compacting after every step for
// a few hundred reclaimed tokens.
func TestCheckpointNearAnExplicitThresholdDefersTheNextAttempt(t *testing.T) {
	server := &precompactionServer{rounds: 14}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "thrash-threshold-model")
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: strings.Repeat("evidence ", 150)}, nil
	}
	if err := provider.RecordConcurrencyRefused(a.Provider, a.Model); err != nil {
		t.Fatal(err)
	}
	system := strings.Repeat("policy ", 1_000)
	fixed := estimateCanonicalRequest([]provider.ChatMessage{provider.NewTextMessage("system", system)}, nil, a.Model)
	system = strings.Repeat("policy ", 1_000*56_000/fixed)
	compactions := 0
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task")}, system, func(event AgentEvent) {
		if event.Type == "context_compacted" {
			compactions++
		}
	}, nil, "", nil)
	if len(server.finals) != 1 {
		t.Fatalf("the run did not finish its task: final requests=%d", len(server.finals))
	}
	if compactions == 0 || compactions > 3 {
		t.Fatalf("compactions=%d over 14 small steps; want the first and then only after real growth", compactions)
	}
}

// A deferral belongs to the agent, not to one run: the next human turn starts
// a new run over the same context, which must not compact again before the
// deferred growth arrives. A replaced context is a new generation.
func TestDeferredCompactionSurvivesTheEndOfARun(t *testing.T) {
	server := &precompactionServer{rounds: 10}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "deferral-across-runs-model")
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: strings.Repeat("evidence ", 150)}, nil
	}
	if err := provider.RecordConcurrencyRefused(a.Provider, a.Model); err != nil {
		t.Fatal(err)
	}
	slot := NewPrecompactionSlot(t.Context(), nil)
	defer slot.Close()
	a.Precompaction = slot
	system := strings.Repeat("policy ", 1_000)
	fixed := estimateCanonicalRequest([]provider.ChatMessage{provider.NewTextMessage("system", system)}, nil, a.Model)
	system = strings.Repeat("policy ", 1_000*57_000/fixed)
	counter := func(count *int) func(AgentEvent) {
		return func(event AgentEvent) {
			if event.Type == "context_compacted" {
				*count++
			}
		}
	}
	first := 0
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task")}, system, counter(&first), nil, "", nil)
	if first == 0 {
		t.Fatal("the first run never reached the 60% boundary")
	}
	second := 0
	next := append(append([]provider.ChatMessage(nil), a.lastActiveContext...), provider.NewTextMessage("user", "and one more thing"))
	a.Run(t.Context(), next, system, counter(&second), nil, "", nil)
	if second != 0 {
		t.Fatalf("a new human turn compacted a deferred context again (%d compactions)", second)
	}
}

// Automatic compaction must work for every price tier of the main and the
// compaction model, and with whatever the provider reports: prices and token
// usage are optional. Cost optimisation decides when, never whether the agent
// can keep working: each run finishes its task, never compacts below the
// quality floor (a user paying for an expensive model keeps exact history up
// to half of capacity), and compactions stay spaced by real growth.
func TestAutomaticCompactionWorksForEveryPriceTierAndMissingData(t *testing.T) {
	type tier struct {
		name  string
		input float64 // 0: unknown price
	}
	cheap, medium, expensive, premium, unknown := tier{"cheap", 0.05e-6}, tier{"medium", 1e-6}, tier{"expensive", 15e-6}, tier{"very-expensive", 75e-6}, tier{"unpriced", 0}
	type scenario struct {
		main, aeron tier
		usage       bool
	}
	var cases []scenario
	for _, main := range []tier{cheap, medium, expensive, premium} {
		for _, aeron := range []tier{cheap, premium} {
			cases = append(cases, scenario{main, aeron, true})
		}
	}
	cases = append(cases, scenario{premium, cheap, false}, scenario{unknown, unknown, true}, scenario{unknown, unknown, false})
	for _, test := range cases {
		name := test.main.name + "-main/" + test.aeron.name + "-aeron"
		if !test.usage {
			name += "/no-usage"
		}
		t.Run(name, func(t *testing.T) {
			server := &precompactionServer{rounds: 30, usage: test.usage}
			endpoint := httptest.NewServer(server.handler(t))
			defer endpoint.Close()
			model := strings.ReplaceAll("tier-"+name, "/", "-")
			a := precompactionAgent(t, endpoint.URL, model+"-main")
			a.ContextCompactionPercent = 0
			price := func(input float64) provider.ModelPrice {
				return provider.ModelPrice{InputPricePerToken: input, OutputPricePerToken: 4 * input, InputCachedPricePerToken: input / 10}
			}
			provider.SetModelPrice(a.Model, price(test.main.input))
			compaction := provider.FromPreset("openai-chat", "key", endpoint.URL)
			compaction.Model = model + "-aeron"
			provider.SetModelContext(compaction.Model, 100_000)
			provider.SetModelPrice(compaction.Model, price(test.aeron.input))
			combinedCompactionFailures.Delete(combinedCompactionKey(compaction, compaction.Model))
			a.CompactionProvider = compaction
			window := a.Provider.ContextWindow(a.Model)
			hardLimit := window - a.outputTokenLimit(window, 0) - contextSafetyReserve(window)

			var befores []int
			failed := false
			a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task")}, "system", func(event AgentEvent) {
				data, _ := event.Data.(map[string]any)
				switch event.Type {
				case "context_compacted":
					before, _ := data["before_tokens"].(int)
					befores = append(befores, before)
				case "error":
					failed = true
				}
			}, nil, "", nil)
			t.Logf("compacted at %v of a %d-token capacity", befores, hardLimit)
			if failed || len(server.finals) != 1 {
				t.Fatalf("the run did not finish its task: failed=%t final requests=%d", failed, len(server.finals))
			}
			if len(befores) == 0 || len(befores) > 8 {
				t.Fatalf("compactions=%d at %v over 30 steps of a %d-token capacity", len(befores), befores, hardLimit)
			}
			for _, before := range befores {
				if before < hardLimit/2 {
					t.Fatalf("compacted at %d tokens, below the quality floor %d: %v", before, hardLimit/2, befores)
				}
			}
		})
	}
}

func TestConnectionThatRefusedConcurrencyCompactsSynchronously(t *testing.T) {
	server := &precompactionServer{rounds: 14}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "serial-connection-model")
	if err := provider.RecordConcurrencyRefused(a.Provider, a.Model); err != nil {
		t.Fatal(err)
	}
	var trigger any
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task")}, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			trigger = event.Data.(map[string]any)["trigger"]
		}
	}, nil, "", nil)
	if trigger != "automatic" {
		t.Fatalf("serial connection compaction trigger=%v, want synchronous", trigger)
	}
}

func TestCheckpointDecaysOnlyOlderReobtainableToolResults(t *testing.T) {
	round := func(id, tool, args, result string) []provider.ChatMessage {
		call := provider.NewTextMessage("assistant", "")
		call.ToolCalls = []provider.ToolCall{{ID: id, Type: "function", Function: provider.FunctionCall{Name: tool, Arguments: args}}}
		output := provider.NewTextMessage("tool", result)
		output.ToolCallID, output.InternalID = id, "msg-"+id
		return []provider.ChatMessage{call, output}
	}
	bulky := strings.Repeat("line of file content\n", 400)
	var tail []provider.ChatMessage
	tail = append(tail, round("read-old", "read_file", `{"path":"agent/run.go"}`, bulky)...)
	tail = append(tail, round("cmd-old", "execute_command", `{"command":"go test ./..."}`, bulky)...)
	tail = append(tail, round("tiny", "list_directory", `{"path":"."}`, "a\nb")...)
	tail = append(tail, round("read-new", "read_file", `{"path":"agent/agent.go"}`, bulky)...)
	decayed := decayVerbatimTail(tail, "model")
	if !strings.Contains(decayed[1].ContentString(), "left out at a context checkpoint") || !strings.Contains(decayed[1].ContentString(), "agent/run.go") ||
		decayed[1].ToolCallID != "read-old" || decayed[1].InternalID != "msg-read-old-elided" {
		t.Fatalf("older read was not replaced by a re-runnable stub: %+v", decayed[1])
	}
	if decayed[3].ContentString() != bulky {
		t.Fatal("command output is not re-obtainable and must stay verbatim")
	}
	if decayed[5].ContentString() != "a\nb" {
		t.Fatal("a result smaller than its stub was replaced")
	}
	if decayed[7].ContentString() != bulky {
		t.Fatal("the latest tool round must stay whole")
	}
	if tail[1].ContentString() != bulky {
		t.Fatal("decay mutated the caller's history")
	}
}

func TestOneLargeJumpDoesNotKeepTheAgentNearCapacity(t *testing.T) {
	a := economicAgent(t, "growth-reset-model")
	a.ContextCompactionPercent = 90
	planner := newCompactionPlanner(0)
	planner.evaluate(a, 10_000, 1_000, 0, 100_000, 120_000, false)
	if plan := planner.evaluate(a, 80_000, 1_000, 0, 100_000, 120_000, false); !plan.swap {
		t.Fatalf("a 70K jump to 80K of 100K must count as near capacity: %+v", plan)
	}
	planner.learn(a, 80_000, 5_000, 20_000, provider.TokenUsage{})
	if plan := planner.evaluate(a, 21_000, 1_000, 0, 100_000, 120_000, false); plan.swap || plan.prepare {
		t.Fatalf("after a checkpoint the old jump still forces compaction: %+v", plan)
	}
}

func TestPreparedCheckpointIsAdoptedOnceItsColdCacheIsPaidFor(t *testing.T) {
	a := economicAgent(t, "sunk-checkpoint-model")
	planner := newCompactionPlanner(0)
	if planner.adoptPrepared(a, 500_000, 20_000, 900_000) {
		t.Fatal("adopted before any spend was observed")
	}
	// A cold 20K-token request costs (2e-6-0.2e-6)*20K = $0.036 more than a cached one.
	for planner.excess < 0.036 {
		planner.observe(a, provider.TokenUsage{PromptTokens: 500_000, PromptTokensCached: 499_000, CacheReadKnown: true}, 500_000, 20_000)
		if planner.excess < 0.036 && planner.adoptPrepared(a, 500_000, 20_000, 900_000) {
			t.Fatalf("adopted at excess %.4f, before the cold cache was paid for", planner.excess)
		}
	}
	if !planner.adoptPrepared(a, 500_000, 20_000, 900_000) {
		t.Fatal("prepared checkpoint not adopted once carrying cost exceeded its cold request")
	}
	a.ContextCompactionPercent = 80
	if planner.adoptPrepared(a, 500_000, 20_000, 900_000) {
		t.Fatal("an explicit percent must keep its own boundary")
	}
}

func TestFailingCompactionModelNeverStopsTheAgentBeforeCapacity(t *testing.T) {
	var compactions, mains atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		var text strings.Builder
		for _, message := range request.Messages {
			text.WriteString(message.ContentString())
		}
		if strings.Contains(text.String(), "Context compaction extraction contract") {
			compactions.Add(1)
			http.Error(w, `{"error":{"message":"model overloaded","type":"server_error"}}`, http.StatusServiceUnavailable)
			return
		}
		call := mains.Add(1)
		if call > 10 {
			writeChatSSE(w, "finished", "stop")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"r-%d\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"f%d\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", call, call)
	}))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "failing-compactor-model")
	a.ContextCompactionPercent = 0 // adaptive: preparation past half capacity, never forced below it
	a.compactionRetryDelay = func(int) time.Duration { return time.Millisecond }
	var failedHard bool
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "work")}, "system", func(event AgentEvent) {
		if event.Type == "error" {
			failedHard = true
			t.Logf("error event: %v", event.Data)
		}
	}, nil, "", nil)
	if failedHard || mains.Load() != 11 {
		t.Fatalf("a failing compaction model stopped the agent: error=%t main requests=%d", failedHard, mains.Load())
	}
	if compactions.Load() == 0 {
		t.Fatal("background preparation was never attempted past half capacity")
	}
	if compactions.Load() > mains.Load()*int32(compactionStageRecoveries+1) {
		t.Fatalf("failed preparation retried without backing off: %d extraction requests for %d agent steps", compactions.Load(), mains.Load())
	}
}

func TestMainModelWritesTheCheckpointWhenTheCompactionModelIsDown(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"invalid credentials","type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	defer broken.Close()
	server := &precompactionServer{rounds: 14}
	endpoint := httptest.NewServer(server.handler(t))
	defer endpoint.Close()
	a := precompactionAgent(t, endpoint.URL, "main-fallback-model")
	compaction := provider.FromPreset("openai-chat", "key", broken.URL)
	compaction.Model = "broken-compaction-model"
	provider.SetModelContext(compaction.Model, 100_000)
	a.CompactionProvider = compaction
	var adopted bool
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Do the long task")}, "system", func(event AgentEvent) {
		if event.Type == "context_compacted" {
			adopted = true
		}
		if event.Type == "error" {
			t.Errorf("run stopped: %v", event.Data)
		}
	}, nil, "", nil)
	if !adopted || server.compactions.Load() == 0 || len(server.finals) != 1 {
		t.Fatalf("main connection did not stand in for the failing compaction model: adopted=%t main-model extractions=%d finals=%d", adopted, server.compactions.Load(), len(server.finals))
	}
}

// Providers sometimes over-advertise what a request may ask for. A combined
// extraction rejected for its shape falls back to one section per request,
// on the same compaction model; it neither aborts nor spends the main model.
func TestRejectedCombinedExtractionFallsBackToStagedSections(t *testing.T) {
	var combined, staged atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		instruction := request.Messages[len(request.Messages)-1].ContentString()
		if strings.Contains(instruction, "All four continuation sections") {
			combined.Add(1)
			http.Error(w, `{"error":{"message":"requested output is larger than this deployment serves","type":"invalid_request_error"}}`, http.StatusBadRequest)
			return
		}
		staged.Add(1)
		writeChatSSE(w, "- section facts", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "over-advertised-model"
	provider.SetModelContext(p.Model, 200_000)
	a := NewAgent(p, p.Model, nil)
	combinedCompactionFailures.Delete(combinedCompactionKey(p, p.Model))
	result, err := a.Compact(t.Context(), compactionTestHistory(provider.NewTextMessage("user", "keep going")), "system", nil)
	if err != nil {
		t.Fatalf("shape rejection aborted compaction: %v", err)
	}
	if combined.Load() != 1 || staged.Load() != int32(len(compactionStagesForTest)) || len(result.Messages) == 0 {
		t.Fatalf("combined=%d staged=%d messages=%d", combined.Load(), staged.Load(), len(result.Messages))
	}
	if combinedCompactionWorks(p, p.Model) {
		t.Fatal("the connection was not learned as needing staged extraction")
	}
}
