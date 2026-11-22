package diffview

import (
	"fmt"
	"strings"

	"crude/text/textdiff"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// Choice is what one change keeps when a comparison is resolved.
type Choice uint8

const (
	ChoiceNone  Choice = iota // not chosen yet
	ChoiceLeft                // keep the left version
	ChoiceRight               // keep the right version
	ChoiceBoth                // keep the left version, then the right version
)

// Resolution actions. Every page that resolves a comparison (a file that
// changed on disk, a Git conflict) uses this one bar, these keys and the
// gutter boxes, so accepting a change works the same way everywhere.
const (
	ActionAcceptLeft  = "diffview.accept-left"
	ActionAcceptRight = "diffview.accept-right"
	ActionAcceptBoth  = "diffview.accept-both"
	ActionPrevious    = "diffview.previous"
	ActionNext        = "diffview.next"
)

// boxWidth is the gutter width of one choice box and its gap.
const boxWidth = 4 // "[x] "

// commitButton is the owner's one finishing action (Save, Resolve file). The
// bar enables it when every change has a choice and the owner allows it.
type commitButton struct {
	label, shortcut, action string
	allowed                 bool
}

type resolution struct {
	enabled  bool
	initial  Choice
	choices  []Choice
	bar      *ui.ActionBar
	onChange func()
	commit   commitButton
	// message replaces the decision state in the bar while the owner has
	// something more important to say (preparing, an error).
	message     string
	messageRole ui.SemanticColor
	statusW     int
	// buttonsW is the narrowest the bar's buttons get: labels without
	// their key captions, which the bar drops first.
	buttonsW int
	// compact names the sides without "Keep" and leaves navigation to the
	// keys, when the full bar does not fit.
	compact bool
}

func newResolutionBar(id ui.ComponentID, tokens *ui.TokenSet) *ui.ActionBar {
	bar := ui.NewActionBar(ui.NewID(id, "resolve"), tokens)
	bar.SetAlignEnd(false)
	bar.SetSurfaceBackground(ui.ColorBg)
	return bar
}

// sideLabel is the short name of one side for its Keep button: the version
// name without its detail ("Current · main" keeps "current").
func sideLabel(label, fallback string) string {
	label, _, _ = strings.Cut(label, " · ")
	label = strings.TrimSpace(label)
	if label == "" {
		return fallback
	}
	return strings.ToLower(label[:1]) + label[1:]
}

// barButtons lists the decisions for the current comparison: keep one side or
// both, move between changes when there are several, and finish. compact
// names only the sides and leaves navigation to n and p.
func (s *Screen) barButtons(compact bool) []ui.ActionBarButton {
	c := s.prepared.Comparison
	open := len(s.resolve.choices) > 0
	keep := func(side string) string {
		if compact {
			return strings.ToUpper(side[:1]) + side[1:]
		}
		return "Keep " + side
	}
	buttons := []ui.ActionBarButton{
		{ID: ui.NewID(s.ID(), "keep-left"), Label: keep(sideLabel(c.BeforeLabel, "left")), Shortcut: "1", Action: ActionAcceptLeft, Enabled: open, Variant: ui.VariantSecondary},
		{ID: ui.NewID(s.ID(), "keep-right"), Label: keep(sideLabel(c.AfterLabel, "right")), Shortcut: "2", Action: ActionAcceptRight, Enabled: open, Variant: ui.VariantSecondary},
		{ID: ui.NewID(s.ID(), "keep-both"), Label: keep("both"), Shortcut: "3", Action: ActionAcceptBoth, Enabled: open, Variant: ui.VariantSecondary},
	}
	if len(s.resolve.choices) > 1 && !compact {
		buttons = append(buttons,
			ui.ActionBarButton{ID: ui.NewID(s.ID(), "previous"), Label: "Previous", Shortcut: "p", Action: ActionPrevious, Enabled: true, Variant: ui.VariantGhost},
			ui.ActionBarButton{ID: ui.NewID(s.ID(), "next"), Label: "Next", Shortcut: "n", Action: ActionNext, Enabled: true, Variant: ui.VariantGhost})
	}
	if commit := s.resolve.commit; commit.action != "" {
		buttons = append(buttons, ui.ActionBarButton{ID: ui.NewID(s.ID(), "commit"), Label: commit.label, Shortcut: commit.shortcut, Action: commit.action,
			Enabled: commit.allowed && s.Decided(), Variant: ui.VariantPrimary})
	}
	return buttons
}

// titlesWidth is the width of buttons without their key captions.
func titlesWidth(buttons []ui.ActionBarButton) int {
	w := 0
	for i, b := range buttons {
		w += rendering.VisibleWidth(b.Label) + 4
		if i > 0 {
			w += 2
		}
	}
	return w
}

func (s *Screen) syncBar() {
	if !s.resolve.enabled {
		return
	}
	buttons := s.barButtons(s.resolve.compact)
	s.resolve.buttonsW = titlesWidth(buttons)
	s.resolve.bar.SetButtons(buttons...)
}

// SetCommit gives the bar the owner's finishing button. It is enabled while
// every change has a choice and allowed is true (nothing is loading or
// running).
func (s *Screen) SetCommit(label, shortcut, action string, allowed bool) {
	commit := commitButton{label: label, shortcut: shortcut, action: action, allowed: allowed}
	if commit == s.resolve.commit {
		return
	}
	s.resolve.commit = commit
	s.syncBar()
}

// BarID is the resolution bar, for keyboard focus (Tab from the panes).
func (s *Screen) BarID() ui.ComponentID { return s.resolve.bar.ID() }

// CommitAllowed reports whether the finishing action may run now.
func (s *Screen) CommitAllowed() bool {
	return s.resolve.enabled && s.resolve.commit.allowed && s.Decided()
}

// SetMessage shows the owner's state (preparing, an error) in place of the
// decision state; empty restores it.
func (s *Screen) SetMessage(text string, role ui.SemanticColor) {
	s.resolve.message, s.resolve.messageRole = text, role
}

// SetResolvable turns per-change decisions on or off. initial is the choice
// every change starts with when a comparison is installed: ChoiceRight keeps
// the user's text until a change is accepted from the left; ChoiceNone makes
// the user choose each change.
func (s *Screen) SetResolvable(enabled bool, initial Choice) {
	s.resolve.enabled, s.resolve.initial = enabled, initial
	s.resetChoices()
	s.syncBar()
}

// SetOnResolutionChange reports every change of a choice to the owner.
func (s *Screen) SetOnResolutionChange(fn func()) { s.resolve.onChange = fn }

// Resolvable reports whether the comparison takes per-change decisions.
func (s *Screen) Resolvable() bool { return s.resolve.enabled }

func (s *Screen) resetChoices() {
	s.resolve.choices = nil
	if !s.resolve.enabled {
		return
	}
	s.resolve.choices = make([]Choice, len(s.prepared.changes))
	for i := range s.resolve.choices {
		s.resolve.choices[i] = s.resolve.initial
	}
	if len(s.resolve.choices) > 0 && s.hunkIndex < 0 {
		s.hunkIndex = 0
	}
}

// Choices returns the decision of every change, in order.
func (s *Screen) Choices() []Choice { return append([]Choice(nil), s.resolve.choices...) }

// SetChoices restores decisions for the same changes. A different count is
// ignored, because the decisions belong to other changes.
func (s *Screen) SetChoices(choices []Choice) {
	if len(choices) == len(s.resolve.choices) {
		copy(s.resolve.choices, choices)
		s.syncBar()
	}
}

// ChangeKey identifies a change by its content, so a decision survives when
// the comparison is prepared again and the same change is still there.
type ChangeKey struct{ Left, Right string }

// ChoiceKeys returns the content key of every change, in order.
func (s *Screen) ChoiceKeys() []ChangeKey {
	keys := make([]ChangeKey, len(s.prepared.changes))
	for i, h := range s.prepared.changes {
		keys[i] = ChangeKey{Left: strings.Join(h.OldLines, ""), Right: strings.Join(h.NewLines, "")}
	}
	return keys
}

// RestoreChoices applies earlier decisions to the changes that are still the
// same; new changes keep their initial choice. It reports how many decisions
// it kept.
func (s *Screen) RestoreChoices(decisions map[ChangeKey]Choice) int {
	kept := 0
	for i, key := range s.ChoiceKeys() {
		if choice, ok := decisions[key]; ok && i < len(s.resolve.choices) {
			s.resolve.choices[i] = choice
			kept++
		}
	}
	s.syncBar()
	return kept
}

// Decisions maps every change's content key to its current choice.
func (s *Screen) Decisions() map[ChangeKey]Choice {
	out := make(map[ChangeKey]Choice, len(s.resolve.choices))
	for i, key := range s.ChoiceKeys() {
		if i < len(s.resolve.choices) {
			out[key] = s.resolve.choices[i]
		}
	}
	return out
}

// Decided reports whether every change has a choice.
func (s *Screen) Decided() bool {
	for _, choice := range s.resolve.choices {
		if choice == ChoiceNone {
			return false
		}
	}
	return s.resolve.enabled
}

// CurrentChange is the change the bar and the keys act on, or -1.
func (s *Screen) CurrentChange() int { return s.hunkIndex }

// ResolutionStatus is the one-line state of the decisions.
func (s *Screen) ResolutionStatus() string {
	total := len(s.resolve.choices)
	if !s.resolve.enabled || total == 0 {
		return ""
	}
	open := 0
	for _, choice := range s.resolve.choices {
		if choice == ChoiceNone {
			open++
		}
	}
	if total == 1 {
		if open > 0 {
			return "1 change to choose"
		}
		return "1 change chosen"
	}
	status := fmt.Sprintf("Change %d of %d", max(0, s.hunkIndex)+1, total)
	if open > 0 {
		return status + fmt.Sprintf(" · %d to choose", open)
	}
	return status + " · all chosen"
}

// barStatusWidth is the state column of the bar: it fits "Change 12 of 34 ·
// 12 to choose", so the buttons keep their place while the state changes.
const barStatusWidth = 30

// layoutBar places the state text and the decisions on the bar row. The
// buttons get their room first; the text takes the rest of a stable column
// (longer messages get more room at the next layout and are cut until then).
func (s *Screen) layoutBar(ctx ui.LayoutContext, w int) {
	if compact := titlesWidth(s.barButtons(false)) > w; compact != s.resolve.compact {
		s.resolve.compact = compact
		s.syncBar()
	}
	text := max(barStatusWidth, rendering.VisibleWidth(s.barStatus()))
	full := s.resolve.bar.Measure(ctx, ui.Constraints{MaxW: w, MaxH: 1}).W
	s.resolve.statusW = max(0, min(text, w-full-2))
	if s.resolve.statusW < min(text, barStatusWidth) {
		// The state is worth more than key captions: the buttons drop
		// them before the state gives way.
		s.resolve.statusW = max(0, min(rendering.VisibleWidth(s.barStatus()), w-s.resolve.buttonsW-2))
	}
	if s.resolve.statusW < 12 {
		// Too narrow for both: the decisions keep the row.
		s.resolve.statusW = 0
	}
	x := 0
	if s.resolve.statusW > 0 {
		x = s.resolve.statusW + 2
	}
	s.resolve.bar.Layout(ctx, ui.Rect{X: min(x, w), W: max(0, w-x), H: 1})
}

func (s *Screen) barStatus() string {
	if s.resolve.message != "" {
		return s.resolve.message
	}
	if len(s.prepared.changes) == 0 {
		return "No differences"
	}
	return s.ResolutionStatus()
}

func (s *Screen) paintBar(buf *surface.CellBuffer, ox, oy int) {
	if s.resolve.statusW <= 0 {
		return
	}
	r := s.Bounds()
	role := ui.ColorFg
	if s.resolve.message != "" {
		role = s.resolve.messageRole
	}
	buf.WriteString(ox+r.X, oy+r.Y, rendering.TruncateVisual(s.barStatus(), s.resolve.statusW, "…"), s.tokens.Color(role), s.tokens.Color(ui.ColorBg), cell.AttrBold)
}

// choose sets one change and makes it current.
func (s *Screen) choose(index int, choice Choice) bool {
	if !s.resolve.enabled || index < 0 || index >= len(s.resolve.choices) {
		return false
	}
	s.resolve.choices[index] = choice
	s.hunkIndex = index
	s.changed()
	return true
}

// toggle flips one side of a change, like the two checkboxes of a merge
// editor: both checked keeps both, none checked leaves the change open.
func (s *Screen) toggle(index int, left bool) bool {
	if !s.resolve.enabled || index < 0 || index >= len(s.resolve.choices) {
		return false
	}
	keepLeft, keepRight := keeps(s.resolve.choices[index])
	if left {
		keepLeft = !keepLeft
	} else {
		keepRight = !keepRight
	}
	choice := ChoiceNone
	switch {
	case keepLeft && keepRight:
		choice = ChoiceBoth
	case keepLeft:
		choice = ChoiceLeft
	case keepRight:
		choice = ChoiceRight
	}
	return s.choose(index, choice)
}

func (s *Screen) changed() {
	s.syncBar()
	if s.resolve.onChange != nil {
		s.resolve.onChange()
	}
}

// keeps reports which sides a decided choice keeps.
func keeps(choice Choice) (left, right bool) {
	return choice == ChoiceLeft || choice == ChoiceBoth, choice == ChoiceRight || choice == ChoiceBoth
}

// kept reports whether a side of a change survives the current decision. An
// open change keeps both sides visible.
func (s *Screen) kept(change int, left bool) bool {
	if !s.resolve.enabled || change < 0 || change >= len(s.resolve.choices) || s.resolve.choices[change] == ChoiceNone {
		return true
	}
	keepLeft, keepRight := keeps(s.resolve.choices[change])
	if left {
		return keepLeft
	}
	return keepRight
}

// ResolvedText builds the text the decisions describe. It reports false while
// a change is open. Equal text between changes comes from the right version.
func (s *Screen) ResolvedText() (string, bool) {
	if !s.resolve.enabled || !s.Decided() {
		return "", false
	}
	return resolveText(s.prepared.Comparison, s.prepared.changes, s.resolve.choices), true
}

func resolveText(c Comparison, changes []textdiff.Hunk, choices []Choice) string {
	before, after := textdiff.Lines(c.Before), textdiff.Lines(c.After)
	var out strings.Builder
	next := 0
	write := func(lines []string) {
		for _, line := range lines {
			out.WriteString(line)
		}
	}
	for i, h := range changes {
		write(after[next:h.NewStart])
		left := before[h.OldStart : h.OldStart+len(h.OldLines)]
		right := after[h.NewStart : h.NewStart+len(h.NewLines)]
		switch choices[i] {
		case ChoiceLeft:
			write(left)
		case ChoiceRight:
			write(right)
		case ChoiceBoth:
			write(left)
			if len(left) > 0 && len(right) > 0 && !strings.HasSuffix(left[len(left)-1], "\n") {
				out.WriteString(lineBreak(c.After))
			}
			write(right)
		}
		next = h.NewStart + len(h.NewLines)
	}
	write(after[next:])
	return out.String()
}

func lineBreak(text string) string {
	if strings.Contains(text, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// changeAt maps a row of the current presentation to its change, or -1.
func (s *Screen) changeAt(row int) int {
	index := s.prepared.rowChange
	if s.presented != SideBySide {
		index = s.prepared.unifiedChange
	}
	if row < 0 || row >= len(index) {
		return -1
	}
	return index[row]
}

// firstChangeRow reports whether row is where the panes show a change's
// boxes: the first row of the change in the current presentation.
func (s *Screen) firstChangeRow(row int) (change int, ok bool) {
	change = s.changeAt(row)
	if change < 0 {
		return -1, false
	}
	if s.presented == SideBySide {
		return change, s.prepared.hunks[change] == row
	}
	return change, s.prepared.unifiedHunks[change] == row
}

func (s *Screen) resolutionAction(id string) bool {
	switch id {
	case ActionAcceptLeft:
		return s.choose(s.hunkIndex, ChoiceLeft)
	case ActionAcceptRight:
		return s.choose(s.hunkIndex, ChoiceRight)
	case ActionAcceptBoth:
		return s.choose(s.hunkIndex, ChoiceBoth)
	case ActionPrevious:
		return s.NavigateHunk(-1)
	case ActionNext:
		return s.NavigateHunk(1)
	}
	return false
}

// resolutionKey maps the shared keys: 1 left, 2 right, 3 both, 0 undo the
// choice, n/p next and previous change.
func (s *Screen) resolutionKey(key string) bool {
	if !s.resolve.enabled {
		return false
	}
	switch key {
	case "1":
		return s.resolutionAction(ActionAcceptLeft)
	case "2":
		return s.resolutionAction(ActionAcceptRight)
	case "3":
		return s.resolutionAction(ActionAcceptBoth)
	case "0":
		return s.choose(s.hunkIndex, ChoiceNone)
	case "n":
		return s.resolutionAction(ActionNext)
	case "p":
		return s.resolutionAction(ActionPrevious)
	}
	return false
}
