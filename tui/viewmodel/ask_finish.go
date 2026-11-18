package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/prompt"
	"crude/types"
)

// "Finish this change" sends the user's recent edits in the file to the chat
// beside the code and asks the agent to carry the change through the project.
// The edit history comes from the core's document owner, so it names exactly
// what the user changed, not what an agent or the disk changed.

const finishChangeEdits = 12

type finishChangeMsg struct {
	generation uint64
	path, name string
	edits      []types.RecentEdit
	err        error
}

// runFinishChange starts "Finish this change" from the editor's Crude menu.
func (m *App) runFinishChange() {
	ed := m.EditorState.Editor
	port, connected := m.suggestionPort()
	if ed == nil || !ed.IsOpen() || !connected {
		return
	}
	if ed.FilePath() == "" {
		m.Feedback.NotifyInfo("Save this file first. Then ask Crude to finish the change.")
		return
	}
	path, generation := ed.FilePath(), m.workspaceSwitchGen.Load()
	base, _ := m.lspRequestContext()
	m.runDetachedAsyncCmd(m.clientIOCommand(base, func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		edits, err := port.RecentEdits(ctx, path, 64)
		return finishChangeMsg{generation: generation, path: path, name: filepath.Base(path), edits: edits, err: err}
	}))
}

// applyFinishChange sends the prepared message once the edits arrive.
func (m *App) applyFinishChange(msg finishChangeMsg) []tea.Cmd {
	if msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Crude could not read your recent edits: " + msg.err.Error())
		return nil
	}
	diff := finishChangeDiff(msg.edits)
	if diff == "" {
		m.Feedback.NotifyInfo("Change this file first. Then Crude can finish the change in other places.")
		return nil
	}
	ed := m.EditorState.Editor
	if ed == nil || ed.FilePath() != msg.path || m.Win == nil {
		return nil
	}
	rel := msg.name
	if r, err := filepath.Rel(m.ws.RootPath(), msg.path); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	req := &askRequest{generation: msg.generation, group: m.Win.ActiveGroup(), path: msg.path, name: msg.name,
		kind: askFinish, text: prompt.FinishChange(rel, diff)}
	cmds := m.askSave(ed, req)
	if group, agentID := m.askVisibleChat(req.group, true); agentID != "" {
		m.focusMainGroup(group)
		m.setFocusInput()
		return append(cmds, m.askAttachTo(agentID, req))
	}
	return append(cmds, m.askNewChat(req))
}

// finishChangeDiff renders the user's own edits, oldest first, as a small
// unified-style diff.
func finishChangeDiff(edits []types.RecentEdit) string {
	var own []types.RecentEdit
	for _, e := range edits {
		if e.Author == types.RecentEditYou {
			own = append(own, e)
		}
	}
	if len(own) > finishChangeEdits {
		own = own[len(own)-finishChangeEdits:]
	}
	var b strings.Builder
	for _, e := range own {
		fmt.Fprintf(&b, "@@ line %d\n", e.Line)
		for _, line := range strings.Split(strings.TrimSuffix(e.Removed, "\n"), "\n") {
			if e.Removed != "" {
				b.WriteString("-" + line + "\n")
			}
		}
		for _, line := range strings.Split(strings.TrimSuffix(e.Inserted, "\n"), "\n") {
			if e.Inserted != "" {
				b.WriteString("+" + line + "\n")
			}
		}
	}
	return b.String()
}
