package ui

import (
	tea "charm.land/bubbletea/v2"
)

// EventKind classifies a normalized UI event.
type EventKind uint8

const (
	// EventCustom carries a transport-level message that has no UI-domain
	// equivalent (paste, resize, ticks, domain events). The raw message is
	// available in Event.Custom.
	EventCustom EventKind = iota
	// EventKey is a keyboard press.
	EventKey
	// EventMouseClick is a mouse button press.
	EventMouseClick
	// EventMouseMotion is a mouse movement.
	EventMouseMotion
	// EventMouseRelease is a mouse button release.
	EventMouseRelease
	// EventMouseWheel is a scroll wheel event.
	EventMouseWheel
)

// Event is a UI-domain event normalized from a transport-level tea.Msg.
// Components switch on Kind and read the typed field for that kind instead
// of type-asserting raw bubbletea messages. The Host is the single
// translation point (see Host.Handle and NormalizeEvent).
type Event struct {
	Kind         EventKind
	Key          tea.KeyPressMsg
	MouseClick   tea.MouseClickMsg
	MouseMotion  tea.MouseMotionMsg
	MouseRelease tea.MouseReleaseMsg
	MouseWheel   tea.MouseWheelMsg
	Custom       tea.Msg
}

// KeyEvent returns a key-press event.
func KeyEvent(msg tea.KeyPressMsg) Event {
	return Event{Kind: EventKey, Key: msg}
}

// MouseClickEvent returns a mouse-click event.
func MouseClickEvent(msg tea.MouseClickMsg) Event {
	return Event{Kind: EventMouseClick, MouseClick: msg}
}

// MouseMotionEvent returns a mouse-motion event.
func MouseMotionEvent(msg tea.MouseMotionMsg) Event {
	return Event{Kind: EventMouseMotion, MouseMotion: msg}
}

// MouseReleaseEvent returns a mouse-release event.
func MouseReleaseEvent(msg tea.MouseReleaseMsg) Event {
	return Event{Kind: EventMouseRelease, MouseRelease: msg}
}

// MouseWheelEvent returns a mouse-wheel event.
func MouseWheelEvent(msg tea.MouseWheelMsg) Event {
	return Event{Kind: EventMouseWheel, MouseWheel: msg}
}

// CustomEvent wraps a transport-level message that has no UI-domain kind.
func CustomEvent(msg tea.Msg) Event {
	return Event{Kind: EventCustom, Custom: msg}
}

// NormalizeEvent translates a transport-level tea.Msg into the UI-domain
// Event consumed by Component.Handle. This is the canonical translation
// used by Host dispatch; screens forwarding to nested components pass the
// already-normalized Event on directly.
func NormalizeEvent(msg tea.Msg) Event {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		return KeyEvent(m)
	case tea.MouseClickMsg:
		return MouseClickEvent(m)
	case tea.MouseMotionMsg:
		return MouseMotionEvent(m)
	case tea.MouseReleaseMsg:
		return MouseReleaseEvent(m)
	case tea.MouseWheelMsg:
		return MouseWheelEvent(m)
	default:
		return CustomEvent(msg)
	}
}

// IsMouse reports whether the event is any pointer event.
func (e Event) IsMouse() bool {
	switch e.Kind {
	case EventMouseClick, EventMouseMotion, EventMouseRelease, EventMouseWheel:
		return true
	}
	return false
}

// IsKey reports whether the event is a key press.
func (e Event) IsKey() bool { return e.Kind == EventKey }

// ToMsg returns the transport-level tea.Msg carried by the event. This is the
// inverse of NormalizeEvent; it exists for bridges that must hand a raw
// message to a legacy consumer (e.g. a per-session deckHost whose Host entry
// still takes tea.Msg).
func (e Event) ToMsg() tea.Msg {
	switch e.Kind {
	case EventKey:
		return e.Key
	case EventMouseClick:
		return e.MouseClick
	case EventMouseMotion:
		return e.MouseMotion
	case EventMouseRelease:
		return e.MouseRelease
	case EventMouseWheel:
		return e.MouseWheel
	}
	return e.Custom
}
