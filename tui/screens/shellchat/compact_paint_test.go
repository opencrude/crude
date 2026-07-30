package shellchat_test

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestCompactNativeRowPaintGutterAlignment(t *testing.T) {
	th := theme.DefaultDark()
	screen := shellchat.New(th)
	tokens := ui.NewTokenSet(th)

	header := chatprojection.BuildSubPaneHeaderLine(th, workspace.SubPaneHeaderView{
		Type:    workspace.PaneAgent,
		Title:   "agent",
		Elapsed: 2 * time.Second,
	}, 28, 0)

	rows := []chatprojection.MessageRow{{
		ID:    "pane",
		Lines: []string{header.PlainText()},
		Native: []chatprojection.PaintLine{
			*header.Native,
		},
		SubPaneID: "agent-1",
	}}

	paneMap := []chatprojection.PaneLineInfo{{PaneID: "agent-1", LineIdx: -1}}
	compose := shellchat.ComposeParams{
		InnerW:       40,
		PadLR:        2,
		Theme:        th,
		NestingLevel: 1,
		PaneLineMap:  paneMap,
		PaneGutters: map[string]shellchat.PaneGutterState{
			"agent-1": {Scroll: ui.NewScrollModel(), TrackH: 1, Visible: true},
		},
		HasPaneBg: func(id string) bool { return id == "agent-1" },
	}

	screen.SetMessages(rows, shellchat.PaintParams{Compose: compose, PaneLineMap: paneMap})
	screen.SetTokens(tokens)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: 1})

	buf := screen.PaintTestBuffer(40, 1)

	gx := shellchat.NestedGutterX(compose, 40)
	if gx < 0 {
		t.Fatal("expected nested gutter column")
	}
	gutterCell := buf.Get(gx, 0)
	if gutterCell.Char == 0 && gutterCell.Transparent {
		t.Fatal("expected native gutter glyph in gutter column")
	}
	if strings.Contains(shellchat.PlainRow(buf, 0, gx-1), "\x1b[") {
		t.Fatal("compact row must not serialize ANSI in content cells")
	}
}

func TestCompactNativeSelectionPlainText(t *testing.T) {
	th := theme.DefaultDark()
	tool := chatprojection.BuildSubPaneToolLines(th, workspace.SubPaneView{
		ToolLines: []workspace.SubPaneToolLineView{
			{Label: "read_file main.go", Phase: "done", Status: "success"},
		},
	}, 30)[0]

	rows := []chatprojection.MessageRow{{
		ID:     "pane",
		Lines:  []string{tool.PlainText()},
		Native: []chatprojection.PaintLine{*tool.Native},
	}}

	flat := flattenRows(rows)
	if len(flat) != 1 || !strings.Contains(flat[0], "read_file") {
		t.Fatalf("flatten=%#v", flat)
	}
}

func flattenRows(rows []chatprojection.MessageRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		for i, line := range row.Lines {
			if i < len(row.Native) && !row.Native[i].PreferANSI() {
				out = append(out, row.Native[i].PlainText())
			} else {
				out = append(out, line)
			}
		}
	}
	return out
}
