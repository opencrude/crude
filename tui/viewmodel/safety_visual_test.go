package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/localhistory"
	"crude/shellport"
	"crude/tui/ui"
)

// Run sequentially through scripts/tuishot.sh: F2 target preview, F3 armed
// confirmation (no mutation), F4 applied/safety snapshot, F5 inverse preview,
// F6 armed inverse (still no mutation), F7 undone, F9 verify and exit.
func TestHistorySafetyVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_HISTORY_SAFETY") != "1" {
		t.Skip("dedicated sequential native restore capture")
	}
	runHistorySafetyVisual(t, false, 0, 0)
}

func TestHistorySafetyVisualHarnessHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runHistorySafetyVisual(t, true, size[0], size[1])
		})
	}
}

func runHistorySafetyVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	path := filepath.Join(root, "safety.txt")
	if err := os.WriteFile(path, []byte("BEFORE RESTORE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, r, root)
	target, _, err := shell.app.ws.LocalHistory().SnapshotIfChanged(t.Context(), localhistory.SnapshotOptions{Kind: "manual", Label: "Reviewed restore target"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("LATER EXACT BYTES\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &historySafetyVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shell.app, runtime: r, root: root, path: path, ctx: t.Context(), completed: map[int]bool{}}, settled: make(chan struct{}, 1)}, target: target.ID}
	if !headless {
		m.resultPath = os.Getenv("CRUDE_HISTORY_SAFETY_RESULT")
	}
	defer func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
	}()
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	p := tea.NewProgram(m, opts...)
	m.program = p
	m.SetProgram(p)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg := <-shell.fifo:
				p.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		go func() {
			for key := tea.KeyF2; key <= tea.KeyF7; key++ {
				p.Send(tea.KeyPressMsg{Code: key})
				select {
				case <-m.settled:
				case <-ctx.Done():
					return
				}
			}
			p.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	}
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		_ = m.evidence("HISTORY_SAFETY_FAILED " + m.failure.Error())
		t.Fatal(m.failure)
	}
	if m.phase != 6 {
		t.Fatalf("completed %d/6 safety phases", m.phase)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "LATER EXACT BYTES\n" {
		t.Fatalf("inverse lost original bytes: %q %v", data, err)
	}
	if m.rootHost.HasPointerCapture() {
		t.Fatal("history left pointer capture armed")
	}
	if err := m.evidence("HISTORY_SAFETY_SUCCESS preview/confirm/restore/safety/inverse/undo; exact disk bytes; local framed core"); err != nil {
		t.Fatal(err)
	}
}

type historySafetyDone struct {
	phase int
	err   error
}
type historySafetyVisualModel struct {
	*featureVisualModel
	target string
	phase  int
}

func (m *historySafetyVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case featureVisualAction:
		return m, v.run(m.App)
	case historySafetyDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("phase %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		m.phase = v.phase
		select {
		case m.settled <- struct{}{}:
		default: // Native capture has no headless phase waiter.
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code >= tea.KeyF2 && v.Code <= tea.KeyF7 {
			phase := int(v.Code-tea.KeyF2) + 1
			if m.busy || phase != m.phase+1 {
				return m, nil
			}
			m.busy = true
			return m, func() tea.Msg { return historySafetyDone{phase, m.journeySafety(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *historySafetyVisualModel) journeySafety(phase int) error {
	if err := m.connect(); err != nil {
		return err
	}
	want, label := "LATER EXACT BYTES\n", ""
	switch phase {
	case 1:
		m.action(func(a *App) tea.Cmd { return a.openLocalHistoryForSnapshot(m.target) })
		label = "immutable target preview"
	case 2, 3, 4, 5, 6:
		m.action(func(a *App) tea.Cmd { _, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); return cmd })
		label = map[int]string{2: "armed restore; no mutation", 3: "restored with durable safety snapshot", 4: "safety inverse preview", 5: "armed inverse; no mutation", 6: "inverse restored original later bytes"}[phase]
	}
	if phase >= 3 && phase <= 5 {
		want = "BEFORE RESTORE\n"
	}
	if err := m.await(func(v *controlVisualModel) error {
		if v.historyScreen == nil || !v.overlayHost.IsOpen(ui.OverlayLocalHistory) {
			return fmt.Errorf("usable history surface disappeared")
		}
		switch phase {
		case 1, 4:
			return featureVisualPaint(v, "Restore preview", "safety.txt")
		case 2, 5:
			return featureVisualPaint(v, "Ready to restore 1 path(s)", "Press Enter again")
		case 3, 6:
			return featureVisualPaint(v, "Undo this restore", "Safety snapshot")
		}
		return nil
	}); err != nil {
		return err
	}
	data, err := os.ReadFile(m.path)
	if err != nil || string(data) != want {
		return fmt.Errorf("phase %d disk=%q want=%q err=%v", phase, data, want, err)
	}
	if phase == 3 {
		records, err := m.ws.LocalHistory().Timeline(100)
		if err != nil {
			return err
		}
		found := false
		for _, record := range records {
			if record.Kind == "safety_before_restore" && strings.HasPrefix(record.Label, "Safety snapshot before restore ") {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("restore has no durable safety snapshot in real timeline")
		}
	}
	return m.evidence(fmt.Sprintf("F%d VERIFIED %s", phase+1, label))
}
