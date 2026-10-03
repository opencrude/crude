package gitreview

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/aymanbagabas/go-udiff"

	"crude/textdiff"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

type documentPane struct {
	ui.Base
	owner *Screen
	ed    *editor.Editor
	surf  *surface.Surface
	kinds map[int]ui.SemanticColor
}

func newDocumentPane(id ui.ComponentID, owner *Screen) *documentPane {
	return &documentPane{Base: ui.NewBase(id), owner: owner}
}

func (pane *documentPane) bind(document *editor.Editor, kinds map[int]ui.SemanticColor) {
	pane.ed, pane.kinds = document, kinds
	document.SetTokens(pane.owner.tokens)
	document.SetStatusChrome(false)
}

func (pane *documentPane) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bounds := pane.Bounds()
	if pane.ed == nil || bounds.Empty() || pane.owner.comparisonState() != "" {
		return
	}
	if bounds.W < 10 || bounds.H < 4 {
		return
	}
	if pane.surf == nil || pane.surf.Width() != bounds.W || pane.surf.Height() != bounds.H {
		pane.surf = surface.NewSurface(string(pane.ID()), bounds.W, bounds.H, cell.TransparentBlank())
	}
	pane.surf.Buffer().Clear()
	pane.ed.SetPointerHovered(ctx.Hovered)
	pane.ed.Render(pane.surf, ctx)
	painted := pane.surf.Buffer()
	for row := 0; row < bounds.H; row++ {
		role, ok := pane.kinds[pane.ed.ScrollY()+row]
		if !ok {
			continue
		}
		fg := pane.owner.tokens.Color(role)
		for column := 0; column < max(0, bounds.W-ui.ScrollbarWidth); column++ {
			current := painted.Get(column, row)
			if current.Width <= 0 || current.Transparent {
				continue
			}
			current.FG = fg
			painted.Set(column, row, current)
		}
	}
	surface.BlitOpaque(buf, originX+bounds.X, originY+bounds.Y, painted)
}

func (pane *documentPane) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if pane.ed == nil {
		return ui.EventResult{}
	}
	if evt.IsKey() {
		switch evt.Key.String() {
		case "esc":
			if pane.ed.HasSelection() {
				pane.ed.ClearSelection()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
			return ui.EventResult{}
		case "ctrl+a":
			lastLine := max(0, pane.ed.LineCount()-1)
			pane.ed.SetSelection(0, 0, lastLine, editor.LineVisualWidth(pane.ed.LineText(lastLine), 4))
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case "tab":
			pane.owner.focusID(ToolbarID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if !ui.IsCopyChord(evt.Key) {
			switch evt.Key.Code {
			case tea.KeyLeft, tea.KeyRight, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
				if evt.Key.Mod.Contains(tea.ModAlt) {
					return ui.EventResult{}
				}
			default:
				return ui.EventResult{}
			}
		}
	}
	var message tea.Msg
	switch evt.Kind {
	case ui.EventKey:
		message = evt.Key
	case ui.EventMouseClick:
		mouse := evt.MouseClick
		mouse.X, mouse.Y = ctx.LocalX, ctx.LocalY
		message = mouse
	case ui.EventMouseMotion:
		mouse := evt.MouseMotion
		mouse.X, mouse.Y = ctx.LocalX, ctx.LocalY
		message = mouse
	case ui.EventMouseRelease:
		mouse := evt.MouseRelease
		mouse.X, mouse.Y = ctx.LocalX, ctx.LocalY
		message = mouse
	case ui.EventMouseWheel:
		mouse := evt.MouseWheel
		mouse.X, mouse.Y = ctx.LocalX, ctx.LocalY
		message = mouse
	default:
		return ui.EventResult{}
	}
	result := ui.EventResult{Handled: true, Cmd: pane.ed.HandleEvent(message), Invalidate: ui.InvalidatePaint}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && (pane.ed.IsMouseDragging() || pane.ed.IsScrollbarDragging()) {
		result.Action = &ui.Action{ID: "ui.capture", Payload: pane.ID()}
	}
	return result
}

func (pane *documentPane) CancelPointerCapture() {
	if pane.ed != nil {
		pane.ed.CancelPointerCapture()
	}
}

func (pane *documentPane) Semantics() ui.Semantics {
	label := "Unified comparison"
	if pane.ID() == BeforeID {
		label = pane.owner.comparison.BaseLabel
	} else if pane.ID() == AfterID {
		label = pane.owner.comparison.TargetLabel
	}
	return ui.Semantics{Role: ui.RoleTextInput, Label: label, Description: "Read-only comparison with native selection, copy and scrolling"}
}

func sideKinds(hunks []workspace.GitHunk, before bool) map[int]ui.SemanticColor {
	kinds := make(map[int]ui.SemanticColor)
	for _, hunk := range hunks {
		start, count, role := hunk.NewStart, len(hunk.NewLines), ui.ColorDiffAddition
		if before {
			start, count, role = hunk.OldStart, len(hunk.OldLines), ui.ColorDiffDeletion
		}
		for row := start; row < start+count; row++ {
			kinds[row] = role
		}
	}
	return kinds
}

func unifiedDocument(comparison workspace.GitComparison) (string, map[int]ui.SemanticColor, []int) {
	diff, err := udiff.ToUnifiedDiff(comparison.BaseLabel, comparison.TargetLabel, comparison.Before, udiff.Lines(comparison.Before, comparison.After), 3)
	if err != nil {
		return "Comparison unavailable: " + err.Error(), nil, nil
	}
	var document strings.Builder
	kinds := make(map[int]ui.SemanticColor)
	exactHunks := workspace.DocumentHunks(comparison.Before, comparison.After)
	hunkRows := make([]int, len(exactHunks))
	fmt.Fprintf(&document, "--- %s\n+++ %s\n", comparison.BaseLabel, comparison.TargetLabel)
	row := 2
	kinds[0], kinds[1] = ui.ColorDiffFile, ui.ColorDiffFile
	numberW := max(1, len(strconv.Itoa(max(len(textdiff.Lines(comparison.Before)), len(textdiff.Lines(comparison.After))))))
	for _, hunk := range diff.Hunks {
		kinds[row] = ui.ColorDiffHunk
		fmt.Fprintf(&document, "@@ -%d +%d @@\n", hunk.FromLine, hunk.ToLine)
		row++
		oldLine, newLine := hunk.FromLine, hunk.ToLine
		for _, line := range hunk.Lines {
			if line.Kind != udiff.Equal {
				for position, exact := range exactHunks {
					if max(0, oldLine-1) == exact.OldStart && max(0, newLine-1) == exact.NewStart && hunkRows[position] == 0 {
						hunkRows[position] = row
					}
				}
			}
			oldNumber, newNumber, mark := "", "", " "
			switch line.Kind {
			case udiff.Delete:
				oldNumber, mark = strconv.Itoa(oldLine), "-"
				oldLine++
				kinds[row] = ui.ColorDiffDeletion
			case udiff.Insert:
				newNumber, mark = strconv.Itoa(newLine), "+"
				newLine++
				kinds[row] = ui.ColorDiffAddition
			case udiff.Equal:
				oldNumber, newNumber = strconv.Itoa(oldLine), strconv.Itoa(newLine)
				oldLine++
				newLine++
			}
			fmt.Fprintf(&document, "%*s %*s %s%s", numberW, oldNumber, numberW, newNumber, mark, line.Content)
			row++
			if !strings.HasSuffix(line.Content, "\n") {
				document.WriteString("\n\\ No newline at end of file\n")
				row++
			}
		}
	}
	return document.String(), kinds, hunkRows
}
