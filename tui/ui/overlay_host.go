package ui

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/wm"
)

// OverlayID is a typed identifier for a managed overlay. Using a
// typed enum instead of raw strings prevents typos and centralizes
// the set of known overlays in one place.
type OverlayID string

// Known overlay IDs. Product code should use these constants rather
// than raw strings when calling the OverlayHost.
const (
	OverlayContextMenu       OverlayID = "context-menu"
	OverlayFindReplace       OverlayID = "find-replace"
	OverlayDirtyClose        OverlayID = "dirty-close-prompt"
	OverlayTreePrompt        OverlayID = "tree-prompt"
	OverlayTreeDelete        OverlayID = "tree-delete-confirm"
	OverlayFilePicker        OverlayID = "filepicker"
	OverlayWorkspaceSettings OverlayID = "workspace-settings"
	OverlayComposer          OverlayID = "composer"
	OverlayTextOverlay       OverlayID = "textoverlay"
	OverlayCommandPalette    OverlayID = "command-palette"
	OverlayFindInFiles       OverlayID = "find-in-files"
	OverlayReplaceInFiles    OverlayID = "replace-in-files"
	OverlayReferences        OverlayID = "references-popup"
	OverlayCompletion        OverlayID = "completion-popup"
	OverlayOutline           OverlayID = "outline-popup"
	OverlayDiagContext       OverlayID = "diag-context"
	OverlayDiagHover         OverlayID = "diag-hover"
	OverlaySessionPicker     OverlayID = "session-picker"
	OverlayMemoryBrowser     OverlayID = "memory-browser"
	OverlayHelp              OverlayID = "help"
	OverlayTerminalPrefix    OverlayID = "terminal/"
)

// TerminalOverlayID is the OverlayHost id for one floating terminal window.
func TerminalOverlayID(id string) OverlayID {
	return OverlayTerminalPrefix + OverlayID(id)
}

// TerminalIDFromOverlay extracts the PTY id from a floating-terminal overlay.
func TerminalIDFromOverlay(id OverlayID) (string, bool) {
	s := string(id)
	p := string(OverlayTerminalPrefix)
	if len(s) <= len(p) || s[:len(p)] != p {
		return "", false
	}
	return s[len(p):], true
}

// IsTerminalOverlay reports a per-session floating terminal window.
func IsTerminalOverlay(id OverlayID) bool {
	_, ok := TerminalIDFromOverlay(id)
	return ok
}

// OutsidePolicy defines what happens when the user clicks outside
// this overlay. Maps 1:1 to wm.ClickOutsideBehavior but is owned by
// the UI layer so product code does not import wm directly.
type OutsidePolicy uint8

const (
	OutsideCloseAndRoute OutsidePolicy = iota
	OutsideCloseAndConsume
	OutsideKeepAndRoute
	OutsideIgnoreAndConsume
)

func (p OutsidePolicy) toWM() wm.ClickOutsideBehavior {
	switch p {
	case OutsideCloseAndRoute:
		return wm.ClickOutsideCloseRoute
	case OutsideCloseAndConsume:
		// WM has no CloseAndConsume; emulate as CloseRoute (close +
		// route) — the consume part is handled by the caller
		// deciding whether to forward the click. We use CloseRoute
		// as the closest WM primitive.
		return wm.ClickOutsideCloseRoute
	case OutsideKeepAndRoute:
		return wm.ClickOutsideKeepRoute
	case OutsideIgnoreAndConsume:
		return wm.ClickOutsideIgnore
	default:
		return wm.ClickOutsideCloseRoute
	}
}

// OverlayPolicy describes the behavior of a managed overlay.
type OverlayPolicy struct {
	Modal     bool // blocks clicks to windows below
	Focusable bool // participates in focus/mouse hit-testing
	Movable   bool
	Resizable bool
	Closable  bool
	Decorated bool // enables standard WindowFrame interaction regions
	Outside   OutsidePolicy
}

// DefaultOverlayPolicy returns a sensible default for a non-modal
// popover (e.g. find/replace, composer).
func DefaultOverlayPolicy() OverlayPolicy {
	return OverlayPolicy{
		Focusable: true,
		Movable:   true,
		Resizable: true,
		Closable:  true,
		Decorated: false,
		Outside:   OutsideKeepAndRoute,
	}
}

// ModalDialogPolicy returns a policy for modal dialogs (dirty-close,
// tree-delete, tree-prompt).
func ModalDialogPolicy() OverlayPolicy {
	return OverlayPolicy{
		Modal:     true,
		Focusable: true,
		Outside:   OutsideIgnoreAndConsume,
		Decorated: false,
	}
}

// PopoverPolicy returns a policy for transient popovers (context
// menu, LSP popups, diagnostic hover).
func PopoverPolicy(closeOnOutside bool) OverlayPolicy {
	p := OverlayPolicy{
		Focusable: true,
		Outside:   OutsideKeepAndRoute,
		Decorated: false,
	}
	if closeOnOutside {
		p.Outside = OutsideCloseAndRoute
	}
	return p
}

// OverlayEntry is one managed overlay in the stack.
type OverlayEntry struct {
	ID      OverlayID
	Window  *wm.FloatingWindow
	Policy  OverlayPolicy
	OnClose func() // cleanup invoked when the overlay is removed

	// priorFocus is the floating-window ID that had window-manager focus when
	// this overlay opened ("" when the shell — editor/tree/chat — owned focus).
	// Closing the focused overlay restores it, so nested overlays unwind to the
	// exact prior focus path instead of "whatever is on top of the z-order".
	priorFocus string
}

// OverlayHost manages the stack of floating overlay windows. It
// centralizes open/close, click-outside dispatch, and content
// click/hover routing so product code does not need bespoke switches
// per popup ID.
//
// The OverlayHost does NOT own the wm.WindowManager — it wraps it.
// The viewmodel creates the OverlayHost with its windowManager and
// uses typed methods instead of raw WM + popupmouse switches.
//
// Phase 9 foundation: this is the typed registry. Product surfaces
// migrate off the bespoke closePopupWindowByID / handlePopupContent*
// switches onto OverlayHost methods over time.
type OverlayHost struct {
	wm    *wm.WindowManager
	stack []*OverlayEntry

	// focusHost is the Host whose focus the overlay stack owns. When set, Open
	// pushes a focus scope (capturing the Host's current focus) and
	// Close/CloseAll pop it, so closing nested overlays restores the exact
	// prior focus path. Optional: hosts constructed without a focusHost keep
	// the legacy flat WM focus behavior.
	focusHost *Host

	// onOpen is an optional hook invoked for every overlay open with the
	// window and overlay ID, right after the window is registered. The
	// viewmodel uses it to start reveal animations; the ui layer itself
	// never animates (presentation policy lives with the caller).
	onOpen func(id OverlayID, fw *wm.FloatingWindow)
}

// NewOverlayHost creates an overlay host wrapping the given WM.
func NewOverlayHost(manager *wm.WindowManager) *OverlayHost {
	return &OverlayHost{wm: manager}
}

// SetFocusHost attaches the Host whose focus the overlay stack scopes. Open
// pushes a focus scope and Close/CloseAll pop it (see FocusScope).
func (h *OverlayHost) SetFocusHost(host *Host) {
	if h == nil {
		return
	}
	h.focusHost = host
}

// SetOnOpen installs the per-open hook (reveal animation start, etc.).
func (h *OverlayHost) SetOnOpen(fn func(id OverlayID, fw *wm.FloatingWindow)) {
	h.onOpen = fn
}

// Open registers an overlay. If an overlay with the same ID is
// already open, it is closed first (OnClose runs). The window is
// added to the WM and positioned by the caller before calling Open.
//
// When a focus host is attached, Open pushes a focus scope for the overlay
// (capturing the Host's current focus) so Close can restore the exact prior
// focus path.
func (h *OverlayHost) Open(id OverlayID, fw *wm.FloatingWindow, policy OverlayPolicy, onClose func()) {
	if h == nil || h.wm == nil || fw == nil {
		return
	}
	// Close existing overlay with same ID.
	if existing := h.Get(id); existing != nil {
		h.Close(id)
	}
	// Apply policy to the window.
	fw.SetClickOutsideBehavior(policy.Outside.toWM())
	// Capture the focused window BEFORE the new window steals focus, so the
	// scope can restore it when this overlay closes.
	prior := h.focusedWindowID()
	// Add to WM.
	h.wm.AddWindow(fw)
	// Track in stack.
	h.stack = append(h.stack, &OverlayEntry{
		ID:         id,
		Window:     fw,
		Policy:     policy,
		OnClose:    onClose,
		priorFocus: prior,
	})
	if h.focusHost != nil {
		h.focusHost.PushFocusScope(ComponentID(string(id)), policy.Modal)
	}
	if h.onOpen != nil {
		h.onOpen(id, fw)
	}
}

// Close removes an overlay and runs its OnClose cleanup.
func (h *OverlayHost) Close(id OverlayID) {
	if h == nil || h.wm == nil {
		return
	}
	for i, e := range h.stack {
		if e.ID == id {
			wasFocused := h.wm.Focused() != nil && h.wm.Focused().ID == string(id)
			// Remove registration before cleanup so OnClose may safely change
			// modes or close related overlays without recursively closing itself.
			h.stack = append(h.stack[:i], h.stack[i+1:]...)
			h.wm.RemoveWindow(string(id))
			if e.OnClose != nil {
				e.OnClose()
			}
			h.popScope(id)
			// Restore the exact prior focus only when this overlay was the
			// focused window; otherwise the overlay above it keeps focus.
			if wasFocused {
				h.restoreWindowFocus(e.priorFocus)
			}
			return
		}
	}
	// Fallback: remove from WM directly if not tracked.
	h.wm.RemoveWindow(string(id))
}

// CloseAll removes all managed overlays.
func (h *OverlayHost) CloseAll() {
	if h == nil {
		return
	}
	entries := h.stack
	h.stack = nil
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if h.wm != nil {
			h.wm.RemoveWindow(string(e.ID))
		}
		h.popScope(e.ID)
		if e.OnClose != nil {
			e.OnClose()
		}
	}
}

// Get returns the overlay entry for id, or nil.
func (h *OverlayHost) Get(id OverlayID) *OverlayEntry {
	if h == nil {
		return nil
	}
	for _, e := range h.stack {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// Window returns the FloatingWindow for id, or nil.
func (h *OverlayHost) Window(id OverlayID) *wm.FloatingWindow {
	if e := h.Get(id); e != nil {
		return e.Window
	}
	return nil
}

// IsOpen reports whether id is currently managed.
func (h *OverlayHost) IsOpen(id OverlayID) bool {
	return h.Get(id) != nil
}

// Top returns the topmost managed overlay, or nil.
func (h *OverlayHost) Top() *OverlayEntry {
	if h == nil || len(h.stack) == 0 {
		return nil
	}
	return h.stack[len(h.stack)-1]
}

// All returns all managed overlay entries (bottom-to-top order).
func (h *OverlayHost) All() []*OverlayEntry {
	if h == nil {
		return nil
	}
	out := make([]*OverlayEntry, len(h.stack))
	copy(out, h.stack)
	return out
}

// HandleClickOutside processes a left-click that may be outside one
// or more managed overlays. Returns true if the click should be
// suppressed (modal block). This replaces the bespoke
// handlePopupClickOutside switch in popupmouse.go.
//
// Overlays are checked top-to-bottom (reverse stack order). The
// first overlay whose Outside policy triggers determines the
// outcome:
//   - OutsideIgnoreAndConsume: suppress the click (return true).
//   - OutsideCloseAndRoute / OutsideCloseAndConsume: close the
//     overlay, do NOT suppress (caller routes the click).
//   - OutsideKeepAndRoute: do nothing (click routes through).
func (h *OverlayHost) HandleClickOutside(x, y int) bool {
	if h == nil {
		return false
	}
	for i := len(h.stack) - 1; i >= 0; i-- {
		e := h.stack[i]
		if e.Window == nil || !e.Window.Visible() || !e.Window.Focusable() {
			continue
		}
		if e.Window.ContainsPoint(x, y) {
			continue // click is inside this overlay
		}
		switch e.Policy.Outside {
		case OutsideIgnoreAndConsume:
			return true
		case OutsideCloseAndRoute, OutsideCloseAndConsume:
			h.Close(e.ID)
		case OutsideKeepAndRoute:
			// do nothing
		}
	}
	return false
}

// ContentHandler is a function that handles a click/hover inside an
// overlay's content area. The OverlayHost delegates to the
// per-overlay handler registered via SetContentHandler.
type ContentHandler func(host *OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd

// HoverHandler handles mouse motion over an overlay's content.
type HoverHandler func(host *OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton)

// contentHandlers maps overlay IDs to their click handlers. Stored
// separately from the stack so handlers persist across open/close.
var contentHandlers = map[OverlayID]ContentHandler{}
var hoverHandlers = map[OverlayID]HoverHandler{}

// RegisterContentHandler associates a click handler with an overlay
// ID. Called once during initialization; the handler is reused for
// every open/close cycle of that overlay.
func (h *OverlayHost) RegisterContentHandler(id OverlayID, fn ContentHandler) {
	contentHandlers[id] = fn
}

// RegisterHoverHandler associates a hover handler with an overlay ID.
func (h *OverlayHost) RegisterHoverHandler(id OverlayID, fn HoverHandler) {
	hoverHandlers[id] = fn
}

// DispatchContentClick routes a click inside an overlay to its
// registered content handler. Returns the tea.Cmd from the handler.
func (h *OverlayHost) DispatchContentClick(fw *wm.FloatingWindow, msg tea.MouseClickMsg) tea.Cmd {
	if h == nil || fw == nil {
		return nil
	}
	mouse := msg.Mouse()
	fx, fy := fw.Position()
	localX := mouse.X - fx
	localY := mouse.Y - fy
	id := OverlayID(fw.ID)
	if fn, ok := contentHandlers[id]; ok {
		return fn(h, fw, localX, localY, msg)
	}
	return nil
}

// DispatchContentHover routes a mouse-motion inside an overlay to its
// registered hover handler.
func (h *OverlayHost) DispatchContentHover(fw *wm.FloatingWindow, screenX, screenY int, button tea.MouseButton) {
	if h == nil || fw == nil || !fw.Focusable() {
		return
	}
	id := OverlayID(fw.ID)
	fn, ok := hoverHandlers[id]
	if !ok {
		return
	}
	fx, fy := fw.Position()
	localX := screenX - fx
	localY := screenY - fy
	fn(h, fw, localX, localY, button)
}

// HasContentHandler reports whether an overlay ID has a registered
// content click handler.
func (h *OverlayHost) HasContentHandler(id OverlayID) bool {
	_, ok := contentHandlers[id]
	return ok
}

// HasHoverHandler reports whether an overlay ID has a registered
// hover handler.
func (h *OverlayHost) HasHoverHandler(id OverlayID) bool {
	_, ok := hoverHandlers[id]
	return ok
}

// focusedWindowID returns the ID of the WM's focused window, or "" when no
// window has focus (the shell — editor/tree/chat — owns focus).
func (h *OverlayHost) focusedWindowID() string {
	if h == nil || h.wm == nil {
		return ""
	}
	if fw := h.wm.Focused(); fw != nil {
		return fw.ID
	}
	return ""
}

// popScope removes the focus scope for the overlay with the given ID. A top
// scope pops and RESTORES the focus it captured; a non-top scope is dropped
// without restoring so the overlay above it keeps focus.
func (h *OverlayHost) popScope(id OverlayID) {
	if h == nil || h.focusHost == nil {
		return
	}
	boundary := ComponentID(string(id))
	scopes := h.focusHost.FocusScopes()
	if len(scopes) == 0 {
		return
	}
	if scopes[len(scopes)-1] == boundary {
		h.focusHost.PopFocusScope()
		return
	}
	h.focusHost.RemoveFocusScope(boundary)
}

// restoreWindowFocus returns WM focus to the window that owned it when the
// closed overlay opened. When no window was focused (prior == ""), focus is
// cleared so the shell (editor/tree/chat) regains keyboard ownership rather
// than the WM snapping to whatever is on top of the z-order.
func (h *OverlayHost) restoreWindowFocus(prior string) {
	if h == nil || h.wm == nil {
		return
	}
	if prior != "" && h.wm.Window(prior) != nil {
		h.wm.FocusWindow(prior)
		return
	}
	h.wm.ClearFocus()
}
