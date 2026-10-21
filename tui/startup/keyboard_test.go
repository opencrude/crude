package startup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/workspace"
)

func keyboardChooser(t *testing.T, count int) *Model {
	t.Helper()
	root := t.TempDir()
	initial := coreprotocol.StartupSnapshot{Root: root, FolderKind: "home"}
	for i := 0; i < count; i++ {
		name := string(rune('A' + i))
		initial.Workspaces = append(initial.Workspaces, workspace.WorkspaceInfo{ProjectName: name, ProjectDir: filepath.Join(root, name)})
	}
	m := NewModel(context.Background(), &fakeStartup{}, initial)
	t.Cleanup(m.Close)
	return m
}

func assertStartupFocus(t *testing.T, m *Model, want ui.ComponentID) {
	t.Helper()
	if got := m.host.Focused(); got != want {
		t.Fatalf("keyboard focus=%q, want %q\n%s", got, want, paintText(m))
	}
}

func TestChooserArrowKeysVisitActionsAndPreviousWorkspaces(t *testing.T) {
	m := keyboardChooser(t, 3)
	assertStartupFocus(t, m, openID)
	send(t, m, key(tea.KeyDown))
	assertStartupFocus(t, m, previousID)
	if selected, _ := m.root.previous.Selected(); selected.Label != "A" {
		t.Fatalf("Down from Open must land on first workspace: %+v", selected)
	}
	send(t, m, key(tea.KeyDown))
	if selected, _ := m.root.previous.Selected(); selected.Label != "B" {
		t.Fatalf("Down did not move to B: %+v", selected)
	}
	send(t, m, key(tea.KeyEnd))
	send(t, m, key(tea.KeyDown))
	assertStartupFocus(t, m, quitID)
	send(t, m, key(tea.KeyUp))
	assertStartupFocus(t, m, previousID)
	if selected, _ := m.root.previous.Selected(); selected.Label != "C" {
		t.Fatalf("Up from Quit must land on last workspace: %+v", selected)
	}
	send(t, m, key(tea.KeyHome))
	send(t, m, key(tea.KeyUp))
	assertStartupFocus(t, m, openID)
	send(t, m, key(tea.KeyDown))
	send(t, m, key(tea.KeyDown))
	send(t, m, key(tea.KeyEnter))
	if m.Result() != m.initial.Workspaces[1].ProjectDir {
		t.Fatalf("Enter did not open keyboard-selected B: %q", m.Result())
	}
}

func TestEmptyChooserSkipsNoninteractiveWorkspaceList(t *testing.T) {
	for _, nav := range []rune{tea.KeyDown, tea.KeyTab} {
		m := keyboardChooser(t, 0)
		send(t, m, key(nav))
		assertStartupFocus(t, m, quitID)
		send(t, m, key(tea.KeyEnter))
		if !m.closed {
			t.Fatal("Enter on Quit did not close")
		}
	}
}

func TestPickerKeyboardCycleSkipsDisabledMoreAndReachesCancel(t *testing.T) {
	m := keyboardChooser(t, 0)
	send(t, m, key(tea.KeyEnter))
	assertStartupFocus(t, m, filepicker.FilterID)
	send(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assertStartupFocus(t, m, pathGoID)
	send(t, m, key(tea.KeyTab))
	assertStartupFocus(t, m, filepicker.FilterID)
	send(t, m, key(tea.KeyTab))
	assertStartupFocus(t, m, filepicker.ListID)
	send(t, m, key(tea.KeyTab))
	assertStartupFocus(t, m, filepicker.ActionsID)
	// The action bar owns two decisions. Tab must visit Cancel, not jump
	// straight out of the bar and make Cancel keyboard-inaccessible.
	send(t, m, key(tea.KeyTab))
	assertStartupFocus(t, m, filepicker.ActionsID)
	send(t, m, key(tea.KeyEnter))
	if m.root.mode != selection || m.closed {
		t.Fatal("keyboard Cancel did not return to the chooser")
	}
	if !strings.Contains(paintText(m), "Choose a project workspace") {
		t.Fatal("Cancel did not repaint the chooser")
	}
}

func TestStartupIntroductionKeyboardAndPickerRoundTrip(t *testing.T) {
	for _, activate := range []rune{tea.KeyEnter, tea.KeySpace} {
		m := NewModel(context.Background(), &fakeStartup{}, coreprotocol.StartupSnapshot{Root: t.TempDir(), ShowIntroduction: true})
		t.Cleanup(m.Close)
		send(t, m, key(tea.KeyTab))
		assertStartupFocus(t, m, goID)
		send(t, m, key(activate))
		if m.root.mode != picking || !m.picker.IsOpen() {
			t.Fatal("keyboard activation of Let's go did not open folder picker")
		}
		send(t, m, key(tea.KeyEsc))
		assertStartupFocus(t, m, openID)
		send(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		assertStartupFocus(t, m, quitID)
		send(t, m, key(tea.KeyEsc))
		if !m.closed {
			t.Fatal("Esc did not quit chooser")
		}
	}
}

func TestPickerReverseTabVisitsBothActionButtons(t *testing.T) {
	m := keyboardChooser(t, 0)
	send(t, m, key(tea.KeyEnter))
	send(t, m, ctrl('l'))
	assertStartupFocus(t, m, pathID)
	back := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	send(t, m, back)
	assertStartupFocus(t, m, filepicker.ActionsID)
	// Entering backwards lands on Cancel. Shift+Tab then visits Open,
	// and only the following Shift+Tab leaves for the tree.
	send(t, m, back)
	assertStartupFocus(t, m, filepicker.ActionsID)
	send(t, m, back)
	assertStartupFocus(t, m, filepicker.ListID)
}
