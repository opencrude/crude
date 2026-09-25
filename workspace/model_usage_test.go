package workspace

import (
	"reflect"
	"testing"

	"crude/sessiondb"
	"crude/types"
)

func TestModelUsageCountsSuccessfulCallsNotActorGenerations(t *testing.T) {
	mgr := NewAgentManager()
	mgr.AddOrchestrator("root", NewAgentState("root", nil, nil))
	sess := NewSession("session", "", nil, nil)
	w := &Workspace{AgentMgr: mgr, SessionsByAgent: map[string]*Session{"root": sess}}
	events := []map[string]any{
		{"request_id": "a", "model": "A", "provider": "p", "account_id": "one", "turn_generation": uint64(1), "reported_at": int64(1), "cost": 0.2, "cost_known": true},
		{"request_id": "b", "model": "A", "provider": "p", "account_id": "one", "turn_generation": uint64(1), "reported_at": int64(2), "cost": 0.3, "cost_known": true},
		{"request_id": "c", "model": "B", "provider": "p", "account_id": "two", "turn_generation": uint64(2), "reported_at": int64(3), "cost": 0.4, "cost_known": true},
		{"request_id": "d", "model": "A", "provider": "p", "account_id": "one", "turn_generation": uint64(3), "reported_at": int64(4), "cost": 0.1, "cost_known": true},
		{"request_id": "aux", "model": "B", "provider": "p", "account_id": "two", "request_kind": "compaction", "turn_generation": uint64(4), "reported_at": int64(5), "cost": 0.2, "cost_known": true},
	}
	for i, event := range events {
		event["request_outcome"] = "succeeded"
		if i == 3 {
			event["request_outcome"] = "interrupted"
		}
		w.RecordAgentUsage("root", event)
	}
	before, _ := w.UsageSummary("root", types.UsageSession)
	if before.Turns != 4 || !before.TurnsKnown || before.AgentTurns != 3 || len(before.Models) != 2 || before.Models[0].Turns != 2 || before.Models[0].Requests != 3 || before.Models[1].Turns != 2 || before.Models[1].Requests != 2 {
		t.Fatalf("model rollup=%+v", before)
	}
	events[0]["cost"], events[0]["reported_at"] = 0.4, int64(6)
	w.RecordAgentUsage("root", events[0])
	after, _ := w.UsageSummary("root", types.UsageSession)
	if after.Turns != 4 || after.Requests != 5 || after.Cost.USD < 1.39 || after.Cost.USD > 1.41 {
		t.Fatalf("revision duplicated requests or cost: %+v", after)
	}
	records := make([]sessiondb.UsageRecord, 0, len(sess.usageRecords))
	for _, record := range sess.usageRecords {
		records = append(records, record)
	}
	resumed := NewSession("session", "", nil, nil)
	for _, record := range records {
		resumed.usageRecords[record.RequestID] = record
	}
	w.SessionsByAgent["root"] = resumed
	loaded, _ := w.UsageSummary("root", types.UsageSession)
	if !reflect.DeepEqual(after, loaded) {
		t.Fatalf("resumed model totals differ: %+v/%+v", after, loaded)
	}
	unknown := events[1]
	unknown["cost_known"], unknown["reported_at"] = false, int64(7)
	w.RecordAgentUsage("root", unknown)
	partial, _ := w.UsageSummary("root", types.UsageSession)
	if partial.Models[0].Cost.Known || !partial.Models[1].Cost.Known || partial.Cost.Known {
		t.Fatalf("unknown cost availability leaked across models: %+v", partial)
	}
	w.RecordAgentUsage("root", map[string]any{"request_id": "unknown", "reported_at": int64(8), "prompt": 50})
	withUnknown, _ := w.UsageSummary("root", types.UsageThisAgent)
	if withUnknown.TurnsKnown || withUnknown.Turns != 4 {
		t.Fatalf("inferred success from a usage report: %+v", withUnknown)
	}
}
