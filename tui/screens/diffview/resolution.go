package diffview

import (
	"fmt"
	"strings"

	"crude/textdiff"
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

type resolution struct {
	enabled  bool
	initial  Choice
	choices  []Choice
	bar      *ui.ActionBar
	onChange func()
}

func newResolutionBar(id ui.ComponentID, tokens *ui.TokenSet) *ui.ActionBar {
	bar := ui.NewActionBar(ui.NewID(id, "resolve"), tokens,
		ui.ActionBarButton{Label: "Accept left", Shortcut: "1", Action: ActionAcceptLeft, Enabled: true},
		ui.ActionBarButton{Label: "Accept right", Shortcut: "2", Action: ActionAcceptRight, Enabled: true},
		ui.ActionBarButton{Label: "Accept both", Shortcut: "3", Action: ActionAcceptBoth, Enabled: true},
		ui.ActionBarButton{Label: "Previous", Shortcut: "p", Action: ActionPrevious, Enabled: true},
		ui.ActionBarButton{Label: "Next", Shortcut: "n", Action: ActionNext, Enabled: true},
	)
	bar.SetAlignEnd(false)
	bar.SetSurfaceBackground(ui.ColorBg)
	return bar
}

// SetResolvable turns per-change decisions on or off. initial is the choice
// every change starts with when a comparison is installed: ChoiceRight keeps
// the user's text until a change is accepted from the left; ChoiceNone makes
// the user choose each change.
func (s *Screen) SetResolvable(enabled bool, initial Choice) {
	s.resolve.enabled, s.resolve.initial = enabled, initial
	s.resetChoices()
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
	}
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
	status := fmt.Sprintf("Change %d of %d", max(0, s.hunkIndex)+1, total)
	if open > 0 {
		return status + fmt.Sprintf(" · %d to choose", open)
	}
	return status + " · all chosen"
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
