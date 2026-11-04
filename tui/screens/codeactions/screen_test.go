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

func TestChoosingCodeActionRequestsReviewDirectly(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			set := actionFixture()
			s.SetSet(set)
			// Presentation storage is detached from all caller-owned containers.
			set.Items[0].Title = "forged"
			set.Boundary.Context.UnknownInputs[0] = "forged"
			host := actionHost(s, size[0], size[1])
			if paint := actionPaint(host, size[0], size[1]); !strings.Contains(paint, "Recommended") ||
				!strings.Contains(paint, "Do not execute") || !strings.Contains(paint, "Not available") ||
				strings.Contains(paint, "forged") || strings.Contains(paint, "literal, complete") {
				t.Fatalf("native list omitted eligible/unsupported/disabled rows:\n%s", paint)
			}
			got := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if got.Cmd == nil {
				t.Fatal("one Enter did not request the selected action's review")
			}
			prepared, ok := got.Cmd().(PrepareMsg)
			if !ok || prepared.Target != s || prepared.Index != 0 {
				t.Fatal("preview lost its original set index or owner", prepared)
			}
			if !s.InList() {
				t.Fatal("choosing an action inserted a second details page before its review")
			}
			if again := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); again.Cmd != nil {
				t.Fatal("busy action was issued twice")
			}
			s.SetBusy(false)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			host.Layout()
			if _, exists := s.actions.ButtonBounds(ActionPrepare); exists {
				t.Fatal("unsupported row gained a Preview button")
			}
			if !strings.Contains(s.text.Value(), "Actions containing commands are not supported.") {
				t.Fatal("unsupported reason was not readable")
			}
			if strings.Contains(s.text.Value(), "original-unknown-build-context") {
				t.Fatal("the unavailable explanation contains a protocol dump")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
			if !s.InList() || host.Focused() != ListID {
				t.Fatal("Escape did not return to the action list")
			}
			if close := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}); close.Cmd == nil {
				t.Fatal("Escape did not close the list")
			} else if msg, ok := close.Cmd().(CloseMsg); !ok || msg.Target != s {
				t.Fatal("Close did not retain its owner")
			}
		})
	}
}

func TestCodeActionsEmptyErrorCopyAndBounds(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {9, 5}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			s := New(nil)
			set := actionFixture()
			set.Items = nil
			s.SetSet(set)
			host := actionHost(s, size[0], size[1])
			if s.text.Value() != "No code actions available here." {
				t.Fatal("empty list did not explain the state in plain words")
			}
			s.SetError("literal transport failure")
			s.SetSet(actionFixture())
			host.MarkLayout() // External data delivery, as the WindowFrame owner does.
			host.Layout()
			if !host.Focus(ListID) {
				t.Fatal("delivered action list was not registered")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
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
			if copied != s.text.Value() || !strings.Contains(copied, "Actions containing commands are not supported.") {
				t.Fatal("copy omitted the unavailable action's explanation")
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

func TestCodeActionOrderKeepsOriginalIndices(t *testing.T) {
	set := actionFixture()
	set.Items = []workspace.CodeActionSummary{
		{Index: 0, Title: "Command action", UnsupportedReason: "This action needs a server command."},
		{Index: 1, Title: "Other fix", CanReview: true},
		{Index: 2, Title: "Recommended fix", CanReview: true, HasPreferred: true, Preferred: true},
	}
	s := New(nil)
	s.SetSet(set)
	host := actionHost(s, 80, 24)
	paint := actionPaint(host, 80, 24)
	if strings.Index(paint, "Recommended fix") > strings.Index(paint, "Other fix") ||
		strings.Index(paint, "Other fix") > strings.Index(paint, "Command action") {
		t.Fatalf("available actions were not presented before unavailable actions:\n%s", paint)
	}
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil {
		t.Fatal("the recommended fix did not request its review")
	}
	if msg, ok := result.Cmd().(PrepareMsg); !ok || msg.Index != 2 || msg.Target != s {
		t.Fatal("presentation order replaced the original action identity", msg)
	}
}

func TestCodeActionsAllUnavailableHasOneExplanation(t *testing.T) {
	set := actionFixture()
	set.Items = set.Items[1:]
	for i := range set.Items {
		set.Items[i].Index = i
	}
	s := New(nil)
	s.SetSet(set)
	host := actionHost(s, 80, 24)
	paint := actionPaint(host, 80, 24)
	if strings.Count(paint, "Crude cannot apply these actions yet.") != 1 ||
		strings.Contains(paint, "Not available") || strings.Contains(paint, "Unsupported") ||
		strings.Contains(paint, "Close") {
		t.Fatalf("unavailable list repeats warnings or old inspection chrome:\n%s", paint)
	}
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Cmd != nil ||
		!strings.Contains(s.text.Value(), "Actions containing commands are not supported.") {
		t.Fatal("choosing an unavailable row requested a review or lost its explanation")
	}
}
