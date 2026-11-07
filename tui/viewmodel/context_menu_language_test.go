package viewmodel

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// shortcutText is the only text a menu row may show beside its label: a key
// chord from the shortcut registry (or several joined by " · ").
var shortcutText = regexp.MustCompile(`^((Ctrl|Alt|Shift|Super)\+)*([A-Z0-9]|F[0-9]+|[^A-Za-z0-9\s]|Delete|Space|Enter|Esc|Left|Right|Up|Down)( · .+)?$`)

// Menu rows speak one dialect: a short verb phrase, no trailing ellipsis, no
// description of what the row does, and the real shortcut when one exists.
func TestPaneMenusShowShortcutsNotDescriptions(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "menu.go")
	tab := app.Win.ActiveTab().ID
	check := func(where string, sections []ui.ContextMenuSection) map[string]string {
		t.Helper()
		rows := map[string]string{}
		for _, section := range sections {
			for _, item := range section.Items {
				rows[item.Label] = item.Detail
				if strings.HasSuffix(item.Label, "…") || strings.HasSuffix(item.Label, "...") {
					t.Errorf("%s: %q ends with an ellipsis", where, item.Label)
				}
				if item.Detail != "" && !shortcutText.MatchString(item.Detail) {
					t.Errorf("%s: %q shows %q, which is not a shortcut", where, item.Label, item.Detail)
				}
			}
		}
		return rows
	}
	single := check("one pane", app.paneMenuSections(tab))
	if single["Split right"] != "Ctrl+Alt+L" || single["Split down"] != "Ctrl+Alt+D" {
		t.Fatalf("split rows lost their shortcuts: right=%q down=%q", single["Split right"], single["Split down"])
	}
	for _, absent := range []string{"Merge all panes", "Move to next pane", "Move to previous pane"} {
		if _, ok := single[absent]; ok {
			t.Fatalf("one pane offers %q, which cannot act", absent)
		}
	}
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	split := check("two panes", app.paneMenuSections(app.Win.ActiveTab().ID))
	for _, present := range []string{"Merge all panes", "Move to next pane", "Open in pane to the left", "Move to pane to the left"} {
		if _, ok := split[present]; !ok {
			t.Fatalf("two panes do not offer %q: %v", present, split)
		}
	}
	check("empty pane", app.emptyPaneMenu().Sections)
	check("editor", app.buildEditorContextMenu(tea.Mouse{X: 40, Y: 10}).Sections)
}
