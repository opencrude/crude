package shellstatus

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
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

type Screen struct {
	ui.Base
	theme  *theme.Theme
	tokens *ui.TokenSet
	width  int
	hint   *ui.Hint

	agentStates  map[string]*agentStatus
	currentAgent string

	thinking, streaming, blocking bool
	isConfirming                  bool
	spinner                       ui.Spinner
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
}

// New creates a status screen.
func New(t *theme.Theme) *Screen {
	if t == nil {
		t = theme.DefaultDark()
	}
	return &Screen{
		Base:        ui.NewBase("shellstatus"),
		theme:       t,
		tokens:      ui.NewTokenSet(t),
		spinner:     ui.NewSpinner(t),
		agentStates: make(map[string]*agentStatus),
		hint:        ui.NewHint("shellstatus/hint", nil, ui.NewTokenSet(t)),
	}
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.hint != nil {
		s.hint.SetTokens(tokens)
	}
}

// SetTheme replaces the theme used for spinner colors and spacing.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	frame := s.spinFrame
	step := s.activityStep
	phase := s.statusPhase
	s.theme = th
	s.tokens = ui.NewTokenSet(th)
	s.spinner = ui.NewSpinner(th)
	s.spinner.SetFrame(frame)
	s.spinner.SetActivityStep(step)
	s.SetPhase(phase)
	if s.hint != nil {
		s.hint.SetTokens(s.tokens)
	}
}

// SetContextHelp replaces the right-side contextual hint segments.
func (s *Screen) SetContextHelp(reg *ui.ActionRegistry, prefix string, segs []ui.HintSegment) {
	if s.hint == nil {
		s.hint = ui.NewHint("shellstatus/hint", reg, s.tokens)
	}
	s.hint.SetRegistry(reg)
	s.hint.SetTokens(s.tokens)
	s.hint.SetPrefix(prefix)
	s.hint.SetSegments(segs)
}

// ContextHelpPlain returns flattened contextual help for clipboard/tests.
func (s *Screen) ContextHelpPlain() string {
	if s.hint == nil {
		return ""
	}
	return s.hint.PlainText()
}

func (s *Screen) Children() []ui.Component { return nil }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := 2
	if cs.MaxH > 0 && cs.MaxH < h {
		h = cs.MaxH
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	if r.W > 0 {
		s.width = r.W
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil {
		return
	}
	pad := 0
	if s.theme != nil {
		pad = s.theme.Spacing.PadLR
	}
	border := s.color(ui.ColorBorder)
	buf.WriteString(ox+b.X, oy+b.Y, strings.Repeat(string(ui.FirstRune(s.tokens.Glyphs().BorderH, '─')), max(0, b.W)), border, cell.NoColor, 0)

	left := s.activitySegments()
	hintW := 0
	if s.hint != nil {
		hintW = s.hint.VisibleWidth()
	}
	available := b.W - 2*pad
	if available < 0 {
		available = 0
	}
	if hintW > available {
		hintW = available
	}
	leftBudget := available - hintW
	if hintW > 0 {
		leftBudget -= 2
	}
	if leftBudget < 0 {
		leftBudget = 0
	}
	left = truncateSegs(left, leftBudget)

	y := oy + b.Y + 1
	x := ox + b.X + pad
	writeSegs(buf, x, y, left, s)
	if s.hint != nil && hintW > 0 {
		hx := ox + b.X + pad + available - hintW
		s.hint.PaintInto(buf, hx, y, hintW)
	}
}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: "Status", Value: s.ActivityView()}
}

func (s *Screen) color(role ui.SemanticColor) cell.Color {
	if s.tokens != nil {
		return s.tokens.Color(role)
	}
	return cell.NoColor
}

func (s *Screen) computeSystemState() systemState {
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

func (s *Screen) IsSystemBusy() bool {
	st := s.computeSystemState()
	return st.activeCount > 0 || s.blocking || s.isConfirming
}

func (s *Screen) ActiveAgentCount() int {
	return s.computeSystemState().activeCount
}

func (s *Screen) SleepingAgentCount() int {
	return s.computeSystemState().sleepingCnt
}

func (s *Screen) SetCurrentAgent(agentID string) {
	s.currentAgent = agentID
}

func (s *Screen) currentAgentPhase() string {
	if s.currentAgent == "" {
		return ""
	}
	st := s.getAgentState(s.currentAgent)
	if st == nil {
		return ""
	}
	return st.phase
}

func (s *Screen) View() string {
	left, right := s.viewParts()
	pad := strings.Repeat(" ", s.theme.Spacing.PadLR)
	spacer := s.width - s.theme.Spacing.PadLR - rendering.VisibleWidth(left) - rendering.VisibleWidth(right) - s.theme.Spacing.PadLR
	if spacer < 0 {
		spacer = 0
	}
	return left + strings.Repeat(" ", spacer) + right + pad
}

// ActivityView is the left-hand project and agent state shown in the bottom bar.
func (s *Screen) ActivityView() string {
	left, _ := s.viewParts()
	return left
}

// UsageView is the compact readiness/token/cost state shown in the header.
func (s *Screen) UsageView() string {
	_, right := s.viewParts()
	return right
}

type seg struct {
	text string
	role ui.SemanticColor
	fg   cell.Color
	attr cell.Attrs
	raw  bool // use fg instead of role
}

func (s *Screen) viewParts() (string, string) {
	left := segsText(s.activitySegments())
	right := segsText(s.usageSegments())
	return left, right
}

func (s *Screen) activitySegments() []seg {
	sys := s.computeSystemState()
	phaseTag := s.currentAgentPhase()
	var left []seg

	if s.lastError != "" && time.Since(s.errorTime) < 12*time.Second {
		left = append(left, seg{text: rendering.TruncateLine(s.lastError, s.width/3), role: ui.ColorError})
	} else if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		left = append(left, seg{text: rendering.TruncateLine(s.retryMsg, s.width/3), role: ui.ColorWarning})
	} else if s.infoMsg != "" && time.Since(s.infoMsgTime) < 5*time.Second && !sys.anyStreaming && !sys.anyThinking {
		left = append(left, seg{text: rendering.TruncateLine(s.infoMsg, s.width/3), role: ui.ColorSuccess})
	} else if s.isConfirming {
		left = append(left, seg{text: "⚠ confirming", role: ui.ColorWarning})
	} else if s.blocking {
		left = append(left, seg{text: "⚠ awaiting input", role: ui.ColorWarning})
	} else if sys.anyBlocking {
		left = append(left, seg{text: "⚠ sub-agent waiting", role: ui.ColorWarning})
	} else if sys.anyStreaming {
		moon, moonClr, label, labelClr := s.spinner.StreamPlain()
		left = append(left,
			seg{text: moon + " ", fg: rendering.ThemeColorToCell(moonClr), raw: true},
			seg{text: label, fg: rendering.ThemeColorToCell(labelClr), raw: true},
		)
		if phaseTag != "" {
			left = append(left, seg{text: " " + phaseTag, role: ui.ColorMuted})
		}
	} else if sys.anyThinking {
		moon, moonClr, label, labelClr := s.spinner.WorkPlain()
		left = append(left,
			seg{text: moon + " ", fg: rendering.ThemeColorToCell(moonClr), raw: true},
			seg{text: label, fg: rendering.ThemeColorToCell(labelClr), raw: true},
		)
		if phaseTag != "" {
			left = append(left, seg{text: " " + phaseTag, role: ui.ColorMuted})
		}
	} else if sys.sleepingCnt > 0 {
		left = append(left, seg{text: fmt.Sprintf("💤%d", sys.sleepingCnt), role: ui.ColorMuted})
		if s.btwCount > 0 {
			left = append(left, seg{text: fmt.Sprintf(" 📝%d", s.btwCount), role: ui.ColorMuted})
		}
	}

	if sys.activeCount > 0 {
		left = append(left, seg{text: " ⚡" + strconv.Itoa(sys.activeCount), role: ui.ColorAccent2})
	}

	lsp := s.lspSegments()
	if len(lsp) > 0 {
		if len(left) > 0 {
			left = append(left, seg{text: "  │  ", role: ui.ColorMuted})
		}
		left = append(left, lsp...)
	}
	return left
}

func (s *Screen) usageSegments() []seg {
	sys := s.computeSystemState()
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
		return []seg{{text: strings.Join(rightParts, " │ "), role: ui.ColorMuted}}
	}
	text := "ready"
	if sys.activeCount > 0 {
		if sys.anyStreaming {
			text = "streaming"
		} else if sys.anyThinking {
			text = "processing"
		} else {
			text = "busy"
		}
	}
	return []seg{{text: text, role: ui.ColorMuted}}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// lspSegments builds whole-project LSP status segments.
//
// Layout:  ● gopls  ✕5 ⚠12 ℹ3  ·  8 files  ·  247 symbols
func (s *Screen) lspSegments() []seg {
	if s.project.ServerName == "" && !s.project.ServerFailed && !s.project.ServerStarting {
		return nil
	}

	showAnimation := s.project.ServerStarting || s.project.ServerBusy || s.project.ServerSyncing
	if !showAnimation && s.project.LastBusyAt > 0 {
		elapsed := time.Since(time.Unix(0, s.project.LastBusyAt))
		showAnimation = elapsed < editor.AnimGrace
	}

	var (
		dotRole  = ui.ColorSuccess
		dotState = "ready"
	)
	switch {
	case s.project.ServerFailed:
		dotRole = ui.ColorError
		dotState = "failed"
	case s.project.ServerStarting:
		dotRole = ui.ColorWarning
		dotState = "starting"
	case s.project.ServerBusy:
		dotRole = ui.ColorAccent
		dotState = "busy"
	case s.project.ServerSyncing:
		dotRole = ui.ColorAccent2
		dotState = "syncing"
	case showAnimation:
		dotRole = ui.ColorAccent2
		dotState = "syncing"
	default:
		dotRole = ui.ColorSuccess
		dotState = "ready"
	}
	glyph, bold, faint := ui.HeartbeatDotPlain(s.spinFrame, dotState)
	attr := cell.Attrs(0)
	if bold {
		attr |= cell.AttrBold
	}
	if faint {
		attr |= cell.AttrDim
	}
	dot := seg{text: glyph, role: dotRole, attr: attr}

	if s.project.ServerFailed && s.project.ServerError != "" {
		return []seg{dot, seg{text: " ", role: ui.ColorMuted}, seg{text: s.project.ServerError, role: ui.ColorError, attr: cell.AttrBold}}
	}

	out := []seg{dot}
	if s.project.ServerName != "" {
		out = append(out, seg{text: " ", role: ui.ColorMuted}, seg{text: s.project.ServerName, role: ui.ColorFg, attr: cell.AttrBold})
	}
	if s.project.Errors+s.project.Warnings+s.project.Info+s.project.Hints > 0 {
		out = append(out, seg{text: " ", role: ui.ColorMuted})
		out = append(out, diagSegs(s.project)...)
	}
	if s.project.FilesAffected > 0 {
		out = append(out, seg{text: " · ", role: ui.ColorMuted},
			seg{text: fmt.Sprintf("%d file%s", s.project.FilesAffected, pluralS(s.project.FilesAffected)), role: ui.ColorMuted})
	}
	if s.project.SymbolsCount > 0 {
		out = append(out, seg{text: " · ", role: ui.ColorMuted},
			seg{text: fmt.Sprintf("%d %s", s.project.SymbolsCount, pluralSym(s.project.SymbolsCount)), role: ui.ColorMuted})
	}
	return out
}

func diagSegs(p editor.ProjectInsights) []seg {
	var parts []seg
	if p.Errors > 0 {
		parts = append(parts, seg{text: fmt.Sprintf("✕%d", p.Errors), role: ui.ColorError})
	}
	if p.Warnings > 0 {
		if len(parts) > 0 {
			parts = append(parts, seg{text: " ", role: ui.ColorMuted})
		}
		parts = append(parts, seg{text: fmt.Sprintf("⚠%d", p.Warnings), role: ui.ColorWarning})
	}
	if p.Info > 0 {
		if len(parts) > 0 {
			parts = append(parts, seg{text: " ", role: ui.ColorMuted})
		}
		parts = append(parts, seg{text: fmt.Sprintf("ℹ%d", p.Info), role: ui.ColorAccent2})
	}
	if p.Hints > 0 {
		if len(parts) > 0 {
			parts = append(parts, seg{text: " ", role: ui.ColorMuted})
		}
		parts = append(parts, seg{text: fmt.Sprintf("·%d", p.Hints), role: ui.ColorMuted})
	}
	return parts
}

func segsText(segs []seg) string {
	var b strings.Builder
	for _, sg := range segs {
		b.WriteString(sg.text)
	}
	return b.String()
}

func writeSegs(buf *surface.CellBuffer, x, y int, segs []seg, s *Screen) {
	for _, sg := range segs {
		if sg.text == "" {
			continue
		}
		fg := sg.fg
		if !sg.raw {
			fg = s.color(sg.role)
		}
		buf.WriteString(x, y, sg.text, fg, cell.NoColor, sg.attr)
		x += rendering.VisibleWidth(sg.text)
	}
}

func truncateSegs(segs []seg, maxW int) []seg {
	if maxW <= 0 {
		return nil
	}
	var out []seg
	used := 0
	for _, sg := range segs {
		tw := rendering.VisibleWidth(sg.text)
		if used+tw <= maxW {
			out = append(out, sg)
			used += tw
			continue
		}
		remain := maxW - used
		if remain <= 0 {
			break
		}
		sg.text = rendering.TruncateLine(sg.text, remain)
		out = append(out, sg)
		break
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func (s *Screen) SetConfirming(c bool) {
	s.isConfirming = c
}

func (s *Screen) SetSpinFrame(frame int) {
	s.spinFrame = frame
	s.spinner.SetFrame(frame)
	s.spinner.SetActivityStep(s.activityStep)
}

func (s *Screen) RecordActivity() {
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

func (s *Screen) ResetActivity() {
	s.activityStep = 0
	s.spinner.SetActivityStep(0)
	s.lastLabelChange = time.Time{}
	s.nextLabelMinGap = 0
}

func (s *Screen) randLabelGap() time.Duration {
	const lo = 3
	const hi = 7
	return time.Duration(lo+randInt(hi-lo+1)) * time.Second
}

func (s *Screen) SetError(err string) {
	s.lastError = err
	s.errorTime = time.Now()
}

func (s *Screen) SetInfo(msg string) {
	s.infoMsg = msg
	s.infoMsgTime = time.Now()
}

func (s *Screen) SetRetry(msg string) {
	s.retryMsg = msg
	s.retryTime = time.Now()
}

func (s *Screen) ClearRetry() {
	s.retryMsg = ""
	s.retryTime = time.Time{}
}

func (s *Screen) ClearExpired() {
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

func (s *Screen) UpdateUsage(prompt, completion, cached int, cost float64, ctxWindow, ctxUsed int, estimated bool) {
	s.tokenPrompt = prompt
	s.tokenCompletion = completion
	s.tokenCached = cached
	s.sessionCost = cost
	s.contextWindow = ctxWindow
	s.contextUsed = ctxUsed
	s.tokenEstimated = estimated
}

func (s *Screen) SetThinking(thinking, streaming, blocking bool) {
	s.thinking = thinking
	s.streaming = streaming
	s.blocking = blocking
}

func (s *Screen) SetThinkingForAgent(agentID string, thinking, streaming, blocking bool) {
	st := s.getAgentState(agentID)
	st.thinking = thinking
	st.streaming = streaming
	st.blocking = blocking
}

func (s *Screen) SetPhaseForAgent(agentID, phase string) {
	st := s.getAgentState(agentID)
	st.phase = phase
}

func (s *Screen) SetStatusLineForAgent(agentID, line string) {
	st := s.getAgentState(agentID)
	st.statusLine = line
}

func (s *Screen) RecordActivityForAgent(agentID string) {
	s.RecordActivity()
}

func (s *Screen) getAgentState(agentID string) *agentStatus {
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

func (s *Screen) AgentStatusView(agentID string) string {
	if agentID == "" {
		return ""
	}
	st := s.getAgentState(agentID)
	if st == nil {
		return ""
	}
	if st.streaming {
		moon, _, label, _ := s.spinner.StreamPlain()
		return moon + " " + label
	}
	if st.thinking {
		moon, _, label, _ := s.spinner.WorkPlain()
		return moon + " " + label
	}
	return ""
}

func (s *Screen) ClearAgentState(agentID string) {
	delete(s.agentStates, agentID)
}

func (s *Screen) SetPhase(phase string) {
	s.statusPhase = phase
	switch phase {
	case "grounding":
		s.spinner.SetPhase(ui.PhaseGrounding)
	case "planning":
		s.spinner.SetPhase(ui.PhasePlanning)
	case "executing":
		s.spinner.SetPhase(ui.PhaseExecuting)
	default:
		s.spinner.SetPhase(ui.PhaseDefault)
	}
}

func (s *Screen) SetWidth(w int) {
	s.width = w
}

func (s *Screen) SetStatusLine(line string) {
	s.statusLine = line
}

func (s *Screen) StatusLine() string {
	return s.statusLine
}

func (s *Screen) Phase() string {
	return s.statusPhase
}

func (s *Screen) BtwCount() int {
	return s.btwCount
}

func (s *Screen) SetBtwCount(n int) {
	s.btwCount = n
}

func (s *Screen) SetDiagnostics(errors, warnings, hints int) {
	s.diagErrors = errors
	s.diagWarnings = warnings
	s.diagHints = hints
}

func (s *Screen) SetLSPVisible(v bool) {
	s.lspVisible = v
}

func (s *Screen) SetLSPStatus(status string) {
	s.lspStatus = status
}

// SetProjectInsights updates the whole-project LSP snapshot rendered in the
// bottom-left of the status bar. This is the canonical entry point — the
// legacy SetDiagnostics/SetLSPVisible/SetLSPStatus methods are kept only for
// compatibility with older callers and are NOT consulted by View() once this
// method has been called at least once.
func (s *Screen) SetProjectInsights(p editor.ProjectInsights) {
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
func (s *Screen) HasAnimation() bool {
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
