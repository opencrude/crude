package ui

import "time"

// WheelGesture groups the wheel events terminals expose without native scroll
// phase markers. Content moving beneath a stationary pointer cannot acquire an
// ongoing gesture. A deliberate pointer move, idle pause, or direct scrollbar
// target starts a new owner; consumers validate retained mount/source identity.
type WheelGesture struct {
	owner string
	scope string
	x, y  int
	last  time.Time
}

const WheelGestureIdle = 500 * time.Millisecond

func (g *WheelGesture) Resolve(now time.Time, x, y int, candidate string, explicit bool) (string, bool) {
	moved := x-g.x >= 2 || g.x-x >= 2 || y != g.y
	fresh := g.owner == "" || g.last.IsZero() || now.Before(g.last) || now.Sub(g.last) > WheelGestureIdle || moved || explicit && candidate != g.owner && (x != g.x || y != g.y)
	if fresh {
		g.owner = candidate
		g.x, g.y = x, y
	}
	g.last = now
	return g.owner, fresh
}
func (g *WheelGesture) Owner() string         { return g.owner }
func (g *WheelGesture) Reset()                { *g = WheelGesture{} }
func (g *WheelGesture) Transfer(owner string) { g.owner = owner }

// ResolveScope separates sibling mounted surfaces from content targets within
// one scroll hierarchy. Crossing a sibling boundary always starts a fresh route.
func (g *WheelGesture) ResolveScope(now time.Time, x, y int, candidate, scope string, explicit bool) (string, bool) {
	if g.scope != "" && g.scope != scope {
		g.Reset()
	}
	owner, fresh := g.Resolve(now, x, y, candidate, explicit)
	g.scope = scope
	return owner, fresh
}

// WheelTargetResetter releases component-local nested scroll state when its
// enclosing mounted Host relinquishes the wheel transaction.
type WheelTargetResetter interface{ ResetWheelGesture() }

// WheelTargetProvider lets direct controls share an ancestor's wheel arbitration
// without redirecting click/drag capture or keyboard focus. Host accepts only a
// currently mounted ancestor, never a sibling or another surface.
type WheelTargetProvider interface{ WheelTarget() ComponentID }

// ScrollWheelDelta returns the unconsumed delta at an exact boundary. A nested
// consumer can hand only that remainder to its parent, never replay the notch.
func ScrollWheelDelta(scroll *ScrollModel, delta int) int {
	before := scroll.Offset()
	scroll.ScrollBy(delta)
	if delta < 0 {
		scroll.SetAutoScroll(false)
	} else if delta > 0 {
		scroll.SetAutoScroll(scroll.NearBottom(0))
	}
	return delta - (scroll.Offset() - before)
}
