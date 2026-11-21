package viewmodel

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/core/port"
	"crude/tui/rendering"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/types"
)

// The palette includes replaced revisions. The review bar always reads its own
// revision, even when another session's chat is visible next to the editor.
func (m *App) browsePlanEvidence() tea.Cmd {
	agentID := m.activeRootAgent()
	if m.EditorState != nil {
		if doc, ok := m.planDocumentOf(m.EditorState.Editor); ok {
			agentID = doc.agentID
		}
	}
	review, _ := m.ws.PlanReviewForAgent(agentID)
	return m.planEvidenceMenu(agentID, review.Revisions)
}

func (m *App) planEvidenceMenu(agentID string, revisions []types.PlanRevision) tea.Cmd {
	menu := &ui.ContextMenuState{Title: "Investigation evidence"}
	owner, gen := m.ws, m.workspaceSwitchGen.Load()
	for n := len(revisions) - 1; n >= 0; n-- {
		rev := revisions[n]
		if len(rev.Evidence) == 0 {
			continue
		}
		section := ui.ContextMenuSection{SectionLabel: fmt.Sprintf("Revision %d", rev.Number)}
		if rev.EvidenceOmitted > 0 {
			section.SectionLabel += fmt.Sprintf(" · %d earlier results not shown", rev.EvidenceOmitted)
		}
		for i, ref := range rev.Evidence {
			label := strings.Join(strings.Fields(rendering.SanitizeForTerminal(ref.Command)), " ")
			section.Items = append(section.Items, ui.ContextMenuItem{Label: fmt.Sprintf("%d. %s", i+1, rendering.TruncateVisual(label, 72, "…")), Detail: string(ref.Status), Action: func() {
				if owner != m.ws || gen != m.workspaceSwitchGen.Load() {
					return
				}
				m.runDetachedAsyncCmd(m.readPlanEvidence(agentID, rev.Number, ref.Digest))
			}})
		}
		menu.Sections = append(menu.Sections, section)
	}
	if len(menu.Sections) == 0 {
		m.Feedback.NotifyInfo("This plan has no recorded investigation results.")
		return nil
	}
	return m.openHeaderPopup(menu)
}

type planEvidenceReadMsg struct {
	owner             shellport.Workspace
	generation, epoch uint64
	screen            *textoverlay.Screen
	text              string
	err               error
}

func (m *App) readPlanEvidence(agentID string, revision int, digest string) tea.Cmd {
	screen := m.sessionTextOverlay()
	if screen == nil || m.overlayHost == nil {
		return nil
	}
	m.overlayHost.Close(ui.OverlayTextOverlay)
	screen.OpenReadOnly("plan-evidence:"+digest, fmt.Sprintf("Investigation evidence · Revision %d", revision), "Reading evidence…")
	m.setMode(ModeTextOverlay)
	msg := planEvidenceReadMsg{owner: m.ws, generation: m.workspaceSwitchGen.Load(), screen: screen, epoch: screen.OpenEpoch()}
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		msg.text, msg.err = msg.owner.ReadPlanEvidence(ctx, agentID, revision, digest)
		return msg
	})
}

func (m *App) applyPlanEvidenceRead(msg planEvidenceReadMsg) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || msg.screen != m.sessionTextOverlay() || msg.screen.OpenEpoch() != msg.epoch || !msg.screen.IsOpen() || !m.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		return
	}
	if msg.err != nil {
		msg.text = "Could not read the evidence. " + sentence(msg.err.Error())
	}
	msg.screen.OpenReadOnly(msg.screen.RefID(), msg.screen.Title(), msg.text)
	m.markReferenceOverlay()
	msg.screen.FocusInput()
}
