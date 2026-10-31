package codeactions

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func actionFixture() workspace.CodeActionSet {
	return workspace.CodeActionSet{
		ID: "actions-original", Digest: strings.Repeat("a", 64), Start: 4, End: 7,
		DiagnosticsIncluded: 1, DiagnosticsDetail: "original diagnostics from exact versioned publication",
		Boundary: types.InsightBoundary{
			Source:  types.InsightSource{Kind: "canonical", DocumentID: "original-document", Revision: 19},
			Context: types.InsightContext{Generation: 23, UnknownInputs: []string{"original-unknown-build-context"}},
		},
		Items: []workspace.CodeActionSummary{
			{Index: 0, Title: "Fix exact literal", Kind: "quickfix", CanReview: true, HasPreferred: true, Preferred: true},
			{Index: 1, Title: "Do not execute", Kind: "unknown.future", UnsupportedReason: "Actions containing commands are not supported."},
			{Index: 2, Title: "Disabled action", DisabledReason: "provider reason", UnsupportedReason: "Disabled actions cannot be applied."},
		},
	}
}

func actionHost(s *Screen, w, h int) *ui.Host {
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(w, h)
	host.Layout()
	host.Focus(s.DefaultFocus())
	return host
}

func actionPaint(host *ui.Host, w, h int) string {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := range h {
		for x := range w {
			c := buf.Get(x, y)
			if c.Char == 0 {
				text.WriteByte(' ')
			} else {
				text.WriteRune(c.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func TestCodeActionsNativeListDetailDefaultCloseAndExplicitPreview(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			set := actionFixture()
			s.SetSet(set)
			// Presentation storage is detached from all caller-owned containers.
			set.Items[0].Title = "forged"
			set.Boundary.Context.UnknownInputs[0] = "forged"
			host := actionHost(s, size[0], size[1])
			if paint := actionPaint(host, size[0], size[1]); !strings.Contains(paint, "Preferred") ||
				!strings.Contains(paint, "Do not execute") || !strings.Contains(paint, "Disabled") {
				t.Fatalf("native list omitted eligible/unsupported/disabled rows:\n%s", paint)
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			host.Layout()
			if s.InList() || !strings.Contains(s.text.Value(), "Fix exact literal") ||
				!strings.Contains(s.text.Value(), "original-unknown-build-context") {
				t.Fatal("row activation failed to expose complete original evidence")
			}
			if got := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); got.Cmd == nil {
				t.Fatal("safe default Close did not emit an owned command")
			} else if close, ok := got.Cmd().(CloseMsg); !ok || close.Target != s {
				t.Fatal("detail Enter defaulted to preview or lost its owner")
			}
			// Only an explicit action selection requests a preview.
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			got := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if got.Cmd == nil {
				t.Fatal("native action scope did not reach explicit Preview")
			}
			prepared, ok := got.Cmd().(PrepareMsg)
			if !ok || prepared.Target != s || prepared.Index != 0 {
				t.Fatal("preview lost its original set index or owner", prepared)
			}
			if again := s.HandleAction(ui.EventContext{}, ui.Action{ID: ActionPrepare}); again.Cmd != nil {
				t.Fatal("busy action was issued twice")
			}
			s.SetBusy(false)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
			host.Layout()
			if !s.InList() || host.Focused() != ListID {
				t.Fatal("Escape did not return from detail to the same list")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			host.Layout()
			if _, exists := s.actions.ButtonBounds(ActionPrepare); exists {
				t.Fatal("unsupported row gained a Preview button")
			}
			if !strings.Contains(s.text.Value(), "Actions containing commands are not supported.") {
				t.Fatal("unsupported reason was not readable")
			}
		})
	}
}

func TestCodeActionsNativeEmptyErrorLongEvidenceCopyAndBounds(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {9, 5}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			set := actionFixture()
			set.Items = nil
			s.SetSet(set)
			host := actionHost(s, size[0], size[1])
			if !strings.Contains(s.text.Value(), "not proof") || !strings.Contains(s.text.Value(), "Source byte selection: [4,7)") {
				t.Fatal("empty evidence lost scope or implied project cleanliness")
			}
			s.SetError("literal transport failure")
			s.SetSet(actionFixture())
			host.MarkLayout() // External data delivery, as the WindowFrame owner does.
			host.Layout()
			if !host.Focus(ListID) {
				t.Fatal("delivered action list was not registered")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			host.Layout()
			host.Focus(TextID)
			for _, child := range s.Children() {
				b := child.Bounds()
				if b.X < 0 || b.Y < 0 || b.W < 0 || b.H < 0 || b.X+b.W > size[0] || b.Y+b.H > size[1] {
					t.Fatalf("child escaped parent-local bounds: %+v", b)
				}
			}
			if size[0] < 80 {
				return
			}
			var copied string
			s.text.SetClipboardCopy(func(text string) tea.Cmd {
				return func() tea.Msg { copied = text; return nil }
			})
			host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			result := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if result.Cmd == nil || copied != "" {
				t.Fatal("native copy was missing or did I/O synchronously")
			}
			result.Cmd()
			if copied != s.text.Value() || !strings.Contains(copied, set.Digest) {
				t.Fatal("copy omitted complete evidence or original digest")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}) // native selection first
			if s.InList() {
				t.Fatal("Escape skipped the read-only text selection lifecycle")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if host.Focused() != ActionsID {
				t.Fatal("reader Tab failed to reach the safe action scope")
			}
			for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultDark()} {
				host.SetTheme(th)
				_ = actionPaint(host, size[0], size[1])
			}
		})
	}
}
