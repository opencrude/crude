// Package editorchanges presents a read-only hunk through the native editor.
package editorchanges

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/screens/shelleditor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const ScreenID ui.ComponentID = "editor-changes"

type NavigateMsg struct {
	Target *Screen
	Delta  int
}

type Screen struct {
	ui.Base
	preview  *editor.Editor
	area     *shelleditor.Screen
	actions  *ui.ActionBar
	th       *theme.Theme
	baseline string
}

func New(th *theme.Theme, hunk editor.GitHunk, baseline string) *Screen {
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, baseline: baseline, area: shelleditor.New(th)}
	s.area.SetFocused(true)
	s.actions = ui.NewActionBar("editor-changes/actions", ui.NewTokenSet(th),
		ui.ActionBarButton{ID: "editor-changes/previous", Label: "Previous change", Action: "changes.previous", Enabled: true},
		ui.ActionBarButton{ID: "editor-changes/next", Label: "Next change", Action: "changes.next", Enabled: true})
	s.SetHunk(hunk)
	return s
}

func (s *Screen) SetHunk(hunk editor.GitHunk) {
	baseline := s.baseline
	if baseline == "" {
		baseline = "HEAD"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "@@ %s %d,%d -> Buffer %d,%d @@\n", baseline, hunk.OldStart+1, len(hunk.OldLines), hunk.NewStart+1, len(hunk.NewLines))
	for _, part := range []struct {
		sign  byte
		start int
		lines []string
	}{
		{'-', hunk.OldStart, hunk.OldLines}, {'+', hunk.NewStart, hunk.NewLines},
	} {
		for i, line := range part.lines {
			fmt.Fprintf(&text, "%c %5d  %s", part.sign, part.start+i+1, line)
			if !strings.HasSuffix(line, "\n") {
				text.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	if s.preview != nil {
		s.preview.Close()
	}
	s.preview = editor.NewPreview(s.th, "changes.diff", text.String(), 1)
	s.preview.SetLineNumbers(false)
	s.area.Bind(s.preview)
}

func (s *Screen) Close() { s.preview.Close() }

func (s *Screen) SyntaxEditor() *editor.Editor { return s.preview }
func (s *Screen) Children() []ui.Component     { return []ui.Component{s.area, s.actions} }
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.area.SetTokens(tokens)
	s.actions.SetTokens(tokens)
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.area.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-1)})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult  { return ui.EventResult{} }
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	delta := 0
	switch action.ID {
	case "changes.previous":
		delta = -1
	case "changes.next":
		delta = 1
	default:
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return NavigateMsg{Target: s, Delta: delta} }}
}
