package viewmodel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// An open dropdown follows the real core topic. Selecting the newly offered
// executable and clicking Save keeps the current page, help and other settings.
func TestConnectedSettingsShellDiscoveryKeepsOpenDraftAndSave(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			oldName, oldLabel, newName, newID, newLabel := "sh", "sh", "bash", "bash", "Bash"
			if runtime.GOOS == "windows" {
				oldName, oldLabel, newName, newID, newLabel = "cmd.exe", "Command Prompt", "pwsh.exe", "pwsh", "PowerShell 7"
				for _, key := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec"} {
					t.Setenv(key, "")
				}
			}
			oldDir, updatedDir := t.TempDir(), t.TempDir()
			for _, path := range []string{filepath.Join(oldDir, oldName), filepath.Join(updatedDir, oldName), filepath.Join(updatedDir, newName)} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", oldDir)
			t.Setenv("SHELL", "")
			root := t.TempDir()
			core := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(ws *workspace.Workspace) {
				if err := ws.SaveWorkspaceSettings(types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`)}); err != nil {
					t.Error(err)
				}
			})
			one := newRemoteJourneyShell(t, core, root)
			app := one.app
			app.Update(tea.WindowSizeMsg{Width: size.W, Height: size.H})
			app.openWorkspaceSettings()
			win := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
			body := app.settingsSurface(win)
			selectSettingsCategory(t, body, "Terminal")
			findForm := func() *ui.Form {
				t.Helper()
				for _, child := range body.Children() {
					if form, ok := child.(*ui.Form); ok && form.ID() == settingsui.WorkspaceSettingsFormID {
						return form
					}
				}
				t.Fatal("Terminal settings form is not mounted")
				return nil
			}
			focus := func(id string) {
				t.Helper()
				body.FocusForm()
				if !findForm().FocusField(id) {
					t.Fatalf("cannot focus %s", id)
				}
				app.paintShellFrame()
			}
			field := func(id string) *ui.FieldState { return findForm().Field(id).FieldState() }
			paintedAuto := func(label string) string {
				text := "Auto (" + label + ")"
				if size.W < 100 && len(text) > 12 {
					// The constrained row clips long labels, not the option.
					return text[:12]
				}
				return text
			}
			draft := body.Model()
			focus("terminal-args")
			for _, r := range "saved-argument" {
				renameConnectedKey(t, one, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			focus("terminal-shell")
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			settingsTextCell(t, win, paintedAuto(oldLabel))
			for _, r := range newID {
				renameConnectedKey(t, one, tea.KeyPressMsg{Code: r, Text: string(r)})
			}

			// A same-host attachment after the installer has updated PATH.
			t.Setenv("PATH", updatedDir)
			two := newRemoteJourneyShell(t, core, root)
			shells := []remoteJourneyShell{one, two}
			updatedPath := filepath.Join(updatedDir, newName)
			remoteJourneyUntil(t, shells, func() bool {
				options := field("terminal-shell").Options
				return len(options) > 0 && options[0].Label == "Auto ("+newLabel+")"
			})
			if body.Model() != draft || !findForm().Field("terminal-shell").SelectOpen() || field("terminal-args").Value.String() != "saved-argument" {
				t.Fatal("a shell observation replaced the mounted draft, open list or unrelated field")
			}
			settingsTextCell(t, win, newID)
			settingsTextCell(t, win, newLabel)
			// The filter is still active as its first match arrives.
			// For Unix, filtering "Bash" matches Auto and Bash; choose Bash.
			if runtime.GOOS != "windows" {
				renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyDown})
			}
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			if got := field("terminal-shell").Value.String(); got != newID {
				t.Fatalf("selected shell = %q, want %q", got, newID)
			}
			focus("terminal-command")
			if command := field("terminal-command"); command.Placeholder != updatedPath || command.Required {
				t.Fatalf("the explicit shell's executable preview disappeared: %+v", command)
			}
			settingsTextCell(t, win, "New terminals start ")
			sx, sy := settingsTextCell(t, win, "[ Save ]")
			x, y := win.Position()
			for _, msg := range []tea.Msg{
				tea.MouseMotionMsg{X: x + sx + 3, Y: y + sy},
				tea.MouseClickMsg{X: x + sx + 3, Y: y + sy, Button: tea.MouseLeft},
				tea.MouseReleaseMsg{X: x + sx + 3, Y: y + sy, Button: tea.MouseLeft},
			} {
				_, cmd := app.Update(msg)
				runAppCommand(t, app, cmd)
			}
			if body.Model() == draft || body.Model().SaveError() != "" || len(body.Model().Edits()) != 0 {
				t.Fatalf("the real Save did not settle the mounted draft: error=%q edits=%+v", body.Model().SaveError(), body.Model().Edits())
			}
			effective := app.ws.LoadAppSettings()
			if effective.Terminal.Shell != newID || !slices.Equal(effective.Terminal.Args, []string{"saved-argument"}) || effective.Editor.TabSize != 8 {
				t.Fatalf("Save lost the explicit selection, arguments or unrelated editor preference: %+v / %+v", effective.Terminal, effective.Editor)
			}
			if field("terminal-command").Placeholder != updatedPath {
				t.Fatal("Save erased the selected executable preview from the retained form")
			}
			settingsTextCell(t, win, "New terminals start ")
			focus("terminal-shell")
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			settingsTextCell(t, win, paintedAuto(newLabel))
			settingsTextCell(t, win, oldLabel)
		})
	}
}
