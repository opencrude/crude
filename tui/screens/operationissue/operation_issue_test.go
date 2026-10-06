package operationissue

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"fmt"
	"strings"
	"testing"
)

func issueBufferText(buf *surface.CellBuffer) string {
	var s strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 {
				if c.Char == 0 {
					s.WriteByte(' ')
				} else {
					s.WriteRune(c.Char)
				}
			}
		}
		s.WriteByte('\n')
	}
	return s.String()
}

func TestRecoveryScreenPaintInteractionAndCompleteEvidence(t *testing.T) {
	for _, width := range []int{28, 72} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			issue := types.OperationIssue{Title: "Session unavailable", Summary: "Crude remains usable while this session needs repair.", Detail: strings.Repeat("Exact diagnostic line.\n", 24) + "Final retained evidence.", Guidance: "Choose a valid account, then retry explicitly.", Target: types.RepairAccounts}
			s := New(theme.DefaultDark(), issue, "Review accounts")
			host := ui.NewHost(theme.DefaultDark(), s)
			host.Resize(width, 10)
			host.Layout()
			host.Focus(ActionsID)
			buf := surface.NewCellBuffer(width, 10)
			host.Paint(buf)
			text := issueBufferText(buf)
			if !strings.Contains(text, "Crude remains") || !strings.Contains(text, "Close") || !strings.Contains(text, "Exact diagnostic") {
				t.Fatal(text)
			}
			result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(ActionMsg).Action != "close" {
				t.Fatal("Close is not the default action")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil || result.Cmd().(ActionMsg).Action != "repair" {
				t.Fatal("keyboard repair is not actionable")
			}
			host.Focus(DetailID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
			buf.Clear()
			host.Paint(buf)
			if !strings.Contains(issueBufferText(buf), "Final retained") {
				t.Fatal("long diagnostic was truncated rather than scrollable")
			}
			if s.detail.Value() != strings.Join([]string{issue.Detail, issue.Guidance}, "\n\n") {
				t.Fatal("scroll changed original evidence")
			}
		})
	}
}
