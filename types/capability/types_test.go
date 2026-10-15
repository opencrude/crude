package capability

import "testing"

func TestDefaultSourcesAreCuratedPinnedAndDisableable(t *testing.T) {
	settings := WithDefaults(Settings{})
	skills, ok := settings.Sources[AnthropicSkillsSourceID]
	if !ok || !skills.Builtin || !skills.Trusted || skills.Kind != "repository" ||
		skills.Revision != AnthropicSkillsPinnedRevision || len(skills.Revision) != 40 {
		t.Fatalf("Anthropic preset=%+v", skills)
	}
	registry, ok := settings.Sources[OfficialMCPRegistrySourceID]
	if !ok || !registry.Builtin || !registry.Trusted || registry.Kind != "registry" ||
		registry.Location != "https://registry.modelcontextprotocol.io/v0/servers" {
		t.Fatalf("MCP Registry preset=%+v", registry)
	}
	directory, ok := settings.Sources[SkillsDirectorySourceID]
	if !ok || !directory.Builtin || !directory.Trusted || directory.Kind != "skill-registry" ||
		directory.Location != "https://skills.sh/api/search" {
		t.Fatalf("Skills Directory preset=%+v", directory)
	}

	registry.Disabled = true
	settings.Sources[registry.ID] = registry
	settings = WithDefaults(settings)
	if !settings.Sources[registry.ID].Disabled {
		t.Fatal("default injection overwrote an explicit disable choice")
	}
}

func TestBuiltinWebConnectionIsInstalledAndKeepsOnlyUserChoices(t *testing.T) {
	settings := WithDefaults(Settings{})
	web, ok := settings.Connections[WebConnectionID]
	if !ok || !web.IsBuiltin() || web.Command != WebConnectionID || web.Scope != ScopeApplication {
		t.Fatalf("built-in web connection=%+v", web)
	}
	web.Disabled, web.Name, web.URL, web.Transport = true, "My web", "https://example.invalid", "http"
	web.Grants = map[string]ToolGrant{"arxiv_search": {Disabled: true}}
	settings.Connections[WebConnectionID] = web
	settings = WithDefaults(settings)
	got := settings.Connections[WebConnectionID]
	if !got.Disabled || got.Name != "My web" || !got.Grants["arxiv_search"].Disabled {
		t.Fatalf("default injection dropped user choices: %+v", got)
	}
	if !got.IsBuiltin() || got.URL != "" || got.Command != WebConnectionID {
		t.Fatalf("built-in identity was not restored: %+v", got)
	}
}
