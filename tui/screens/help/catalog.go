package help

import (
	"strings"

	"crude/tui/rendering"
	"crude/tui/ui"
)

// Command is one slash command. The viewmodel fills this from
// input.AvailableCmds so the overlay cannot drift from the slash popup.
type Command struct {
	Name  string
	Label string
	Desc  string
}

// Shortcut is one keybinding row. The viewmodel fills this from the action
// registry (HintFor + FormatShortcut) so displayed chords match live bindings.
type Shortcut struct {
	Category    string
	Keys        string
	Label       string
	Description string
}

// Topic is a teaching article. Update these when user-facing behavior
// changes. Slash commands and keyboard shortcuts are generated from live
// sources and must not be duplicated here.
type Topic struct {
	ID    string
	Title string
	Body  string
}

const (
	rowSection rowKind = iota
	rowTopic
	rowCommand
	rowShortcut
)

type rowKind uint8

type rowData struct {
	Kind  rowKind
	Lines []string
}

// UsageTopics is the how-to-use Crude guide. Keep it current: when a
// workflow is added or renamed, edit the matching article here.
func UsageTopics() []Topic {
	return []Topic{
		{
			ID:    "start",
			Title: "Getting started",
			Body:  "Crude is a terminal IDE with an agent. The file tree is on the left, the editor or chat in the middle, and the agent input at the bottom. The Command Palette searches every action so you do not have to memorize chords. This Help window is rebuilt from live slash commands and keybindings whenever you open it.",
		},
		{
			ID:    "chat",
			Title: "Chat",
			Body:  "Type in the input at the bottom and press Enter to send. Alt+Enter inserts a newline. Type / to pick a slash command, use @ to reference a file, or press Ctrl+O to open Attach File from any non-terminal view. Ctrl+C copies an active input or chat-message selection. The agent can edit the project; tool confirmations and questions appear in the dock above the input, not as chat spam.",
		},
		{
			ID:    "palette",
			Title: "Command Palette",
			Body:  "Open the Command Palette and type part of an action name (settings, find, sessions, theme). Every runnable command lives there. Help itself is in the Help category, and F1 opens this window from anywhere.",
		},
		{
			ID:    "sessions",
			Title: "Sessions",
			Body:  "Each conversation is a session with its own agent settings. Resume a past session from the Sessions overlay or /sessions. New Session starts a fresh chat in the current workspace without leaving the project.",
		},
		{
			ID:    "memory",
			Title: "Workspace memory",
			Body:  "The agent keeps durable memory per workspace: conventions, decisions, preferences, and lessons persist across sessions, and every past turn is searchable (the agent's search_history tool answers \"what did we do last time\"). Relevant memories are recalled into context automatically; a background curator extracts new ones after turns and consolidates duplicates. Browse, pin, or forget them in the Workspace Memory window (/memory or Ctrl+Alt+M) — forgetting keeps an audit trail. Tune or disable everything under Settings > Memory.",
		},
		{
			ID:    "settings",
			Title: "Settings",
			Body:  "Application Settings are global (theme, editor, LSP, credentials). Workspace Settings overlay those for this project. /settings opens the session dock for per-agent provider, model, thinking, and multi-agent. Save writes only the layer you are editing.",
		},
		{
			ID:    "editor",
			Title: "Editor",
			Body:  "Open files from the tree or Find in Files. Ctrl+[ and Ctrl+] switch tabs globally. Ctrl+W closes the active editor tab; Esc is contextual Back and also closes the tab after transient editor UI is dismissed. Unsaved changes always open the Save / Don't Save / Cancel prompt. Diagnostics, go-to-definition, references, and outline use the workspace language server when LSP is enabled in settings. Find/replace is an overlay on the current buffer. Find in Files and Replace in Files are sibling project windows (Ctrl+Alt+F / Ctrl+Alt+H). Editing is IDE-grade: Enter auto-indents, brackets and quotes auto-close and wrap selections, Tab/Shift+Tab indent or outdent selected lines, Alt+Up/Down moves lines, Ctrl+Shift+D duplicates, Ctrl+Shift+K deletes, and Ctrl+/ toggles comments — each is a single undo step.",
		},
		{
			ID:    "files",
			Title: "Files and the tree",
			Body:  "The left rail is Files, Agents, Terminals, and Git. Files is the project tree: just start typing to fuzzy-filter it (Esc clears; matches highlight and ancestors auto-expand), Ctrl+R refreshes, Ctrl+. toggles hidden files. The other views list open agents, PTY sessions, and git-changed files — click selects, double-click or Enter opens. Ctrl+B hides the sidebar body; the rail stays.",
		},
		{
			ID:    "docks",
			Title: "Session docks",
			Body:  "Session AI settings, agent questions, tool confirmation, YOLO, and background jobs sit in the session dock between the chat and the input. Arrow keys move inside a list; Tab reaches Save/Cancel; Esc closes forms. Terminals live in a separate workspace dock above the status bar.",
		},
		{
			ID:    "terminals",
			Title: "Terminals",
			Body:  "New Terminal opens a libghostty-vt PTY (TERM=xterm-ghostty, version 1.3.0) in the panel right of the sidebar. The panel header holds the tabs plus + (new), ⇱ (float), and ✕ (close); a floating terminal docks back from the ⇲ button in its title bar — same PTY, no restart. Ctrl+` toggles focus; Esc/Ctrl+C/Ctrl+Q go to the shell while focused. Drag-select copies on release (Shift bypasses app mouse tracking), Ctrl+Shift+C copies, Shift+PgUp/PgDn scrolls history, and the wheel scrolls full-screen apps too. Closing a tab or window kills that PTY and its process group.",
		},
		{
			ID:    "safety",
			Title: "Safety",
			Body:  "The agent asks before destructive or network tools. Deny (n) explicitly rejects a permission; Esc stops the turn without recording a denial. /yolo disables checks for the live session, including the current turn, after confirmation. /undo reverts the last agent file edit; /undo all reverts every edit in this session. Undo and context compaction require the active turn to be stopped first.",
		},
	}
}

func BuildItems(width int, query string, cmds []Command, shortcuts []Shortcut) []ui.VirtItem {
	if width < 8 {
		width = 8
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var items []ui.VirtItem

	appendSection := func(id, title string) {
		items = append(items, ui.VirtItem{
			ID:      "section:" + id,
			Height:  2,
			Label:   title,
			Payload: rowData{Kind: rowSection, Lines: []string{title}},
		})
	}

	topics := filterTopics(UsageTopics(), q)
	if len(topics) > 0 {
		appendSection("usage", "Using Crude")
		for _, topic := range topics {
			items = append(items, topicItem(topic, width))
		}
	}

	matchedCmds := filterCommands(cmds, q)
	if len(matchedCmds) > 0 {
		appendSection("slash", "Slash commands")
		for _, cmd := range matchedCmds {
			items = append(items, commandItem(cmd, width))
		}
	}

	groups := groupShortcuts(filterShortcuts(shortcuts, q))
	if len(groups) > 0 {
		appendSection("keys", "Keyboard shortcuts")
		for _, g := range groups {
			if g.category != "" {
				items = append(items, ui.VirtItem{
					ID:      "cat:" + strings.ToLower(g.category),
					Height:  1,
					Label:   g.category,
					Payload: rowData{Kind: rowSection, Lines: []string{g.category}},
				})
			}
			for _, sc := range g.items {
				items = append(items, shortcutItem(sc, width))
			}
		}
	}

	if len(items) > 0 && items[0].Height == 2 {
		items[0].Height = 1
	}
	return items
}

func topicItem(topic Topic, width int) ui.VirtItem {
	bodyWidth := max(8, width-2)
	lines := []string{topic.Title}
	for _, line := range rendering.WrapText(topic.Body, bodyWidth) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return ui.VirtItem{
		ID:      "topic:" + topic.ID,
		Height:  max(1, len(lines)),
		Label:   topic.Title,
		Detail:  topic.Body,
		Payload: rowData{Kind: rowTopic, Lines: lines},
	}
}

func commandItem(cmd Command, width int) ui.VirtItem {
	label := "/" + cmd.Label
	detail := cmd.Desc
	line := rendering.TruncateVisual(label+"  "+detail, width, "…")
	return ui.VirtItem{
		ID:      "cmd:" + cmd.Name,
		Height:  1,
		Label:   label,
		Detail:  detail,
		Payload: rowData{Kind: rowCommand, Lines: []string{line}},
	}
}

func shortcutItem(sc Shortcut, width int) ui.VirtItem {
	detail := sc.Label
	if sc.Description != "" && sc.Description != sc.Label {
		detail = sc.Label + "  ·  " + sc.Description
	}
	keysW := rendering.VisibleWidth(sc.Keys)
	pad := max(1, 18-keysW)
	line := rendering.TruncateVisual(sc.Keys+strings.Repeat(" ", pad)+detail, width, "…")
	return ui.VirtItem{
		ID:      "key:" + sc.Category + ":" + sc.Label,
		Height:  1,
		Label:   sc.Keys,
		Detail:  detail,
		Payload: rowData{Kind: rowShortcut, Lines: []string{line}},
	}
}

func filterTopics(topics []Topic, q string) []Topic {
	if q == "" {
		return topics
	}
	var out []Topic
	for _, topic := range topics {
		if match(q, topic.Title, topic.Body, topic.ID) {
			out = append(out, topic)
		}
	}
	return out
}

func filterCommands(cmds []Command, q string) []Command {
	if q == "" {
		return cmds
	}
	var out []Command
	for _, cmd := range cmds {
		if match(q, cmd.Name, cmd.Label, cmd.Desc, "/"+cmd.Label) {
			out = append(out, cmd)
		}
	}
	return out
}

func filterShortcuts(shortcuts []Shortcut, q string) []Shortcut {
	if q == "" {
		return shortcuts
	}
	var out []Shortcut
	for _, sc := range shortcuts {
		if match(q, sc.Category, sc.Keys, sc.Label, sc.Description) {
			out = append(out, sc)
		}
	}
	return out
}

type shortcutGroup struct {
	category string
	items    []Shortcut
}

func groupShortcuts(shortcuts []Shortcut) []shortcutGroup {
	var groups []shortcutGroup
	for _, sc := range shortcuts {
		cat := sc.Category
		if cat == "" {
			cat = "General"
		}
		if len(groups) == 0 || groups[len(groups)-1].category != cat {
			groups = append(groups, shortcutGroup{category: cat})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, sc)
	}
	return groups
}

func match(q string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), q) {
			return true
		}
	}
	return false
}
