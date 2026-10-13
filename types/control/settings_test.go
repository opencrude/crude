package control_test

import (
	"crude/provider"
	"crude/types"
	"crude/types/control"
	"testing"
)

func TestSettingsChangesAuthorityDerivedCollectorFacts(t *testing.T) {
	old := types.AppSettings{Accounts: map[string]provider.Account{"a": {ProviderID: "openai"}, "b": {ProviderID: "anthropic"}}, Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "a", Provider: "openai", Model: "old", ModelInputPrice: 3, ModelOutputPrice: 4, ModelCachePrice: 1, ModelSupportsTools: true}}}}
	candidate := func() types.AppSettings {
		next := old
		next.Agents.Categories = map[string]types.CategorySettings{"default": old.Agents.Categories["default"]}
		return next
	}
	next := candidate()
	cfg := next.Agents.Categories["default"]
	cfg.AccountID, cfg.Provider, cfg.Model = "b", "anthropic", "new"
	cfg.ModelInputPrice, cfg.ModelOutputPrice, cfg.ModelCachePrice, cfg.ModelSupportsTools = 0, 0, 0, false
	next.Agents.Categories["default"] = cfg
	if control.SettingsChangesAuthority(old, next, "") {
		t.Fatal("exact account projection/model clears rejected")
	}
	for _, unsafe := range []string{"provider", "price", "tools", "enabled", "policy", "inherit", "supports"} {
		t.Run(unsafe, func(t *testing.T) {
			bad := candidate()
			c := bad.Agents.Categories["default"]
			switch unsafe {
			case "provider":
				c.Provider = "anthropic"
			case "price":
				c.ModelInputPrice = 0
			case "tools":
				c.AllowedTools = []string{"execute_command"}
			case "enabled":
				enabled := false
				c.Enabled = &enabled
			case "policy":
				c.Policy = ""
				c.DeniedTools = []string{"x"}
			case "inherit":
				c.Inherit = "main"
			case "supports":
				c.ModelSupportsTools = false
			}
			bad.Agents.Categories["default"] = c
			if !control.SettingsChangesAuthority(old, bad, "") {
				t.Fatal("unsafe change admitted")
			}
		})
	}
	if control.OrdinaryAgentLeaf("Provider") {
		t.Fatal("derived provider globally ordinary")
	}
}
