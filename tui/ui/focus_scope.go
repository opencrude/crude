package ui

import "strings"

// FocusScope is a saved focus boundary on a Host. When an overlay opens, the
// Host captures the current focus into a scope and (when the boundary is
// mounted) focuses the scope's boundary component, so closing the overlay can
// restore the EXACT prior focus path. Modal scopes additionally trap keyboard
// focus within their subtree.
//
// Focus scopes live on the Host (the single focus authority), not in the
// window manager, so any component subtree can declare a focus scope (modal
// dialogs, popovers, menus) independent of how floating windows are layered.
type FocusScope struct {
	// boundary is the component subtree this scope owns. RootID scopes the
	// whole host. A scope's boundary is a ComponentID (an overlay's window
	// ID in the common case).
	boundary ComponentID
	// saved is the focus to restore when the scope is popped.
	saved ComponentID
	// modal scopes trap keyboard focus within boundary.
	modal bool
}

// PushFocusScope saves the current focus, focuses boundary when it is mounted,
// and records the scope. The boundary focuses only when registered in this
// Host's tree; otherwise the current focus is left in place (the scope still
// captures and restores it on pop). A boundary of RootID scopes the whole host.
func (h *Host) PushFocusScope(boundary ComponentID, modal bool) {
	if h == nil {
		return
	}
	h.scopes = append(h.scopes, FocusScope{boundary: boundary, saved: h.focused, modal: modal})
	if boundary != RootID {
		h.Focus(boundary)
	}
	h.needsPnt = true
}

// PopFocusScope pops the topmost scope and restores the focus it captured.
// Returns the restored focus (RootID when the stack is empty or the saved
// focus is not mounted).
func (h *Host) PopFocusScope() ComponentID {
	if h == nil || len(h.scopes) == 0 {
		return RootID
	}
	top := h.scopes[len(h.scopes)-1]
	h.scopes = h.scopes[:len(h.scopes)-1]
	h.restoreFocus(top.saved)
	return h.focused
}

// RemoveFocusScope drops the scope whose boundary matches WITHOUT restoring
// its saved focus. Used when a non-topmost overlay closes: the focus belongs
// to the overlay above it and must not be clobbered. If the scope above had
// captured the removed boundary as its own restore target, that saved is
// rewound to the removed scope's saved so the eventual top-close still
// restores the focus that existed before the removed overlay opened.
func (h *Host) RemoveFocusScope(boundary ComponentID) bool {
	if h == nil {
		return false
	}
	for i := len(h.scopes) - 1; i >= 0; i-- {
		if h.scopes[i].boundary == boundary {
			removed := h.scopes[i]
			h.scopes = append(h.scopes[:i], h.scopes[i+1:]...)
			if i < len(h.scopes) && h.scopes[i].saved == boundary {
				h.scopes[i].saved = removed.saved
			}
			return true
		}
	}
	return false
}

// FocusScopes returns the boundary IDs of all active scopes, bottom-to-top.
func (h *Host) FocusScopes() []ComponentID {
	if h == nil {
		return nil
	}
	out := make([]ComponentID, len(h.scopes))
	for i, s := range h.scopes {
		out[i] = s.boundary
	}
	return out
}

// HasFocusScope reports whether a scope with the given boundary is active.
func (h *Host) HasFocusScope(boundary ComponentID) bool {
	if h == nil {
		return false
	}
	for _, s := range h.scopes {
		if s.boundary == boundary {
			return true
		}
	}
	return false
}

// FocusScopeModal reports whether the topmost scope traps keyboard focus.
func (h *Host) FocusScopeModal() bool {
	if h == nil || len(h.scopes) == 0 {
		return false
	}
	return h.scopes[len(h.scopes)-1].modal
}

// FocusWithinScope reports whether id is inside the topmost scope's boundary
// subtree (the whole host when the boundary is RootID).
func (h *Host) FocusWithinScope(id ComponentID) bool {
	if h == nil || len(h.scopes) == 0 {
		return false
	}
	top := h.scopes[len(h.scopes)-1]
	if top.boundary == RootID {
		return true
	}
	if id == top.boundary {
		return true
	}
	return strings.HasPrefix(string(id), string(top.boundary)+"/")
}

// restoreFocus returns keyboard focus to the saved component of a popped
// scope. Mounted components go through Focus (which honors any modal scope
// that remains below); unmounted saved IDs are assigned directly so an
// overlay boundary from a deeper scope survives a pop and Layout validates it.
func (h *Host) restoreFocus(id ComponentID) {
	if id == RootID {
		h.focused = RootID
		h.needsPnt = true
		return
	}
	if _, ok := h.registry[id]; ok {
		h.Focus(id)
		return
	}
	h.focused = id
	h.needsPnt = true
}
