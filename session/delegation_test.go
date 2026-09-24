package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/agent"
	"crude/provider"
)

func TestLiveDelegationToggleAtRequestAndExecutionBoundaries(t *testing.T) {
	requests := make(chan provider.ChatRequest, 8)
	releases := make(chan struct{}, 8)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		select {
		case <-releases:
		case <-r.Context().Done():
			return
		}
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")
		switch requestCount {
		case 1:
			// This was advertised when the request started, but the user disabled
			// delegation while the model was generating the call.
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"spawn-stale\",\"type\":\"function\",\"function\":{\"name\":\"run_sub_agent\",\"arguments\":\"{\\\"description\\\":\\\"must not launch\\\",\\\"wait\\\":false}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"plan-check\",\"type\":\"function\",\"function\":{\"name\":\"get_plan\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued directly\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	defer close(releases)
	cfg := modelAttributionConfig(server.URL, "toggle-model")
	applySessionMultiAgent(cfg, true)
	sess, err := NewWithSessionDir("toggle", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sess.SetYolo(true)
	sender := newMockEventSender()
	orch, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if sess.SubAgentRunner() != nil {
		t.Fatal("enabled session/orchestrator eagerly allocated a runner")
	}
	if !orch.SubmitInput(provider.PlainInput("work")) {
		t.Fatal("turn not admitted")
	}
	nextRequest := func() provider.ChatRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(5 * time.Second):
			t.Fatal("model request missing")
			return provider.ChatRequest{}
		}
	}
	hasTool := func(request provider.ChatRequest, name string) bool {
		for _, tool := range request.Tools {
			if tool.Function.Name == name {
				return true
			}
		}
		return false
	}
	first := nextRequest()
	if !hasTool(first, "run_sub_agent") {
		t.Fatal("enabled request missing launch tool")
	}
	setEnabled := func(enabled bool) {
		t.Helper()
		settings := sess.SettingsSnapshot()
		settings.SessionMultiAgent = &enabled
		settings.Agents.Enabled = enabled
		if err := sess.HandleSettingsSave(settings); err != nil {
			t.Fatal(err)
		}
	}
	setEnabled(false)
	releases <- struct{}{}
	second := nextRequest()
	if hasTool(second, "run_sub_agent") {
		t.Fatal("disabled request still advertises launches")
	}
	for _, name := range []string{"list_sub_agents", "poll_sub_agent", "cancel_sub_agent", "edit_file"} {
		if !hasTool(second, name) {
			t.Errorf("disabled request removed %s", name)
		}
	}
	disabledNotice, disabledResult := false, false
	for _, message := range second.Messages {
		if message.Role == "user" && strings.Contains(message.ContentString(), "user has disabled multi-agent") {
			disabledNotice = true
		}
		if message.Role == "tool" && strings.Contains(message.ContentString(), `"status":"disabled_by_user"`) && strings.Contains(message.ContentString(), `"reason":"user_disabled_multi_agent"`) {
			disabledResult = true
		}
	}
	if !disabledNotice || !disabledResult {
		t.Fatalf("parent notice=%v distinct nonfailure result=%v", disabledNotice, disabledResult)
	}
	if sess.SubAgentRunner() != nil {
		t.Fatal("disabled stale call allocated a runner")
	}
	setEnabled(true)
	releases <- struct{}{}
	third := nextRequest()
	if !hasTool(third, "run_sub_agent") {
		t.Fatal("re-enabled active turn did not regain launch tool")
	}
	enabledNotice := false
	for _, message := range third.Messages {
		if message.Role == "user" && strings.Contains(message.ContentString(), "user has enabled multi-agent") {
			enabledNotice = true
		}
	}
	if !enabledNotice {
		t.Fatal("parent did not receive re-enable notice")
	}
	releases <- struct{}{}
	waitTurnDone(t, sender, orch.ID)
	waitAssistantCount(t, sess, 1)
	// A settings-origin refusal is not a failed child and persists as runtime
	// context rather than being attributed to human chat.
	for _, message := range sess.MessagesWithMeta() {
		if strings.Contains(message.Msg.ContentString(), "user has disabled multi-agent") && message.Msg.Role == "user" {
			if message.Msg.EffectiveOrigin() != provider.OriginRuntimeContext || message.Msg.EffectiveVisibility() != provider.VisibilityContextOnly {
				t.Fatal("settings notice impersonates human input")
			}
		}
	}
}

func TestConcurrentLazyRunnerAndDisableKeepsChildControl(t *testing.T) {
	cfg := testConfig()
	sess, err := NewWithSessionDir("lazy", cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sender := &liveSubAgentSender{}
	orch, err := sess.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if sess.SubAgentRunner() != nil {
		t.Fatal("runner was created without a launch")
	}
	var wg sync.WaitGroup
	runners := make(chan *SubAgentRunner, 16)
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if !sess.initSubAgentRunner() {
				t.Error("lazy initialization failed")
				return
			}
			runners <- sess.SubAgentRunner()
		})
	}
	wg.Wait()
	close(runners)
	runner := sess.SubAgentRunner()
	for got := range runners {
		if got != runner {
			t.Fatal("concurrent launches allocated different runners")
		}
	}
	if runner.orchestratorID != orch.ID || runner.sender != sender {
		t.Fatal("lazy runner was published without parent routing")
	}
	childCtx, childCancel := context.WithCancel(context.Background())
	defer childCancel()
	handle := &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: "active-child", Name: "active-child", Category: agent.CatResearcher, ParentTurn: 1}, Agent: &agent.Agent{Ctx: childCtx}, Cancel: childCancel}
	if err := runner.Manager().Register(handle); err != nil {
		t.Fatal(err)
	}
	settings := sess.SettingsSnapshot()
	off := false
	settings.SessionMultiAgent = &off
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	if childCtx.Err() != nil || sess.SubAgentRunner() != runner {
		t.Fatal("disable destroyed a running child or its control path")
	}
	toolFn := sess.newToolFn(true) // stale enabled closure must consult live policy
	result, err := toolFn(t.Context(), "run_sub_agent", map[string]any{"description": "not allowed"})
	if err != nil || agent.IsToolError(result.Content) || !strings.Contains(result.Content, "disabled_by_user") {
		t.Fatalf("disabled launch misreported as failure: %+v %v", result, err)
	}
	result, err = toolFn(t.Context(), "list_sub_agents", nil)
	if err != nil || !strings.Contains(result.Content, "active-child") {
		t.Fatal("disable hid active children")
	}
	sess.CloseOrchestrator(orch.ID)
	if childCtx.Err() != context.Canceled {
		t.Fatal("closing disabled parent did not cancel its child")
	}
	runner.Manager().MarkFinished(handle.Spec.ID, agent.SubAgentResult{AgentID: handle.Spec.ID, Status: agent.SubAgentCancelled})
}
