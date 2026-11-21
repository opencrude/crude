package launcher

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
	"fmt"
	"strconv"
	"strings"
)

// WorkspaceBadges is the same compact presentation in each recent-workspace
// collection. Unknown and zero counts have no badge.
func WorkspaceBadges(summary workspace.WorkspaceActivity) ui.RowBadges {
	var badges ui.RowBadges
	for i, value := range [3]workspace.ActivityCount{summary.Agents, summary.Terminals, summary.Unsaved} {
		if value.Observed && value.Value > 0 {
			digits := "99+"
			if value.Value < 100 {
				digits = strconv.Itoa(value.Value)
			}
			badges[i] = [...]string{"◈", ">_", "✎"}[i] + digits
		}
	}
	return badges
}

// WorkspaceTooltip disambiguates shortened paths and names the exact counts.
// A cold or busy workspace makes no cleanliness or inactivity claim.
func WorkspaceTooltip(path string, summary workspace.WorkspaceActivity) string {
	var lines [5]string
	lines[0] = path
	n := 1
	for i, count := range [3]workspace.ActivityCount{summary.Agents, summary.Terminals, summary.Unsaved} {
		if count.Observed && count.Value > 0 {
			labels := [...]string{"Running agents", "Open terminals", "Unsaved documents"}
			lines[n] = labels[i] + ": " + strconv.Itoa(count.Value)
			n++
		}
	}
	if !summary.Agents.Observed || !summary.Terminals.Observed || !summary.Unsaved.Observed {
		lines[n] = "Some activity is not available."
		n++
	}
	return strings.Join(lines[:n], "\n")
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
