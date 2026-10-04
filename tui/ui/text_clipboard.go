package ui

import (
	tea "charm.land/bubbletea/v2"
)

// TextClipboardTarget is the whole-value clipboard contract shared by the
// text-editing controls (TextArea, TextField). Hosts use it to copy, cut, or
// clear an entire value without knowing which control type owns it, so the
// chat composer, form fields, filters, and overlay editors expose the same
// capabilities from one implementation instead of per-host copies.
//
// Selection state itself (copy-on-release, chord handling, Esc) is owned by
// each control through textClipboardChord below.
type TextClipboardTarget interface {
	Component
	// TextValue returns the entire control value.
	TextValue() string
	// TextEditable reports whether the value accepts edits. Read-only
	// controls never report Cut/Clear as available.
	TextEditable() bool
	// TextSecret reports a masked control (password/API key). Such controls
	// refuse every clipboard command so rewritten text cannot be exfiltrated.
	TextSecret() bool
	// ReplaceTextValue replaces the entire value; Cut and Clear use it.
	ReplaceTextValue(v string)
	// TextSelection returns the active selected text, if any.
	TextSelection() (string, bool)
	// ClearTextSelection drops the selection without touching the text.
	ClearTextSelection()
	// DeleteTextSelection deletes the active selection with history.
	DeleteTextSelection() bool
}

// ClipboardChord identifies a shared clipboard chord in a key press so hosts
// can layer their own policy on top of the control contract.
type ClipboardChord uint8

const (
	// ClipboardChordNone is not a clipboard chord.
	ClipboardChordNone ClipboardChord = iota
	// ClipboardChordCopy is Ctrl+C. It copies a live selection; a host may
	// treat an *unhandled* Ctrl+C as "clear the draft" (the chat composer does).
	ClipboardChordCopy
	// ClipboardChordCopyExplicit is Ctrl+Shift+C, the pure copy chord used by
	// terminals. It never triggers a host fallback: an explicit copy with
	// nothing selected must stay a no-op.
	ClipboardChordCopyExplicit
	// ClipboardChordCut is Ctrl+X: cut the selection, or the whole value when
	// nothing is selected.
	ClipboardChordCut
)

// ClipboardChordFor classifies a key press against the shared chords. Alt is
// excluded so Alt+Ctrl combinations stay available to hosts.
func ClipboardChordFor(m tea.KeyPressMsg) ClipboardChord {
	if !m.Mod.Contains(tea.ModCtrl) && !m.Mod.Contains(tea.ModSuper) {
		return ClipboardChordNone
	}
	if m.Mod.Contains(tea.ModAlt) {
		return ClipboardChordNone
	}
	shift := m.Mod.Contains(tea.ModShift)
	// Key codes arrive lowercase for ctrl chords but uppercase from encodings
	// that report the shifted code point (kitty/CSI-u), so both are matched.
	switch m.Code {
	case 'c', 'C':
		if shift {
			return ClipboardChordCopyExplicit
		}
		return ClipboardChordCopy
	case 'x', 'X':
		if shift {
			return ClipboardChordNone
		}
		return ClipboardChordCut
	}
	return ClipboardChordNone
}

// IsCopyChord reports whether the key is any copy chord (Ctrl+C or
// Ctrl+Shift+C). Hosts that copy their own selection (editor, chat message
// list, overlays) use it so every copy entry point accepts both chords.
func IsCopyChord(m tea.KeyPressMsg) bool {
	switch ClipboardChordFor(m) {
	case ClipboardChordCopy, ClipboardChordCopyExplicit:
		return true
	}
	return false
}

// IsPasteChord reports the shared system-clipboard fallback chord. Shift is
// accepted because Windows and terminal key protocols can retain it when the
// same physical paste gesture is decoded as an application key event.
func IsPasteChord(m tea.KeyPressMsg) bool {
	if (!m.Mod.Contains(tea.ModCtrl) && !m.Mod.Contains(tea.ModSuper)) ||
		m.Mod.Contains(tea.ModAlt) {
		return false
	}
	return m.Code == 'v' || m.Code == 'V'
}

// textClipboardChord applies the shared text-selection/clipboard contract:
//
//	Ctrl+C, Ctrl+Shift+C  copy the selection (never mutates the text)
//	Ctrl+X                cut the selection, or the whole value when nothing is
//	                      selected — the same thing the context menu's Cut does
//	Esc                   drop the selection, leaving Esc to the host after
//
// copier builds the clipboard command so controls can inject their own write
// path (hosts) or a recorder (tests); callers pass their clipboardCopyFn.
//
// It reports handled=false for chords the host must own: Ctrl+C with no
// selection (the chat composer clears its draft) and Esc with no selection
// (popups close). Secret controls consume clipboard chords without copying.
func textClipboardChord(target TextClipboardTarget, copier func(string) tea.Cmd, m tea.KeyPressMsg) (EventResult, bool) {
	if m.Code == tea.KeyEsc {
		if _, ok := target.TextSelection(); !ok {
			return EventResult{}, false
		}
		target.ClearTextSelection()
		return EventResult{Handled: true, Invalidate: InvalidatePaint}, true
	}
	switch ClipboardChordFor(m) {
	case ClipboardChordCopy, ClipboardChordCopyExplicit:
		selected, ok := target.TextSelection()
		if !ok {
			return EventResult{}, false
		}
		if target.TextSecret() {
			return EventResult{Handled: true}, true
		}
		return EventResult{Handled: true, Cmd: copier(selected)}, true
	case ClipboardChordCut:
		if !target.TextEditable() {
			return EventResult{Handled: true}, true
		}
		if target.TextSecret() {
			return EventResult{Handled: true}, true
		}
		selected, ok := target.TextSelection()
		if ok && target.DeleteTextSelection() {
			return EventResult{
				Handled:    true,
				Cmd:        copier(selected),
				Invalidate: InvalidatePaint,
			}, true
		}
		whole := target.TextValue()
		target.ReplaceTextValue("")
		if whole == "" {
			return EventResult{Handled: true}, true
		}
		return EventResult{
			Handled:    true,
			Cmd:        copier(whole),
			Invalidate: InvalidatePaint,
		}, true
	}
	return EventResult{}, false
}

// TextEditorProvider is implemented by composite controls that own a
// text-editing control (form fields, filter/search fields, overlay editors).
// A right-click lands on the composite, so hosts ask it for the editor the
// clipboard commands must act on.
type TextEditorProvider interface {
	// TextEditor returns the editor this control owns, or nil.
	TextEditor() TextClipboardTarget
}

// TextClipboardTargetIn resolves the text-editing control behind a pointer
// hit: the hit component itself, or the editor a composite control owns.
func TextClipboardTargetIn(c Component) TextClipboardTarget {
	if c == nil {
		return nil
	}
	if target, ok := c.(TextClipboardTarget); ok {
		return target
	}
	if provider, ok := c.(TextEditorProvider); ok {
		return provider.TextEditor()
	}
	return nil
}

// TextControlAtProvider is implemented by composites that own their children's
// layout and therefore do not expose them to host hit-testing (forms). A host
// asks the composite which control sits under a composite-local point so a
// context menu edits the field the user pointed at.
type TextControlAtProvider interface {
	// TextControlAt returns the component owning the control at a local point,
	// or nil when the point is not over one.
	TextControlAt(x, y int) Component
}
