package settings

import (
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

func TestMemoryRankingSettingsJourney(t *testing.T) {
	for _, width := range []int{80, 160} {
		cfg := types.AppSettings{Accounts: map[string]provider.Account{"chosen": {ID: "chosen", ProviderID: "openai", Label: "Decision service"}}}
		m := New(theme.DefaultDark(), cfg, nil)
		s := NewSurface(theme.DefaultDark(), m)
		m.SelectCategory(catAgents)
		before := m.AppSettings()
		c := s.agents
		c.begin("role/memory", false)
		c.setPage("ranking")
		frame := bufferPlain(paintAccountSurface(t, s, width, 32))
		if !strings.Contains(frame, "Ranking") || !strings.Contains(frame, "Rank after actions") {
			t.Fatalf("missing ranking settings at %d: %s", width, frame)
		}
		c.draft.SetValue("ranker-enabled", ui.StringValue("on"))
		if c.apply() {
			t.Fatal("enabled without a connection")
		}
		effect, human, err := s.ControlMetadata(ui.NewID(AgentSettingsFormID, "ranker-enabled"), "")
		if err != nil || !human || effect != control.Authority {
			t.Fatal("native opt-in not protected", effect, human, err)
		}
		c.draft.SetValue("ranker-account_id", ui.StringValue("chosen"))
		c.draft.SetValue("ranker-model", ui.StringValue("my-decider"))
		c.draft.SetValue("ranker-every_actions", ui.StringValue("0"))
		if c.apply() {
			t.Fatal("zero cadence accepted")
		}
		c.draft.SetValue("ranker-every_actions", ui.StringValue("1"))
		c.draft.SetValue("ranker-top_k", ui.StringValue("3"))
		c.draft.SetValue("ranker-endpoint", ui.StringValue(types.MemoryRankerChat))
		if !c.apply() {
			t.Fatal("valid ranking settings rejected")
		}
		saved := m.AppSettings()
		if saved.Memory.Ranker.AccountID != "chosen" || saved.Memory.Ranker.EveryActions != 1 || saved.Memory.Ranker.TopK != 3 || saved.Memory.Ranker.Endpoint != types.MemoryRankerChat || saved.Memory.MemoryCategory.Model != "" || saved.Memory.Ranker.Model != "my-decider" {
			t.Fatalf("wrong owner: %+v", saved.Memory)
		}
		if !control.SettingsChangesAuthority(before.Memory.Ranker, saved.Memory.Ranker, "memory.ranker") {
			t.Fatal("autonomous account opt-in allowed")
		}
		c.begin("role/memory", false)
		c.setPage("ranking")
		if c.draft.Field("ranker-model").Value.String() != "my-decider" {
			t.Fatal("saved ranker was not restored")
		}
	}
}
