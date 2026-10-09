package viewmodel

import (
	"strings"
	"testing"

	"crude/types"
)

func TestSavedModelIdentityUpdatesHeaderWithoutRewritingUsage(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	first := addBackendTestOrchestrator(t, app)
	old := app.AgentModelIdentity(first)
	owner := app.SessionsByAgent[first]
	app.ActiveSession = owner
	backend := app.BackendSession(owner.SessionID)
	app.RecordAgentUsage(first, map[string]any{"request_outcome": "succeeded", "request_id": "old", "reported_at": int64(1), "model": old.ModelID, "provider": old.ProviderID, "account_id": old.AccountID, "turn_generation": uint64(1), "prompt": 200, "context_prompt": 200, "context_window": 1000, "cost": 0.2, "cost_known": true})
	second := addBackendTestOrchestrator(t, app)
	settings := backend.SettingsSnapshot()
	settings.SessionOverrides = map[string]types.CategorySettings{"default": {AccountID: old.AccountID, Model: "replacement-model"}}
	if err := app.HandleSettingsSaveForSession(owner.SessionID, settings); err != nil {
		t.Fatal(err)
	}
	if got := app.AgentModelIdentity(second); got.ModelID == "replacement-model" {
		t.Fatal("saving background session changed another tab")
	}
	app.switchToAgentView(first)
	app.paintShellFrame()
	header := rowText(app, 0)
	if !strings.Contains(header, "replacement-model") || strings.Contains(header, "200/") {
		t.Fatalf("header retained previous model/context: %s", header)
	}
	app.RecordAgentUsage(first, map[string]any{"request_outcome": "succeeded", "request_id": "new", "reported_at": int64(2), "model": "replacement-model", "provider": old.ProviderID, "account_id": old.AccountID, "turn_generation": uint64(2), "cost": 0.4, "cost_known": true})
	usage, _ := app.UsageSummary(first, types.UsageThisAgent)
	if len(usage.Models) != 2 || usage.Requests != 2 || usage.Turns != 2 || usage.Cost.USD < 0.59 {
		t.Fatalf("model change rewrote usage: %+v", usage)
	}
	for _, item := range app.statusTabItems() {
		if item.ID != "agent:"+first {
			continue
		}
		if !strings.Contains(item.Subtitle, "replacement-model") || !strings.Contains(item.Compact, "2 turns") {
			t.Fatalf("collapsed tab omitted useful identity/usage: %+v", item)
		}
		var details strings.Builder
		for _, section := range item.Usage {
			for _, line := range section.Lines {
				details.WriteString(line.Value)
			}
		}
		if !strings.Contains(details.String(), old.ModelID) || !strings.Contains(details.String(), "replacement-model") || !strings.Contains(details.String(), "1 turn") {
			t.Fatalf("per-model details missing: %s", details.String())
		}
		return
	}
	t.Fatal("open tab was absent from overview")
}
