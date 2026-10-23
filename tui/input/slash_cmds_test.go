package input

import "testing"

func TestAvailableCmdsOfferSessionCommandsOnly(t *testing.T) {
	seen := map[string]bool{}
	for _, cmd := range AvailableCmds() {
		seen[cmd.Name] = true
	}
	for _, want := range []string{"plan", "goal", "fork", "settings"} {
		if !seen[want] {
			t.Fatalf("slash list missing /%s: %#v", want, seen)
		}
	}
	// Model selection lives in /settings; help lives in the palette.
	for _, gone := range []string{"provider", "api-key", "changes", "model", "help"} {
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
