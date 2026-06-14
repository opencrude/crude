package widgets

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"crude/fileio"
	"crude/tui/components"
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
	theme  *theme.Theme
	width  int

	agentStates  map[string]*agentStatus
	currentAgent string

	thinking, streaming, blocking bool
	isConfirming                   bool
	spinner    components.Spinner
	statusLine string
	statusPhase string
	btwCount   int

	lastLabelChange time.Time
	nextLabelMinGap time.Duration

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost   float64
	contextWindow int
	contextUsed   int
	tokenEstimated bool

	lastError   string
	errorTime   time.Time
	infoMsg     string
	infoMsgTime time.Time
	retryMsg    string
	retryTime   time.Time

	asyncCommands []fileio.AsyncCmdInfo

	spinFrame     int
	activityStep  int

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
	dim := lipgloss.NewStyle().Foreground(s.theme.Colors.Muted)
	pad := strings.Repeat(" ", s.theme.Spacing.PadLR)
	sys := s.computeSystemState()
	phaseTag := s.currentAgentPhase()

	var left string
	if s.lastError != "" && time.Since(s.errorTime) < 12*time.Second {
		left = s.theme.Styles.ErrorText.Render(
			rendering.TruncateLine(s.lastError, s.width/2))
	} else if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		left = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render(
			rendering.TruncateLine(s.retryMsg, s.width/2))
	} else if s.infoMsg != "" && time.Since(s.infoMsgTime) < 5*time.Second && !sys.anyStreaming && !sys.anyThinking {
		left = lipgloss.NewStyle().Foreground(s.theme.Colors.Success).Render(
			rendering.TruncateLine(s.infoMsg, s.width/2))
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

	lspParts := ""
	hasLSPDiag := s.diagErrors > 0 || s.diagWarnings > 0
	if s.lspVisible && hasLSPDiag {
		var parts []string
		if s.diagErrors > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(s.theme.Colors.Error).Render(fmt.Sprintf("✕%d", s.diagErrors)))
		}
		if s.diagWarnings > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(s.theme.Colors.Warning).Render(fmt.Sprintf("⚠%d", s.diagWarnings)))
		}
		lspParts = " " + strings.Join(parts, " ")
	} else if s.lspVisible && s.lspStatus != "" {
		lspParts = " " + dim.Render(s.lspStatus)
	} else if hasLSPDiag {
		var parts []string
		if s.diagErrors > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(s.theme.Colors.Error).Render(fmt.Sprintf("✕%d", s.diagErrors)))
		}
		if s.diagWarnings > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(s.theme.Colors.Warning).Render(fmt.Sprintf("⚠%d", s.diagWarnings)))
		}
		lspParts = " " + strings.Join(parts, " ")
	}
	left += lspParts

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

	spacer := s.width - s.theme.Spacing.PadLR - rendering.VisibleWidth(left) - rendering.VisibleWidth(right) - s.theme.Spacing.PadLR
	if spacer < 0 {
		spacer = 0
	}

	return left + strings.Repeat(" ", spacer) + right + pad
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
