package viewmodel

import (
	"fmt"
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
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
	"crude/workspace"
)

func (m *App) openStatusOverview(view statusoverview.ViewID, targetID string) {
	m.overlayHost.Close(ui.OverlayStatusOverview)
	projection := m.statusOverviewProjection(view)
	screen := statusoverview.New(m.Theme, projection)
	if m.statusOverviewSplit > 0 {
		screen.SetSplitFirst(m.statusOverviewSplit)
	}
	m.statusOverview = screen
	m.statusOverviewSourceVersion = m.LSPMgr.Version()
	if m.Status != nil {
		m.statusOverviewCenterVersion = m.Status.Version()
	}
	m.statusOverviewSecond = time.Now().Unix()
	w, h := fitFloatingSize(m.width, m.height, 104, 30, 48, 16)
	frame := ui.NewWindowFrameWidget(
		m.Theme, ui.NewID(ui.RootID, "status-overview-frame"), ui.FrameWindow,
		"Status Overview", screen, nil, true, w, h,
	)
	screen.SetFocusHandler(frame.Focus)
	if targetID != "" {
		screen.Select(targetID)
	}
	if id := screen.DisplayedUnreadID(); id != "" && m.Status.MarkRead(id, true) {
		selected, _ := screen.Selected()
		screen.SetProjection(m.statusOverviewProjection(view))
		screen.Select(selected.ID)
	}
	screen.FocusSearch()
	win := wm.NewFloatingWindow(string(ui.OverlayStatusOverview), "Status Overview", frame,
		w, h,
		wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true),
		wm.WithClosable(true), wm.WithFocusable(true), wm.WithModal(true),
		wm.WithMinSize(minInt(48, w), minInt(16, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayStatusOverview, win, ui.ModalDialogPolicy(), func() {
		if split := screen.SplitFirst(); split > 0 {
			m.statusOverviewSplit = split
		}
		m.statusOverview = nil
	})
}

func (m *App) statusHeaderLeftSegments() []shellheader.Segment {
	segments := []shellheader.Segment{{Text: "Crude", Role: ui.ColorAccent, Priority: 0}}
	workspaceName := filepath.Base(m.RootPath)
	if workspaceName != "" && workspaceName != "." && !strings.EqualFold(workspaceName, "crude") {
		segments = append(segments, shellheader.Segment{Text: workspaceName, Role: ui.ColorMuted, Priority: 6})
	}
	if m.sourceControl != nil {
		if branch := m.sourceControl.Snapshot().Branch; branch != "" {
			branchIcon := activitybar.ViewGit.Icon(panelchrome.NerdFont(m.Theme))
			segments = append(segments, shellheader.Segment{Text: branchIcon + " " + branch, Role: ui.ColorMuted, Action: "git", Priority: 2})
		}
	}
	return segments
}

func (m *App) configureFooterScreen() {
	if m.footerScreen == nil {
		return
	}
	m.footerScreen.SetProjection(shellfooter.Projection{Left: m.statusFooterLeftSegments(), Right: m.statusFooterRightSegments()})
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
			status, _ := m.Status.Agent(node.ID)
			usage, _ := m.UsageSummary(node.ID, ttypes.UsageThisAgent)
			backendModel := ""
			if backend := m.BackendSession(); backend != nil {
				backendModel = backend.ModelName()
			}
			if model := firstNonempty(usage.ModelID, node.Model, status.ModelID, backendModel, m.ModelName); model != "" {
				segments = append(segments, shellheader.Segment{Text: model, Role: ui.ColorMuted, Action: "tabs", TargetID: target, Priority: 4})
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
	if m.termDockOwnsKeys() && m.termDock != nil && m.termDock.ActiveID() != "" {
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

func (m *App) statusFooterLeftSegments() []shellfooter.Segment {
	segments := make([]shellfooter.Segment, 0, 8)
	center := m.Status
	if center == nil {
		return []shellfooter.Segment{{Text: "Status", Role: ui.ColorMuted, Action: "workspace", Priority: 0}}
	}
	segments = append(segments, m.footerLSPAndGitSegments()...)
	_, _, activeTarget := m.activeHeaderContext()
	for _, issue := range center.Issues() {
		if issue.ResolvedAt.IsZero() && ownerNavigation(issue.Owner) != activeTarget {
			segments = append(segments, shellfooter.Segment{Text: issue.Summary, Role: severityRole(issue.Severity), Action: "workspace", TargetID: "issue:" + issue.ID, Priority: 0})
			break
		}
	}
	backgroundWorking, needsAction := m.backgroundAgentCounts()
	if needsAction > 0 {
		segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("%d need input", needsAction), Role: ui.ColorWarning, Action: "activity", Priority: 0})
	}
	if backgroundWorking > 0 {
		label := "1 background agent"
		if backgroundWorking > 1 {
			label = fmt.Sprintf("%d background agents", backgroundWorking)
		}
		segments = append(segments, shellfooter.Segment{Text: label, Role: ui.ColorMuted, Action: "activity", Priority: 2})
	}
	if len(segments) == 0 {
		if notice, ok := center.LatestTransient(time.Now(), 5*time.Second); ok {
			segments = append(segments, shellfooter.Segment{Text: notice.Summary, Role: ui.ColorMuted, Action: "notifications", TargetID: "notice:" + notice.ID, Priority: 4})
		}
	}
	if len(segments) == 0 {
		segments = append(segments, shellfooter.Segment{Text: "Status", Role: ui.ColorMuted, Action: "workspace", Priority: 5})
	}
	return segments
}

func (m *App) footerLSPAndGitSegments() []shellfooter.Segment {
	segments := make([]shellfooter.Segment, 0, 4)
	servers := m.LSPMgr.ServerHealthSnapshots()
	failed, working, ready := 0, 0, 0
	serverName := ""
	for _, server := range servers {
		serverName = server.Name
		switch {
		case server.Error != "":
			failed++
		case server.Starting || server.Syncing || server.Busy:
			working++
		case server.Connected:
			ready++
		}
	}
	switch {
	case failed > 0:
		segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("✕ LSP %d", failed), Role: ui.ColorError, Action: "workspace", Priority: 0})
	case working > 0:
		segments = append(segments, shellfooter.Segment{Text: "● LSP", Role: ui.ColorAccent, Action: "workspace", Priority: 1})
	case ready == 1:
		segments = append(segments, shellfooter.Segment{Text: "◇ " + serverName, Role: ui.ColorMuted, Action: "workspace", Priority: 4})
	case ready > 1:
		segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("◇ LSP %d", ready), Role: ui.ColorMuted, Action: "workspace", Priority: 4})
	}
	insights := m.LSPMgr.ProjectInsights()
	if insights.Errors > 0 || insights.Warnings > 0 {
		parts := make([]string, 0, 2)
		if insights.Errors > 0 {
			parts = append(parts, fmt.Sprintf("✕%d", insights.Errors))
		}
		if insights.Warnings > 0 {
			parts = append(parts, fmt.Sprintf("⚠%d", insights.Warnings))
		}
		segments = append(segments, shellfooter.Segment{Text: strings.Join(parts, " "), Role: diagnosticRole(insights.Errors, insights.Warnings), Action: "workspace", TargetID: "workspace-problems", Priority: 1})
	}
	if m.sourceControl != nil {
		snapshot := m.sourceControl.Snapshot()
		gitIcon := activitybar.ViewGit.Icon(panelchrome.NerdFont(m.Theme))
		if len(snapshot.Changes) > 0 {
			segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("%s %d", gitIcon, len(snapshot.Changes)), Role: ui.ColorWarning, Action: "git", Priority: 2})
		}
		if snapshot.Ahead > 0 || snapshot.Behind > 0 {
			segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("↑%d ↓%d", snapshot.Ahead, snapshot.Behind), Role: ui.ColorMuted, Action: "git", Priority: 3})
		}
	}
	return segments
}

func (m *App) statusFooterRightSegments() []shellfooter.Segment {
	segments := make([]shellfooter.Segment, 0, 4)
	if center := m.Status; center != nil {
		if unread := center.UnreadCount(); unread > 0 {
			segments = append(segments, shellfooter.Segment{Text: fmt.Sprintf("Notices %d", unread), Role: ui.ColorWarning, Action: "notifications", Priority: 0})
		}
	}
	for _, hint := range m.compactContextHelpSegments() {
		if text := m.hintSegmentText(hint); text != "" {
			segments = append(segments, shellfooter.Segment{Text: text, Role: ui.ColorMuted, Priority: 3})
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
	if m.EditorState.ActiveView == "" && !m.termDockOwnsKeys() {
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
		case ttypes.AgentPreparing, ttypes.AgentUploading, ttypes.AgentWaiting, ttypes.AgentThinking, ttypes.AgentResponding, ttypes.AgentTool, ttypes.AgentChildren, ttypes.AgentRetrying, ttypes.AgentCancelling:
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
	if m.statusOverview == nil || !m.overlayHost.IsOpen(ui.OverlayStatusOverview) {
		return
	}
	lspVersion := m.LSPMgr.Version()
	centerVersion := uint64(0)
	if m.Status != nil {
		centerVersion = m.Status.Version()
	}
	second := time.Now().Unix()
	if lspVersion == m.statusOverviewSourceVersion && centerVersion == m.statusOverviewCenterVersion && second == m.statusOverviewSecond {
		return
	}
	m.statusOverviewSourceVersion, m.statusOverviewCenterVersion, m.statusOverviewSecond = lspVersion, centerVersion, second
	m.statusOverview.SetProjection(m.statusOverviewProjection(m.statusOverview.ActiveView()))
	if win := m.windowManager.Window(string(ui.OverlayStatusOverview)); win != nil {
		win.MarkContentDirty()
	}
}

func (m *App) statusOverviewProjection(view statusoverview.ViewID) statusoverview.Projection {
	projection := statusoverview.Projection{Views: make(map[statusoverview.ViewID][]statusoverview.Item), Active: view}
	switch view {
	case statusoverview.ViewWorkspace:
		projection.Views[view] = m.statusWorkspaceItems()
	case statusoverview.ViewActivity:
		projection.Views[view] = m.statusActivityItems()
	case statusoverview.ViewNotifications:
		projection.Views[view] = m.statusNotificationItems()
	default:
		projection.Views[statusoverview.ViewTabs] = m.statusTabItems()
	}
	return projection
}

func (m *App) statusTabItems() []statusoverview.Item {
	items := make([]statusoverview.Item, 0)
	activeAgent := m.AgentMgr.ActiveID()
	nodes := m.AgentMgr.AllNodes()
	if len(nodes) > 0 {
		items = append(items, statusoverview.Item{ID: "group-agents", Label: "Agents", Header: true})
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
		if node.Hidden {
			actionLabel = "Restore Agent"
		} else if node.ID == activeAgent && m.EditorState.ActiveView == "" {
			actionLabel = "Return to Tab"
		}
		agentUsage, hasUsage := m.UsageSummary(node.ID, ttypes.UsageThisAgent)
		backendModel := ""
		if backend := m.BackendSession(); backend != nil {
			backendModel = backend.ModelName()
		}
		model := firstNonempty(state.ModelID, node.Model, agentUsage.ModelID, backendModel, "Model unavailable")
		subtitle := model
		provider := firstNonempty(state.ProviderID, agentUsage.ProviderID)
		if provider != "" {
			subtitle = displayProvider(provider) + " / " + model
		}
		lines := make([]statusoverview.DetailLine, 0, 4)
		if hasUsage {
			lines = append(lines, statusoverview.DetailLine{Label: "Context", Value: formatContext(agentUsage), Role: contextRole(agentUsage)})
			lines = append(lines, statusoverview.DetailLine{Label: "Reported", Value: agentUsage.MeasuredAt.Local().Format("2006-01-02 15:04:05"), Role: ui.ColorMuted})
		}
		if state.TotalBytes > 0 {
			lines = append(lines, statusoverview.DetailLine{Label: "Payload", Value: rendering.FormatBytes(state.TotalBytes) + " serialized; not tokens", Role: ui.ColorMuted})
		}
		if node.Hidden {
			lines = append(lines, statusoverview.DetailLine{Label: "Visibility", Value: "Minimized", Role: ui.ColorMuted})
		}
		if state.ReadOnly {
			lines = append(lines, statusoverview.DetailLine{Label: "Access", Value: "Read only", Role: ui.ColorMuted})
		}
		items = append(items, statusoverview.Item{
			ID: "agent:" + node.ID, Label: label, Summary: formatAgentState(state),
			Active: node.ID == activeAgent && m.EditorState.ActiveView == "", Owner: state.Ref,
			Subtitle: subtitle, Lines: lines, Usage: m.agentUsageSections(node.ID),
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
		items = append(items, statusoverview.Item{ID: "group-editors", Label: "Editors", Header: true})
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
			summary += fmt.Sprintf(" · %d errors · %d warnings", errors, warnings)
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
			lines = append(lines, statusoverview.DetailLine{Label: "Diagnostics", Value: fmt.Sprintf("%d errors · %d warnings", errors, warnings), Role: diagnosticRole(errors, warnings)})
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
				ID: "terminal:" + terminal.ID, Label: terminal.Title, Summary: state + " · " + placement, Icon: ">_",
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
	return compactSingleGroup(items)
}

func compactSingleGroup(items []statusoverview.Item) []statusoverview.Item {
	headers := 0
	for _, item := range items {
		if item.Header {
			headers++
		}
	}
	if headers != 1 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if !item.Header {
			out = append(out, item)
		}
	}
	return out
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
			{Label: "Input", Value: formatUsageValue(summary.InputTotal), Role: ui.ColorFg},
			{Label: "Output", Value: formatUsageValue(summary.Output), Role: ui.ColorFg},
			{Label: "Cost", Value: formatCost(summary.Cost), Role: ui.ColorFg},
			{Label: "Requests", Value: fmt.Sprintf("%d", summary.Requests), Role: ui.ColorMuted},
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

func (m *App) statusWorkspaceItems() []statusoverview.Item {
	insights := m.LSPMgr.ProjectInsights()
	memoryAvailable := m.BackendSession() != nil && m.BackendSession().MemoryStore() != nil
	items := []statusoverview.Item{
		{ID: "workspace:summary", Label: filepath.Base(m.RootPath), Summary: workspaceHealthSummary(insights), Icon: "⬡", Active: true,
			Owner: ttypes.ResourceRef{Kind: ttypes.ResourceWorkspace, ID: m.RootPath},
			Lines: []statusoverview.DetailLine{
				{Label: "Path", Value: m.RootPath, Role: ui.ColorFg},
				{Label: "Diagnostics", Value: fmt.Sprintf("%d errors · %d warnings · %d files", insights.Errors, insights.Warnings, insights.FilesAffected), Role: diagnosticRole(insights.Errors, insights.Warnings)},
				{Label: "Tracked files", Value: fmt.Sprintf("%d", insights.FilesTracked), Role: ui.ColorMuted},
				{Label: "Uptime", Value: formatDuration(time.Since(m.StartTime)), Role: ui.ColorMuted},
			}, PrimaryAction: "settings:workspace", PrimaryLabel: "Workspace Settings", PrimaryEnabled: true,
			Actions: []ttypes.StatusAction{{ID: "lsp:start-all", Label: "Start Language Servers", Enabled: true}, {ID: "lsp:stop-all", Label: "Stop Language Servers", Enabled: true}, {ID: "lsp:restart-all", Label: "Restart Language Servers", Enabled: true}}},
	}
	servers := m.LSPMgr.ServerHealthSnapshots()
	if len(servers) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-lsp", Label: "Language Servers", Header: true})
	}
	for _, server := range servers {
		primaryLabel := "Restart Server"
		if !server.Connected && !server.Starting {
			primaryLabel = "Start Server"
		}
		lines := []statusoverview.DetailLine{
			{Label: "State", Value: server.Status, Role: mapBool(server.Connected, ui.ColorSuccess, mapBool(server.Error != "", ui.ColorError, ui.ColorWarning))},
			{Label: "Root", Value: server.RootURI, Role: ui.ColorFg},
		}
		if server.Busy || server.Syncing {
			activity := mapBool(server.Busy, "Busy", "Syncing")
			lines = append(lines, statusoverview.DetailLine{Label: "Activity", Value: activity, Role: ui.ColorAccent})
		}
		if server.Errors+server.Warnings+server.Information+server.Hints > 0 {
			lines = append(lines, statusoverview.DetailLine{Label: "Problems", Value: fmt.Sprintf("%d errors · %d warnings · %d info · %d hints", server.Errors, server.Warnings, server.Information, server.Hints), Role: diagnosticRole(server.Errors, server.Warnings)})
		}
		if server.Error != "" {
			lines = append(lines, statusoverview.DetailLine{Label: "Error", Value: server.Error, Role: ui.ColorError})
		}
		items = append(items, statusoverview.Item{
			ID: "lsp:" + server.ID, Label: server.Name, Summary: server.Status, Icon: "◇",
			Owner: ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID},
			Lines: lines, PrimaryAction: "lsp:restart", PrimaryLabel: primaryLabel, PrimaryEnabled: true,
			Actions: []ttypes.StatusAction{{ID: "lsp:stop", Label: "Stop Server", Enabled: server.Connected || server.Starting}},
		})
	}
	diagnostics := m.LSPMgr.AllDiagnostics()
	if len(diagnostics) > 0 {
		items = append(items, statusoverview.Item{ID: "workspace-problems", Label: "Problems", Header: true})
		paths := make([]string, 0, len(diagnostics))
		for path := range diagnostics {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			for index, diagnostic := range diagnostics[path] {
				severity := "Hint"
				role := ui.ColorMuted
				switch diagnostic.Severity {
				case workspace.DiagError:
					severity, role = "Error", ui.ColorError
				case workspace.DiagWarning:
					severity, role = "Warning", ui.ColorWarning
				case workspace.DiagInfo:
					severity, role = "Information", ui.ColorMuted
				}
				action := fmt.Sprintf("diagnostic:%d:%d:%s", diagnostic.Line, diagnostic.Col, path)
				items = append(items, statusoverview.Item{
					ID:    fmt.Sprintf("problem:%s:%d:%d:%d", path, diagnostic.Line, diagnostic.Col, index),
					Label: diagnostic.Message, Summary: fmt.Sprintf("%s:%d · %s", filepath.Base(path), diagnostic.Line+1, severity), Icon: "!",
					Owner: ttypes.ResourceRef{Kind: ttypes.ResourceEditor, ID: path}, Subtitle: path,
					Lines:         []statusoverview.DetailLine{{Label: "Severity", Value: severity, Role: role}, {Label: "Location", Value: fmt.Sprintf("Ln %d, Col %d", diagnostic.Line+1, diagnostic.Col+1), Role: ui.ColorFg}, {Blank: true}, {Value: diagnostic.Message, Role: ui.ColorFg}},
					PrimaryAction: action, PrimaryLabel: "Open Problem", PrimaryEnabled: true,
				})
			}
		}
	}
	items = append(items,
		statusoverview.Item{ID: "workspace-services", Label: "Workspace Services", Header: true},
		m.gitStatusOverviewItem(),
		statusoverview.Item{ID: "workspace:history", Label: "Local History", Summary: mapBool(m.LocalHistory != nil, "Available", "Unavailable"), Icon: "↶", Owner: ttypes.ResourceRef{Kind: ttypes.ResourceHistory, ID: m.RootPath}, PrimaryAction: "history:open", PrimaryLabel: "Open History", PrimaryEnabled: m.LocalHistory != nil, DisabledReason: "Local history is unavailable"},
		statusoverview.Item{ID: "workspace:memory", Label: "Memory", Summary: mapBool(memoryAvailable, "Available", "Disabled"), Icon: "◈", Owner: ttypes.ResourceRef{Kind: ttypes.ResourceMemory, ID: m.RootPath}, PrimaryAction: "memory:open", PrimaryLabel: "Open Memory", PrimaryEnabled: memoryAvailable, DisabledReason: "Memory is disabled"},
	)
	for _, issue := range m.Status.Issues() {
		items = append(items, statusoverview.Item{ID: "issue:" + issue.ID, Label: issue.Summary, Summary: issue.Detail, Icon: "!", Owner: issue.Owner, Lines: []statusoverview.DetailLine{{Label: "Issue", Value: issue.Detail, Role: severityRole(issue.Severity)}}})
	}
	return items
}

func (m *App) gitStatusOverviewItem() statusoverview.Item {
	item := statusoverview.Item{ID: "workspace:git", Label: "Source Control", Icon: activitybar.ViewGit.Icon(panelchrome.NerdFont(m.Theme)), Owner: ttypes.ResourceRef{Kind: ttypes.ResourceGit, ID: m.RootPath}, PrimaryAction: "git:open", PrimaryLabel: "Open Source Control", PrimaryEnabled: true}
	if m.sourceControl == nil {
		item.Summary = "Not loaded"
		return item
	}
	snapshot := m.sourceControl.Snapshot()
	item.Summary = firstNonempty(snapshot.Branch, "No repository")
	item.Lines = []statusoverview.DetailLine{
		{Label: "Repository", Value: firstNonempty(snapshot.RepositoryRoot, m.RootPath), Role: ui.ColorFg},
		{Label: "Branch", Value: firstNonempty(snapshot.Branch, "Detached"), Role: ui.ColorFg},
		{Label: "Changes", Value: fmt.Sprintf("%d", len(snapshot.Changes)), Role: mapBool(len(snapshot.Changes) > 0, ui.ColorWarning, ui.ColorMuted)},
	}
	if snapshot.Upstream != "" {
		item.Lines = append(item.Lines, statusoverview.DetailLine{Label: "Upstream", Value: snapshot.Upstream, Role: ui.ColorMuted}, statusoverview.DetailLine{Label: "Tracking", Value: fmt.Sprintf("↑%d ↓%d", snapshot.Ahead, snapshot.Behind), Role: ui.ColorMuted})
	}
	if snapshot.Head != "" {
		item.Lines = append(item.Lines, statusoverview.DetailLine{Label: "HEAD", Value: snapshot.Head, Role: ui.ColorMuted})
	}
	return item
}

func (m *App) statusActivityItems() []statusoverview.Item {
	items := []statusoverview.Item{{ID: "activity-active", Label: "Active", Header: true}}
	completedHeader := false
	for _, operation := range m.Status.Operations() {
		ended := operation.State == ttypes.OperationSucceeded || operation.State == ttypes.OperationFailed || operation.State == ttypes.OperationCancelled
		if ended && !completedHeader {
			items = append(items, statusoverview.Item{ID: "activity-recent", Label: "Recent", Header: true})
			completedHeader = true
		}
		durationEnd := time.Now()
		if !operation.EndedAt.IsZero() {
			durationEnd = operation.EndedAt
		}
		lines := []statusoverview.DetailLine{
			{Label: "State", Value: string(operation.State), Role: operationRole(operation.State)},
			{Label: "Owner", Value: string(operation.Owner.Kind) + " · " + operation.Owner.ID, Role: ui.ColorFg},
			{Label: "Duration", Value: formatDuration(durationEnd.Sub(operation.StartedAt)), Role: ui.ColorMuted},
		}
		if operation.Total > 0 {
			lines = append(lines, statusoverview.DetailLine{Label: "Progress", Value: fmt.Sprintf("%d / %d %s", operation.Progress, operation.Total, operation.Unit), Role: ui.ColorFg})
		}
		if operation.Error != "" {
			lines = append(lines, statusoverview.DetailLine{Label: "Error", Value: operation.Error, Role: ui.ColorError})
		}
		items = append(items, statusoverview.Item{ID: "operation:" + operation.ID, Label: operation.Label, Summary: string(operation.State), Icon: operationIcon(operation.State), Owner: operation.Owner, Lines: lines})
	}
	return items
}

func (m *App) statusNotificationItems() []statusoverview.Item {
	items := []statusoverview.Item{{ID: "notices", Label: "Notifications", Header: true}}
	for _, notice := range m.Status.Notifications(false) {
		state := "Read"
		if !notice.Read {
			state = "Unread"
		}
		if !notice.ResolvedAt.IsZero() {
			state += " · Resolved"
		}
		items = append(items, statusoverview.Item{
			ID: "notice:" + notice.ID, Label: notice.Summary, Summary: state + " · " + relativeTime(notice.UpdatedAt), Icon: severityIcon(notice.Severity), Owner: notice.Owner,
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
		})
	}
	return items
}

func (m *App) handleStatusOverviewNavigate(msg statusoverview.NavigateMsg) tea.Cmd {
	action := msg.Action
	switch {
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
	case action == "settings:workspace":
		m.openWorkspaceSettings()
		return nil
	case action == "git:open":
		return m.toggleActivity(activityGit)
	case action == "history:open":
		return m.openLocalHistory()
	case action == "memory:open":
		_, _ = m.toggleMemoryBrowser()
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
	m.overlayHost.Close(ui.OverlayStatusOverview)
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
	label := state.Detail
	if label == "" {
		label = strings.ReplaceAll(string(state.Phase), "_", " ")
	}
	if !state.PhaseStartedAt.IsZero() && (state.Phase == ttypes.AgentWaiting || state.Phase == ttypes.AgentThinking || state.Phase == ttypes.AgentResponding || state.Phase == ttypes.AgentTool) {
		label += " · " + formatDuration(time.Since(state.PhaseStartedAt))
	}
	if state.Phase == ttypes.AgentUploading && state.ProgressKnown {
		label += fmt.Sprintf(" · %d%%", state.Progress)
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

func operationRole(state ttypes.OperationState) ui.SemanticColor {
	switch state {
	case ttypes.OperationFailed:
		return ui.ColorError
	case ttypes.OperationCancelled:
		return ui.ColorWarning
	case ttypes.OperationSucceeded:
		return ui.ColorSuccess
	default:
		return ui.ColorFg
	}
}

func operationIcon(state ttypes.OperationState) string {
	switch state {
	case ttypes.OperationFailed:
		return "×"
	case ttypes.OperationCancelled:
		return "-"
	case ttypes.OperationSucceeded:
		return "✓"
	default:
		return "●"
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
