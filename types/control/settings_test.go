package control_test

import (
	"crude/agent/provider"
	"crude/types/control"
	"crude/types/setting"
	"testing"
)

func TestSettingsChangesAuthorityDerivedCollectorFacts(t *testing.T) {
	old := setting.App{Accounts: map[string]provider.Account{"a": {ProviderID: "openai"}, "b": {ProviderID: "anthropic"}}, Agents: setting.Agents{Categories: map[string]setting.Category{"default": {AccountID: "a", Provider: "openai", Model: "old", ModelInputPrice: 3, ModelOutputPrice: 4, ModelCachePrice: 1, ModelSupportsTools: true}}}}
	candidate := func() setting.App {
		next := old
		next.Agents.Categories = map[string]setting.Category{"default": old.Agents.Categories["default"]}
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

func TestTerminalEnvironmentRemainsAuthority(t *testing.T) {
	for _, path := range []string{"terminal.environment", "terminal.environment.PATH", "terminal.environment.VIRTUAL_ENV", "lsp.ask_errand"} {
		if !control.SettingsAuthority(path) {
			t.Fatalf("terminal environment no longer needs caller-specific authority: %s", path)
		}
	}
}

func TestErrandBindingCannotRedirectTheReviewer(t *testing.T) {
	for _, path := range []string{"agents.Categories.errand.Model", "agents.Categories.errand.account_id"} {
		if !control.SettingsAuthority(path) {
			t.Fatalf("agent could change the reviewer through %s", path)
		}
	}
	if control.SettingsAuthority("agents.Categories.errand.ThinkingEffort") || control.SettingsAuthority("agents.Categories.default.Model") {
		t.Fatal("an unrelated setting was treated as the reviewer binding")
	}
}
