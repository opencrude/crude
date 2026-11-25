package turnloop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/tools"
	"crude/internal/providersim"
	recoverytypes "crude/types/recovery"
)

// On 2026-11-25 a Responses stream was cut after partial output. The resumed
// request uploaded the whole 499K-token history (3.78 MB) instead of a delta,
// because only the last assistant message could anchor previous_response_id
// and a cut response has no stored ID. The latest completed response still
// anchors the continuation. The tool result, the accepted partial output and
// the continuation request travel once, after it.
func TestResponsesPartialResumeKeepsStoredAnchor(t *testing.T) {
	const original = "ORIGINAL request kept on the server"
	const partial = "ACCEPTED partial answer"
	server := providersim.New(t)
	server.Script(
		providersim.CallTools(providersim.ToolCall{ID: "call-read", Name: "read_file", Arguments: `{"path":"a.go"}`}),
		providersim.Text(partial).WithCut(5),
		providersim.Text(" and the rest"),
	)
	p := provider.FromPreset("openai", "key", server.BaseURL(providersim.Responses))
	p.Model = "stored-model"
	p.AccountID = t.TempDir()
	a := newTestAgent(t, p, p.Model)
	a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		return tools.ToolResult{Content: "file body"}, nil
	}
	var events []AgentEvent
	archive := a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", original)}, "system", func(e AgentEvent) { events = append(events, e) }, nil, "", nil)
	reqs := server.Requests()
	// The run continues by itself; the recorded interruption stays for recovery.
	if !hasEvent(events, "done") || hasEvent(events, "error") || len(reqs) != 3 || archive[len(archive)-1].ContentString() != " and the rest" {
		t.Fatalf("run did not continue after the cut: requests=%d events=%d", len(reqs), len(events))
	}
	accepted := false
	for _, m := range archive {
		if m.Role == "assistant" && m.ContentString() == partial {
			accepted = m.ResponseReference == nil
		}
	}
	if !accepted {
		t.Fatal("the cut output was not accepted without a stored ID")
	}
	anchor := reqs[1].PreviousResponseID
	if anchor != "resp_sim_1" {
		t.Fatalf("tool round anchor = %q", anchor)
	}
	resume := reqs[2]
	sent := string(resume.Raw)
	if resume.PreviousResponseID != anchor || strings.Contains(sent, original) || strings.Count(sent, partial) != 1 ||
		!strings.Contains(sent, "file body") || !strings.Contains(sent, "runtime_continuation") {
		t.Fatalf("resume did not send only the delta after the completed response: previous=%q body=%s", resume.PreviousResponseID, sent)
	}
	if seen := conversationText(resume.Effective); strings.Count(seen, original) != 1 || strings.Count(seen, partial) != 1 {
		t.Fatalf("the model did not see the complete conversation once: %s", seen)
	}
}

// A resume after a cut stream must keep everything the cut request sent as an
// exact prefix on every registered wire, so a provider cache can serve it.
// Only the accepted partial output and the continuation request are new. The
// simulator measures rendered blocks; this guards prefix stability, not a
// provider's actual cache residency or bill.
func TestRegisteredProtocolsResumeKeepsTheCutRequestAsPrefix(t *testing.T) {
	t.Parallel()
	// Frames each wire sends through the first text delta of a plain answer.
	textCut := map[string]int{"openai": 2, "openai-responses": 5, "codex": 5, "anthropic": 4, "gemini": 1, "gemini-interactions": 4, "ollama": 1}
	for _, protocol := range provider.ProtocolList() {
		id := protocol.Info().ID
		fixture, ok := supervisedWires[id]
		if !ok || textCut[id] == 0 {
			t.Fatalf("missing fixture for registered protocol %q", id)
		}
		for _, supervised := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/supervised=%v", id, supervised), func(t *testing.T) {
				t.Parallel()
				const original = "ORIGINAL request for the resume check"
				partial := "ACCEPTED-" + id
				server := providersim.New(t)
				server.Script(
					providersim.CallTools(providersim.ToolCall{ID: "call-read-" + id, Name: "read_file", Arguments: `{"path":"a.go"}`}),
					providersim.Text(partial).WithCut(textCut[id]),
					providersim.Text(" finished"),
				)
				p := provider.FromPreset("openai-chat", "key", server.BaseURL(fixture.wire))
				if err := p.SetProtocolID(id); err != nil {
					t.Fatal(err)
				}
				p.Model, p.AccountID = "resume-"+id, t.TempDir()
				p.SetDialSettings(provider.DialSettings{MaxAttempts: 1})
				a := newTestAgent(t, p, p.Model)
				a.FilteredTools = FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
				a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
					return tools.ToolResult{Content: "file body " + id}, nil
				}
				var events []AgentEvent
				emit := func(e AgentEvent) { events = append(events, e) }
				history := []provider.ChatMessage{provider.NewTextMessage("user", original)}
				var completed bool
				if supervised {
					// A recovery attempt after the run stopped, as the session runs it.
					a.preserveReportState = true
					supervisor := NewSupervisor("root", 1, nil)
					supervisor.Persist = func(recoverytypes.Status) error { return nil }
					recovered := false
					supervisor.Emit = func(e AgentEvent) {
						status, ok := e.Data.(recoverytypes.Status)
						recovered = recovered || e.Type == "recovery" && ok && status.State == recoverytypes.Recovering
					}
					result := a.RunSupervised(t.Context(), history, "system", emit, nil, "session", nil, SupervisedRunOptions{Supervisor: supervisor})
					completed = result.State == SupervisedRunCompleted && recovered
				} else {
					a.Run(t.Context(), history, "system", emit, nil, "", nil)
					completed = hasEvent(events, "done") && !hasEvent(events, "error")
				}
				reqs := server.Requests()
				if len(reqs) != 3 || !completed {
					t.Fatalf("requests=%d completed=%v; want the tool round, the cut answer and its continuation", len(reqs), completed)
				}
				cut, resume := reqs[1], reqs[2]
				if resume.Prefix.PreviousIndex != cut.Index || resume.Prefix.SharedTokens < cut.Prefix.InputTokens {
					t.Fatalf("resume changed the cut request's prefix at %s: shared %d of %d tokens with request %d",
						resume.Prefix.FirstDifference, resume.Prefix.SharedTokens, cut.Prefix.InputTokens, resume.Prefix.PreviousIndex)
				}
				if seen := conversationText(resume.Effective); strings.Count(seen, original) != 1 || strings.Count(seen, partial) != 1 {
					t.Fatalf("the model did not see the conversation and the accepted output exactly once")
				}
				if id == "openai-responses" && (resume.PreviousResponseID == "" || resume.PreviousResponseID != cut.PreviousResponseID || strings.Contains(string(resume.Raw), original)) {
					t.Fatalf("stored Responses resume uploaded more than the delta: previous=%q", resume.PreviousResponseID)
				}
			})
		}
	}
}
