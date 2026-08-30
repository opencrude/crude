package shellstatus

import (
	"fmt"
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
	active    bool
	thinking  bool
	streaming bool
	blocking  bool
}

type systemState struct {
	anyStreaming bool
	anyThinking  bool
	anyBlocking  bool
	activeCount  int
	idleCount    int
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
	statusLine                    string
	statusPhase                   string
	btwCount                      int

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost                               float64
	costUpperBound                            bool
	contextWindow                             int
	contextUsed                               int
	tokenEstimated                            bool

	lastError   string
	errorTime   time.Time
	infoMsg     string
	infoMsgTime time.Time
	retryMsg    string
	retryTime   time.Time

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

// SetTheme replaces the theme used for status colors and spacing.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	phase := s.statusPhase
	s.theme = th
	s.tokens = ui.NewTokenSet(th)
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
		}
		if a.thinking {
			state.anyThinking = true
		}
		if a.active || a.streaming || a.thinking || a.blocking {
			state.activeCount++
		} else {
			state.idleCount++
		}
		if a.blocking {
			state.anyBlocking = true
		}
	}
	if state.activeCount == 0 && state.idleCount == 0 {
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

func (s *Screen) IdleAgentCount() int {
	return s.computeSystemState().idleCount
}

func (s *Screen) SetCurrentAgent(agentID string) {
	s.currentAgent = agentID
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
	var left []seg

	if s.lastError != "" && time.Since(s.errorTime) < 12*time.Second {
		left = append(left, seg{text: rendering.TruncateLine("✕ "+s.lastError, s.width/3), role: ui.ColorError})
	} else if s.retryMsg != "" && time.Since(s.retryTime) < 30*time.Second {
		left = append(left, seg{text: rendering.TruncateLine(s.retryMsg, s.width/3), role: ui.ColorWarning})
	} else if s.infoMsg != "" && time.Since(s.infoMsgTime) < 5*time.Second && sys.activeCount == 0 {
		left = append(left, seg{text: rendering.TruncateLine(s.infoMsg, s.width/3), role: ui.ColorSuccess})
	} else if s.isConfirming {
		left = append(left, seg{text: "⚠ Agent needs confirmation", role: ui.ColorWarning})
	} else if s.blocking {
		left = append(left, seg{text: "⚠ Agent awaiting input", role: ui.ColorWarning})
	} else if sys.anyBlocking {
		left = append(left, seg{text: "⚠ Agent awaiting input", role: ui.ColorWarning})
	} else if sys.activeCount > 0 || sys.idleCount > 0 {
		parts := make([]string, 0, 2)
		if sys.activeCount > 0 {
			parts = append(parts, fmt.Sprintf("%d agent%s active", sys.activeCount, pluralS(sys.activeCount)))
		}
		if sys.idleCount > 0 {
			idleLabel := fmt.Sprintf("%d idle", sys.idleCount)
			if sys.activeCount == 0 {
				idleLabel = fmt.Sprintf("%d agent%s idle", sys.idleCount, pluralS(sys.idleCount))
			}
			parts = append(parts, idleLabel)
		}
		left = append(left, seg{text: strings.Join(parts, " · "), role: ui.ColorMuted})
		if s.btwCount > 0 {
			left = append(left, seg{text: fmt.Sprintf(" 📝%d", s.btwCount), role: ui.ColorMuted})
		}
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
		} else if s.costUpperBound {
			parts = append(parts, "cache ?")
		}
		parts = append(parts, fmt.Sprintf("↓%s", rendering.FormatTokens(s.tokenCompletion)))
		if s.contextWindow > 0 {
			percent := int(float64(s.contextUsed) / float64(s.contextWindow) * 100)
			parts = append(parts, fmt.Sprintf("%d%%", percent))
		}
		if s.sessionCost > 0 {
			cost := rendering.FormatCost(s.sessionCost)
			if s.costUpperBound {
				cost = "≤" + cost
			}
			parts = append(parts, cost)
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
// Layout:  gopls  ✕5 ⚠12 ℹ3  ·  8 files  ·  247 symbols
func (s *Screen) lspSegments() []seg {
	if s.project.ServerName == "" && !s.project.ServerFailed && !s.project.ServerStarting {
		return nil
	}

	if s.project.ServerFailed && s.project.ServerError != "" {
		return []seg{{text: "✕ " + s.project.ServerError, role: ui.ColorError, attr: cell.AttrBold}}
	}

	var out []seg
	if s.project.ServerName != "" {
		out = append(out, seg{text: s.project.ServerName, role: ui.ColorMuted})
	} else if s.project.ServerFailed {
		out = append(out, seg{text: "✕ Language server failed", role: ui.ColorError})
	} else {
		out = append(out, seg{text: "Language server", role: ui.ColorMuted})
	}
	switch {
	case s.project.ServerStarting:
		out = append(out, seg{text: " starting", role: ui.ColorWarning})
	case s.project.ServerBusy:
		out = append(out, seg{text: " busy", role: ui.ColorAccent})
	case s.project.ServerSyncing:
		out = append(out, seg{text: " syncing", role: ui.ColorAccent2})
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

func (s *Screen) SetConfirming(c bool) {
	s.isConfirming = c
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

func (s *Screen) UpdateUsage(prompt, completion, cached int, cost float64, ctxWindow, ctxUsed int, estimated, costUpperBound bool) {
	s.tokenPrompt = prompt
	s.tokenCompletion = completion
	s.tokenCached = cached
	s.sessionCost = cost
	s.contextWindow = ctxWindow
	s.contextUsed = ctxUsed
	s.tokenEstimated = estimated
	s.costUpperBound = costUpperBound
}

func (s *Screen) SetThinking(thinking, streaming, blocking bool) {
	s.thinking = thinking
	s.streaming = streaming
	s.blocking = blocking
}

func (s *Screen) SetThinkingForAgent(agentID string, thinking, streaming, blocking bool) {
	st := s.getAgentState(agentID)
	if st == nil {
		return
	}
	st.active = thinking || streaming || blocking
	st.thinking = thinking
	st.streaming = streaming
	st.blocking = blocking
}

func (s *Screen) SetActiveForAgent(agentID string, active bool) {
	st := s.getAgentState(agentID)
	if st != nil {
		st.active = active
	}
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
	st := s.agentStates[agentID]
	if st == nil {
		return ""
	}
	if st.active || st.streaming || st.thinking || st.blocking {
		return "active"
	}
	return ""
}

func (s *Screen) ClearAgentState(agentID string) {
	delete(s.agentStates, agentID)
}

func (s *Screen) SetPhase(phase string) {
	s.statusPhase = phase
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
