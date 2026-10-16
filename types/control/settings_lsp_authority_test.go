package control

import "testing"

// Agents may toggle language features, but a server definition launches a
// program and resource limits load the user's machine.
func TestLSPLaunchAndResourceSettingsAreHumanOwned(t *testing.T) {
	for path, human := range map[string]bool{
		"lsp.enabled":                             false,
		"lsp.diagnostics":                         false,
		"lsp.languages.go.enabled":                false,
		"lsp.languages.go.command":                true,
		"lsp.languages.go.args":                   true,
		"lsp.languages.go.settings":               true,
		"lsp.languages.go.initialization_options": true,
		"lsp.languages.go.environment":            true,
		"lsp.languages.zz.extensions":             true,
		"lsp.max_servers":                         true,
		"lsp.server_memory_mb":                    true,
	} {
		if got := SettingsAuthority(path); got != human {
			t.Errorf("SettingsAuthority(%q) = %v, want %v", path, got, human)
		}
	}
}
