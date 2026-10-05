package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/panelchrome"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/shellheader"
	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
	"crude/workspace"
)

// Status detail lives in the workspace dock; the old modal overview was
// intentionally removed so there is one retained projection and one focus path.

func (m *App) statusHeaderLeftSegments() []shellheader.Segment {
	segments := []shellheader.Segment{{Text: "Crude", Role: ui.ColorAccent, Priority: 0}}
	if workspacePath := shortWorkspacePath(m.RootPath); workspacePath != "" {
		segments = append(segments, shellheader.Segment{Text: workspacePath, Role: ui.ColorMuted, Priority: 1})
	}
	if m.sourceControl != nil {
		snapshot := m.sourceControl.Snapshot()
		if branch := snapshot.Branch; branch != "" {
			branchIcon := activitybar.ViewGit.Icon(panelchrome.NerdFont(m.Theme))
			segments = append(segments, shellheader.Segment{Text: headerGitIdentity(branchIcon, gitChangesIcon(), snapshot), Role: ui.ColorMuted, Action: "git-branch", Priority: 2})
		}
	}
	return segments
}

// gitChangesIcon is the header's changed-file marker. It deliberately differs
// from the branch glyph — and matches the dirty-tab pencil — so
// "⑂ master · ✎ 9" never reads as two branch icons. A BMP glyph keeps it
// font-independent (already in the terminal-width probe corpus).
func gitChangesIcon() string { return "✎" }

// headerGitIdentity renders the header's Git identity: the current branch
// plus the working-tree counts that used to live in the footer (changed file
// count and ahead/behind tracking). Keeping demand (M files) and direction
// (↑/↓) on one clickable segment puts the whole repo state next to the branch
// picker without growing the segment count.
func headerGitIdentity(branchIcon, changesIcon string, snapshot workspace.GitSnapshot) string {
	identity := branchIcon + " " + snapshot.Branch
	if count := len(snapshot.Changes); count > 0 {
		identity += fmt.Sprintf(" · %s %d", changesIcon, count)
	}
	if snapshot.Ahead > 0 || snapshot.Behind > 0 {
		identity += fmt.Sprintf(" ↑%d ↓%d", snapshot.Ahead, snapshot.Behind)
	}
	return identity
}

// shortWorkspacePath renders the active workspace root for the header identity
// group. The home directory collapses to "~" so the workspace stays readable
// next to the app name and the branch. Unresolved roots and the filesystem
// root carry no identity and return "".
func shortWorkspacePath(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	cleaned := filepath.Clean(root)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return ""
	}
	if !filepath.IsAbs(cleaned) {
		return cleaned
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return cleaned
	}
	home = filepath.Clean(home)
	if cleaned == home {
		return "~"
	}
	relative, err := filepath.Rel(home, cleaned)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return cleaned
	}
	return "~" + string(filepath.Separator) + relative
}

// configureFooterScreen places the footer's two groups on their rails: the
// guidance group (unread Notices, contextual hints, Help) owns the left rail
// and the quieter status group (LSP health, project Problems, background work,
// the Status fallback) is right-aligned.
func (m *App) configureFooterScreen() {
	if m.footerScreen == nil {
		return
	}
	m.footerScreen.SetProjection(shellfooter.Projection{Left: m.footerHelpSegments(), Right: m.footerStatusSegments()})
}

func (m *App) statusHeaderRightSegments() []shellheader.Segment {
	segments := make([]shellheader.Segment, 0, 8)
	context, metrics, target := m.activeHeaderContext()
	if m.OverlayState.IsYolo() {
		segments = append(segments, shellheader.Segment{Text: "! YOLO", Role: ui.ColorError, Action: "tabs", TargetID: target, Priority: 0})
	}
	if strings.HasPrefix(target, "agent:") {
		agentID := strings.TrimPrefix(target, "agent:")
		if node := m.AgentMgr.Get(agentID); node != nil {
			usage, _ := m.UsageSummary(node.ID, ttypes.UsageThisAgent)
			identity := m.AgentModelIdentity(node.ID)
			if model := firstNonempty(identity.ModelID, m.ModelName); model != "" {
				segments = append(segments, shellheader.Segment{Text: model, Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 4})
			}
			// A previous model's last request is not the new model's context.
			if usage.ModelID != identity.ModelID || usage.AccountID != identity.AccountID {
				usage.ContextUsed.Known, usage.ContextLimit.Known = false, false
			}
			segments = append(segments, agentHeaderUsageSegments(usage, target)...)
		}
	} else {
		if context != "" {
			segments = append(segments, shellheader.Segment{Text: context, Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 1})
		}
		if metrics != "" {
			segments = append(segments, shellheader.Segment{Text: metrics, Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 3})
		}
	}
	if strings.HasPrefix(target, "editor:") && m.EditorState.Editor != nil {
		if blame := m.EditorState.Editor.CursorBlameSummary(); blame != "" {
			segments = append(segments, shellheader.Segment{Text: blame, Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 4})
		}
	}
	if !m.StartTime.IsZero() {
		segments = append(segments, shellheader.Segment{Text: formatDuration(time.Since(m.StartTime)), Role: ui.ColorMuted, Action: "workspace", TargetID: "workspace:summary", Priority: 2})
	}
	return segments
}

func agentHeaderUsageSegments(usage ttypes.UsageSummary, target string) []shellheader.Segment {
	if usage.Requests == 0 {
		return nil
	}
	segments := make([]shellheader.Segment, 0, 4)
	if usage.ContextUsed.Known {
		segments = append(segments, shellheader.Segment{Text: "↑" + headerContextReport(usage), Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 1})
	}
	if usage.Output.Known {
		segments = append(segments, shellheader.Segment{Text: "↓" + compactNumber(usage.Output.Value), Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 2})
	}
	if usage.CacheRead.Known && usage.InputTotal.Known && usage.InputTotal.Value > 0 {
		rate := float64(usage.CacheRead.Value) * 100 / float64(usage.InputTotal.Value)
		segments = append(segments, shellheader.Segment{Text: fmt.Sprintf("cache %.1f%%", rate), Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 3})
	}
	if usage.Cost.Known {
		segments = append(segments, shellheader.Segment{Text: headerCost(usage.Cost), Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 2})
	}
	return segments
}

func headerContextReport(usage ttypes.UsageSummary) string {
	text := preciseUsageValue(usage.ContextUsed)
	if usage.ContextLimit.Known && usage.ContextLimit.Value > 0 {
		text += "/" + preciseUsageValue(usage.ContextLimit)
		percent := float64(usage.ContextUsed.Value) * 100 / float64(usage.ContextLimit.Value)
		if percent > 0 && percent < 1 {
			text += " <1%"
		} else {
			text += fmt.Sprintf(" %.0f%%", percent)
		}
	}
	return text
}

func (m *App) activeHeaderContext() (context, metrics, target string) {
	if m.windowManager != nil {
		if window := m.windowManager.Focused(); window != nil && ui.IsTerminalOverlay(ui.OverlayID(window.ID)) {
			if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(window.ID)); ok {
				return m.terminalHeaderContext(id)
			}
		}
	}
	if m.terminalDockOwnsKeys() && m.termDock != nil && m.termDock.ActiveID() != "" {
		return m.terminalHeaderContext(m.termDock.ActiveID())
	}
	if path := m.EditorState.ActiveView; path != "" && m.EditorState.Editor != nil {
		ed := m.EditorState.Editor
		state := fmt.Sprintf("%d:%d", ed.CursorLine()+1, ed.CursorCol()+1)
		if ed.Modified() {
			state = "● " + state
		}
		errors, warnings, hints := ed.DiagnosticCounts()
		if errors+warnings+hints > 0 {
			parts := make([]string, 0, 3)
			if errors > 0 {
				parts = append(parts, fmt.Sprintf("✕%d", errors))
			}
			if warnings > 0 {
				parts = append(parts, fmt.Sprintf("⚠%d", warnings))
			}
			if hints > 0 {
				parts = append(parts, fmt.Sprintf("ℹ%d", hints))
			}
			return state, strings.Join(parts, " "), "editor:" + path
		}
		return state, "", "editor:" + path
	}
	if id := m.AgentMgr.ActiveID(); id != "" {
		state, _ := m.Status.Agent(id)
		context = formatAgentState(state)
		if usage, ok := m.UsageSummary(id, ttypes.UsageThisAgent); ok {
			parts := make([]string, 0, 2)
			if usage.ContextUsed.Known && usage.ContextLimit.Known && usage.ContextLimit.Value > 0 {
				percent := float64(usage.ContextUsed.Value) * 100 / float64(usage.ContextLimit.Value)
				if percent > 0 && percent < 1 {
					parts = append(parts, "Context <1%")
				} else {
					parts = append(parts, fmt.Sprintf("Context %.0f%%", percent))
				}
			}
			if usage.Cost.Known {
				parts = append(parts, headerCost(usage.Cost))
			} else if usage.Requests > 0 {
				parts = append(parts, "Cost ?")
			}
			metrics = strings.Join(parts, " · ")
		}
		return context, metrics, "agent:" + id
	}
	return "", "", ""
}

func (m *App) terminalHeaderContext(id string) (context, metrics, target string) {
	target = "terminal:" + id
	manager := m.termMgr()
	if manager == nil {
		return "Terminal", "", target
	}
	terminal, ok := manager.Get(id)
	if !ok {
		return "Terminal", "Unavailable", target
	}
	context = firstNonempty(terminal.Title, "Terminal")
	if terminal.Alive {
		metrics = "PTY running"
	} else if terminal.ExitErr != "" {
		metrics = "PTY failed"
	} else {
		metrics = fmt.Sprintf("Exited %d", terminal.ExitCode)
	}
	return context, metrics, target
}

// footerStatusSegments forms a contiguous, vivid strip of status tools and
// Workspace. Previous Sessions lives inside Workspace, not in the footer.
func (m *App) footerStatusSegments() []shellfooter.Segment {
	insights := m.LSPMgr.ProjectInsights()
	problemText := "Problems"
	if insights.Errors > 0 || insights.Warnings > 0 {
		parts := make([]string, 0, 2)
		if insights.Errors > 0 {
			parts = append(parts, fmt.Sprintf("%d✕", insights.Errors))
		}
		if insights.Warnings > 0 {
			parts = append(parts, fmt.Sprintf("%d⚠", insights.Warnings))
		}
		problemText = strings.Join(parts, "") + " Problems"
	}
	problemRole := diagnosticRole(insights.Errors, insights.Warnings)
	for _, server := range m.LSPMgr.ServerHealthSnapshots() {
		if server.Error != "" {
			problemRole = ui.ColorError
			break
		}
	}

	unread := 0
	if m.Status != nil {
		unread = m.Status.UnreadCount()
	}
	noticeText := "Notices"
	if unread > 0 {
		noticeText = fmt.Sprintf("%d Notices", unread)
	}
	terminalCount := 0
	if manager := m.termMgr(); manager != nil {
		terminalCount = len(manager.List())
	}
	terminalText := "Terminals"
	if terminalCount > 0 {
		terminalText = fmt.Sprintf("%d Terminals", terminalCount)
	}
	active := ""
	if m.termDock != nil && m.termDock.IsOpen() {
		active = m.termDock.ActivePanel()
	}
	problemAttention := problemRole == ui.ColorError || problemRole == ui.ColorWarning
	if !problemAttention {
		problemRole = ui.ColorAccent2
	}
	working, needsInput := m.backgroundAgentCounts()
	workspaceHue := ui.ColorAccent
	if needsInput > 0 {
		workspaceHue = ui.ColorWarning
	} else if working > 0 {
		workspaceHue = ui.ColorAccent2
	}
	return []shellfooter.Segment{
		{Text: problemText, FillRole: problemRole, Attention: problemAttention, Pill: true, Active: active == termdock.PanelProblems, Action: "problems", Priority: 1},
		{Text: noticeText, FillRole: ui.ColorAccent, Attention: unread > 0, Pill: true, Active: active == termdock.PanelNotices, Action: "notices", Priority: 1},
		{Text: terminalText, FillRole: mapBool(terminalCount > 0, ui.ColorSuccess, ui.ColorDiffHunk), Attention: terminalCount > 0, Pill: true, Active: active == termdock.PanelTerminal, Action: "terminals", Priority: 1},
		{Text: "Board", FillRole: ui.ColorAccent2, Pill: true, Active: active == termdock.PanelBoard, Action: "board", Priority: 0},
		{Text: "Memory", FillRole: ui.ColorMuted, Pill: true, Active: active == termdock.PanelMemory, Action: "memory", Priority: 0},
		{Text: "Workspace", FillRole: workspaceHue, Attention: needsInput > 0 || working > 0, Pill: true, Active: active == termdock.PanelWorkspace, Action: "workspace", Priority: 0},
	}
}

// footerHelpSegments is the footer's guidance group: unread Notices when
// present, a small ranked set of contextual hints, and the Help entry.
// configureFooterScreen left-aligns it.
func (m *App) footerHelpSegments() []shellfooter.Segment {
	segments := make([]shellfooter.Segment, 0, 4)
	if center := m.Status; center != nil {
		if notice, ok := center.LatestTransient(time.Now(), 5*time.Second); ok {
			segments = append(segments, shellfooter.Segment{Text: notice.Summary, Role: ui.ColorMuted, Priority: 0})
		}
	}
	if len(segments) == 0 {
		for _, hint := range m.compactContextHelpSegments() {
			if text := m.hintSegmentText(hint); text != "" {
				segments = append(segments, shellfooter.Segment{Text: text, Role: ui.ColorMuted, Priority: 3})
			}
		}
	}
	segments = append(segments, shellfooter.Segment{Text: "Help", Role: ui.ColorMuted, Action: "help", Priority: 1})
	return segments
}

func (m *App) backgroundAgentCounts() (working, needsAction int) {
	center := m.Status
	if center == nil {
		return 0, 0
	}
	foreground := ""
	if m.EditorState.ActiveView == "" && !m.terminalDockOwnsKeys() {
		foreground = m.AgentMgr.ActiveID()
	}
	for _, status := range center.Agents() {
		if status.Ref.ID == foreground {
			continue
		}
		if status.NeedsAction {
			needsAction++
		}
		switch status.Phase {
		case ttypes.AgentPreparing, ttypes.AgentUploading, ttypes.AgentWaiting, ttypes.AgentCompacting, ttypes.AgentThinking, ttypes.AgentResponding, ttypes.AgentTool, ttypes.AgentChildren, ttypes.AgentRetrying, ttypes.AgentRecovering, ttypes.AgentCancelling:
			working++
		}
	}
	return working, needsAction
}

func (m *App) compactContextHelpSegments() []ui.HintSegment {
	if m.LastSettings.UI.ContextHelp == "off" {
		return nil
	}
	if m.LastSettings.UI.ContextHelp == "expanded" {
		_, hints := m.contextualHelpSegments()
		return hints
	}
	if m.windowManager != nil {
		if window := m.windowManager.Focused(); window != nil {
			switch window.ID {
			case string(ui.OverlayFindReplace):
				if m.FindReplace != nil && m.FindReplace.ReplaceMode {
					return []ui.HintSegment{ui.ActionSeg("replace.hint.replace"), ui.ActionSeg("find.hint.navigate")}
				}
				return []ui.HintSegment{ui.ActionSeg("find.hint.navigate")}
			case findInFilesWindowID, replaceInFilesWindowID:
				return []ui.HintSegment{ui.ActionSeg("list.preview.navigate")}
			default:
				return nil
			}
		}
	}
	if m.sessionDeckFocused() {
		return []ui.HintSegment{ui.ActionSeg("panel.previous"), ui.ActionSeg("panel.next")}
	}
	switch m.focusTarget() {
	case FocusTree:
		if m.Tree != nil && m.Tree.Model != nil && m.Tree.Model.FilterActive() {
			return []ui.HintSegment{ui.ActionSeg("list.navigate"), ui.ActionSeg("list.open"), ui.ActionSeg("launcher.filter.clear")}
		}
		return []ui.HintSegment{ui.ActionSeg("list.navigate"), ui.ActionSeg("list.open"), ui.ActionSeg("launcher.filter")}
	case FocusLauncher:
		return nil
	case FocusEditor:
		return []ui.HintSegment{ui.ActionSeg("edit.find"), ui.ActionSeg("tabs.next"), ui.ActionSeg("view.focus.sidebar")}
	case FocusTabBar:
		return []ui.HintSegment{ui.ActionSeg("tabbar.previous"), ui.ActionSeg("tabbar.next"), ui.ActionSeg("tabbar.select")}
	}
	hints := m.Input.ContextHelpSegments()
	if len(hints) > 0 && hints[0].Kind == ui.HintKindText && strings.Contains(hints[0].Label, "note") {
		return []ui.HintSegment{ui.ChordSeg("Enter", "send note"), ui.ChordSeg("Esc", "cancel")}
	}
	return []ui.HintSegment{ui.ActionSeg("input.newline"), ui.ActionSeg("file.attach"), ui.ActionSeg("input.history")}
}

func (m *App) hintSegmentText(segment ui.HintSegment) string {
	switch segment.Kind {
	case ui.HintKindAction:
		if m.actionRegistry == nil {
			return ""
		}
		action := m.actionRegistry.Get(segment.ActionID)
		if action == nil {
			return ""
		}
		key := ui.FormatShortcut(m.actionRegistry.HintFor(segment.ActionID))
		label := strings.ToLower(action.Label)
		if key == "" {
			return label
		}
		if label == "" {
			return key
		}
		return key + " " + label
	case ui.HintKindChord:
		if segment.Key == "" {
			return segment.Label
		}
		if segment.Label == "" {
			return segment.Key
		}
		return segment.Key + " " + segment.Label
	default:
		return segment.Label
	}
}

func headerCost(cost ttypes.CostValue) string {
	if !cost.Known {
		return "Cost ?"
	}
	if cost.USD > 0 && cost.USD < 0.01 {
		return "<$0.01"
	}
	prefix := ""
	switch cost.Confidence {
	case ttypes.ConfidenceEstimated:
		prefix = "~"
	case ttypes.ConfidenceUpperBound:
		prefix = "≤"
	}
	return fmt.Sprintf("%s$%.2f", prefix, cost.USD)
}

func (m *App) syncStatusOverview() {
	m.syncWorkspacePanels()
}

func (m *App) statusTabItems() []statusoverview.Item {
	items := make([]statusoverview.Item, 0)
	activeAgent := m.AgentMgr.ActiveID()
	nodes := m.AgentMgr.AllNodes()
	if len(nodes) > 0 {
		items = append(items, statusoverview.Item{ID: "group-agents", Label: fmt.Sprintf("Open Agents (%d)", len(nodes)), Header: true})
	}
	for _, node := range nodes {
		label := node.Label
		if label == "" {
			label = "Main Agent"
		}
		state, found := m.Status.Agent(node.ID)
		if !found {
			state = ttypes.AgentStatusSnapshot{Phase: ttypes.AgentIdle, Detail: "Idle", ModelID: node.Model}
		}
		action, actionLabel := "agent:"+node.ID, "Switch to Tab"
		if node.IsHidden() {
			actionLabel = "Restore Agent"
		} else if node.ID == activeAgent && m.EditorState.ActiveView == "" {
			actionLabel = "Return to Tab"
		}
		agentUsage, hasUsage := m.UsageSummary(node.ID, ttypes.UsageThisAgent)
		identity := m.AgentModelIdentity(node.ID)
		model := firstNonempty(identity.ModelID, "Model unavailable")
		subtitle := model
		provider := identity.ProviderID
		if provider != "" {
			subtitle = displayProvider(provider) + " / " + model
		}
		lines := []statusoverview.DetailLine{{Label: "Activity", Value: firstNonempty(state.Detail, string(state.Phase)), Role: ui.ColorMuted}}
		if activity := m.AgentActivity(node.ID); activity.Summary != "" {
			lines = append(lines, statusoverview.DetailLine{Label: "Session", Value: activity.Summary, Role: ui.ColorMuted})
		}
		if node.IsActive() && state.ModelID != "" && state.ModelID != identity.ModelID {
			lines = append(lines, statusoverview.DetailLine{Label: "Active request", Value: state.ModelID + " / " + state.ProviderID, Role: ui.ColorMuted})
		}
		if hasUsage {
			lines = append(lines, statusoverview.DetailLine{Label: "Last context", Value: formatContext(agentUsage) + " / " + agentUsage.ModelID, Role: contextRole(agentUsage)})
			lines = append(lines, statusoverview.DetailLine{Label: "Reported", Value: agentUsage.MeasuredAt.Local().Format("2006-01-02 15:04:05"), Role: ui.ColorMuted})
		}
		if state.TotalBytes > 0 {
			lines = append(lines, statusoverview.DetailLine{Label: "Payload", Value: rendering.FormatBytes(state.TotalBytes) + " serialized; not tokens", Role: ui.ColorMuted})
		}
		if node.IsHidden() {
			lines = append(lines, statusoverview.DetailLine{Label: "Visibility", Value: "Minimized", Role: ui.ColorMuted})
		}
		if state.ReadOnly {
			lines = append(lines, statusoverview.DetailLine{Label: "Access", Value: "Read only", Role: ui.ColorMuted})
		}
		compact := ""
		if hasUsage {
			compact = usageTurns(agentUsage.Turns, agentUsage.TurnsKnown)
			if agentUsage.Cost.Known {
				compact += " · " + headerCost(agentUsage.Cost)
			}
			if len(agentUsage.Models) > 1 {
				compact = fmt.Sprintf("%d models · %s", len(agentUsage.Models), compact)
			}
		}
		items = append(items, statusoverview.Item{
			ID: "agent:" + node.ID, Label: label, Summary: formatAgentState(state), Compact: compact,
			Active: node.ID == activeAgent && m.EditorState.ActiveView == "", Owner: state.Ref,
			Context: model, Subtitle: subtitle, Lines: lines, Usage: m.agentUsageSections(node.ID),
			PrimaryAction: action, PrimaryLabel: actionLabel, PrimaryEnabled: true,
		})
	}
	paths := make([]string, 0, len(m.EditorState.Tabs))
	seenPaths := make(map[string]bool, len(m.EditorState.Tabs))
	if m.Win != nil {
		for _, windowTab := range m.Win.Tabs() {
			if path := m.editorPathFromTabTitle(windowTab.Title); path != "" && m.EditorState.Tabs[path] != nil {
				paths = append(paths, path)
				seenPaths[path] = true
			}
		}
	}
	var missing []string
	for path := range m.EditorState.Tabs {
		if !seenPaths[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	paths = append(paths, missing...)
	if len(paths) > 0 {
		items = append(items, statusoverview.Item{ID: "group-editors", Label: fmt.Sprintf("Open Files (%d)", len(paths)), Header: true})
	}
	for _, path := range paths {
		tab := m.EditorState.Tabs[path]
		if tab == nil || tab.Editor == nil {
			continue
		}
		ed := tab.Editor
		state := "Ready"
		if ed.Modified() {
			state = "Modified"
		}
		errors, warnings := editorDiagnosticCounts(ed.Diagnostics())
		summary := state
		if errors+warnings > 0 {
			summary += fmt.Sprintf("  %d errors  %d warnings", errors, warnings)
		}
		actionLabel := "Switch to Tab"
		if path == m.EditorState.ActiveView {
			actionLabel = "Return to Tab"
		}
		lines := []statusoverview.DetailLine{
			{Label: "Cursor", Value: fmt.Sprintf("Ln %d, Col %d", ed.CursorLine()+1, ed.CursorCol()+1), Role: ui.ColorFg},
			{Label: "Lines", Value: fmt.Sprintf("%d", ed.LineCount()), Role: ui.ColorMuted},
		}
		if errors+warnings > 0 {
			lines = append(lines,
				statusoverview.DetailLine{Label: "Errors", Value: fmt.Sprintf("%d", errors), Role: diagnosticRole(errors, 0)},
				statusoverview.DetailLine{Label: "Warnings", Value: fmt.Sprintf("%d", warnings), Role: diagnosticRole(0, warnings)},
			)
		}
		actions := make([]ttypes.StatusAction, 0, 1)
		if blame, ok := ed.BlameAtLine(ed.CursorLine()); ok {
			lines = append(lines,
				statusoverview.DetailLine{Label: "Blame", Value: ed.CursorBlameSummary(), Role: ui.ColorMuted},
				statusoverview.DetailLine{Label: "Commit", Value: blame.Hash, Role: ui.ColorMuted},
			)
			if blame.Hash != "" && !blame.Uncommitted {
				actions = append(actions, ttypes.StatusAction{ID: "copy:blame:" + blame.Hash, Label: "Copy blame commit", Enabled: true})
			}
		}
		items = append(items, statusoverview.Item{
			ID: "editor:" + path, Label: filepath.Base(path), Summary: summary, Icon: "▤",
			Subtitle:      path,
			Compact:       fmt.Sprintf("%d:%d · %d lines", ed.CursorLine()+1, ed.CursorCol()+1, ed.LineCount()),
			Active:        path == m.EditorState.ActiveView,
			Owner:         ttypes.ResourceRef{Kind: ttypes.ResourceEditor, ID: path},
			Lines:         lines,
			Actions:       actions,
			PrimaryAction: "editor:" + path, PrimaryLabel: actionLabel, PrimaryEnabled: true,
		})
	}
	if manager := m.termMgr(); manager != nil {
		terminals := manager.List()
		if len(terminals) > 0 {
			items = append(items, statusoverview.Item{ID: "group-terminals", Label: "Terminals", Header: true})
		}
		for _, terminal := range terminals {
			state := "PTY running"
			if !terminal.Alive {
				state = fmt.Sprintf("Exited %d", terminal.ExitCode)
			}
			placement := "Docked"
			if terminal.Placement == workspace.TerminalFloating {
				placement = "Floating"
			}
			items = append(items, statusoverview.Item{
				ID: "terminal:" + terminal.ID, Label: terminal.Title, Summary: state + "  " + placement, Icon: ">_",
				Subtitle: firstNonempty(terminal.CWD, placement),
				Active:   terminal.Alive && m.termDock != nil && m.termDock.ActiveID() == terminal.ID,
				Owner:    ttypes.ResourceRef{Kind: ttypes.ResourceTerminal, ID: terminal.ID},
				Lines: []statusoverview.DetailLine{
					{Label: "Placement", Value: placement, Role: ui.ColorFg},
					{Label: "Process", Value: fmt.Sprintf("PID %d", terminal.PID), Role: ui.ColorMuted},
					{Label: "Grid", Value: fmt.Sprintf("%d x %d", terminal.Cols, terminal.Rows), Role: ui.ColorMuted},
					{Label: "Error", Value: firstNonempty(terminal.ExitErr, "None"), Role: mapBool(terminal.ExitErr != "", ui.ColorError, ui.ColorMuted)},
				},
				PrimaryAction: "terminal:" + terminal.ID, PrimaryLabel: "Focus Terminal", PrimaryEnabled: terminal.Alive,
				DisabledReason: "Terminal process has exited",
			})
		}
	}
	return items
}

func usageCount(count int, label string) string {
	if count != 1 {
		label += "s"
	}
	return fmt.Sprintf("%d %s", count, label)
}

func usageTurns(count int, known bool) string {
	if !known {
		if count == 0 {
			return "turns unreported"
		}
		return fmt.Sprintf("%d+ turns", count)
	}
	return usageCount(count, "turn")
}

func (m *App) agentUsageSections(agentID string) []statusoverview.UsageSection {
	sections := make([]statusoverview.UsageSection, 0, 4)
	var previous ttypes.UsageSummary
	for _, scope := range []ttypes.UsageScope{ttypes.UsageThisAgent, ttypes.UsageSubtree, ttypes.UsageSession} {
		summary, ok := m.UsageSummary(agentID, scope)
		if !ok {
			continue
		}
		if len(sections) > 0 && usageEquivalent(previous, summary) {
			continue
		}
		lines := []statusoverview.DetailLine{
			{Label: "Turns", Value: usageTurns(summary.Turns, summary.TurnsKnown) + " (successful API calls)", Role: ui.ColorMuted},
			{Label: "Input", Value: formatUsageValue(summary.InputTotal), Role: ui.ColorFg},
			{Label: "Output", Value: formatUsageValue(summary.Output), Role: ui.ColorFg},
			{Label: "Cost", Value: formatCost(summary.Cost), Role: ui.ColorFg},
			{Label: "Requests", Value: fmt.Sprintf("%d", summary.Requests), Role: ui.ColorMuted},
		}
		if len(summary.Models) > 0 {
			lines = append(lines, statusoverview.DetailLine{Blank: true}, statusoverview.DetailLine{Value: "Usage by model", Role: ui.ColorAccent2})
			for _, model := range summary.Models {
				identity := firstNonempty(model.ModelID, "Unknown model")
				if model.ProviderID != "" {
					identity += " / " + model.ProviderID
				}
				if model.AccountID != "" {
					identity += " / " + model.AccountID
				}
				lines = append(lines, statusoverview.DetailLine{Value: identity, Role: ui.ColorFg}, statusoverview.DetailLine{Value: usageTurns(model.Turns, model.TurnsKnown) + " · " + usageCount(model.Requests, "request") + " · " + formatCost(model.Cost), Role: ui.ColorMuted})
			}
		}
		optional := false
		if summary.UncachedInput.Known {
			lines = append(lines, statusoverview.DetailLine{Label: "Uncached", Value: formatUsageValue(summary.UncachedInput), Role: ui.ColorMuted})
			optional = true
		}
		if summary.CacheRead.Known {
			lines = append(lines, statusoverview.DetailLine{Label: "Cache read", Value: formatUsageValue(summary.CacheRead), Role: ui.ColorMuted})
			optional = true
		}
		if summary.CacheWrite.Known {
			lines = append(lines, statusoverview.DetailLine{Label: "Cache write", Value: formatUsageValue(summary.CacheWrite), Role: ui.ColorMuted})
			optional = true
		}
		if summary.Reasoning.Known {
			lines = append(lines, statusoverview.DetailLine{Label: "Reasoning", Value: formatReasoning(summary), Role: ui.ColorMuted})
			optional = true
		}
		if !optional {
			lines = append(lines, statusoverview.DetailLine{Blank: true}, statusoverview.DetailLine{Value: "Cache and reasoning usage were not reported for this scope.", Role: ui.ColorMuted})
		}
		sections = append(sections, statusoverview.UsageSection{ID: string(scope), Label: usageScopeLabel(scope), Lines: lines})
		previous = summary
	}
	return sections
}

func usageEquivalent(a, b ttypes.UsageSummary) bool {
	return a.InputTotal == b.InputTotal && a.Output == b.Output && a.CacheRead == b.CacheRead && a.CacheWrite == b.CacheWrite && a.Reasoning == b.Reasoning && a.Cost == b.Cost && a.Requests == b.Requests
}

func displayProvider(provider string) string {
	provider = strings.ReplaceAll(provider, "-", " ")
	words := strings.Fields(provider)
	for index := range words {
		if len(words[index]) > 0 {
			words[index] = strings.ToUpper(words[index][:1]) + words[index][1:]
		}
	}
	return strings.Join(words, " ")
}

func (m *App) statusNotificationItems() []statusoverview.Item {
	items := []statusoverview.Item{{ID: "notices", Label: "Notifications", Header: true}}
	for _, notice := range m.Status.Notifications(false) {
		state := "Read"
		if !notice.Read {
			state = "Unread"
		}
		if !notice.ResolvedAt.IsZero() {
			state += "  Resolved"
		}
		items = append(items, statusoverview.Item{
			ID: "notice:" + notice.ID, Label: notice.Summary, Summary: state + "  " + relativeTime(notice.UpdatedAt), Icon: severityIcon(notice.Severity), Owner: notice.Owner,
			NotificationID:     notice.ID,
			NotificationUnread: !notice.Read,
			Lines: []statusoverview.DetailLine{
				{Label: "Source", Value: notice.Source, Role: ui.ColorMuted},
				{Label: "State", Value: state, Role: severityRole(notice.Severity)},
				{Label: "When", Value: notice.UpdatedAt.Format("2006-01-02 15:04:05"), Role: ui.ColorMuted},
				{Label: "Occurrences", Value: fmt.Sprintf("%d", notice.Occurrences), Role: ui.ColorMuted},
				{Blank: true}, {Value: notice.Detail, Role: ui.ColorFg},
			},
			PrimaryAction: ownerNavigation(notice.Owner), PrimaryLabel: ownerNavigationLabel(notice.Owner), PrimaryEnabled: notice.Owner.ID != "",
			DisabledReason: "The originating resource is no longer open",
			Actions:        append([]ttypes.StatusAction(nil), notice.Actions...),
		})
	}
	return items
}

func (m *App) handleStatusOverviewNavigate(msg statusoverview.NavigateMsg) tea.Cmd {
	action := msg.Action
	switch {
	case action == "agent.retry":
		return m.retryAgentRecovery(msg.Item.Owner.ID)
	case action == "status.switch":
		m.restoreAgent(msg.Item.Owner.ID)
		return nil
	case strings.HasPrefix(action, "prompt:attachment:"):
		return m.openHistoricalAttachment(strings.TrimPrefix(action, "prompt:attachment:"))
	case strings.HasPrefix(action, "prompt:restore:"):
		if m.Prompts != nil {
			for _, entry := range m.Prompts.Entries() {
				if entry.ID == strings.TrimPrefix(action, "prompt:restore:") {
					m.overlayHost.Close(ui.OverlayPromptHistory)
					return m.recallPrompt(m.Input.RecallPrompt(entry))
				}
			}
		}
		return nil
	case strings.HasPrefix(action, "agent:"):
		m.restoreAgent(strings.TrimPrefix(action, "agent:"))
	case strings.HasPrefix(action, "editor:"):
		_, _ = m.openEditorTab(strings.TrimPrefix(action, "editor:"))
	case strings.HasPrefix(action, "terminal:"):
		m.focusTerminal(strings.TrimPrefix(action, "terminal:"))
	case strings.HasPrefix(action, "diagnostic:"):
		parts := strings.SplitN(strings.TrimPrefix(action, "diagnostic:"), ":", 3)
		if len(parts) == 3 {
			line, lineErr := strconv.Atoi(parts[0])
			col, colErr := strconv.Atoi(parts[1])
			if lineErr == nil && colErr == nil {
				m.jumpToDiagnostic(parts[2], workspace.LSPDiagnostic{Line: line, Col: col})
			}
		}
	case strings.HasPrefix(action, "session:resume:"):
		return m.resumeWorkspaceSession(strings.TrimPrefix(action, "session:resume:"))
	case action == "settings:workspace":
		m.openWorkspaceSettings()
		return nil
	case action == "git:open":
		return m.toggleActivity(activityGit)
	case action == "history:open":
		return m.openLocalHistory()
	case action == "memory:open":
		_, command := m.toggleMemoryBrowser()
		return command
	case action == "lsp:restart":
		if !m.LSPMgr.RestartServer(msg.Item.Owner.ID) {
			m.Feedback.NotifyError("Language server is no longer available")
		}
		return nil
	case action == "lsp:stop":
		if !m.LSPMgr.StopServer(msg.Item.Owner.ID) {
			m.Feedback.NotifyError("Language server is no longer available")
		}
		return nil
	case action == "lsp:start-all":
		m.startLSPForWorkspace()
		return nil
	case action == "lsp:stop-all":
		m.StopLSP()
		return nil
	case action == "lsp:restart-all":
		m.StopLSP()
		m.startLSPForWorkspace()
		return nil
	case strings.HasPrefix(action, "copy:blame:"):
		m.copyToClipboard(strings.TrimPrefix(action, "copy:blame:"))
		return nil
	}
	if m.termDock != nil {
		m.termDock.ClosePanel()
		m.setTermDockFocused(false)
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayTabStats)
	}
	return nil
}

func (m *App) openStatusOverviewMore(item statusoverview.Item) {
	menuItems := make([]ui.ContextMenuItem, 0, len(item.Actions)+2)
	menuItems = append(menuItems, ui.ContextMenuItem{Label: "Copy summary", Action: func() { m.copyToClipboard(statusSummaryText(item)) }})
	if item.NotificationID != "" {
		id := item.NotificationID
		read := !item.NotificationUnread
		label := "Mark read"
		if read {
			label = "Mark unread"
		}
		menuItems = append(menuItems,
			ui.ContextMenuItem{Label: label, Action: func() {
				m.Status.MarkRead(id, !read)
				m.syncStatusOverview()
			}},
			ui.ContextMenuItem{Label: "Dismiss", Action: func() {
				m.Status.Dismiss(id)
				m.syncStatusOverview()
			}},
		)
	}
	for _, statusAction := range item.Actions {
		action := statusAction
		menuItems = append(menuItems, ui.ContextMenuItem{
			Label: action.Label, Disabled: !action.Enabled, Detail: action.Reason,
			Action: func() { _ = m.handleStatusOverviewNavigate(statusoverview.NavigateMsg{Item: item, Action: action.ID}) },
		})
	}
	if len(menuItems) == 0 {
		return
	}
	state := &ui.ContextMenuState{Title: item.Label, Sections: []ui.ContextMenuSection{{Items: menuItems}}}
	state.Build()
	frame := ui.BuildContextMenuFrame(m.Theme, state)
	w, h := ui.ContextMenuPopupWidth(state), ui.ContextMenuPopupHeight(state)
	x, y := m.clampShellPopupPosition(m.width/2-w/2, m.height/2-h/2, w, h)
	window := wm.NewFloatingWindow(string(ui.OverlayContextMenu), "", frame, w, h,
		wm.WithDecoration(false), wm.WithMovable(false), wm.WithResizable(false),
		wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute),
	)
	window.SetPosition(x, y)
	m.overlayHost.Open(ui.OverlayContextMenu, window, ui.PopoverPolicy(true), func() { m.LastContextMenu = nil })
	m.LastContextMenu = state
	m.ContextMenuOpenedAt = time.Now()
}

func statusSummaryText(item statusoverview.Item) string {
	var builder strings.Builder
	builder.WriteString(item.Label)
	if item.Summary != "" {
		builder.WriteString("\n")
		builder.WriteString(item.Summary)
	}
	for _, line := range item.Lines {
		if line.Blank {
			builder.WriteString("\n")
			continue
		}
		builder.WriteString("\n")
		if line.Label != "" {
			builder.WriteString(line.Label)
			builder.WriteString(": ")
		}
		builder.WriteString(line.Value)
	}
	return builder.String()
}

func formatAgentState(state ttypes.AgentStatusSnapshot) string {
	// The row reports the turn lifecycle, not token-by-token transport phases.
	// Keep detailed tool/provider state in the expanded information.
	switch state.Phase {
	case ttypes.AgentOpening, ttypes.AgentPreparing, ttypes.AgentUploading, ttypes.AgentWaiting, ttypes.AgentCompacting,
		ttypes.AgentThinking, ttypes.AgentResponding, ttypes.AgentTool, ttypes.AgentChildren:
		if state.NeedsAction {
			return firstNonempty(state.Detail, "Needs attention")
		}
		label := "Working"
		if !state.StartedAt.IsZero() {
			label += " · " + formatDuration(time.Since(state.StartedAt))
		}
		return label
	}
	label := state.Detail
	if label == "" {
		label = strings.ReplaceAll(string(state.Phase), "_", " ")
	}
	return firstNonempty(label, "Idle")
}

func formatUsageValue(value ttypes.UsageValue) string {
	if !value.Known {
		return "Not reported"
	}
	label := compactNumber(value.Value)
	switch value.Confidence {
	case ttypes.ConfidenceEstimated:
		return "~" + label
	case ttypes.ConfidencePartial:
		return label + " partial"
	case ttypes.ConfidenceUpperBound:
		return "≤ " + label
	default:
		return label
	}
}

func formatContext(summary ttypes.UsageSummary) string {
	if !summary.ContextUsed.Known {
		return "Not measured"
	}
	used := preciseUsageValue(summary.ContextUsed)
	if !summary.ContextLimit.Known || summary.ContextLimit.Value <= 0 {
		return used
	}
	percent := float64(summary.ContextUsed.Value) * 100 / float64(summary.ContextLimit.Value)
	return fmt.Sprintf("%s / %s · %.1f%%", used, preciseUsageValue(summary.ContextLimit), percent)
}

func preciseUsageValue(value ttypes.UsageValue) string {
	if !value.Known {
		return "Not measured"
	}
	prefix := ""
	if value.Confidence == ttypes.ConfidenceEstimated {
		prefix = "~"
	}
	switch {
	case value.Value >= 1_000_000:
		return fmt.Sprintf("%s%.2fM", prefix, float64(value.Value)/1_000_000)
	case value.Value >= 1_000:
		return fmt.Sprintf("%s%.1fk", prefix, float64(value.Value)/1_000)
	default:
		return prefix + fmt.Sprintf("%d", value.Value)
	}
}

func formatReasoning(summary ttypes.UsageSummary) string {
	value := formatUsageValue(summary.Reasoning)
	if summary.Reasoning.Known && summary.ReasoningInOut {
		value += " · included in output"
	}
	return value
}

func formatCost(cost ttypes.CostValue) string {
	if !cost.Known {
		if cost.USD > 0 {
			return fmt.Sprintf(">= $%.4f known · partial", cost.USD)
		}
		return "Cost unavailable"
	}
	estimated := cost.Confidence == ttypes.ConfidenceEstimated || strings.Contains(cost.Source, "catalog")
	if cost.USD > 0 && cost.USD < 0.01 {
		if estimated {
			return "<$0.01 estimated"
		}
		return "<$0.01"
	}
	label := fmt.Sprintf("$%.2f", cost.USD)
	if estimated {
		label = "~" + label
	}
	if cost.Source != "" {
		label += " · " + cost.Source
	}
	return label
}

func usageScopeLabel(scope ttypes.UsageScope) string {
	switch scope {
	case ttypes.UsageSubtree:
		return "With Children"
	case ttypes.UsageSession:
		return "Session"
	case ttypes.UsageAuxiliary:
		return "Auxiliary"
	default:
		return "This Agent"
	}
}

func workspaceHealthSummary(insights ttypes.ProjectInsights) string {
	if insights.ServerFailed {
		return "IDE issue · language server failed"
	}
	if insights.Errors > 0 || insights.Warnings > 0 {
		return fmt.Sprintf("%d errors · %d warnings", insights.Errors, insights.Warnings)
	}
	if insights.ServerStarting || insights.ServerSyncing || insights.ServerBusy {
		return "Language services working"
	}
	if insights.ServerReady {
		return "Language services ready"
	}
	return "No language server"
}

func editorDiagnosticCounts(diags []workspace.LSPDiagnostic) (errors, warnings int) {
	for _, diagnostic := range diags {
		switch diagnostic.Severity {
		case workspace.DiagError:
			errors++
		case workspace.DiagWarning:
			warnings++
		}
	}
	return errors, warnings
}

func ownerNavigation(owner ttypes.ResourceRef) string {
	switch owner.Kind {
	case ttypes.ResourceAgent:
		return "agent:" + owner.ID
	case ttypes.ResourceEditor, ttypes.ResourceFile:
		return "editor:" + owner.ID
	case ttypes.ResourceTerminal:
		return "terminal:" + owner.ID
	default:
		return ""
	}
}

func ownerNavigationLabel(owner ttypes.ResourceRef) string {
	switch owner.Kind {
	case ttypes.ResourceAgent:
		return "Inspect Agent"
	case ttypes.ResourceEditor, ttypes.ResourceFile:
		return "Open File"
	case ttypes.ResourceTerminal:
		return "Focus Terminal"
	default:
		return ""
	}
}

func contextRole(summary ttypes.UsageSummary) ui.SemanticColor {
	if !summary.ContextUsed.Known || !summary.ContextLimit.Known || summary.ContextLimit.Value <= 0 {
		return ui.ColorMuted
	}
	percent := summary.ContextUsed.Value * 100 / summary.ContextLimit.Value
	if percent >= 95 {
		return ui.ColorError
	}
	if percent >= 80 {
		return ui.ColorWarning
	}
	return ui.ColorFg
}

func diagnosticRole(errors, warnings int) ui.SemanticColor {
	if errors > 0 {
		return ui.ColorError
	}
	if warnings > 0 {
		return ui.ColorWarning
	}
	return ui.ColorMuted
}

func severityRole(severity ttypes.StatusSeverity) ui.SemanticColor {
	switch severity {
	case ttypes.SeverityCritical, ttypes.SeverityError:
		return ui.ColorError
	case ttypes.SeverityWarning:
		return ui.ColorWarning
	default:
		return ui.ColorFg
	}
}

func severityIcon(severity ttypes.StatusSeverity) string {
	switch severity {
	case ttypes.SeverityCritical, ttypes.SeverityError:
		return "×"
	case ttypes.SeverityWarning:
		return "!"
	default:
		return "i"
	}
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(duration.Minutes()), int(duration.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(duration.Hours()), int(duration.Minutes())%60)
}

func relativeTime(value time.Time) string {
	if value.IsZero() {
		return "unknown time"
	}
	return formatDuration(time.Since(value)) + " ago"
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func compactNumber(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func mapBool[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}
