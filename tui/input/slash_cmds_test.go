package input

import "testing"

func TestAvailableCmdsExposeModelNotProvider(t *testing.T) {
	seen := map[string]bool{}
	for _, cmd := range AvailableCmds() {
		seen[cmd.Name] = true
	}
	if !seen["model"] || !seen["settings"] {
		t.Fatalf("slash list missing model/settings: %#v", seen)
	}
	for _, gone := range []string{"provider", "api-key", "changes"} {
		if seen[gone] {
			t.Fatalf("slash list still has /%s", gone)
		}
	}
}

func TestAvailableCmdsExcludeAppPerformance(t *testing.T) {
	for _, cmd := range AvailableCmds() {
		if cmd.Name == "perf" {
			t.Fatal("application performance must not be a session slash command")
		}
	}
}
