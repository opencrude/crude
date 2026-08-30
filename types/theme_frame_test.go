package types

import (
	"encoding/json"
	"testing"
)

func intPtr(n int) *int { return &n }

// TestThemeFramePadSparseMerge verifies frame paddings inherit leaf-by-leaf
// through the workspace overlay: naming one side keeps the others live.
func TestThemeFramePadSparseMerge(t *testing.T) {
	app := AppSettings{Theme: ThemeSettings{Preset: "dark"}}
	ApplyAppDefaults(&app)
	ws := WorkspaceOverrides{
		// Overlay JSON names only pad_top.
		Theme: json.RawMessage(`{"pad_top":3}`),
	}
	merged := MergeAppWorkspace(app, ws)
	if got := merged.Theme.PadTopOrDefault(); got != 3 {
		t.Fatalf("merged pad_top=%d want 3", got)
	}
	if got := merged.Theme.PadBottomOrDefault(); got != 0 {
		t.Fatalf("merged pad_bottom=%d want inherited default 0", got)
	}
	if got := merged.Theme.PadLeftOrDefault(); got != 0 || got != merged.Theme.PadRightOrDefault() {
		t.Fatalf("merged pad_left/right=%d/%d want inherited 0", merged.Theme.PadLeftOrDefault(), merged.Theme.PadRightOrDefault())
	}
}

// TestThemeFramePadDiffRecordsOnlyChangedLeaves verifies a workspace save with
// one edited side stores only that side, so app-level edits to the others
// keep flowing through.
func TestThemeFramePadDiffRecordsOnlyChangedLeaves(t *testing.T) {
	app := AppSettings{Theme: ThemeSettings{Preset: "dark", PadTop: intPtr(1), PadRight: intPtr(2)}}
	working := app
	working.Theme.PadBottom = intPtr(4)

	overrides := OverridesFromDiff(app, working)
	if len(overrides.Theme) == 0 {
		t.Fatal("expected a theme overlay")
	}
	var delta map[string]any
	if err := json.Unmarshal(overrides.Theme, &delta); err != nil {
		t.Fatalf("overlay json: %v", err)
	}
	if _, ok := delta["pad_bottom"]; !ok {
		t.Fatalf("overlay must name pad_bottom, got %v", delta)
	}
	if _, polluted := delta["pad_top"]; polluted {
		t.Fatalf("overlay must not freeze pad_top, got %v", delta)
	}
}

// TestThemeFramePadNegativeTreatedAsUnset verifies negative stored values are
// configuration noise and resolve to the zero default.
func TestThemeFramePadNegativeTreatedAsUnset(t *testing.T) {
	th := ThemeSettings{PadTop: intPtr(-1), PadBottom: intPtr(-9), PadLeft: intPtr(-2), PadRight: intPtr(-7)}
	if th.PadTopOrDefault() != 0 || th.PadBottomOrDefault() != 0 || th.PadLeftOrDefault() != 0 || th.PadRightOrDefault() != 0 {
		t.Fatal("negative paddings must resolve to the zero default")
	}
}

// TestThemeFramePadJSONRoundTrip pins the wire format: pointers marshal as
// absent keys when nil and as numbers when set.
func TestThemeFramePadJSONRoundTrip(t *testing.T) {
	empty, err := json.Marshal(ThemeSettings{Preset: "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `{"preset":"dark"}` {
		t.Fatalf("nil paddings must marshal as absent keys, got %s", empty)
	}

	data, err := json.Marshal(ThemeSettings{PadTop: intPtr(2), PadRight: intPtr(0)})
	if err != nil {
		t.Fatal(err)
	}
	var back ThemeSettings
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.PadTop == nil || *back.PadTop != 2 || back.PadRight == nil || *back.PadRight != 0 {
		t.Fatalf("round trip lost paddings: %s", data)
	}
	if back.PadBottom != nil || back.PadLeft != nil {
		t.Fatal("unset sides must stay nil after round trip")
	}
}
