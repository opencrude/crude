package scrollbar

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	VisibleDuration = 1500 * time.Millisecond
	FadeDuration    = 500 * time.Millisecond
	ScrollbarWidth  = 2
)

type Visibility int

const (
	Hidden Visibility = iota
	Visible
	Fading
)

type FadeTickMsg time.Time
type HideTickMsg time.Time

type ScrollBar struct {
	visibility     Visibility
	trackH         int
	lastActivity   time.Time
	dragging       bool
	dragOffsetY    int
	contentH       int
	visibleH       int
	scrollY        int
	trackChar      string
	thumbChar      string
	trackStyle     string
	thumbStyle     string
	fadeThumbStyle string
	scrollChanged  bool
}

func New(trackH int) ScrollBar {
	return ScrollBar{
		trackH:         trackH,
		visibility:     Hidden,
		trackChar:      "░",
		thumbChar:      "█",
		trackStyle:     defaultTrackStyle,
		thumbStyle:     defaultThumbStyle,
		fadeThumbStyle: defaultFadeThumbStyle,
	}
}

func (s *ScrollBar) SetScrollInfo(contentH, visibleH, scrollY int) tea.Cmd {
	s.contentH = contentH
	s.visibleH = visibleH
	s.scrollY = scrollY
	s.lastActivity = time.Now()
	if s.visibility == Hidden || s.visibility == Fading {
		s.visibility = Visible
	}
	return s.startFadeTimer()
}

func (s *ScrollBar) startFadeTimer() tea.Cmd {
	return tea.Tick(VisibleDuration, func(t time.Time) tea.Msg {
		return FadeTickMsg(t)
	})
}

func (s ScrollBar) Update(msg tea.Msg) (ScrollBar, tea.Cmd) {
	switch m := msg.(type) {
	case FadeTickMsg:
		if s.visibility == Visible && time.Since(s.lastActivity) >= VisibleDuration {
			s.visibility = Fading
			return s, tea.Tick(FadeDuration, func(t time.Time) tea.Msg {
				return HideTickMsg(t)
			})
		}
		return s, nil
	case HideTickMsg:
		if s.visibility == Fading && time.Since(s.lastActivity) >= VisibleDuration+FadeDuration {
			s.visibility = Hidden
		}
		return s, nil
	case tea.MouseMsg:
		return s.handleMouse(m)
	default:
		s.lastActivity = time.Now()
		if s.visibility == Hidden || s.visibility == Fading {
			s.visibility = Visible
			return s, s.startFadeTimer()
		}
		return s, nil
	}
}

func (s *ScrollBar) handleMouse(msg tea.MouseMsg) (ScrollBar, tea.Cmd) {
	if s.contentH <= s.visibleH || s.trackH == 0 {
		return *s, nil
	}
	thumbTop, thumbH := s.ThumbBounds()
	maxScroll := s.contentH - s.visibleH
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if msg.Y >= thumbTop && msg.Y < thumbTop+thumbH {
			s.dragging = true
			s.dragOffsetY = msg.Y - thumbTop
		} else {
			// Jump-to-position: center thumb on click Y, then begin drag
			availableTrack := s.trackH - thumbH
			if availableTrack <= 0 {
				break
			}
			desiredThumbTop := msg.Y - thumbH/2
			if desiredThumbTop < 0 {
				desiredThumbTop = 0
			}
			if desiredThumbTop > availableTrack {
				desiredThumbTop = availableTrack
			}
			s.scrollY = desiredThumbTop * maxScroll / availableTrack
			if s.scrollY < 0 {
				s.scrollY = 0
			}
			if s.scrollY > maxScroll {
				s.scrollY = maxScroll
			}
			s.dragging = true
			s.dragOffsetY = msg.Y - desiredThumbTop
			s.scrollChanged = true
		}
		s.lastActivity = time.Now()
		if s.visibility != Visible {
			s.visibility = Visible
			return *s, s.startFadeTimer()
		}
		return *s, nil

	case msg.Action == tea.MouseActionMotion && s.dragging:
		s.scrollY = s.DragScrollY(msg.Y)
		s.scrollChanged = true
		s.lastActivity = time.Now()
		return *s, nil

	case msg.Action == tea.MouseActionRelease && s.dragging:
		s.dragging = false
		s.lastActivity = time.Now()
		return *s, nil

	default:
		return *s, nil
	}
	return *s, nil
}

func (s ScrollBar) ThumbBounds() (thumbTop, thumbHeight int) {
	if s.contentH <= 0 || s.visibleH <= 0 {
		return 0, s.trackH
	}
	maxScroll := s.contentH - s.visibleH
	if maxScroll <= 0 {
		return 0, s.trackH
	}
	thumbHeight = s.visibleH * s.trackH / s.contentH
	if thumbHeight < 1 {
		thumbHeight = 1
	}
	availableTrack := s.trackH - thumbHeight
	thumbTop = s.scrollY * availableTrack / maxScroll
	return thumbTop, thumbHeight
}

func (s ScrollBar) View() string {
	if s.visibility == Hidden || s.trackH == 0 || s.visibleH == 0 || s.contentH == 0 {
		return ""
	}
	thumbTop, thumbH := s.ThumbBounds()
	activeThumbStyle := s.thumbStyle
	if s.visibility == Fading {
		activeThumbStyle = s.fadeThumbStyle
	}
	var lines []string
	for i := 0; i < s.trackH; i++ {
		if i >= thumbTop && i < thumbTop+thumbH {
			lines = append(lines, styleChar(s.thumbChar, activeThumbStyle)+" ")
		} else {
			lines = append(lines, styleChar(s.trackChar, s.trackStyle)+" ")
		}
	}
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func (s ScrollBar) Width() int {
	return ScrollbarWidth
}

func (s ScrollBar) IsVisible() bool {
	return s.visibility != Hidden
}

func (s ScrollBar) IsDragging() bool {
	return s.dragging
}

func (s ScrollBar) GetScrollY() int {
	return s.scrollY
}

func (s *ScrollBar) ConsumeScrollChanged() bool {
	changed := s.scrollChanged
	s.scrollChanged = false
	return changed
}

func (s *ScrollBar) SetTrackHeight(h int) {
	s.trackH = h
}

func (s ScrollBar) DragScrollY(mouseY int) int {
	adjustedY := mouseY - s.dragOffsetY
	if adjustedY < 0 {
		adjustedY = 0
	}
	maxScroll := s.contentH - s.visibleH
	if maxScroll <= 0 {
		return 0
	}
	thumbH := s.visibleH * s.trackH / s.contentH
	if thumbH < 1 {
		thumbH = 1
	}
	availableTrack := s.trackH - thumbH
	if availableTrack <= 0 {
		return 0
	}
	scrollY := adjustedY * maxScroll / availableTrack
	if scrollY < 0 {
		scrollY = 0
	}
	if scrollY > maxScroll {
		scrollY = maxScroll
	}
	return scrollY
}

func (s *ScrollBar) Touch() {
	s.lastActivity = time.Now()
}
