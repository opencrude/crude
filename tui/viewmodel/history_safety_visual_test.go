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

	"crude/shellport"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/workspace"
)

// The ordinary reviewed mutation creates the fixture through real connected
// editor keys. All scored safety navigation and consent then comes from native
// input. F3 only observes each boundary; it never authorizes a protection change.
func TestReviewSafetyVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_REVIEW_SAFETY") != "1" {
		t.Skip("dedicated sequential native capture only")
	}
	runReviewSafetyVisual(t, false, 0, 0)
}

func TestReviewSafetyVisualHeadless(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			runReviewSafetyVisual(t, true, size.W, size.H)
		})
	}
}

type reviewSafetyVisualModel struct {
	*featureVisualModel
	phase    int
	original workspace.ReviewedEditResult
	path     string
	peerPath string
}

type reviewSafetyVisualDone struct {
	phase int
	err   error
}

func runReviewSafetyVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	// Do not inherit or operate the developer's live core/profile.
	t.Setenv("CRUDE_HOME", t.TempDir())
	f, _, original := committedHistorySafetyFixture(t, 160, 40)
	one := f.shells[0]
	m := &reviewSafetyVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{App: one.app, runtime: f.runtime, root: one.app.ws.RootPath(), ctx: t.Context()},
			resultPath:         os.Getenv("CRUDE_REVIEW_SAFETY_VISUAL_RESULT"), settled: make(chan struct{}, 1),
		},
		original: original, path: f.engine.main, peerPath: f.engine.peer,
	}
	options := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		options = append(options, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, options...)
	m.program = program
	m.SetProgram(program)
	pumpCtx, cancelPump := context.WithCancel(t.Context())
	m.ctx = pumpCtx
	pumpDone := make(chan struct{})
	defer func() { cancelPump(); <-pumpDone }()
	go func() {
		defer close(pumpDone)
		for {
			select {
			case message := <-one.fifo:
				program.Send(message)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	if headless {
		driverDone := make(chan struct{})
		defer func() { cancelPump(); <-driverDone }()
		go func() {
			defer close(driverDone)
			key := func(code rune) { program.Send(tea.KeyPressMsg{Code: code}) }
			observe := func() bool {
				key(tea.KeyF3)
				select {
				case <-m.settled:
					return true
				case <-pumpCtx.Done():
					return false
				}
			}
			if !observe() {
				return
			}
			program.Send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
			for _, r := range "Local History" {
				program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			key(tea.KeyEnter)
			// Enter the timeline's first native command row.
			if !m.awaitHistoryTimeline() {
				return
			}
			key(tea.KeyEnter)
			if !observe() {
				return
			}
			key(tea.KeyEnter)
			if !observe() {
				return
			}
			if !m.clickSafety(historyscreen.ActionSafetyRelease) {
				return
			}
			if !observe() {
				return
			}
			key(tea.KeyEnter) // Cancel
			if !observe() {
				return
			}
			if !m.clickSafety(historyscreen.ActionSafetyRelease) {
				return
			}
			key(tea.KeyLeft)
			key(tea.KeyEnter) // explicit release
			if !observe() {
				return
			}
			if !m.clickSafety(historyscreen.ActionSafetyReprotect) {
				return
			}
			key(tea.KeyLeft)
			key(tea.KeyEnter) // explicit re-protection
			if observe() {
				key(tea.KeyEsc) // detail to reference page
				key(tea.KeyEsc) // reference page to existing timeline
				key(tea.KeyEsc) // close Local History
				key(tea.KeyEsc) // leave editor focus before native quit
				program.Send(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
			}
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		_ = m.evidence("HISTORY_SAFETY_VISUAL_FAILED " + m.failure.Error())
		t.Fatal(m.failure)
	}
	if m.phase != 7 {
		t.Fatal("review-safety native phases incomplete", m.phase)
	}
	if err := m.evidence("HISTORY_SAFETY_VISUAL_SUCCESS F3x7; exact original outcome unchanged; native Cancel/release/reprotect; no project bytes changed; local framed core and deterministic provider fixture only"); err != nil {
		t.Fatal(err)
	}
}

func (m *reviewSafetyVisualModel) awaitHistoryTimeline() bool {
	err := m.await(func(v *controlVisualModel) error {
		if v.historyScreen == nil {
			return fmt.Errorf("Local History timeline not yet mounted")
		}
		if strings.Contains(ansi.Strip(v.View().RenderContent()), "Loading Local History") {
			return fmt.Errorf("Local History timeline has not settled")
		}
		return featureVisualPaint(v, "Review safety references")
	})
	if err != nil {
		m.program.Send(reviewSafetyVisualDone{phase: -1, err: err})
	}
	return err == nil
}

func (m *reviewSafetyVisualModel) clickSafety(action string) bool {
	var x, y int
	err := m.await(func(v *controlVisualModel) error {
		v.View()
		fw := v.overlayHost.Window(ui.OverlayLocalHistory)
		if fw == nil {
			return fmt.Errorf("Local History is not visible")
		}
		frame, ok := fw.Content.(*ui.WindowFrameWidget)
		if !ok {
			return fmt.Errorf("Local History is not a native frame")
		}
		id := ui.NewID(ui.NewID(historyscreen.SafetyScreenID, "actions"), action)
		node, err := frame.Host().InspectControl(id, v.ws.(shellport.ShellControl).ControlIdentity())
		if err != nil || !node.State.Enabled || node.Bounds == nil || node.Bounds.Width == 0 || node.Bounds.Height == 0 {
			return fmt.Errorf("phase %d native safety action %s has no rendered bounds (reader=%t):\n%s",
				m.phase, action, v.historySafety != nil, ansi.Strip(v.View().RenderContent()))
		}
		left, top := fw.Position()
		x, y = left+node.Bounds.X+1, top+node.Bounds.Y
		return nil
	})
	if err != nil {
		m.program.Send(reviewSafetyVisualDone{phase: -1, err: err})
		return false
	}
	m.program.Send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.program.Send(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return true
}

func (m *reviewSafetyVisualModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case controlVisualCheck:
		msg.reply <- msg.check(m.controlVisualModel)
		return m, nil
	case reviewSafetyVisualDone:
		m.busy = false
		if msg.err != nil {
			m.failure = fmt.Errorf("safety phase %d: %w", msg.phase, msg.err)
			return m, tea.Quit
		}
		m.phase++
		select {
		case m.settled <- struct{}{}:
		default:
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.Code == tea.KeyF3 && !m.busy && m.phase < 7 {
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return reviewSafetyVisualDone{phase, m.observeSafety(phase)} }
		}
	}
	_, command := m.App.Update(message)
	return m, command
}

func (m *reviewSafetyVisualModel) observeSafety(phase int) error {
	err := m.await(func(v *controlVisualModel) error {
		if phase == 0 {
			return featureVisualPaint(v, "var new = 1")
		}
		view := v.historySafety
		if !v.historySafetyVisible(view) {
			return fmt.Errorf("review safety reader not mounted")
		}
		if phase == 1 {
			if len(view.page.Records) != 1 || view.page.Records[0].Reference != m.original.SafetyReference || view.page.RetainedCount != 1 {
				return fmt.Errorf("exact retained safety page is not ready")
			}
			return featureVisualPaint(v, "Review safety", "retained")
		}
		detail, ready := view.screen.SelectedDetail()
		if !ready || detail.Result != m.original || detail.Reference != m.original.SafetyReference {
			return fmt.Errorf("exact original operation and detail not ready")
		}
		switch phase {
		case 2, 4:
			if !detail.Retained || !detail.CanRelease || view.confirm != nil || v.historySafetyOperation != nil || detail.Revision != 0 {
				return fmt.Errorf("initial detail or cancelled consent lost protection")
			}
			return featureVisualPaint(v, "Review safety reference", "Release protection", "Back")
		case 3:
			fw := v.overlayHost.Window(historySafetyConfirmOverlay)
			if fw == nil || view.confirm == nil {
				return fmt.Errorf("exact release confirmation is not mounted")
			}
			widget, ok := fw.Content.(*ui.ComponentWidget)
			if !ok || ui.ConfirmButtonIndexFromID(widget.Host().Focused()) != 1 || !detail.Retained {
				return fmt.Errorf("native confirmation did not default to Cancel")
			}
			return featureVisualPaint(v, "Release safety protection", "Cancel")
		case 5:
			if detail.Retained || !detail.CanReprotect || detail.Revision != 1 || detail.LastEvent == nil || detail.LastEvent.Action != "release" || v.historySafetyOperation != nil {
				return fmt.Errorf("explicit release and its immutable event not settled")
			}
			return featureVisualPaint(v, "Keep protected again", "Restore preview")
		case 6:
			if !detail.Retained || !detail.CanRelease || detail.Revision != 2 || detail.LastEvent == nil || detail.LastEvent.Action != "reprotect" || v.historySafetyOperation != nil {
				return fmt.Errorf("exact re-protection and audit not settled")
			}
			return featureVisualPaint(v, "Release protection", "Restore preview")
		}
		return fmt.Errorf("unknown safety phase")
	})
	if err != nil {
		return err
	}
	for path, expected := range map[string]string{m.path: "package journey\n\nvar new = 1\n", m.peerPath: "package journey\n\nvar use = new\n"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != expected {
			return fmt.Errorf("protection administration changed %s: %v", filepath.Base(path), readErr)
		}
	}
	port := m.ws.(shellport.ReviewedEdits)
	original, err := port.InspectReviewedEdit(m.ctx, m.original.ID, m.original.Digest)
	if err != nil || original != m.original {
		return fmt.Errorf("original operation receipt was rewritten: %v", err)
	}
	return m.evidence(fmt.Sprintf("F3 VERIFIED safety phase %d; exact original outcome and postimage bytes preserved", phase))
}
