package widgets

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"crude/tui/components"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"

	"charm.land/lipgloss/v2"
)

type agentStatus struct {
	thinking   bool
	streaming  bool
	blocking   bool
	phase      string
	statusLine string
}

type systemState struct {
	anyStreaming bool
	anyThinking  bool
	anyBlocking  bool
	activeCount  int
	sleepingCnt  int
}

type StatusBar struct {
	theme *theme.Theme
	width int

	agentStates  map[string]*agentStatus
	currentAgent string

	thinking, streaming, blocking bool
	isConfirming                  bool
	spinner                       components.Spinner
	statusLine                    string
	statusPhase                   string
	btwCount                      int

	lastLabelChange time.Time
	nextLabelMinGap time.Duration

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost                               float64
	contextWindow                             int
	contextUsed                               int
	tokenEstimated                            bool

	lastError   string
	errorTime   time.Time
	infoMsg     string
	infoMsgTime time.Time
	retryMsg    string
	retryTime   time.Time

	spinFrame    int
	activityStep int

	// Whole-project LSP insights. The bottom-left is reserved for these:
	// diagnostic counts, files affected, symbol count, server health.
	// Per-file diagnostics live in the editor (gutter + hover + status line).
	project editor.ProjectInsights
	// Legacy fields kept for backwards compatibility with older callers.
	diagErrors   int
	diagWarnings int
	diagHints    int
	lspVisible   bool
	lspStatus    string
	lspError     string
}

func NewStatusBar(t *theme.Theme) *StatusBar {
	return &StatusBar{theme: t, spinner: components.NewSpinner(t), agentStates: make(map[string]*agentStatus)}
}

func (s *StatusBar) computeSystemState() systemState {
	state := systemState{}
	for _, a := range s.agentStates {
		if a.streaming {
			state.anyStreaming = true
			state.activeCount++
		} else if a.thinking {
			state.anyThinking = true
			state.activeCount++
		} else {
			state.sleepingCnt++
		}
		if a.blocking {
			state.anyBlocking = true
		}
	}
	if state.activeCount == 0 && state.sleepingCnt == 0 {
		if s.streaming {
			state.anyStreaming = true
			state.activeCount = 1
		}
		if s.thinking {
			state.anyThinking = true
			if !state.anyStreaming {
				state.activeCount = 1
			}
		}
	}
	return state
}

func (s *StatusBar) IsSystemBusy() bool {
	st := s.computeSystemState()
	return st.activeCount > 0 || s.blocking || s.isConfirming
}

func (s *StatusBar) ActiveAgentCount() int {
	return s.computeSystemState().activeCount
}

func (s *StatusBar) SleepingAgentCount() int {
	return s.computeSystemState().sleepingCnt
}

func (s *StatusBar) SetCurrentAgent(agentID string) {
	s.currentAgent = agentID
}

func (s *StatusBar) currentAgentPhase() string {
	if s.currentAgent == "" {
		return ""
	}
	st := s.getAgentState(s.currentAgent)
	if st == nil {
		return ""
	}
	return st.phase
}

func (s *StatusBar) currentAgentStatusLine() string {
	if s.currentAgent == "" {
		return ""
	}
	st := s.getAgentState(s.currentAgent)
	if st == nil {
		return ""
	}
	return st.statusLine
}

func (s *StatusBar) View() string {
	left, right := s.viewParts()
	pad := strings.Repeat(" ", s.theme.Spacing.PadLR)
	spacer := s.width - s.theme.Spacing.PadLR - rendering.VisibleWidth(left) - rendering.VisibleWidth(right) - s.theme.Spacing.PadLR
	if spacer < 0 {
		spacer = 0
	}
	return left + strings.Repeat(" ", spacer) + right + pad
}

// ActivityView is the left-hand project and agent state shown in the bottom bar.
func (s *StatusBar) ActivityView() string {
	left, _ := s.viewParts()
	return left
}

// UsageView is the compact readiness/token/cost state shown in the header.
func (s *StatusBar) UsageView() string {
	_, right := s.viewParts()
	return right
}

func (s *StatusBar) viewParts() (string, string) {
	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	sys := s.computeSystemState()
	phaseTag := s.currentAgentPhase()

	var left string
	if s.lastError != "" && time.Since(s.errorTime) < 12*time.Second {
		left = s.theme.Styles.ErrorText.Render(
			rendering.TruncateLine(s.lastError, s.width/3))
	} else if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render(
			rendering.TruncateLine(s.retryMsg, s.width/3))
	} else if s.infoMsg != "" && time.Since(s.infoMsgTime) < 5*time.Second && !sys.anyStreaming && !sys.anyThinking {
		left = lipgloss.NewStyle().Foreground(s.theme.Colors.Success).Render(
			rendering.TruncateLine(s.infoMsg, s.width/3))
	} else if s.isConfirming {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("⚠ confirming")
	} else if s.blocking {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("⚠ awaiting input")
	} else if sys.anyBlocking {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render("⚠ sub-agent waiting")
	} else if sys.anyStreaming {
		left = s.spinner.StreamAnimation()
		if phaseTag != "" {
			left += " " + dim.Render(phaseTag)
		}
	} else if sys.anyThinking {
		left = s.spinner.WorkAnimation()
		if phaseTag != "" {
			left += " " + dim.Render(phaseTag)
		}
	} else if sys.sleepingCnt > 0 {
		left = dim.Render(fmt.Sprintf("💤%d", sys.sleepingCnt))
		if s.btwCount > 0 {
			left += dim.Render(fmt.Sprintf(" 📝%d", s.btwCount))
		}
	} else {
		left = ""
	}

	if sys.activeCount > 0 {
		accent := lipgloss.NewStyle().Foreground(s.theme.Colors.Accent2)
		left += " " + accent.Render("⚡"+strconv.Itoa(sys.activeCount))
	}

	// Whole-project LSP insights (bottom-left)             ─
	lspView := s.renderLSPInsights()

	// Compose left side: agent activity, separator, LSP insights.
	// The LSP section is preserved for project-wide insight only — per-file
	// diagnostics live in the editor (gutter, hover popup, status line).
	if lspView != "" {
		if left != "" {
			left += "  " + dim.Render("│") + "  " + lspView
		} else {
			left = lspView
		}
	}

	right := ""
	var rightParts []string
	if s.btwCount > 0 {
		rightParts = append(rightParts, fmt.Sprintf("📝%d", s.btwCount))
	}
	if s.tokenPrompt > 0 || s.tokenCompletion > 0 {
		prefix := ""
		if s.tokenEstimated {
			prefix = "~"
		}
		var parts []string
		parts = append(parts, fmt.Sprintf("%s↑%s", prefix, rendering.FormatTokens(s.tokenPrompt)))
		if s.tokenCached > 0 {
			parts = append(parts, rendering.FormatTokens(s.tokenCached))
		}
		parts = append(parts, fmt.Sprintf("↓%s", rendering.FormatTokens(s.tokenCompletion)))
		if s.contextWindow > 0 {
			percent := int(float64(s.contextUsed) / float64(s.contextWindow) * 100)
			parts = append(parts, fmt.Sprintf("%d%%", percent))
		}
		if s.sessionCost > 0 {
			parts = append(parts, rendering.FormatCost(s.sessionCost))
		}
		rightParts = append(rightParts, strings.Join(parts, " "))
	}
	if len(rightParts) > 0 {
		right = dim.Render(strings.Join(rightParts, " │ "))
	} else {
		if sys.activeCount > 0 {
			if sys.anyStreaming {
				right = dim.Render("streaming")
			} else if sys.anyThinking {
				right = dim.Render("processing")
			} else {
				right = dim.Render("busy")
			}
		} else {
			right = dim.Render("ready")
		}
	}

	return left, right
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderLSPInsights renders the whole-project LSP section of the status bar.
//
// Layout:  ● gopls  ✕5 ⚠12 ℹ3  ·  8 files  ·  247 symbols
//
// The dot's color and animation reflect server health: solid green when
// ready, fast pulse when busy, alternating halves when starting, red cross
// when failed.
//
// IMPORTANT: The animation always lasts at least AnimGrace after the last
// busines event, even if the instantaneous state is "ready". This prevents
// the common case where the LSP boots and syncs between 400ms ticks —
// without the grace period the user would never see any blink.
func (s *StatusBar) renderLSPInsights() string {
	if s.project.ServerName == "" && !s.project.ServerFailed && !s.project.ServerStarting {
		// No LSP server configured for this workspace.
		return ""
	}

	// Determine whether to show an animated dot. The instantaneous state
	// takes priority, but if it's "ready" we still animate for AnimGrace
	// after the last busy event so the user doesn't miss the blink.
	showAnimation := s.project.ServerStarting || s.project.ServerBusy || s.project.ServerSyncing
	if !showAnimation && s.project.LastBusyAt > 0 {
		elapsed := time.Since(time.Unix(0, s.project.LastBusyAt))
		showAnimation = elapsed < editor.AnimGrace
	}

	var (
		dotColor = s.theme.Colors.Success
		dotState = "ready"
	)
	switch {
	case s.project.ServerFailed:
		dotColor = s.theme.Colors.Error
		dotState = "failed"
	case s.project.ServerStarting:
		dotColor = s.theme.Colors.Warning
		dotState = "starting"
	case s.project.ServerBusy:
		dotColor = s.theme.Colors.Accent
		dotState = "busy"
	case s.project.ServerSyncing:
		dotColor = s.theme.Colors.Accent2
		dotState = "syncing"
	case showAnimation:
		// Grace period: the LSP is currently "ready" but was busy recently.
		// Keep the syncing animation alive for AnimGrace after the last
		// activity so the user sees the blink even though the server
		// transitioned to idle between ticks.
		dotColor = s.theme.Colors.Accent2
		dotState = "syncing"
	default:
		dotColor = s.theme.Colors.Success
		dotState = "ready"
	}
	dot := components.HeartbeatDot(s.spinFrame, dotState, dotColor)

	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	sep := dim.Render("·")

	// Error message overrides everything else when the server failed.
	if s.project.ServerFailed && s.project.ServerError != "" {
		return dot + " " + lipgloss.NewStyle().Foreground(s.theme.Colors.Error).Bold(true).Render(s.project.ServerError)
	}

	var parts []string
	parts = append(parts, dot)

	// Server name (skip if none configured).
	if s.project.ServerName != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(s.theme.Colors.Fg).Bold(true).Render(s.project.ServerName))
	}

	// Diagnostic counts (only if at least one).
	if s.project.Errors+s.project.Warnings+s.project.Info+s.project.Hints > 0 {
		parts = append(parts, renderDiagCounts(s.project, s.theme))
	}

	// Files affected (only when there are diagnostics).
	if s.project.FilesAffected > 0 {
		parts = append(parts, dim.Render(fmt.Sprintf("%d file%s", s.project.FilesAffected, pluralS(s.project.FilesAffected))))
	}

	// Symbol count (only if we've actually indexed something).
	if s.project.SymbolsCount > 0 {
		parts = append(parts, dim.Render(fmt.Sprintf("%d %s", s.project.SymbolsCount, pluralSym(s.project.SymbolsCount))))
	}

	return strings.Join(parts, " "+sep+" ")
}

func renderDiagCounts(p editor.ProjectInsights, t *theme.Theme) string {
	var parts []string
	if p.Errors > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Colors.Error).Render(fmt.Sprintf("✕%d", p.Errors)))
	}
	if p.Warnings > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Colors.Warning).Render(fmt.Sprintf("⚠%d", p.Warnings)))
	}
	if p.Info > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Colors.Accent2).Render(fmt.Sprintf("ℹ%d", p.Info)))
	}
	if p.Hints > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Colors.Muted).Render(fmt.Sprintf("·%d", p.Hints)))
	}
	return strings.Join(parts, " ")
}

func pluralSym(n int) string {
	if n == 1 {
		return "symbol"
	}
	return "symbols"
}

func randInt(n int) int {
	if n <= 1 {
		return 0
	}
	return rand.Intn(n)
}

func (s *StatusBar) confirming() bool {
	return s.isConfirming
}

func (s *StatusBar) SetConfirming(c bool) {
	s.isConfirming = c
}

func (s *StatusBar) SetSpinFrame(frame int) {
	s.spinFrame = frame
	s.spinner.SetFrame(frame)
	s.spinner.SetActivityStep(s.activityStep)
}

func (s *StatusBar) RecordActivity() {
	now := time.Now()
	if s.nextLabelMinGap == 0 {
		s.nextLabelMinGap = s.randLabelGap()
	}
	if now.Sub(s.lastLabelChange) >= s.nextLabelMinGap {
		s.activityStep++
		s.spinner.SetActivityStep(s.activityStep)
		s.lastLabelChange = now
		s.nextLabelMinGap = s.randLabelGap()
	}
}

func (s *StatusBar) ResetActivity() {
	s.activityStep = 0
	s.spinner.SetActivityStep(0)
	s.lastLabelChange = time.Time{}
	s.nextLabelMinGap = 0
}

func (s *StatusBar) randLabelGap() time.Duration {
	const lo = 3
	const hi = 7
	return time.Duration(lo+randInt(hi-lo+1)) * time.Second
}

func (s *StatusBar) SetError(err string) {
	s.lastError = err
	s.errorTime = time.Now()
}

func (s *StatusBar) SetInfo(msg string) {
	s.infoMsg = msg
	s.infoMsgTime = time.Now()
}

func (s *StatusBar) SetRetry(msg string) {
	s.retryMsg = msg
	s.retryTime = time.Now()
}

func (s *StatusBar) ClearRetry() {
	s.retryMsg = ""
	s.retryTime = time.Time{}
}

func (s *StatusBar) ClearExpired() {
	if time.Since(s.errorTime) > 12*time.Second {
		s.lastError = ""
	}
	if time.Since(s.infoMsgTime) > 5*time.Second {
		s.infoMsg = ""
	}
	if time.Since(s.retryTime) > 30*time.Second {
		s.retryMsg = ""
	}
}

func (s *StatusBar) UpdateUsage(prompt, completion, cached int, cost float64, ctxWindow, ctxUsed int, estimated bool) {
	s.tokenPrompt = prompt
	s.tokenCompletion = completion
	s.tokenCached = cached
	s.sessionCost = cost
	s.contextWindow = ctxWindow
	s.contextUsed = ctxUsed
	s.tokenEstimated = estimated
}

func (s *StatusBar) SetThinking(thinking, streaming, blocking bool) {
	s.thinking = thinking
	s.streaming = streaming
	s.blocking = blocking
}

func (s *StatusBar) SetThinkingForAgent(agentID string, thinking, streaming, blocking bool) {
	st := s.getAgentState(agentID)
	st.thinking = thinking
	st.streaming = streaming
	st.blocking = blocking
}

func (s *StatusBar) SetPhaseForAgent(agentID, phase string) {
	st := s.getAgentState(agentID)
	st.phase = phase
}

func (s *StatusBar) SetStatusLineForAgent(agentID, line string) {
	st := s.getAgentState(agentID)
	st.statusLine = line
}

func (s *StatusBar) RecordActivityForAgent(agentID string) {
	s.RecordActivity()
}

func (s *StatusBar) getAgentState(agentID string) *agentStatus {
	if agentID == "" {
		return nil
	}
	if s.agentStates == nil {
		s.agentStates = make(map[string]*agentStatus)
	}
	st, ok := s.agentStates[agentID]
	if !ok {
		st = &agentStatus{}
		s.agentStates[agentID] = st
	}
	return st
}

func (s *StatusBar) AgentStatusView(agentID string) string {
	if agentID == "" {
		return ""
	}
	st := s.getAgentState(agentID)
	if st == nil {
		return ""
	}
	if st.streaming {
		return s.spinner.StreamAnimation()
	}
	if st.thinking {
		return s.spinner.WorkAnimation()
	}
	return ""
}

func (s *StatusBar) ClearAgentState(agentID string) {
	delete(s.agentStates, agentID)
}

func (s *StatusBar) SetPhase(phase string) {
	s.statusPhase = phase
	switch phase {
	case "grounding":
		s.spinner.SetPhase(components.PhaseGrounding)
	case "planning":
		s.spinner.SetPhase(components.PhasePlanning)
	case "executing":
		s.spinner.SetPhase(components.PhaseExecuting)
	default:
		s.spinner.SetPhase(components.PhaseDefault)
	}
}

func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

func (s *StatusBar) SetStatusLine(line string) {
	s.statusLine = line
}

func (s *StatusBar) StatusLine() string {
	return s.statusLine
}

func (s *StatusBar) Phase() string {
	return s.statusPhase
}

func (s *StatusBar) BtwCount() int {
	return s.btwCount
}

func (s *StatusBar) SetBtwCount(n int) {
	s.btwCount = n
}

func (s *StatusBar) SetDiagnostics(errors, warnings, hints int) {
	s.diagErrors = errors
	s.diagWarnings = warnings
	s.diagHints = hints
}

func (s *StatusBar) SetLSPVisible(v bool) {
	s.lspVisible = v
}

func (s *StatusBar) SetLSPStatus(status string) {
	s.lspStatus = status
}

// SetProjectInsights updates the whole-project LSP snapshot rendered in the
// bottom-left of the status bar. This is the canonical entry point — the
// legacy SetDiagnostics/SetLSPVisible/SetLSPStatus methods are kept only for
// compatibility with older callers and are NOT consulted by View() once this
// method has been called at least once.
func (s *StatusBar) SetProjectInsights(p editor.ProjectInsights) {
	s.project = p
}

// HasAnimation reports whether the current state contains an element that
// must be re-rendered every tick. The view-cache uses this to decide
// whether to invalidate the cached output for a status-bar update.
//
// Returns true when the spinner is showing streaming/thinking work, when
// the LSP heartbeat dot is in an animated state or its AnimGrace period is
// still running, or when an info/retry/error toast is still on its display
// timer.
func (s *StatusBar) HasAnimation() bool {
	sys := s.computeSystemState()
	if sys.anyStreaming || sys.anyThinking {
		return true
	}
	// Heartbeat dot animates for these states; everything else is static.
	switch {
	case s.project.ServerStarting,
		s.project.ServerBusy,
		s.project.ServerSyncing:
		return true
	}
	// Grace period: even if the LSP is "ready", the dot animates for
	// AnimGrace after the last busy event. Without this, fast LSP
	// transitions between 400ms ticks would be invisible.
	if s.project.LastBusyAt > 0 {
		elapsed := time.Since(time.Unix(0, s.project.LastBusyAt))
		if elapsed < editor.AnimGrace {
			return true
		}
	}
	// Time-bounded toasts must be re-checked every tick to clear at
	// expiry; if any is still in its display window, the bar changed.
	if s.lastError != "" && time.Since(s.errorTime) < 12*time.Second {
		return true
	}
	if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		return true
	}
	if s.infoMsg != "" && time.Since(s.infoMsgTime) < 5*time.Second {
		return true
	}
	return false
}
