package virtlist

import (
	"math"
	"time"
)

type ScrollState struct {
	Velocity      float64
	LastWheelTime time.Time
	AutoScroll    bool
}

func NewScrollState() ScrollState {
	return ScrollState{
		Velocity:   1,
		AutoScroll: true,
	}
}

func (s *ScrollState) OnWheelUp() int {
	now := time.Now()
	elapsed := now.Sub(s.LastWheelTime)
	if !s.LastWheelTime.IsZero() && elapsed < 200*time.Millisecond {
		// Gradual acceleration based on how rapid the scrolling is
		accel := 0.3 * (1.0 - float64(elapsed)/(200*float64(time.Millisecond)))
		s.Velocity = math.Min(s.Velocity+accel, 3.0)
	} else {
		s.Velocity = 1.0
	}
	s.LastWheelTime = now
	lines := int(math.Ceil(s.Velocity))
	if lines < 1 {
		lines = 1
	}
	return lines
}

func (s *ScrollState) OnWheelDown() int {
	now := time.Now()
	elapsed := now.Sub(s.LastWheelTime)
	if !s.LastWheelTime.IsZero() && elapsed < 200*time.Millisecond {
		// Gradual acceleration based on how rapid the scrolling is
		accel := 0.3 * (1.0 - float64(elapsed)/(200*float64(time.Millisecond)))
		s.Velocity = math.Min(s.Velocity+accel, 3.0)
	} else {
		s.Velocity = 1.0
	}
	s.LastWheelTime = now
	lines := int(math.Ceil(s.Velocity))
	if lines < 1 {
		lines = 1
	}
	return lines
}

func (s *ScrollState) ShouldAutoScroll(scrollY, maxScroll int) bool {
	if maxScroll <= 0 {
		s.AutoScroll = true
		return true
	}
	nearBottom := scrollY >= maxScroll-5
	if s.AutoScroll && nearBottom {
		return true
	}
	if !nearBottom {
		s.AutoScroll = false
		return false
	}
	s.AutoScroll = true
	return true
}

func (s *ScrollState) SetAutoScroll(v bool) {
	s.AutoScroll = v
}

func (s *ScrollState) OnContentInserted(isAtBottom bool, contentAboveViewport bool, heightChange int) int {
	if s.AutoScroll {
		return 0
	}
	if contentAboveViewport {
		return heightChange
	}
	return 0
}
