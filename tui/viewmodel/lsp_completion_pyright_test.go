package viewmodel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/types"
	"crude/workspace"
)

func TestConnectedAutomaticCompletionWithRealPyright(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an installed language server")
	}
	server, err := exec.LookPath("pyright-langserver")
	if err != nil {
		t.Skip("pyright-langserver is not installed")
	}
	const declarations = "class Device:\n    status: int = 1\n    def start(self) -> None:\n        pass\n\ndevice = Device()\nprivate_value = 1\ndef print_value() -> int:\n    return 1\n\n"
	for _, tc := range []struct {
		initial, typed, wanted string
		suggestions            []string
	}{
		{"pr", "i", "private_value", []string{"private_value", "print_value"}},
		{"device", ".", "device.status", []string{"start", "status"}},
	} {
		t.Run(tc.initial, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sample.py")
			source := declarations + tc.initial
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "pyrightconfig.json"), []byte(`{"include":["."]}`), 0600); err != nil {
				t.Fatal(err)
			}
			var owned *workspace.Workspace
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				owned = w
				settings := w.AppDefaults()
				settings.LSP = types.DefaultLSPSettings()
				settings.LSP.AutoDetect = false
				settings.LSP.Languages = map[string]types.LSPLanguageSettings{"python": {
					Enabled: true, Command: server, Args: []string{"--stdio"}, RootMarkers: []string{"pyrightconfig.json"},
				}}
				w.SetAppDefaults(settings)
				grantSemanticFixture(t, w)
			})
			shell := newRemoteJourneyShell(t, runtime, root)
			ed := liveTestOpen(t, shell.app, path)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if _, _, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 7}); err != nil {
				t.Fatal(err)
			}
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				_, current := ed.CurrentInsightSource()
				return current && shell.app.ws.LSPMgr().ServerReady()
			})
			ed.SetCursorPosition(strings.Count(declarations, "\n"), len(tc.initial))
			shell.app.setFocusEditor()
			renameConnectedKey(t, shell, tea.KeyPressMsg{Code: []rune(tc.typed)[0], Text: tc.typed})
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				return shell.app.completion != nil && shell.app.completion.screen != nil
			})
			completionRequireText(t, shell.app, tc.suggestions, nil)
			wantLabel := strings.TrimPrefix(tc.wanted, "device.")
			// Navigate the real chooser, not a synthetic acceptance call.
			for n := 0; n < len(shell.app.completion.set.Entries); n++ {
				s := shell.app.completion
				selected := s.screen.SelectedID()
				found := false
				for _, row := range s.set.Entries {
					if row.ID == selected && row.Insertion.Label == wantLabel {
						found = true
					}
				}
				if found {
					break
				}
				renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyDown})
			}
			renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyTab})
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				return ed.Value() == declarations+tc.wanted && ed.DocumentState().PendingEdits == 0
			})
			if data, err := os.ReadFile(path); err != nil || string(data) != source {
				t.Fatalf("acceptance must not save the file: %v %q", err, data)
			}
		})
	}
}
