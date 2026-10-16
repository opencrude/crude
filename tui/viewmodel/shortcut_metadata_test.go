package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

// overrideHintKey returns a copy of DefaultRegistry with one action's
// HintKey replaced, so a hint surface can prove it renders the registry
// value rather than a hardcoded literal.
func overrideHintKey(t *testing.T, id ui.ActionID, hint string) *ui.ActionRegistry {
	t.Helper()
	reg := ui.NewActionRegistry()
	for _, a := range ui.DefaultRegistry().All() {
		reg.Register(a)
	}
	reg.Register(&ui.ActionDef{ID: id, Label: "Save", Category: "File", HintKey: hint})
	return reg
}

// TestStatusHelpRendersShortcutsFromRegistryMetadata proves the editor
// status hints paint FormatShortcut(registry.HintFor(actionID)); an
// overridden registry key appears and the hardcoded default does not.
func TestStatusHelpRendersShortcutsFromRegistryMetadata(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.width = 100
	app.height = 30
	app.actionRegistry = overrideHintKey(t, "file.save", "Ctrl+Alt+Q")

	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path

	help := app.contextualHelpView()
	want := ui.FormatShortcut(app.actionRegistry.HintFor("file.save")) + " save"
	if !strings.Contains(help, want) {
		t.Fatalf("status help must render %q from registry metadata, got %q", want, help)
	}
	if strings.Contains(help, "Ctrl+S save") {
		t.Fatalf("status help must not contain the hardcoded default literal when the registry overrides it: %q", help)
	}
}

// TestStatusDeckHelpDerivesPanelNavFromRegistry proves the session-deck
// "switch panels" hint renders panel.previous/panel.next keys from the
// registry instead of a hardcoded "Ctrl+[ ]" chord.
func TestStatusDeckHelpDerivesPanelNavFromRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.ActiveSession = workspace.NewSession("", "", nil, nil)
	selectDeckSession(t, app, app.ActiveSession, "agent")
	app.openSessionSettings()
	if !app.sessionDeckFocused() {
		t.Fatal("session settings did not focus the session deck")
	}

	help := app.contextualHelpView()
	for _, id := range []ui.ActionID{"panel.previous", "panel.next"} {
		want := ui.FormatShortcut(app.actionRegistry.HintFor(id))
		if want == "" || !strings.Contains(help, want) {
			t.Fatalf("deck status help must render %q from registry (%s), got %q", want, id, help)
		}
	}
}

// TestStatusTabBarHelpDerivesNavFromRegistry proves the tab-bar
// "navigate" hint renders tabbar.previous/tabbar.next keys from the
// registry instead of a hardcoded "← →" chord.
func TestStatusTabBarHelpDerivesNavFromRegistry(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.FocusStack.Push(FocusTabBar)

	help := app.contextualHelpView()
	for _, id := range []ui.ActionID{"tabbar.previous", "tabbar.next"} {
		want := ui.FormatShortcut(app.actionRegistry.HintFor(id))
		if want == "" || !strings.Contains(help, want) {
			t.Fatalf("tab-bar status help must render %q from registry (%s), got %q", want, id, help)
		}
	}
}

// TestLauncherShortcutsDeriveFromRegistryMetadata proves the launcher's
// fixed dismiss/search/delete rows source their keys from the registry
// (launcher.filter.clear/filter/delete), not hardcoded "Esc"//"/"/"d".
func TestLauncherShortcutsDeriveFromRegistryMetadata(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.Tree = &TreeState{}
	app.actionRegistry = overrideHintKey(t, "launcher.filter", "Ctrl+Shift+F2")
	app.paintShellFrame()
	text := bufferPlain(app.mainSurf.Buffer())
	if !strings.Contains(text, "Ctrl+Shift+F2") {
		t.Fatalf("launcher missing registry shortcut:\n%s", text)
	}
	if strings.Contains(text, "D remove") {
		t.Fatal("launcher offers destructive shortcut")
	}
}

func TestHelpListingDerivesShortcutsFromRegistryMetadata(t *testing.T) {
	app := newTestApp(t)
	app.width, app.height = 100, 30
	app.actionRegistry = overrideHintKey(t, "edit.find", "Ctrl+Alt+F2")
	app.openHelp()
	if app.helpScreen == nil || app.overlayHost.Window(ui.OverlayHelp) == nil {
		t.Fatal("help overlay did not open")
	}

	want := ui.FormatShortcut(app.actionRegistry.HintFor("edit.find"))
	found := false
	for _, sc := range app.helpShortcuts() {
		if sc.Keys == want && sc.Label == "Save" {
			// overrideHintKey replaces the whole ActionDef with Label "Save"
			found = true
		}
		if sc.Keys == want {
			found = true
		}
		if strings.EqualFold(sc.Label, "Send") || strings.EqualFold(sc.Label, "New line") {
			t.Fatalf("help catalog must exclude DisplayOnly hint %q", sc.Label)
		}
	}
	if !found {
		t.Fatalf("help catalog missing overridden shortcut %q", want)
	}
	if want == "Ctrl+F" {
		t.Fatal("precondition: override should not be the default Ctrl+F")
	}
}

// TestPaletteEntriesRenderFromActionMetadata proves the command palette
// derives every row (label, detail, icon) from ActionDef metadata.
func TestPaletteEntriesRenderFromActionMetadata(t *testing.T) {
	reg := ui.NewActionRegistry()
	reg.Register(&ui.ActionDef{ID: "palette.custom", Label: "Custom Thing", Description: "does custom things", Icon: "!", Category: "Tools"})
	reg.Register(&ui.ActionDef{ID: "input.submit", Label: "Send", HintKey: "Enter", DisplayOnly: true})

	entries := paletteEntriesFromRegistry(reg)
	var found bool
	for _, e := range entries {
		if e.ID == "palette.custom" {
			found = true
			if e.Label != "Custom Thing" || e.Detail != "does custom things" || e.Icon != "!" {
				t.Fatalf("palette entry must render registry metadata verbatim, got %+v", e)
			}
		}
		if e.ID == "input.submit" {
			t.Fatal("palette must exclude DisplayOnly hint fragments")
		}
	}
	if !found {
		t.Fatal("palette entries must include actions registered in the registry")
	}
}

// TestPaletteListPaintsRegistryMetadata proves the command palette List
// paints the registry's label for a registered action; the frame renders
// from ActionDef metadata, not a hardcoded command table.
func TestPaletteListPaintsRegistryMetadata(t *testing.T) {
	reg := ui.NewActionRegistry()
	reg.Register(&ui.ActionDef{ID: "palette.custom", Label: "Custom Thing", Description: "does custom things", Icon: "!", Category: "Tools"})
	m := newCommandPaletteModel(testTheme(), nil, reg)
	cw := ui.BuildCommandPaletteFrame(testTheme(), ui.CommandPaletteSpec{
		Items: m.listItems(),
		Width: 60, Height: 14,
	})
	host := cw.Host()
	host.Resize(60, 14)
	host.Layout()
	buf := surface.NewCellBuffer(60, 14)
	host.Paint(buf)
	if painted := bufferPlain(buf); !strings.Contains(painted, "Custom Thing") {
		t.Fatalf("palette must paint the registry action label, got:\n%s", painted)
	}
}
