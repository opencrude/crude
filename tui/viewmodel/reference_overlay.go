package viewmodel

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/imagepreview"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/types/attachment"
	"crude/workspace"
)

type referenceOverlayState struct {
	session *workspace.Session
	store   workspace.SessionReferences
	screen  *textoverlay.Screen
	ref     sessiondb.Reference

	generation uint64

	graphics tea.ImageProtocol
	agentID  string
	epoch    uint64
	ctx      context.Context
	cancel   context.CancelFunc
}

type referenceContentLoadedMsg struct {
	owner   *referenceOverlayState
	content string

	err      error
	image    image.Image
	resource *tea.TerminalImage
}

func (m *App) beginReferencePreview(ref sessiondb.Reference) tea.Cmd {
	store, screen := m.RefStore(), m.sessionTextOverlay()
	if store == nil || screen == nil || m.ActiveSession == nil || !ref.Active() || ref.SessionID != m.ActiveSession.SessionID {
		return nil
	}
	m.cancelAttachmentOpen()
	// Close the old owner before opening new content; closing the old frame
	// after Open would clear this same retained screen's new value.
	m.overlayHost.Close(ui.OverlayTextOverlay)
	screen.OpenReadOnly(ref.ID, ref.Caption(), "Loading attachment…")
	m.setMode(ModeTextOverlay)
	state := &referenceOverlayState{session: m.ActiveSession, store: store, screen: screen, ref: ref, generation: m.workspaceSwitchGen.Load(), graphics: m.terminalImages.protocol(), agentID: m.activeAgentTabID(), epoch: screen.OpenEpoch()}
	state.ctx, state.cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	m.referenceOverlay = state
	return m.clientIOCommand(state.ctx, func(ctx context.Context) tea.Msg {
		loading := *state
		loading.ctx = ctx
		result := loadReferencePreview(&loading)
		result.owner = state
		return result
	})
}

func loadReferencePreview(state *referenceOverlayState) referenceContentLoadedMsg {
	ctx := state.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return referenceContentLoadedMsg{owner: state, err: err}
	}
	if state.ref.Kind == sessiondb.KindPastedImage || strings.HasPrefix(state.ref.Mime, "image/") {
		reader, ok := state.store.(interface {
			ReadReferenceMedia(context.Context, sessiondb.Reference) ([]byte, error)
		})
		if !ok {
			return referenceContentLoadedMsg{owner: state, err: fmt.Errorf("original attachment media is unavailable")}
		}
		data, err := reader.ReadReferenceMedia(ctx, state.ref)
		if err != nil {
			return referenceContentLoadedMsg{owner: state, err: err}
		}
		img, resource, err := imagepreview.Decode(ctx, data, state.graphics)
		return referenceContentLoadedMsg{owner: state, image: img, resource: resource, err: err}
	}
	content, _, err := state.store.ReadReferenceContent(state.ref.ID)
	if ctx.Err() != nil {
		err = ctx.Err()
		content = ""
	}
	return referenceContentLoadedMsg{owner: state, content: content, err: err}
}

func (m *App) openChatAttachment(payload chatprojection.ChatAttachmentOpenPayload) tea.Cmd {
	list := m.activeMessageList()
	if list == nil {
		return nil
	}
	message := list.MessageByID(payload.MessageID)
	if message == nil || !payload.Target.Valid(message.Content) {
		return nil
	}
	retained := false
	for _, target := range message.Attachments {
		if target == payload.Target {
			retained = true
			break
		}
	}
	if !retained && payload.Target.ReferenceID == "" {
		for _, label := range message.ReferenceLabels {
			if label == payload.Target.Label {
				retained = true
				break
			}
		}
	}
	if !retained {
		return nil
	}
	if store := m.RefStore(); store != nil {
		if ref, ok := store.Reference(payload.Target.ReferenceID); ok && ref.Active() && m.ActiveSession != nil && ref.SessionID == m.ActiveSession.SessionID {
			return m.openReferenceIn(ref, true, m.attachmentDestination())
		}
	}
	m.showUnavailableReference(payload.Target)
	return nil
}

func (m *App) showUnavailableReference(target attachment.Target) {
	screen := m.sessionTextOverlay()
	if screen == nil {
		return
	}
	m.overlayHost.Close(ui.OverlayTextOverlay)
	screen.OpenReadOnly("unavailable-attachment", target.Label, "Attachment unavailable. The message does not retain an accessible reference identity. Reattach the original content to view it.")
	m.setMode(ModeTextOverlay)
	m.referenceOverlay = nil
	m.markReferenceOverlay()
	screen.FocusInput()
}

func (m *App) ownsReferenceOverlay(state *referenceOverlayState) bool {
	return state != nil && m.referenceOverlay == state && state.session == m.ActiveSession &&
		state.store == m.RefStore() && state.generation == m.workspaceSwitchGen.Load() &&
		state.screen == m.sessionTextOverlay() && state.agentID == m.activeAgentTabID() && state.epoch == state.screen.OpenEpoch() && m.overlayHost.IsOpen(ui.OverlayTextOverlay)
}

func (m *App) markReferenceOverlay() {
	if window := m.overlayHost.Window(ui.OverlayTextOverlay); window != nil {
		if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
			frame.Host().RegisterTree(frame.Host().Root())
			frame.Host().MarkLayout()
		}
		window.MarkContentDirty()
	}
}

func (m *App) applyReferenceContent(msg referenceContentLoadedMsg) {
	state := msg.owner
	if !m.ownsReferenceOverlay(state) {
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	if msg.err != nil {
		state.screen.OpenReadOnly(state.ref.ID, state.ref.Caption(), "Attachment could not be opened.\n"+msg.err.Error())
		m.Feedback.NotifyError("Could not open attachment: " + msg.err.Error())
	} else {
		current, ok := state.store.Reference(state.ref.ID)
		if !ok || current != state.ref {
			m.setMode(ModeChat)
			m.Feedback.NotifyError("The attachment changed while it was loading; open it again")
			return
		}
		if msg.image != nil {
			state.screen.OpenImage(state.ref.ID, state.ref.Caption(), msg.image)
			state.screen.SetImageResource(msg.resource)
			m.configureReferenceGraphics(state.screen)
		} else {
			state.screen.OpenReadOnly(state.ref.ID, state.ref.Caption(), msg.content)
		}
	}
	state.epoch = state.screen.OpenEpoch()
	m.markReferenceOverlay()
	state.screen.FocusInput()
}
