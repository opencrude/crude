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
func FormatShortcut(key string) string {
	parts := strings.Split(key, "+")
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = strings.ToUpper(p)
		} else {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
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
var defaultActions = []*ActionDef{
	{
		ID:          "palette.open",
		Label:       "Command Palette",
		Description: "search and run commands",
		Icon:        "⌕",
		Category:    "System",
		Bindings: []KeyBinding{
			{Key: "ctrl+alt+p"},
			{Key: "ctrl+alt+P"},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'p'},
			{Mod: tea.ModCtrl | tea.ModAlt, Code: 'P'},
		},
		HintKey: "Ctrl+Alt+P",
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
		ID:          "composer.toggle",
		Label:       "Composer",
		Description: "compose a detailed prompt",
		Icon:        "✏",
		Category:    "Tools",
		Bindings:    []KeyBinding{{Key: "alt+c"}},
		HintKey:     "Alt+C",
	},
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
		ID:          "settings.open",
		Label:       "Settings",
		Description: "workspace settings: theme, editor, LSP, UI, font",
		Icon:        "⚙",
		Category:    "Tools",
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
		ID:          "agent.orchestrator.settings",
		Label:       "Orchestrator Settings",
		Description: "override model and provider",
		Icon:        "⚙",
		Category:    "Agent",
	},
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
		ID:          "safety.yolo.toggle",
		Label:       "YOLO Mode",
		Description: "toggle safety checks",
		Icon:        "☠",
		Category:    "Tools",
	},
	{
		ID:          "system.undo",
		Label:       "Undo",
		Description: "revert last file change",
		Icon:        "↩",
		Category:    "System",
	},
	{
		ID:          "help.show",
		Label:       "Help",
		Description: "show commands and shortcuts",
		Icon:        "?",
		Category:    "Help",
	},
	{
		ID:          "app.quit",
		Label:       "Quit",
		Description: "exit immediately (does not close tabs first)",
		Icon:        "✖",
		Category:    "System",
		Bindings:    []KeyBinding{{Key: "ctrl+q"}},
		HintKey:     "Ctrl+Q",
	},
	{
		ID:          "app.sessions",
		Label:       "Welcome / Sessions",
		Description: "open the welcome launcher",
		Icon:        "☰",
		Category:    "System",
	},

	// ─── Editor context actions (FocusContext: "editor") ───
	{
		ID:           "file.save",
		Label:        "Save",
		Description:  "save current file",
		Icon:         "⭿",
		Category:     "File",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+s"}},
		HintKey:      "Ctrl+S",
	},
	{
		ID:           "edit.undo",
		Label:        "Undo",
		Description:  "undo last edit",
		Icon:         "↩",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+z"}},
		HintKey:      "Ctrl+Z",
	},
	{
		ID:           "edit.redo",
		Label:        "Redo",
		Description:  "redo last undone edit",
		Icon:         "↪",
		Category:     "Edit",
		FocusContext: "editor",
		Bindings:     []KeyBinding{{Key: "ctrl+y"}},
		HintKey:      "Ctrl+Y",
	},

	// ─── Chat input context actions (FocusContext: "chat") ───
	{
		ID:           "input.submit",
		Label:        "Send",
		Description:  "submit message to agent",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "Enter",
	},
	{
		ID:           "input.newline",
		Label:        "New line",
		Description:  "insert line break in input",
		Category:     "Chat",
		FocusContext: "chat",
		HintKey:      "Alt+Enter",
	},
}
