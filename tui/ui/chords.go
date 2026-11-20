package ui

import (
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Semantic chords are shortcuts whose physical key depends on what the
// attached terminal can deliver. Ctrl+Enter and Shift+Enter only exist when
// the terminal disambiguates modified Enter (Windows console input, the kitty
// keyboard protocol); tmux and most legacy terminals send a plain Enter for
// both. Alt+Enter reaches every Unix terminal but Windows Terminal and conhost
// consume it to toggle fullscreen.
//
// Every caption (hints, button labels, help text, semantics) and every matcher
// goes through these tokens, so a label can never name a key the matcher
// refuses or the terminal swallows. Tokens are valid ActionBarButton.Shortcut
// and ActionDef.HintKey values; FormatShortcut resolves them.
const (
	// CommitChord submits a multi-line field or its primary action.
	// Accepts Ctrl+Enter and Alt+Enter (Ctrl+Alt+Enter stays distinct).
	CommitChord = "chord:commit"
	// CodeActionsChord opens read-only editor action discovery. It uses the
	// same deliverable Enter modifiers as commit, without committing an edit.
	CodeActionsChord = "chord:code-actions"
	// NewlineChord inserts a line break in a field where Enter sends.
	// Accepts Shift+Enter, Alt+Enter and Ctrl+J (LF, which every terminal
	// delivers distinctly from Enter's CR).
	NewlineChord = "chord:newline"
	// PreviousMatchChord steps backwards through search matches.
	// Accepts Shift+Enter and Shift+F3.
	PreviousMatchChord = "chord:previous-match"
	// ReplaceAllChord replaces every match from a find/replace popup.
	// Accepts Ctrl+Alt+Enter and Alt+Enter (tmux drops Ctrl from the former).
	ReplaceAllChord      = "chord:replace-all"
	EditorLineBelowChord = "chord:editor-line-below"
	EditorLineAboveChord = "chord:editor-line-above"
)

// AskCrudeKey asks Crude about the editor's code. Every terminal delivers
// Ctrl+K as its own key, and no terminal or multiplexer binds it by default.
const AskCrudeKey = "ctrl+k"

// lockModifiers are keyboard states, not parts of a chord. With the kitty
// keyboard protocol a terminal reports them on every non-text key, so with
// Num Lock on, Ctrl+K arrives as Ctrl+Num Lock+K.
const lockModifiers = tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

// shortcutModifiers make a key a shortcut instead of text.
const shortcutModifiers = tea.ModCtrl | tea.ModAlt | tea.ModSuper | tea.ModMeta | tea.ModHyper

// KeyboardEnhancements are the kitty keyboard protocol features every
// program requests. Alternate keys add the US-layout key to modified keys, so
// shortcuts keep working while a non-Latin keyboard layout is active.
func KeyboardEnhancements() tea.KeyboardEnhancements {
	return tea.KeyboardEnhancements{ReportAlternateKeys: true}
}

// NormalizeKeyMsg gives every matcher one key shape. Every program calls it
// at key ingress:
//   - lock modifiers are removed;
//   - a shortcut without text and with a non-ASCII printable key (Ctrl+ش
//     on a Persian layout) uses the reported US-layout key (Ctrl+A);
//   - an ASCII layout key stays as typed (Ctrl+A on AZERTY stays Ctrl+A),
//     and the alternate code is dropped so that String agrees with Code.
func NormalizeKeyMsg(msg tea.Msg) tea.Msg {
	switch k := msg.(type) {
	case tea.KeyPressMsg:
		if key, changed := normalizeKey(tea.Key(k)); changed {
			return tea.KeyPressMsg(key)
		}
	case tea.KeyReleaseMsg:
		if key, changed := normalizeKey(tea.Key(k)); changed {
			return tea.KeyReleaseMsg(key)
		}
	}
	return msg
}

func normalizeKey(k tea.Key) (tea.Key, bool) {
	changed := false
	if k.Mod&lockModifiers != 0 {
		k.Mod &^= lockModifiers
		changed = true
	}
	if k.BaseCode != 0 && k.BaseCode != k.Code {
		if k.Text == "" && k.Mod&shortcutModifiers != 0 && nonASCIIPrintable(k.Code) && asciiKey(k.BaseCode) {
			k.Code = unicode.ToLower(k.BaseCode)
			k.ShiftedCode = 0
		}
		k.BaseCode = 0
		changed = true
	}
	return k, changed
}

// SymbolShortcutFallback gives the shell a second candidate for an ASCII
// symbol chord, e.g. French Ctrl+$ on the PC-101 ] key. Try the logical chord
// first: in particular, Alt+; must still work where ; moved to another key.
// Letters stay mnemonic, and events that carry literal text are not shortcuts.
func SymbolShortcutFallback(k tea.KeyPressMsg) rune {
	if k.Text == "" && k.Mod&shortcutModifiers != 0 &&
		k.Code != k.BaseCode && asciiSymbol(k.Code) && asciiSymbol(k.BaseCode) {
		return k.BaseCode
	}
	return 0
}

func asciiKey(r rune) bool { return r > ' ' && r < 0x7f }
func asciiSymbol(r rune) bool {
	return asciiKey(r) && !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// nonASCIIPrintable reports a printable key outside ASCII. Special keys (arrows,
// function keys) are outside the Unicode range and never match.
func nonASCIIPrintable(r rune) bool {
	return r >= 0x80 && r <= unicode.MaxRune && unicode.IsPrint(r)
}

// IsModifiedEscape distinguishes a chord (such as Windows Ctrl+Shift+Esc)
// from the shell's unmodified Back/Stop key. Lock state never counts.
func IsModifiedEscape(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeyEsc && k.Mod&^lockModifiers != 0
}

// KeyboardProfile records which modified Enter chords reach the application.
type KeyboardProfile struct {
	// ModifiedEnter: Ctrl+Enter and Shift+Enter arrive as distinct keys.
	ModifiedEnter bool
	// AltEnterReserved: the hosting terminal consumes Alt+Enter itself.
	AltEnterReserved bool
}

// DetectKeyboardProfile derives the startup profile from the platform and
// environment. The Windows console reports modifiers on every key and both
// Windows Terminal and conhost bind Alt+Enter to fullscreen; WT_SESSION marks
// Windows Terminal hosting a Unix shell (WSL, SSH).
func DetectKeyboardProfile(goos string, getenv func(string) string) KeyboardProfile {
	profile := KeyboardProfile{}
	if goos == "windows" {
		profile.ModifiedEnter = true
		profile.AltEnterReserved = true
	}
	if getenv != nil && getenv("WT_SESSION") != "" {
		profile.AltEnterReserved = true
	}
	return profile
}

var (
	keyboardProfileBits atomic.Uint32
	keyboardProfileGen  atomic.Uint64
)

const (
	profileModifiedEnter uint32 = 1 << iota
	profileAltEnterReserved
)

func init() {
	SetKeyboardProfile(DetectKeyboardProfile(runtime.GOOS, os.Getenv))
}

// CurrentKeyboardProfile returns the process keyboard profile.
func CurrentKeyboardProfile() KeyboardProfile {
	bits := keyboardProfileBits.Load()
	return KeyboardProfile{
		ModifiedEnter:    bits&profileModifiedEnter != 0,
		AltEnterReserved: bits&profileAltEnterReserved != 0,
	}
}

// SetKeyboardProfile replaces the process keyboard profile. Components that
// cache resolved captions compare KeyboardProfileGeneration.
func SetKeyboardProfile(profile KeyboardProfile) {
	var bits uint32
	if profile.ModifiedEnter {
		bits |= profileModifiedEnter
	}
	if profile.AltEnterReserved {
		bits |= profileAltEnterReserved
	}
	if keyboardProfileBits.Swap(bits) != bits {
		keyboardProfileGen.Add(1)
	}
}

// NoteKeyboardEnhancements records the terminal's answer to the kitty
// keyboard query: key disambiguation means modified Enter is delivered.
func NoteKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) {
	if !msg.SupportsKeyDisambiguation() {
		return
	}
	profile := CurrentKeyboardProfile()
	profile.ModifiedEnter = true
	SetKeyboardProfile(profile)
}

// KeyboardProfileGeneration changes whenever resolved captions may change.
func KeyboardProfileGeneration() uint64 { return keyboardProfileGen.Load() }

// IsCommitChord reports whether a key commits a multi-line field or its
// primary action.
func IsCommitChord(m tea.KeyPressMsg) bool {
	if m.Code != tea.KeyEnter || m.Mod.Contains(tea.ModShift) {
		return false
	}
	ctrl, alt := m.Mod.Contains(tea.ModCtrl), m.Mod.Contains(tea.ModAlt)
	return ctrl != alt
}

// IsNewlineChord reports whether a key inserts a line break in a field whose
// plain Enter sends.
func IsNewlineChord(m tea.KeyPressMsg) bool {
	if m.Code == 'j' && m.Mod == tea.ModCtrl {
		return true
	}
	if m.Code != tea.KeyEnter || m.Mod.Contains(tea.ModCtrl) {
		return false
	}
	return m.Mod.Contains(tea.ModShift) || m.Mod.Contains(tea.ModAlt)
}

// IsPreviousMatchChord reports whether a key steps to the previous match.
func IsPreviousMatchChord(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "shift+enter", "shift+f3":
		return true
	}
	return false
}

// IsReplaceAllChord reports whether a key replaces every match.
func IsReplaceAllChord(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "ctrl+alt+enter", "alt+enter":
		return true
	}
	return false
}

// ChordLabel returns the display caption of a semantic chord token for the
// current keyboard profile. ok is false for an ordinary key string.
func ChordLabel(token string) (label string, ok bool) {
	profile := CurrentKeyboardProfile()
	switch token {
	case EditorLineBelowChord:
		if profile.ModifiedEnter {
			return "Shift+Enter", true
		}
		return "Ctrl+Alt+O", true
	case EditorLineAboveChord:
		if profile.ModifiedEnter {
			return "Ctrl+Alt+Enter", true
		}
		return "Ctrl+Alt+Shift+O", true
	case CommitChord, CodeActionsChord:
		if profile.ModifiedEnter || profile.AltEnterReserved {
			return "Ctrl+Enter", true
		}
		return "Alt+Enter", true
	case NewlineChord:
		switch {
		case profile.ModifiedEnter:
			return "Shift+Enter", true
		case profile.AltEnterReserved:
			return "Ctrl+J", true
		}
		return "Alt+Enter", true
	case PreviousMatchChord:
		if profile.ModifiedEnter {
			return "Shift+Enter", true
		}
		return "Shift+F3", true
	case ReplaceAllChord:
		if profile.ModifiedEnter || profile.AltEnterReserved {
			return "Ctrl+Alt+Enter", true
		}
		return "Alt+Enter", true
	}
	return "", false
}

// TerminalPasteLabel names the terminal's own paste shortcut, which delivers
// a bracketed paste even where the application cannot read the system
// clipboard (SSH, headless tmux).
func TerminalPasteLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "Cmd+V"
	case "windows":
		return "Ctrl+V"
	}
	return "Ctrl+Shift+V"
}

// ClipboardReadUnavailable is the one message for a Ctrl+V whose system
// clipboard read returned nothing.
func ClipboardReadUnavailable() string {
	return "Clipboard is empty or unreadable here; paste with your terminal's " + TerminalPasteLabel()
}

// MatchesShortcut reports whether a key press triggers a shortcut string: a
// semantic chord token or a Bubble Tea key string compared case-insensitively
// ("Ctrl+S" and "ctrl+s" are the same shortcut).
func MatchesShortcut(m tea.KeyPressMsg, shortcut string) bool {
	switch shortcut {
	case "":
		return false
	case EditorLineBelowChord:
		return m.Code == tea.KeyEnter && m.Mod == tea.ModShift || (m.Code == 'o' || m.Code == 'O') && m.Mod == tea.ModCtrl|tea.ModAlt
	case EditorLineAboveChord:
		return m.Code == tea.KeyEnter && m.Mod == tea.ModCtrl|tea.ModAlt || (m.Code == 'o' || m.Code == 'O') && m.Mod == tea.ModCtrl|tea.ModAlt|tea.ModShift
	case CommitChord, CodeActionsChord:
		return IsCommitChord(m)
	case NewlineChord:
		return IsNewlineChord(m)
	case PreviousMatchChord:
		return IsPreviousMatchChord(m)
	case ReplaceAllChord:
		return IsReplaceAllChord(m)
	}
	return strings.EqualFold(m.String(), shortcut)
}
