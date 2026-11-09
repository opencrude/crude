package settings

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestCompletionSettingsNativeInputSparseSaveAndReset(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			th := theme.DefaultDark()
			parent := types.AppSettings{LSP: types.DefaultLSPSettings()}
			stored := types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":8}`)}
			m := NewScoped(th, types.MergeAppWorkspace(parent, stored), nil, types.SettingsScopeWorkspace, parent, stored)
			m.SelectCategory(catLSP)
			root := NewSurface(th, m)
			host := ui.NewHost(th, root)
			root.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
			host.Resize(size.W, size.H)
			host.Layout()
			if len(m.Edits()) != 0 {
				t.Fatal("opening suggestions settings made inherited values explicit")
			}
			focus := func(id string) {
				t.Helper()
				root.FocusForm()
				if !root.form.FocusField(id) {
					t.Fatalf("cannot focus %s", id)
				}
				host.MarkLayout()
				host.Layout()
			}
			focus("completion-delay")
			host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			host.Handle(tea.KeyPressMsg{Code: '0', Text: "0"})
			focus("completion-accept")
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			focus("auto-complete")
			host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "})
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if paint := bufferPlain(buf); !strings.Contains(paint, "Suggest while typing") || !strings.Contains(paint, "Off") {
				t.Fatalf("changed preference is not visible:\n%s", paint)
			}
			got := m.AppSettings().LSP
			if got.AutoComplete || got.CompletionDelay() != 0 || got.CompletionAcceptance() != "tab" || !got.Completions {
				t.Fatalf("native input did not reach behavior choices: %+v", got)
			}
			edits := m.Edits()
			if len(edits) != 3 {
				t.Fatalf("suggestion edits changed unrelated leaves: %+v", edits)
			}
			if err := types.ApplySettingsEdits(&stored, edits); err != nil {
				t.Fatal(err)
			}
			reopened := types.MergeAppWorkspace(parent, stored)
			if reopened.LSP.AutoComplete || reopened.LSP.CompletionDelay() != 0 || reopened.LSP.CompletionAcceptance() != "tab" || reopened.Editor.TabSize != 8 {
				t.Fatal("saved choices did not survive reload")
			}
			next := NewScoped(th, reopened, nil, types.SettingsScopeWorkspace, parent, stored)
			next.SelectCategory(catLSP)
			reset, ok := next.PageResetEdits()
			if !ok {
				t.Fatal("Code Intelligence reset unavailable")
			}
			if err := types.ApplySettingsEdits(&stored, reset); err != nil {
				t.Fatal(err)
			}
			delay := 500
			parent.LSP.CompletionDelayMS = &delay
			inherited := types.MergeAppWorkspace(parent, stored)
			if !inherited.LSP.AutoComplete || inherited.LSP.CompletionDelay() != 500 || inherited.LSP.CompletionAcceptance() != "tab-enter" || inherited.Editor.TabSize != 8 {
				t.Fatal("reset did not inherit the latest parent or changed unrelated settings")
			}
		})
	}
}
