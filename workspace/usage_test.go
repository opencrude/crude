package workspace

import (
	"testing"

	"crude/types/statuscenter"
)

func TestUsageCacheReadEvidenceRemainsRequestScoped(t *testing.T) {
	manager := NewAgentManager()
	manager.AddOrchestrator("root", NewAgentState("root", nil, nil))
	w := &Workspace{AgentMgr: manager, SessionsByAgent: map[string]*Session{"root": NewSession("session", "root", nil, nil)}}
	scope := statuscenter.UsageThisAgent
	record := func(id string, time int64, input, read, write int, readKnown, writeKnown bool) statuscenter.UsageSummary {
		t.Helper()
		summary, ok := w.RecordAgentUsage("root", map[string]any{
			"request_id": id, "reported_at": time, "prompt": input,
			"prompt_cached": read, "cache_read_known": readKnown,
			"prompt_written": write, "cache_write_known": writeKnown,
		})
		if !ok {
			t.Fatal("usage event was rejected")
		}
		return summary
	}
	assertMeasured := func(got statuscenter.UsageValue, want int64) {
		t.Helper()
		if got.Value != want || !got.Known || got.Confidence != statuscenter.ConfidenceMeasured {
			t.Fatalf("usage = %+v, want %d measured", got, want)
		}
	}
	assertUnknown := func(got statuscenter.UsageValue) {
		t.Helper()
		if got.Known || got.Confidence != statuscenter.ConfidenceUnknown {
			t.Fatalf("usage = %+v, want unavailable", got)
		}
	}

	hit := record("hit", 10, 1000, 950, 25, true, true)
	assertMeasured(hit.InputTotal, 1000)
	assertMeasured(hit.CacheRead, 950)
	assertMeasured(hit.CacheWrite, 25)
	assertMeasured(hit.UncachedInput, 50)
	miss := record("miss", 20, 100, 0, 0, true, false)
	assertMeasured(miss.InputTotal, 1100)
	assertMeasured(miss.CacheRead, 950)
	assertMeasured(miss.UncachedInput, 150)
	assertUnknown(miss.CacheWrite)

	// A count outside one request cannot become valid because other requests
	// supply a larger denominator. A corrected revision restores the totals.
	bad := record("bad", 30, 10, 11, 2, true, true)
	assertUnknown(bad.CacheRead)
	assertUnknown(bad.UncachedInput)
	corrected := record("bad", 40, 10, 1, 2, true, true)
	assertMeasured(corrected.CacheRead, 951)
	assertMeasured(corrected.UncachedInput, 159)
	unknown := record("bad", 50, 10, 0, 0, false, false)
	assertUnknown(unknown.CacheRead)
	assertUnknown(unknown.UncachedInput)
	assertMeasured(miss.LatestCache.InputTotal, 100)
	assertMeasured(miss.LatestCache.Read, 0)
	assertUnknown(unknown.LatestCache.Read)
	// A revision to an earlier request and newer auxiliary work affect totals,
	// not the identity of the latest normal request.
	record("hit", 60, 1000, 900, 25, true, true)
	w.RecordAgentUsage("root", map[string]any{
		"request_id": "checkpoint", "reported_at": int64(70), "request_kind": "compaction",
		"prompt": 500, "prompt_cached": 200, "cache_read_known": true,
	})
	cached, _ := w.UsageSummary("root", scope)
	if cached.Requests != 4 || !cached.LatestCache.Present {
		t.Fatalf("cached revision is stale: %+v", cached)
	}
	assertUnknown(cached.CacheRead)
	assertUnknown(cached.LatestCache.Read)
	assertMeasured(cached.LatestCache.InputTotal, 10)
}
