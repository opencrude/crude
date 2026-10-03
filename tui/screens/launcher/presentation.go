package launcher

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"fmt"
	"path/filepath"
	"strings"
)

func (s *Screen) displayPath(path string, width int) string {
	if s.home != "" && (path == s.home || strings.HasPrefix(path, s.home+string(filepath.Separator))) {
		path = "~" + strings.TrimPrefix(path, s.home)
	}
	return rendering.TruncateMiddleVisual(path, width, "…")
}

type sectionHeader struct {
	ui.Base
	owner *Screen
}

func (header *sectionHeader) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r, s := header.Bounds(), header.owner
	if r.Empty() {
		return
	}
	title := "Recent workspaces"
	meta := fmt.Sprintf("%d", len(s.filtered))
	if s.Query() != "" {
		meta = fmt.Sprintf("%d of %d", len(s.filtered), len(s.workspaces))
	}
	if s.loading {
		meta = "Loading…"
	}
	if s.loadError != "" {
		meta = "Couldn’t refresh"
	}
	title = rendering.TruncateVisual(title, r.W, "…")
	tx := ox + r.X
	ty := oy + r.Y
	buf.WriteString(tx, ty, title, s.tokens.Color(ui.ColorFg), cell.NoColor, cell.AttrBold)
	left := rendering.VisibleWidth(title) + 2
	if left < r.W {
		buf.WriteString(tx+left, ty, rendering.TruncateVisual(meta, r.W-left, "…"), s.tokens.Color(ui.ColorMuted), cell.NoColor, 0)
	}
}
func (header *sectionHeader) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.Kind == ui.EventMouseWheel {
		return header.owner.search.forwardWheel(event.MouseWheel)
	}
	return ui.EventResult{}
}
func (header *sectionHeader) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: "Recent workspaces"}
}

type workspaceEmpty struct {
	ui.Base
	owner *Screen
}

func (e *workspaceEmpty) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r, s := e.Bounds(), e.owner
	if r.Empty() {
		return
	}
	title, detail := "No recent workspaces", "Open a workspace to get started."
	role := ui.ColorMuted
	switch {
	case s.loading:
		title, detail = "Loading recent workspaces…", "You can start a session while we look."
	case s.loadError != "":
		title, detail, role = "Couldn’t load recent workspaces", s.loadError, ui.ColorError
	case s.Query() != "":
		title, detail = "No matching workspaces", "Try another name or path, or clear the search."
	}
	buf.WriteString(ox+r.X, oy+r.Y, rendering.TruncateVisual(title, r.W, "…"), s.tokens.Color(role), cell.NoColor, 0)
	if r.H > 1 {
		buf.WriteString(ox+r.X, oy+r.Y+1, rendering.TruncateVisual(detail, r.W, "…"), s.tokens.Color(ui.ColorMuted), cell.NoColor, 0)
	}
}
func (e *workspaceEmpty) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: "Workspace results", Value: e.owner.loadError}
}
