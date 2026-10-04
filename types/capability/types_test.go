package capability

import "testing"

func TestDefaultSourcesAreCuratedPinnedAndDisableable(t *testing.T) {
	settings := WithDefaultSources(Settings{})
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
	settings = WithDefaultSources(settings)
	if !settings.Sources[registry.ID].Disabled {
		t.Fatal("default injection overwrote an explicit disable choice")
	}
}
