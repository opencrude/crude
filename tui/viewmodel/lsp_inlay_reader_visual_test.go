package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
)

// This journey supplies deterministic protocol evidence for a long tooltip.
// Keys and pointer input operate the real connected editor and native reader;
// it is not evidence of an installed server's tooltip/resolve support.
func TestInlayReaderLongVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_INLAY_READER") != "1" {
		t.Skip("dedicated inlay reader capture")
	}
	runInlayReaderLongVisual(t, false, 0, 0)
}
func TestInlayReaderLongVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runInlayReaderLongVisual(t, true, size[0], size[1]) })
	}
}

type inlayReaderLongModel struct {
	*featureVisualModel
	fixture                          inlayConnectedFixture
	phase                            int
	scrollX, scrollTop, scrollBottom int
}
type inlayReaderLongDone struct {
	phase int
	err   error
}

func runInlayReaderLongVisual(t *testing.T, headless bool, width, height int) {
	var tooltip strings.Builder
	for i := 1; i <= 35; i++ {
		fmt.Fprintf(&tooltip, "Useful hint line %02d.\n", i)
	}
	tooltip.WriteString("Final useful hint.")
	raw, err := json.Marshal(tooltip.String())
	if err != nil {
		t.Fatal(err)
	}
	item := strings.TrimSuffix(inlayConnectedOriginal, "}") + `,"tooltip":` + string(raw) + `}`
	f := newInlayConnectedFixture(t, true, "["+item+"]")
	ctx, cancel := context.WithCancel(t.Context())
	m := &inlayReaderLongModel{fixture: f, featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: f.shells[0].app, ctx: ctx, path: f.engine.main},
		settled:            make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_INLAY_READER_RESULT"),
	}}
	t.Cleanup(func() {
		if err := m.evidence(fmt.Sprintf("INLAY_READER_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
			t.Error(err)
		}
	})
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	workers.Go(func() {
		for {
			select {
			case msg := <-f.shells[0].fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	if headless {
		workers.Go(func() {
			key := func(code rune, mod tea.KeyMod) { program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
			observe := func() bool {
				key(tea.KeyF4, 0)
				select {
				case <-m.settled:
					return true
				case <-ctx.Done():
					return false
				}
			}
			if !observe() {
				return
			}
			key('j', tea.ModAlt)
			if !observe() {
				return
			}
			key(tea.KeyEnter, 0)
			if !observe() {
				return
			}
			key(tea.KeyTab, 0)
			key(tea.KeyEnd, tea.ModCtrl)
			if !observe() {
				return
			}
			key(tea.KeyHome, tea.ModCtrl)
			program.Send(tea.MouseClickMsg{X: m.scrollX, Y: m.scrollTop, Button: tea.MouseLeft})
			program.Send(tea.MouseMotionMsg{X: m.scrollX, Y: m.scrollBottom, Button: tea.MouseLeft})
			program.Send(tea.MouseReleaseMsg{X: m.scrollX, Y: m.scrollBottom, Button: tea.MouseLeft})
			if !observe() {
				return
			}
			key(tea.KeyEsc, 0)
			if !observe() {
				return
			}
			key(tea.KeyEsc, 0)
			if observe() {
				key(tea.KeyF9, 0)
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 7 {
		t.Fatal("native hint reader journey incomplete", m.phase)
	}
	f.unchanged(t)
}

func (m *inlayReaderLongModel) Init() tea.Cmd { return tickCmd() }
func (m *inlayReaderLongModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case inlayReaderLongDone:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			if err := m.evidence(fmt.Sprintf("INLAY_READER_READY phase=%d", phase)); err != nil {
				return inlayReaderLongDone{-1, err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && v.Mod == 0 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 && v.Mod == 0 {
			if m.busy || m.phase >= 7 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return inlayReaderLongDone{phase, m.observeReader(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *inlayReaderLongModel) observeReader(phase int) error {
	var frame, hit string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || ed.Value() != inlayConnectedSource || ed.Modified() || v.Input.Value() != "private first inlay draft" {
			return fmt.Errorf("reader changed source or draft")
		}
		frame = ansi.Strip(v.View().RenderContent())
		s := v.inlayInspector
		switch phase {
		case 0, 6:
			if s != nil || v.focusTarget() != FocusEditor || !ed.HasInlayProjection() {
				return fmt.Errorf("editor or hints are not restored")
			}
		case 1, 5:
			if s == nil || !s.shown || !s.screen.InList() || !strings.Contains(frame, ": int") || !strings.Contains(frame, "Show details") {
				return fmt.Errorf("hint list is not mounted")
			}
		case 2, 3, 4:
			if s == nil || !s.screen.InDetail() || s.reading {
				return fmt.Errorf("selected hint detail has not settled")
			}
			text := inlayVisualReader(s)
			for _, bad := range []string{"Original raw JSON", "zero-based UTF-16", "9007199254740993", "NEVER INSERT"} {
				if strings.Contains(text, bad) {
					return fmt.Errorf("reader exposed %q", bad)
				}
			}
			if !strings.Contains(text, "Useful hint line 01.") || !strings.Contains(text, "Final useful hint.") {
				return fmt.Errorf("complete tooltip is missing")
			}
			fw := v.overlayHost.Window(ui.OverlayInlayHints)
			if fw == nil {
				return fmt.Errorf("reader window is missing")
			}
			widget, ok := fw.Content.(*ui.WindowFrameWidget)
			if !ok {
				return fmt.Errorf("reader window has wrong owner")
			}
			bar, ok := widget.Host().ScreenRect("inlay/detail-scrollbar")
			if !ok || bar.W != 1 || bar.H < 2 {
				return fmt.Errorf("long tooltip has no native scrollbar: %+v", bar)
			}
			if phase == 2 {
				x, y := fw.Position()
				m.scrollX, m.scrollTop, m.scrollBottom = x+bar.X, y+bar.Y, y+bar.Y+bar.H-1
				hit = fmt.Sprintf("INLAY_READER_SCROLL size=%dx%d from=%d,%d to=%d,%d", v.width, v.height, m.scrollX, m.scrollTop, m.scrollX, m.scrollBottom)
				if !strings.Contains(frame, "Useful hint line 01.") {
					return fmt.Errorf("first tooltip line is not painted")
				}
			} else if !strings.Contains(frame, "Final useful hint.") {
				return fmt.Errorf("last tooltip line is not painted")
			}
			if m.rootHost.HasPointerCapture() || widget.Host().HasPointerCapture() {
				return fmt.Errorf("reader retained pointer capture")
			}
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("INLAY_READER_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("INLAY_READER_VERIFIED phase=%d\n%s\n%s", phase, hit, frame))
}
