package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ActionID is a namespaced identifier for a user-invokable action.
// Convention: "domain.verb" (e.g. "palette.open", "file.save").
type ActionID string

// KeyBinding describes one key sequence that triggers an action.
type KeyBinding struct {
	// Key is the Bubble Tea key string (e.g. "ctrl+alt+p", "alt+c").
	Key string
	// Mod is the modifier mask (alternative matching for terminals
	// that encode modifiers differently, e.g. tmux ESC-prefix).
	Mod tea.KeyMod
	// Code is the key code for mod-based matching (0 = use Key only).
	Code int32
}

// MatchesKey reports whether a KeyPressMsg matches this binding.
func (b KeyBinding) MatchesKey(msg tea.KeyPressMsg) bool {
	if b.Key != "" && msg.String() == b.Key {
		return true
	}
	if b.Mod != 0 && msg.Mod.Contains(b.Mod) && b.Code != 0 && msg.Code == b.Code {
		return true
	}
	return false
}

// FocusContext is the focus target where a binding is active.
// Empty = global (works everywhere). Non-empty = only when that
// focus target is active.
type FocusContext string

// ActionDef describes a user-invokable command with its metadata.
// The command palette, launcher shortcuts, status-bar hints, help
// surfaces, and keyboard dispatch all read from the same Action
// registry so displayed shortcuts can never drift from active
// bindings.
//
// This is distinct from the runtime `Action` struct (component.go)
// which is the event emitted by a control when the user activates
// it. ActionDef is the metadata definition; Action is the event.
type ActionDef struct {
	ID           ActionID
	Label        string // display label (e.g. "Find in Files")
	Description  string // short description for palette/help
	Icon         string // optional icon glyph
	Category     string // grouping label (e.g. "File", "Tools")
	Bindings     []KeyBinding
	FocusContext FocusContext // empty = global
	HintKey      string       // shortcut label for hints (e.g. "Ctrl+Alt+F")
	// DisplayOnly marks a registry entry that exists only to feed the
	// status-bar HintFor / HintLabelFor helpers — it has no runnable
	// command and MUST be hidden from the command palette. Status-bar
	// hint fragments (Close, Next, Navigate, …) are DisplayOnly.
	DisplayOnly bool
}

// ActionRegistry holds the set of registered actions. It is the
// single source of truth for keyboard shortcuts and their display
// labels.
//
// Phase 5 foundation: the registry is defined here. Product keyboard
// dispatch migrates to query the registry over time. Until then,
// the existing keyboard.go switches coexist with the registry —
// the registry is authoritative for display labels (palette,
// launcher, status hints) and the keyboard dispatch is being
// progressively migrated to call MatchGlobal.
type ActionRegistry struct {
	actions []*ActionDef
	byID    map[ActionID]*ActionDef
}

// NewActionRegistry creates an empty registry.
func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{byID: make(map[ActionID]*ActionDef)}
}

// Register adds an action to the registry. If an action with the
// same ID already exists, it is replaced.
func (r *ActionRegistry) Register(a *ActionDef) {
	if r.byID == nil {
		r.byID = make(map[ActionID]*ActionDef)
	}
	if existing, ok := r.byID[a.ID]; ok {
		// Replace in-place in the slice.
		for i, e := range r.actions {
			if e == existing {
				r.actions[i] = a
				break
			}
		}
	} else {
		r.actions = append(r.actions, a)
	}
	r.byID[a.ID] = a
}

// Get returns the action for id, or nil.
func (r *ActionRegistry) Get(id ActionID) *ActionDef {
	if r.byID == nil {
		return nil
	}
	return r.byID[id]
}

// All returns all registered actions.
func (r *ActionRegistry) All() []*ActionDef {
	out := make([]*ActionDef, len(r.actions))
	copy(out, r.actions)
	return out
}

// ByCategory groups actions by their Category field.
func (r *ActionRegistry) ByCategory() map[string][]*ActionDef {
	out := make(map[string][]*ActionDef)
	for _, a := range r.actions {
		out[a.Category] = append(out[a.Category], a)
	}
	return out
}

// MatchGlobal checks all global bindings (FocusContext == "") against
// the key press and returns the matching action, or nil.
func (r *ActionRegistry) MatchGlobal(msg tea.KeyPressMsg) *ActionDef {
	for _, a := range r.actions {
		if a.FocusContext != "" {
			continue
		}
		for _, b := range a.Bindings {
			if b.MatchesKey(msg) {
				return a
			}
		}
	}
	return nil
}

// MatchContext checks bindings for the given focus context and
// returns the matching action, or nil.
func (r *ActionRegistry) MatchContext(ctx FocusContext, msg tea.KeyPressMsg) *ActionDef {
	for _, a := range r.actions {
		if a.FocusContext != ctx {
			continue
		}
		for _, b := range a.Bindings {
			if b.MatchesKey(msg) {
				return a
			}
		}
	}
	return nil
}

// HintFor returns the display shortcut string for an action ID,
// or "" if the action has no bindings.
func (r *ActionRegistry) HintFor(id ActionID) string {
	a := r.Get(id)
	if a == nil || a.HintKey != "" {
		if a != nil {
			return a.HintKey
		}
		return ""
	}
	if len(a.Bindings) == 0 {
		return ""
	}
	return a.Bindings[0].Key
}

// HintLabelFor returns a combined shortcut+label string suitable for
// rendering in the status-bar contextual help section. The format is
// "<Shortcut> <label>" (e.g. "Ctrl+S save", "Enter send"). Returns ""
// if the action has no HintKey or Bindings.
func (r *ActionRegistry) HintLabelFor(id ActionID) string {
	a := r.Get(id)
	if a == nil {
		return ""
	}
	hint := r.HintFor(id)
	if hint == "" {
		return ""
	}
	return FormatShortcut(hint) + " " + strings.ToLower(a.Label)
}

// FormatShortcut returns a human-readable shortcut string from a
// key string, normalizing for display (e.g. "ctrl+alt+p" → "Ctrl+Alt+P").
// Capitalisation is rune-correct so multi-byte glyphs (↑↓, emoji) are
// never split into invalid UTF-8 / U+FFFD replacement characters.
func FormatShortcut(key string) string {
	parts := strings.Split(key, "+")
	for i, p := range parts {
		runes := []rune(p)
		if len(runes) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(string(runes[0])) + string(runes[1:])
	}
	return strings.Join(parts, "+")
}

// DefaultRegistry returns a registry pre-populated with the global
// actions used by Crude. This is the single source of truth for
// shortcuts — the command palette, launcher, status hints, and
// keyboard dispatch all read from here.
func DefaultRegistry() *ActionRegistry {
	r := NewActionRegistry()
	for _, a := range defaultActions {
		r.Register(a)
	}
	return r
}

// defaultActions is the canonical list of global actions and their
// keybindings. Product code registers additional context-specific
// actions at runtime, but these globals are always available.
//
// Categories (used as palette group headers, in order of definition):
//
//	File, Edit, View, Tabs, Agent, Tools, System, Help, Chat, App.
//
// Hidden from the palette (excluded by paletteEntriesFromRegistry):
//   - palette.open          (self-reference; opening it from itself is a no-op)
//   - any entry with DisplayOnly: true (status-bar hint fragments)
//
// Icons are single-rune glyphs that render at display width 1 under
// the shared grapheme-width path. Do NOT use emoji or ZWJ sequences
// here; the layout math in tui/ui/list.go would break.
var defaultActions = []*ActionDef{
	// ─── Self-reference: hidden from the palette (palette.open is the
	//     menu you launched the palette FROM; never shown as a result). ───
	{
		ID:          "palette.open",
		Label:       "Command Palette",
		Description: "search and run commands",
		Icon:        "⌘",
		Category:    "System",
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+p"},
			{Key: "ctrl+alt+P"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'p'},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'P'},
		},
		HintKey: "Ctrl+Alt+P",
	},

	// ─── File ────────────────────────────────────────────────────────
	{
		ID:           "file.save",
		Label:        "Save",
		Description:  "save current file",
		Icon:         "↓",
		Category:     "File",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+s"}},
		HintKey:      "Ctrl+S",
	},
	{
		ID:          "file.attach",
		Label:       "Attach File",
		Description: "add a file reference",
		Icon:        "⧉",
		Category:    "File",
		HintKey:     "Ctrl+O",
	},
	{
		ID:          "file.new",
		Label:       "New File",
		Description: "create a new file in the workspace",
		Icon:        "+",
		Category:    "File",
	},
	{
		ID:          "file.close",
		Label:       "Close Tab",
		Description: "close the active editor tab",
		Icon:        "✕",
		Category:    "File",
		Bindings:    []KeyBinding{{Key: "ctrl+w"}},
		HintKey:     "Ctrl+W",
	},
	{
		ID:          "file.revealInTree",
		Label:       "Reveal in Tree",
		Description: "expand the file tree to the active file",
		Icon:        "▸",
		Category:    "File",
	},
	{
		ID:          "search.project.open",
		Label:       "Find in Files",
		Description: "search file names and contents",
		Icon:        "⌕",
		Category:    "File",
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+f"},
			{Key: "ctrl+alt+F"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'f'},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'F'},
		},
		HintKey: "Ctrl+Alt+F",
	},
	{
		ID:          "search.project.replace",
		Label:       "Replace in Files",
		Description: "find and replace across the project",
		Icon:        "⇅",
		Category:    "File",
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+h"},
			{Key: "ctrl+alt+H"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'h'},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'H'},
		},
		HintKey: "Ctrl+Alt+H",
	},

	// ─── Edit ────────────────────────────────────────────────────────
	{
		ID:           "edit.undo",
		Label:        "Undo Typing",
		Description:  "undo the last editor edit",
		Icon:         "↩",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+z"}},
		HintKey:      "Ctrl+Z",
	},
	{
		ID:           "edit.redo",
		Label:        "Redo Typing",
		Description:  "redo the last undone editor edit",
		Icon:         "↪",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+y"}},
		HintKey:      "Ctrl+Y",
	},
	{
		ID:           "edit.find",
		Label:        "Find",
		Description:  "search within the current file",
		Icon:         "≡",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+f"}},
		HintKey:      "Ctrl+F",
	},
	{
		ID:           "edit.replace",
		Label:        "Replace",
		Description:  "find and replace within the current file",
		Icon:         "⇄",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+h"}},
		HintKey:      "Ctrl+H",
	},
	{
		ID:           "edit.moveLineUp",
		Label:        "Move Line Up",
		Description:  "move the current line or selection up",
		Icon:         "⇡",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "alt+up"}, {Mod: tea.ModAlt, Code: int32(tea.KeyUp)}},
		HintKey:      "Alt+Up",
	},
	{
		ID:           "edit.moveLineDown",
		Label:        "Move Line Down",
		Description:  "move the current line or selection down",
		Icon:         "⇣",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "alt+down"}, {Mod: tea.ModAlt, Code: int32(tea.KeyDown)}},
		HintKey:      "Alt+Down",
	},
	{
		ID:           "edit.duplicateLine",
		Label:        "Duplicate Line",
		Description:  "duplicate the current line or selection",
		Icon:         "⊕",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+shift+d"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'd'}},
		HintKey:      "Ctrl+Shift+D",
	},
	{
		ID:           "edit.deleteLine",
		Label:        "Delete Line",
		Description:  "delete the current line or selected lines",
		Icon:         "✂",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+shift+k"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'k'}},
		HintKey:      "Ctrl+Shift+K",
	},
	{
		ID:           "edit.toggleComment",
		Label:        "Toggle Line Comment",
		Description:  "comment or uncomment the current line or selection",
		Icon:         "#",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+/"}, {Key: "ctrl+_"}},
		HintKey:      "Ctrl+/",
	},
	{
		ID:           "tree.refresh",
		Label:        "Refresh Tree",
		Description:  "reload the file tree from disk",
		Category:     "View",
		FocusContext: "tree",
		Bindings:     []KeyBinding{{Key: "ctrl+r"}},
		HintKey:      "Ctrl+R",
		DisplayOnly:  true,
	},
	{
		ID:           "tree.toggleHidden",
		Label:        "Toggle Hidden Files",
		Description:  "show or hide dotfiles in the file tree",
		Category:     "View",
		FocusContext: "tree",
		Bindings:     []KeyBinding{{Key: "ctrl+."}},
		HintKey:      "Ctrl+.",
		DisplayOnly:  true,
	},

	// ─── View ────────────────────────────────────────────────────────
	{
		ID:          "view.toggle.sidebar",
		Label:       "Toggle Sidebar",
		Description: "show or hide the file tree",
		Icon:        "☰",
		Category:    "View",
		Bindings:    []KeyBinding{{Key: "ctrl+b"}},
		HintKey:     "Ctrl+B",
	},
	{
		ID:          "view.focus.sidebar",
		Label:       "Focus File Tree",
		Description: "move keyboard focus to the file tree",
		Icon:        "▤",
		Category:    "View",
		Bindings: []KeyBinding{
			{Key: "ctrl+1"},
			{Mod: tea.ModCtrl, Code: '1'},
		},
		HintKey: "Ctrl+1",
	},
	{
		ID:          "view.focus.editor",
		Label:       "Focus Editor",
		Description: "move keyboard focus to the editor",
		Icon:        "▥",
		Category:    "View",
	},
	{
		ID:          "view.focus.chat",
		Label:       "Focus Chat",
		Description: "move keyboard focus to the chat input",
		Icon:        "▣",
		Category:    "View",
	},
	{
		ID:          "view.activity.files",
		Label:       "Files",
		Description: "show the file explorer in the sidebar",
		Icon:        "F",
		Category:    "View",
		Bindings:    []KeyBinding{{Key: "ctrl+shift+e"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'e'}},
		HintKey:     "Ctrl+Shift+E",
	},
	{
		ID:          "view.activity.agents",
		Label:       "Agents",
		Description: "show open agents in the sidebar",
		Icon:        "A",
		Category:    "View",
		Bindings:    []KeyBinding{{Key: "ctrl+shift+a"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'a'}},
		HintKey:     "Ctrl+Shift+A",
	},
	{
		ID:          "view.activity.terminals",
		Label:       "Terminals",
		Description: "show open terminals in the sidebar",
		Icon:        "T",
		Category:    "View",
		Bindings:    []KeyBinding{{Key: "ctrl+shift+u"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'u'}},
		HintKey:     "Ctrl+Shift+U",
	},
	{
		ID:          "view.activity.git",
		Label:       "Git",
		Description: "show git status in the sidebar",
		Icon:        "G",
		Category:    "View",
		Bindings:    []KeyBinding{{Key: "ctrl+shift+g"}, {Mod: tea.ModCtrl | tea.ModShift, Code: 'g'}},
		HintKey:     "Ctrl+Shift+G",
	},
	{
		ID:          "terminal.new",
		Label:       "New Terminal",
		Description: "open a new tabbed terminal (Ghostty-pinned PTY)",
		Icon:        "$",
		Category:    "View",
		Bindings: []KeyBinding{
			{Key: "ctrl+shift+`"},
			{Mod: tea.ModCtrl | tea.ModShift, Code: '`'},
		},
		HintKey: "Ctrl+Shift+`",
	},
	{
		ID:          "terminal.toggle",
		Label:       "Toggle Terminal",
		Description: "focus the last terminal or open a new one",
		Icon:        ">_",
		Category:    "View",
		Bindings: []KeyBinding{
			{Key: "ctrl+`"},
			{Mod: tea.ModCtrl, Code: '`'},
		},
		HintKey: "Ctrl+`",
	},

	// ─── Tabs ────────────────────────────────────────────────────────
	{
		ID:          "tabs.next",
		Label:       "Next Tab",
		Description: "switch to the next editor tab",
		Icon:        "→",
		Category:    "Tabs",
		Bindings:    []KeyBinding{{Key: "ctrl+]"}},
		HintKey:     "Ctrl+]",
	},
	{
		ID:          "tabs.previous",
		Label:       "Previous Tab",
		Description: "switch to the previous editor tab",
		Icon:        "←",
		Category:    "Tabs",
		Bindings:    []KeyBinding{{Key: "ctrl+["}},
		HintKey:     "Ctrl+[",
	},

	// ─── Agent ───────────────────────────────────────────────────────
	{
		ID:          "agent.orchestrator.reset",
		Label:       "Reset Overrides",
		Description: "clear orchestrator overrides",
		Icon:        "↺",
		Category:    "Agent",
	},
	{
		ID:          "agent.toggle.multiagent",
		Label:       "Toggle Multi-Agent",
		Description: "enable or disable sub-agents",
		Icon:        "☆",
		Category:    "Agent",
	},
	{
		ID:          "agent.cancel",
		Label:       "Cancel Agent",
		Description: "stop the running agent",
		Icon:        "⊘",
		Category:    "Agent",
	},
	{
		ID:          "agent.newSession",
		Label:       "New Session",
		Description: "start a fresh session",
		Icon:        "✦",
		Category:    "Agent",
	},

	// ─── Tools (LSP, Composer, Settings, Safety) ─────────────────────
	{
		ID:          "composer.toggle",
		Label:       "Composer",
		Description: "compose a detailed prompt",
		Icon:        "✏",
		Category:    "Tools",
		Bindings:    []KeyBinding{{Key: "alt+c"}},
		HintKey:     "Alt+C",
	},
	{
		ID:          "settings.open",
		Label:       "Application Settings",
		Description: "app-wide theme, editor, LSP, UI, font, and credentials",
		Icon:        "⚙",
		Category:    "Tools",
	},
	{
		ID:          "settings.workspace",
		Label:       "Workspace Settings",
		Description: "project overrides on top of application settings",
		Icon:        "⬡",
		Category:    "Tools",
	},
	{
		ID:          "lsp.outline",
		Label:       "Go to Symbol",
		Description: "document outline — jump to any function or class",
		Icon:        "§",
		Category:    "Tools",
		Bindings:    []KeyBinding{{Key: "alt+o"}},
		HintKey:     "Alt+O",
	},
	{
		ID:          "safety.yolo.toggle",
		Label:       "YOLO Mode",
		Description: "toggle safety checks",
		Icon:        "☠",
		Category:    "Tools",
	},

	// ─── System (sessions, undo, theme, copy) ────────────────────────
	{
		ID:          "app.sessions",
		Label:       "Sessions",
		Description: "browse and resume past sessions",
		Icon:        "⊟",
		Category:    "System",
		// Ctrl+Alt+S: free of collisions with palette (Ctrl+Alt+P), find
		// (Ctrl+Alt+F), sidebar (Ctrl+B), composer (Alt+C), save (Ctrl+S
		// editor-only), and quit (Ctrl+Q). Available while tabs are open —
		// the launcher is unreachable then.
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+s"},
			{Key: "ctrl+alt+S"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 's'},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'S'},
		},
		HintKey: "Ctrl+Alt+S",
	},
	{
		ID:          "system.undo",
		Label:       "Undo Agent File Change",
		Description: "revert the last file change made by the agent",
		Icon:        "⤺",
		Category:    "System",
	},
	{
		ID:          "app.toggleTheme",
		Label:       "Toggle Theme",
		Description: "cycle through theme presets",
		Icon:        "◐",
		Category:    "System",
	},
	{
		ID:          "app.copyLastAssistant",
		Label:       "Copy Last Assistant",
		Description: "copy the last assistant message to clipboard",
		Icon:        "⎘",
		Category:    "System",
	},
	{
		ID:          "app.quit",
		Label:       "Quit",
		Description: "exit immediately",
		Icon:        "✖",
		Category:    "System",
		Bindings:    []KeyBinding{{Key: "ctrl+q"}},
		HintKey:     "Ctrl+Q",
	},

	// ─── Help ────────────────────────────────────────────────────────
	{
		ID:          "help.show",
		Label:       "Help",
		Description: "open help: usage, slash commands, and shortcuts",
		Icon:        "?",
		Category:    "Help",
		Bindings:    []KeyBinding{{Key: "f1"}},
		HintKey:     "F1",
	},

	// ─── Chat input context actions (FocusContext: "chat") ───
	// DisplayOnly: "Send" and "New line" are contextual hints for the
	// status bar, not palette commands. They fire through the chat
	// input's own key dispatch (dispatchChatKey → input.Update), not
	// through executePaletteCommand. Marking them DisplayOnly keeps
	// them out of the palette (where they would be inert dead entries)
	// while HintLabelFor continues to surface them in the status bar.
	{
		ID:           "input.submit",
		Label:        "Send",
		Description:  "submit message to agent",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "Enter",
		DisplayOnly:  true,
	},
	{
		ID:           "input.newline",
		Label:        "New line",
		Description:  "insert line break in input",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "Alt+Enter",
		DisplayOnly:  true,
	},

	// ─── Contextual status-bar hints (display-first) ───
	{
		ID:          "editor.close",
		Label:       "Close",
		Category:    "Editor",
		HintKey:     "Esc",
		DisplayOnly: true,
	},
	{
		ID:          "focus.tab.next",
		Label:       "Next",
		Category:    "Focus",
		HintKey:     "Tab",
		DisplayOnly: true,
	},
	{
		ID:          "focus.tab.previous",
		Label:       "Previous",
		Category:    "Focus",
		HintKey:     "Shift+Tab",
		DisplayOnly: true,
	},
	{
		ID:          "panel.previous",
		Label:       "Prev panel",
		Category:    "Panel",
		HintKey:     "Ctrl+[",
		DisplayOnly: true,
	},
	{
		ID:          "panel.next",
		Label:       "Next panel",
		Category:    "Panel",
		HintKey:     "Ctrl+]",
		DisplayOnly: true,
	},
	{
		ID:          "panel.close",
		Label:       "Back",
		Category:    "Panel",
		HintKey:     "Esc",
		DisplayOnly: true,
	},
	{
		ID:          "tabbar.previous",
		Label:       "Navigate",
		Category:    "Tabs",
		HintKey:     "←",
		DisplayOnly: true,
	},
	{
		ID:          "tabbar.next",
		Label:       "Navigate",
		Category:    "Tabs",
		HintKey:     "→",
		DisplayOnly: true,
	},
	{
		ID:          "tabbar.select",
		Label:       "Select",
		Category:    "Tabs",
		HintKey:     "Enter",
		DisplayOnly: true,
	},
	{
		ID:          "tabbar.unfocus",
		Label:       "Unfocus",
		Category:    "Tabs",
		HintKey:     "Esc",
		DisplayOnly: true,
	},
	{
		ID:          "list.open",
		Label:       "Open",
		Category:    "List",
		HintKey:     "Enter",
		DisplayOnly: true,
	},
	{
		ID:          "list.navigate",
		Label:       "Navigate",
		Category:    "List",
		HintKey:     "↑↓",
		DisplayOnly: true,
	},
	{
		ID:          "list.preview.navigate",
		Label:       "Preview",
		Category:    "Search",
		HintKey:     "↑↓",
		DisplayOnly: true,
	},
	{
		ID:          "list.open.mouse",
		Label:       "Open",
		Category:    "List",
		HintKey:     "Double-click",
		DisplayOnly: true,
	},
	{
		ID:          "overlay.close",
		Label:       "Close",
		Category:    "Overlay",
		HintKey:     "Esc",
		DisplayOnly: true,
	},
	{
		ID:          "search.regex.toggle",
		Label:       "Regex",
		Category:    "Search",
		HintKey:     "Ctrl+R",
		DisplayOnly: true,
	},
	{
		ID:          "launcher.filter",
		Label:       "Search",
		Category:    "Launcher",
		HintKey:     "/",
		DisplayOnly: true,
	},
	{
		ID:          "launcher.filter.clear",
		Label:       "Clear",
		Category:    "Launcher",
		HintKey:     "Esc",
		DisplayOnly: true,
	},
	{
		ID:          "launcher.delete",
		Label:       "Remove",
		Category:    "Launcher",
		HintKey:     "D",
		DisplayOnly: true,
	},
	{
		ID:           "input.history",
		Label:        "History",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "↑↓",
		DisplayOnly:  true,
	},
	{
		ID:           "input.cancel",
		Label:        "Exit",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "Esc",
		DisplayOnly:  true,
	},
}
