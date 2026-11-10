package statusoverview

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/screens/problems"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/control"
)

func TestWorkspacePanelLinkMatchesProblems(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{34, 80, 160} {
			s := NewDocked(th, Projection{Active: ViewTabs, HeaderAction: "workspace-aeron", HeaderLabel: "Open workspace Aeron"})
			host := ui.NewHost(th, s)
			host.Resize(width, 10)
			buf := surface.NewCellBuffer(width, 10)
			host.Paint(buf)
			link, ok := host.ScreenRect(HeaderLinkID)
			if !ok || buf.Get(link.X+1, link.Y).Char != 'O' {
				t.Fatal("Workspace panel link is not painted")
			}
			got := buf.Get(link.X+1, link.Y)
			p := problems.New(th)
			p.SetModel(problems.Model{Refresh: true})
			pHost := ui.NewHost(th, p)
			pHost.Resize(width, 10)
			pBuf := surface.NewCellBuffer(width, 10)
			pHost.Paint(pBuf)
			var reference cell.Cell
			for y := range pBuf.Height() {
				for x := 0; x+len("Refresh") <= pBuf.Width(); x++ {
					match := true
					for i, char := range "Refresh" {
						match = match && pBuf.Get(x+i, y).Char == char
					}
					if match {
						reference = pBuf.Get(x, y)
					}
				}
			}
			if reference.Char != 'R' || !reference.Attr.Has(cell.AttrUnderline) {
				t.Fatal("Problems does not provide the expected underlined link")
			}
			if got.Attr != reference.Attr || !got.FG.Equal(reference.FG) || !got.BG.Equal(reference.BG) {
				t.Fatalf("Workspace and Problems link styles differ at width %d: workspace=%+v problems=%+v", width, got, reference)
			}
		}
	}
}

func TestDockedHeaderActionUsesPanelFlow(t *testing.T) {
	for _, width := range []int{34, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			s := NewDocked(theme.DefaultDark(), Projection{Active: ViewTabs, HeaderAction: "workspace-aeron", HeaderLabel: "Open workspace Aeron"})
			s.SetDockHeaderInset(10)
			host := ui.NewHost(theme.DefaultDark(), s)
			s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
			host.Resize(width, 12)
			host.Layout()
			buf := surface.NewCellBuffer(width, 12)
			host.Paint(buf)
			link, ok := host.ScreenRect(HeaderLinkID)
			if !ok || link.Empty() || link.X < 0 || link.X+link.W > width || link.Y > 2 {
				t.Fatalf("panel header link is not bounded: %+v", link)
			}
			result := host.Handle(tea.MouseClickMsg{X: link.X + 1, Y: link.Y, Button: tea.MouseLeft})
			if result.Cmd == nil || result.Cmd().(NavigateMsg).Action != "workspace-aeron" {
				t.Fatal("pointer link does not use panel navigation")
			}
			host.Focus(SearchID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if host.Focused() != HeaderLinkID {
				t.Fatal("panel link is not keyboard-reachable")
			}
			result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(NavigateMsg).Action != "workspace-aeron" {
				t.Fatal("keyboard did not activate the same panel link")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if host.Focused() != ListID {
				t.Fatal("panel link trapped keyboard focus")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			if host.Focused() != HeaderLinkID {
				t.Fatal("reverse Tab skipped the panel link")
			}
			page, err := host.InspectControls(control.Target{WorkspaceID: "workspace"}, control.PageRequest{Limit: 128})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, node := range page.Nodes {
				if node.ID != string(HeaderLinkID) {
					continue
				}
				found = true
				result, err := host.Control(HeaderLinkID, node.OpenEpoch, &node.Revision, ui.ControlOperation{Verb: "activate"})
				if err != nil || result.Cmd == nil || result.Cmd().(NavigateMsg).Action != "workspace-aeron" {
					t.Fatal("semantic activation differs from the native panel link", err)
				}
			}
			if !found {
				t.Fatal("panel link is absent from semantic control inspection")
			}
		})
	}
}
