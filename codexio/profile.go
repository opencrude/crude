package codexio

import (
	"embed"
	"strings"
)

const UpstreamRevision = "aa380897f67b91e1a47d530d7286d497b6726d3f"
const PortRevision = "95ed66f6d36a575b00001f2e27aedc1ec7455812"

//go:embed prompts/*
var promptFiles embed.FS

// Instructions returns an unchanged upstream template. Unknown model slugs use
// Codex's own models-manager/prompt.md, not a guessed neighboring model prompt.
// The second return identifies which exact source was selected.
func Instructions(model string) (string, string) {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if !strings.ContainsAny(name, `/\\`) {
		if text, err := promptFiles.ReadFile("prompts/" + name + ".md"); err == nil {
			return string(text), name
		}
	}
	text, _ := promptFiles.ReadFile("prompts/default.md")
	return string(text), "default"
}
