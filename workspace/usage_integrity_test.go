package workspace

import (
	"testing"

	"crude/sessiondb"
	"crude/types"
)

func TestUsageContextIsAgentLocalAndLiveEqualsPersisted(t *testing.T) {
	makeWorkspace := func() *Workspace {
		manager := NewAgentManager()
		manager.AddOrchestrator("root", NewAgentState("root", nil, nil))
		manager.AddSubAgent("child", "root", "child", "model", "researcher", NewAgentState("child", nil, nil))
		return &Workspace{AgentMgr: manager, SessionsByAgent: map[string]*Session{"root": NewSession("session", "root", nil, nil)}}
	}
	live, resumed := makeWorkspace(), makeWorkspace()
	events := []struct {
		agent string
		data  map[string]any
	}{
		{"root", map[string]any{"request_id": "root-request", "turn_generation": uint64(1), "reported_at": int64(100), "prompt": 100, "completion": 10, "prompt_cached": 20, "cache_read_known": true, "context_prompt": 100, "context_window": 1000, "model": "root-model", "cost": 0.1, "cost_known": true}},
		{"child", map[string]any{"request_id": "child-request", "turn_generation": uint64(1), "reported_at": int64(200), "prompt": 20, "completion": 5, "context_prompt": 20, "context_window": 200, "model": "child-model", "cost": 99.0, "cost_known": false}},
		{"root", map[string]any{"request_id": "root-compact", "request_kind": "compaction", "turn_generation": uint64(1), "reported_at": int64(250), "prompt": 500, "completion": 20, "context_prompt": 500, "context_window": 1000, "model": "root-model"}},
		{"root", map[string]any{"request_id": "cancelled", "turn_generation": uint64(2), "reported_at": int64(300), "prompt": 4, "completion": 1, "partial_or_retry": true}},
	}
	for _, event := range events {
		if _, ok := live.RecordAgentUsage(event.agent, event.data); !ok {
			t.Fatal("valid event rejected")
		}
		parent := ""
		if event.agent == "child" {
			parent = "root"
		}
		r, ok := sessiondb.UsageRecordFromEvent(event.data, "session", event.agent, parent, "")
		if !ok {
			t.Fatal("same event rejected by durable decoder")
		}
		resumed.SessionsByAgent["root"].usageRecords[r.RequestID] = r
	}
	for _, scope := range []types.UsageScope{types.UsageThisAgent, types.UsageSubtree, types.UsageSession} {
		a, _ := live.UsageSummary("root", scope)
		b, _ := resumed.UsageSummary("root", scope)
		if a != b {
			t.Fatalf("live and resumed usage disagree for %s: live=%+v resumed=%+v", scope, a, b)
		}
		if a.ContextUsed.Value != 100 || a.ContextLimit.Value != 1000 || a.ModelID != "root-model" {
			t.Fatalf("child/error replaced latest root context in %s: %+v", scope, a)
		}
		if a.Cost.USD != 0.1 || a.Cost.Known {
			t.Fatalf("unknown price counted as known money: %+v", a.Cost)
		}
	}
}

func TestUsageRequestRevisionsInvalidateCacheWithoutDoubleCounting(t *testing.T) {
	manager := NewAgentManager()
	manager.AddOrchestrator("root", NewAgentState("root", nil, nil))
	w := &Workspace{AgentMgr: manager, SessionsByAgent: map[string]*Session{"root": NewSession("session", "root", nil, nil)}}
	event := map[string]any{"request_id": "request", "reported_at": int64(10), "turn_generation": uint64(1), "prompt": 100, "completion": 10, "context_prompt": 100, "context_window": 1000}
	w.RecordAgentUsage("root", event)
	w.UsageSummary("root", types.UsageThisAgent)
	event["completion"], event["reported_at"] = 20, int64(20)
	w.RecordAgentUsage("root", event)
	event["completion"], event["reported_at"] = 999, int64(15)
	w.RecordAgentUsage("root", event)
	usage, _ := w.UsageSummary("root", types.UsageThisAgent)
	if usage.Requests != 1 || usage.Output.Value != 20 {
		t.Fatalf("revision counted twice or stale revision won: %+v", usage)
	}
	r := w.SessionsByAgent["root"].usageRecords["request"]
	if r.TurnGeneration != 1 {
		t.Fatalf("live generation dropped: %+v", r)
	}
}
