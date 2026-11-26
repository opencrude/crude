package turnloop

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rootprompt "crude/agent/prompt"
	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
)

// Testers' gateways refused Errand's compaction with "413 Request Entity Too
// Large" and similar limit errors on every run: the model advertised a large
// window, the route accepted far less. Errand now plans within the limits it
// knows, reads a long history in chained parts, adapts to the evidence in a
// rejection and remembers the limit, so the next compaction does not fail the
// same way. Each case runs two compactions on fresh agents: the first adapts,
// the second must not hit the limit again.

var factMarker = regexp.MustCompile(`fact-\d{3}`)

// factHistory is a long conversation with one numbered fact per message, so
// a checkpoint proves which messages it read. Every tenth message is a short
// human request; the others are long tool output.
func factHistory(count, filler int) []provider.ChatMessage {
	history := make([]provider.ChatMessage, 0, count+1)
	for i := range count {
		if i%10 == 0 {
			history = append(history, provider.NewTextMessage("user", fmt.Sprintf("fact-%03d keep the parser constraint", i)))
			continue
		}
		history = append(history, provider.NewTextMessage("assistant", fmt.Sprintf("fact-%03d %s", i, strings.Repeat("evidence ", filler))))
	}
	return append(history, provider.NewTextMessage("user", "Continue with the parser fix."))
}

// factReply answers like an extraction model: it carries every fact it was
// given, from the source and from the running checkpoint, in every section
// a combined request asks for.
func factReply(r providersim.Request) providersim.Turn {
	text := conversationText(r.Conversation)
	seen := factMarker.FindAllString(text, -1)
	slices.Sort(seen)
	facts := "- facts: " + strings.Join(slices.Compact(seen), " ")
	if !strings.Contains(text, "All four continuation sections") {
		return providersim.Text(facts)
	}
	var sections strings.Builder
	for _, stage := range rootprompt.CompactionStages() {
		fmt.Fprintf(&sections, "<<<%s>>>\n%s\n", stage.Name, facts)
	}
	return providersim.Text(sections.String())
}

type limitedErrand struct {
	server     *providersim.Server
	connection *provider.Provider
	rejected   atomic.Int32
	chained    atomic.Int32
	requests   atomic.Int32
}

// newLimitedErrand serves Errand on a Chat route that advertises a 1M window
// and refuses what reject returns an error for.
func newLimitedErrand(t *testing.T, reject func(r providersim.Request, body provider.ChatRequest) (providersim.Turn, bool), options ...providersim.Option) *limitedErrand {
	t.Helper()
	e := &limitedErrand{server: providersim.New(t, options...)}
	e.server.Respond(func(r providersim.Request) providersim.Turn {
		e.requests.Add(1)
		body := canonicalChatRequest(t, r)
		if reject != nil {
			if turn, refused := reject(r, body); refused {
				e.rejected.Add(1)
				return turn
			}
		}
		if strings.Contains(conversationText(r.Conversation), "Running checkpoint of source parts") {
			e.chained.Add(1)
		}
		return factReply(r)
	})
	e.connection = provider.FromPreset("openai-chat", "key", e.server.BaseURL(providersim.Chat))
	e.connection.Model = "errand-" + strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	e.connection.SetContextWindow(1_000_000)
	return e
}

// compact runs one manual compaction on a fresh agent and returns the
// checkpoint text and the rejections it caused.
func (e *limitedErrand) compact(t *testing.T, history []provider.ChatMessage) (string, int) {
	t.Helper()
	before := int(e.rejected.Load()) + e.server.ProxyRejections()
	a := newTestAgent(t, e.connection, e.connection.Model)
	result, err := a.Compact(t.Context(), history, "stable system prompt", nil)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if len(result.Messages) == 0 || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("no smaller checkpoint: %+v", result)
	}
	return result.Messages[0].ContentString(), int(e.rejected.Load()) + e.server.ProxyRejections() - before
}

func requireEveryFact(t *testing.T, checkpoint string, count int) {
	t.Helper()
	for i := range count {
		if fact := fmt.Sprintf("fact-%03d", i); !strings.Contains(checkpoint, fact) {
			t.Fatalf("the checkpoint lost %s", fact)
		}
	}
}

func TestErrandCompactionReadsHistoryInChainedPartsWithinAProxyBodyLimit(t *testing.T) {
	const limit = 96 << 10
	e := newLimitedErrand(t, nil, providersim.WithProxyBodyLimit(limit))
	history := factHistory(80, 300)
	// A screenshot is small in tokens and large in bytes.
	screenshot := provider.NewTextMessage("user", "")
	screenshot.Content = provider.PartsContent([]provider.ContentPart{
		{Type: "text", Text: "fact-080 the failing screen"},
		{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, 120<<10))}},
	})
	history = append(history[:len(history)-1], screenshot, history[len(history)-1])

	checkpoint, rejected := e.compact(t, history)
	if rejected == 0 {
		t.Fatal("the proxy never refused a request; the test does not reach the limit")
	}
	requireEveryFact(t, checkpoint, 81)
	if e.chained.Load() == 0 {
		t.Fatal("later parts did not carry the running checkpoint")
	}
	if learned := provider.LearnedRequestLimits("", e.connection, e.connection.Model); learned.Bytes <= 0 || learned.Bytes > limit {
		t.Fatalf("learned body limit=%d, want a value within %d", learned.Bytes, limit)
	}
	again, rejectedAgain := e.compact(t, history)
	if rejectedAgain != 0 {
		t.Fatalf("the next compaction hit the same limit %d times", rejectedAgain)
	}
	requireEveryFact(t, again, 81)
}

func TestErrandCompactionLearnsAStatedContextWindow(t *testing.T) {
	const window = 24_000
	e := newLimitedErrand(t, func(_ providersim.Request, body provider.ChatRequest) (providersim.Turn, bool) {
		input := estimateCanonicalRequest(body.Messages, body.Tools, body.Model)
		output := max(body.MaxTokens, body.MaxCompletionTokens)
		if input+output <= window {
			return providersim.Turn{}, false
		}
		return providersim.HTTPError(providersim.ContextOverflow).WithFaultMessage(fmt.Sprintf(
			"This model's maximum context length is %d tokens. However, you requested %d tokens (%d in the messages, %d in the completion). Please reduce the length of the messages or completion.",
			window, input+output, input, output)), true
	})
	history := factHistory(60, 400)
	checkpoint, rejected := e.compact(t, history)
	if rejected != 1 {
		t.Fatalf("rejections=%d; one stated limit should be enough to plan every later request", rejected)
	}
	requireEveryFact(t, checkpoint, 60)
	// The window is kept in Crude's estimate of the provider's count.
	if learned := provider.LearnedRequestLimits("", e.connection, e.connection.Model); learned.Window <= 0 || learned.Window > window*21/20 {
		t.Fatalf("learned window=%d, want about %d", learned.Window, window)
	}
	if _, again := e.compact(t, history); again != 0 {
		t.Fatalf("the next compaction exceeded the learned window %d times", again)
	}
}

func TestErrandCompactionLearnsTheOutputCeiling(t *testing.T) {
	const ceiling = 4096
	e := newLimitedErrand(t, func(_ providersim.Request, body provider.ChatRequest) (providersim.Turn, bool) {
		output := max(body.MaxTokens, body.MaxCompletionTokens)
		if output <= ceiling {
			return providersim.Turn{}, false
		}
		return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage(fmt.Sprintf(
			"max_tokens: %d > %d, which is the maximum allowed number of output tokens for this model", output, ceiling)), true
	})
	history := factHistory(20, 200)
	checkpoint, rejected := e.compact(t, history)
	if rejected != 1 {
		t.Fatalf("rejections=%d; the stated ceiling should apply to every later request", rejected)
	}
	requireEveryFact(t, checkpoint, 20)
	if learned := provider.LearnedRequestLimits("", e.connection, e.connection.Model); learned.Output <= 0 || learned.Output > ceiling {
		t.Fatalf("learned output=%d, want at most %d", learned.Output, ceiling)
	}
	if _, again := e.compact(t, history); again != 0 {
		t.Fatalf("the next compaction exceeded the output ceiling %d times", again)
	}
}

// A route that answers only "Invalid request." for a request it cannot take
// gets smaller attempts, and the limit that worked is remembered.
func TestErrandCompactionSurvivesAnUnexplainedRejection(t *testing.T) {
	e := newLimitedErrand(t, func(_ providersim.Request, body provider.ChatRequest) (providersim.Turn, bool) {
		if estimateCanonicalRequest(body.Messages, body.Tools, body.Model) <= 20_000 {
			return providersim.Turn{}, false
		}
		return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("Invalid request."), true
	})
	history := factHistory(40, 500)
	checkpoint, rejected := e.compact(t, history)
	if rejected == 0 {
		t.Fatal("the route never refused; the test does not reach the limit")
	}
	requireEveryFact(t, checkpoint, 40)
	if learned := provider.LearnedRequestLimits("", e.connection, e.connection.Model); learned.Input <= 0 || learned.Input > 20_000 {
		t.Fatalf("learned input=%d, want at most 20000", learned.Input)
	}
	if _, again := e.compact(t, history); again != 0 {
		t.Fatalf("the next compaction was refused %d times", again)
	}
}

// OpenAI refuses a request above the account's tokens-per-minute limit with
// HTTP 429. Waiting never helps; a smaller request does.
func TestErrandCompactionTreatsARequestAboveTheTokenRateAsALimit(t *testing.T) {
	const perMinute = 30_000
	e := newLimitedErrand(t, func(_ providersim.Request, body provider.ChatRequest) (providersim.Turn, bool) {
		requested := estimateCanonicalRequest(body.Messages, body.Tools, body.Model) + max(body.MaxTokens, body.MaxCompletionTokens)
		if requested <= perMinute {
			return providersim.Turn{}, false
		}
		return providersim.HTTPError(providersim.RateLimit).WithFaultMessage(fmt.Sprintf(
			"Request too large for errand in organization org-test on tokens per min (TPM): Limit %d, Requested %d. The input or output tokens must be reduced in order to run successfully.", perMinute, requested)), true
	})
	e.connection.SetContextWindow(200_000)
	history := factHistory(50, 500)
	checkpoint, rejected := e.compact(t, history)
	if rejected == 0 {
		t.Fatal("the route never refused; the test does not reach the limit")
	}
	requireEveryFact(t, checkpoint, 50)
	if _, again := e.compact(t, history); again != 0 {
		t.Fatalf("the next compaction was refused %d times", again)
	}
}

// A gateway that times out on a very large prompt gets a smaller one after
// the same request timed out twice.
func TestErrandCompactionShrinksARequestThatKeepsTimingOut(t *testing.T) {
	e := newLimitedErrand(t, func(_ providersim.Request, body provider.ChatRequest) (providersim.Turn, bool) {
		if estimateCanonicalRequest(body.Messages, body.Tools, body.Model) <= 30_000 {
			return providersim.Turn{}, false
		}
		return providersim.HTTPError(providersim.RequestTimeout), true
	})
	history := factHistory(50, 600)
	a := newTestAgent(t, e.connection, e.connection.Model)
	a.compactionRetryDelay = func(int) time.Duration { return 0 }
	result, err := a.Compact(t.Context(), history, "stable system prompt", nil)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	requireEveryFact(t, result.Messages[0].ContentString(), 50)
	if e.rejected.Load() < 2 {
		t.Fatalf("timeouts=%d; the large request should be retried once before it is split", e.rejected.Load())
	}
}

// A provider that names nothing Crude can act on still fails the compaction,
// after a bounded number of requests, and keeps the source.
func TestErrandCompactionStopsWhenNoSmallerRequestHelps(t *testing.T) {
	e := newLimitedErrand(t, func(providersim.Request, provider.ChatRequest) (providersim.Turn, bool) {
		return providersim.HTTPError(providersim.InvalidRequest).WithFaultMessage("Invalid request."), true
	})
	a := newTestAgent(t, e.connection, e.connection.Model)
	history := factHistory(30, 500)
	if _, err := a.Compact(t.Context(), history, "stable system prompt", nil); err == nil {
		t.Fatal("a route that refuses every request produced a checkpoint")
	}
	if requests := e.requests.Load(); requests > 5 {
		t.Fatalf("requests=%d; a refusal that size does not explain must not keep shrinking", requests)
	}
	if learned := provider.LearnedRequestLimits("", e.connection, e.connection.Model); learned.Input != 0 || learned.Output != 0 {
		t.Fatalf("a failed guess was remembered: %+v", learned)
	}
}

// The whole journey: the user types /compact while the agent works, and
// Errand's route refuses the full history. Errand reads it in parts, the
// checkpoint installs, and the agent's next request keeps its system prompt,
// its tools and the pending request, with no raw history behind it.
func TestHumanCompactThroughALimitedErrandKeepsPromptToolsAndTask(t *testing.T) {
	gateway := providersim.New(t)
	var mu sync.Mutex
	var main []providersim.Request
	gateway.Respond(func(r providersim.Request) providersim.Turn {
		mu.Lock()
		main = append(main, r)
		mu.Unlock()
		return providersim.Text("Parser fix continued.")
	})
	p := &provider.Provider{ID: "custom", AccountID: "limited-errand-main", StandardName: "openai-responses", Model: "main-model", BaseURL: gateway.BaseURL(providersim.Responses)}
	p.SetContextWindow(400_000)
	a := newTestAgent(t, p, p.Model)
	e := newLimitedErrand(t, nil, providersim.WithProxyBodyLimit(96<<10))
	a.CompactionProvider = e.connection
	a.ContextCompactionPercent = CompactionOff
	a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file", "edit_file"}, nil, "", false)
	taken := false
	a.CompactionRequest = func() string {
		if taken {
			return ""
		}
		taken = true
		return "human-request"
	}
	var commits []CompactionCommit
	a.CommitCompaction = func(c CompactionCommit) error { commits = append(commits, c); return nil }
	history := factHistory(80, 300)
	a.Run(t.Context(), history, "SYSTEM PROMPT MARKER", func(AgentEvent) {}, nil, "", nil)

	if a.RunFailure() != nil || len(commits) != 1 || commits[0].ModelID != e.connection.Model {
		t.Fatalf("failure=%v commits=%+v; Errand should write the checkpoint", a.RunFailure(), commits)
	}
	if e.server.ProxyRejections() == 0 || e.chained.Load() == 0 {
		t.Fatalf("rejections=%d chained=%d; the route limit was not exercised", e.server.ProxyRejections(), e.chained.Load())
	}
	if len(main) != 1 {
		t.Fatalf("main requests=%d, want only the continuation", len(main))
	}
	sent := conversationText(main[0].Conversation)
	for _, want := range []string{"SYSTEM PROMPT MARKER", "context_checkpoint", "Continue with the parser fix.", "fact-000", "fact-079"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("the continuation lost %q", want)
		}
	}
	if strings.Contains(sent, "evidence evidence") {
		t.Fatal("raw history was sent behind the checkpoint")
	}
	var declared []string
	for _, tool := range main[0].Tools {
		declared = append(declared, tool.Name)
	}
	if !slices.Contains(declared, "read_file") || !slices.Contains(declared, "edit_file") {
		t.Fatalf("the continuation lost its tools: %v", declared)
	}
}
