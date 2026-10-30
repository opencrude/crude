package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/types"
)

func TestInsightThreeRenderedShellsEditAuthorUndoAndSave(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	root := t.TempDir()
	path := filepath.Join(root, "shared.py")
	baseline := "# shared remark\nvalue = 42\n"
	if err := os.WriteFile(path, []byte(baseline), 0600); err != nil {
		t.Fatal(err)
	}
	shells := []remoteJourneyShell{
		newRemoteJourneyShell(t, runtime, root),
		newRemoteJourneyShell(t, runtime, root),
		newRemoteJourneyShell(t, runtime, root),
	}
	editors := make([]*editor.Editor, len(shells))
	for i, shell := range shells {
		editors[i] = liveTestOpen(t, shell.app, path)
		size := [][2]int{{160, 40}, {80, 24}, {120, 40}}[i]
		_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		runAppCommand(t, shell.app, cmd)
	}
	shared := func(want string) bool {
		for i, ed := range editors {
			state, _ := ed.InsightStatus()
			if ed.Value() != want || state != types.InsightCurrent || shells[i].app.liveEditors[ed].sync {
				return false
			}
		}
		return true
	}
	remoteJourneyUntil(t, shells, func() bool { return shared(baseline) })
	for i, shell := range shells {
		shell.app.View()
		if !bufferContainsPlain(shell.app.mainSurf.Buffer(), "Syntax shared") {
			t.Fatalf("shell %d hid source coverage in the rendered header/footer", i)
		}
		// Inspect the actual mounted editor region, not only its cached result.
		x, y := shell.app.editorRect.X+editors[i].GutterWidth()+2, shell.app.editorRect.Y
		r, g, b, _ := shell.app.Theme.Colors.Code.Comment.RGBA()
		wantInk := cell.NewTrueColor(uint8(r>>8), uint8(g>>8), uint8(b>>8))
		if got := shell.app.mainSurf.Buffer().Get(x, y); got.Char != 's' || !got.FG.Equal(wantInk) {
			t.Fatalf("shell %d failed to paint shared native comment: %+v", i, got)
		}
	}
	input := func(index int, messages ...tea.Msg) {
		t.Helper()
		for _, msg := range messages {
			_, cmd := shells[index].app.Update(msg)
			runAppCommand(t, shells[index].app, cmd)
		}
	}
	input(0, tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}, tea.PasteMsg{Content: "# author A\n"})
	remoteJourneyUntil(t, shells, func() bool { return shared("# author A\n" + baseline) })
	input(1, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl}, tea.PasteMsg{Content: "# author B\n"})
	remoteJourneyUntil(t, shells, func() bool { return shared("# author A\n" + baseline + "# author B\n") })
	input(0, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return shared(baseline + "# author B\n") })
	input(1, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool {
		return shared(baseline+"# author B\n") && !editors[0].Modified() && !editors[1].Modified() && !editors[2].Modified()
	})
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != baseline+"# author B\n" {
		t.Fatalf("canonical save lost the other author: %q %v", disk, err)
	}
	for _, shell := range shells {
		shell.app.View()
		if !bufferContainsPlain(shell.app.mainSurf.Buffer(), "Syntax shared") {
			t.Fatal("save cleared or falsely recomputed canonical syntax coverage")
		}
	}
}

func TestInsightPreMountAckAndStaleWorkspaceFence(t *testing.T) {
	app := newFullTestApp(t)
	acknowledged := 0
	valid := true
	message := shellport.DocumentInsightMsg{
		Owner: app.ws,
		View:  types.DocumentInsight{DocumentID: "not-mounted-yet", Generation: "core", Version: 1, Revision: 1, State: types.InsightPending},
		Valid: func() bool { return valid },
		Done: func(err error) {
			if err != nil {
				t.Error(err)
			}
			acknowledged++
		},
	}
	_, cmd := app.Update(message)
	runAppCommand(t, app, cmd)
	if acknowledged != 1 || len(app.pendingInsights) != 1 {
		t.Fatal("pre-mount insight stalled the independent push stream")
	}
	valid = false
	runAppCommand(t, app, app.drainLiveEditorCommands())
	if len(app.pendingInsights) != 0 {
		t.Fatal("expired open token retained a deferred insight")
	}
	message.Owner = nil
	message.Done = func(err error) {
		if err == nil {
			t.Error("foreign workspace insight was acknowledged as installed")
		}
	}
	app.Update(message)
	if len(app.pendingInsights) != 0 {
		t.Fatal("foreign workspace cached an analysis result")
	}
}

func TestInsightCoverageDoesNotMeanNoCompilerErrors(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	root := t.TempDir()
	path := filepath.Join(root, "unknown.cr")
	if err := os.WriteFile(path, []byte("puts \"unknown language\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		state, _ := ed.InsightStatus()
		return state == types.InsightUnsupported
	})
	shell.app.View()
	_, metrics, _ := shell.app.activeHeaderContext()
	if !strings.Contains(metrics, "Syntax lexical") || strings.Contains(metrics, "0 errors") {
		t.Fatalf("unsupported native syntax disguised as semantic success: %q", metrics)
	}
}
