package viewmodel

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"
	"time"

	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/screens/textoverlay"
	"crude/tui/ui"
	"crude/types/attachment"
	"crude/workspace"
)

type referenceOverlayState struct {
	session    *workspace.Session
	store      workspace.SessionReferences
	screen     *textoverlay.Screen
	ref        sessiondb.Reference
	original   string
	generation uint64
	loaded     bool
	saving     bool
	readOnly   bool
	agentID    string
	epoch      uint64
	ctx        context.Context
	cancel     context.CancelFunc
}

type referenceContentLoadedMsg struct {
	owner    *referenceOverlayState
	content  string
	readOnly bool
	err      error
	image    image.Image
	resource *tea.KittyImage
}

type referenceContentSavedMsg struct {
	owner *referenceOverlayState
	err   error
}

func (m *App) openReferenceOverlay(id string) {
	m.runDetachedAsyncCmd(m.beginReferenceOverlay(id))
}

func (m *App) beginReferenceOverlay(id string) tea.Cmd {
	store, screen := m.RefStore(), m.sessionTextOverlay()
	if store == nil || screen == nil {
		return nil
	}
	ref, ok := store.GetReference(id)
	if !ok || !ref.Active() {
		return nil
	}
	return m.beginReferencePreview(ref, false)
}

func (m *App) beginReferencePreview(ref sessiondb.Reference, readOnly bool) tea.Cmd {
	store, screen := m.RefStore(), m.sessionTextOverlay()
	if store == nil || screen == nil || m.ActiveSession == nil || !ref.Active() || ref.SessionID != m.ActiveSession.SessionID {
		return nil
	}
	m.cancelAttachmentOpen()
	// Close the old owner before opening new content; closing the old frame
	// after Open would clear this same retained screen's new value.
	m.overlayHost.Close(ui.OverlayTextOverlay)
	screen.OpenReadOnly(ref.ID, ref.DisplayName(), "Loading attachment…")
	m.setMode(ModeTextOverlay)
	state := &referenceOverlayState{session: m.ActiveSession, store: store, screen: screen, ref: ref, generation: m.workspaceSwitchGen.Load(), readOnly: readOnly, agentID: m.activeAgentTabID(), epoch: screen.OpenEpoch()}
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
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err == nil && (config.Width <= 0 || config.Height <= 0 || config.Width > (32*1024*1024)/config.Height) {
			err = fmt.Errorf("image dimensions exceed the native preview limit")
		}
		if err != nil {
			return referenceContentLoadedMsg{owner: state, err: err}
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if ctx.Err() != nil {
			err = ctx.Err()
			img = nil
		}
		var resource *tea.KittyImage
		if err == nil {
			// Keep original PNG bytes; other decoded formats become PNG once on
			// this existing off-UI read worker, never during Paint or resize.
			if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
				var encoded bytes.Buffer
				err = png.Encode(&encoded, img)
				data = encoded.Bytes()
			}
			if err == nil && ctx.Err() == nil {
				resource = tea.NewKittyImage(data)
			}
		}
		if ctx.Err() != nil {
			err, img, resource = ctx.Err(), nil, nil
		}
		return referenceContentLoadedMsg{owner: state, image: img, resource: resource, readOnly: true, err: err}
	}
	content, readOnly, err := state.store.ReadReferenceContent(state.ref.ID)
	if ctx.Err() != nil {
		err = ctx.Err()
		content = ""
	}
	return referenceContentLoadedMsg{owner: state, content: content, readOnly: readOnly || state.readOnly, err: err}
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
		if ref, ok := store.GetReference(payload.Target.ReferenceID); ok && ref.Active() && m.ActiveSession != nil && ref.SessionID == m.ActiveSession.SessionID {
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
		state.screen.OpenReadOnly(state.ref.ID, state.ref.DisplayName(), "Attachment could not be opened.\n"+msg.err.Error())
		m.Feedback.NotifyError("Could not open attachment: " + msg.err.Error())
	} else {
		current, ok := state.store.GetReference(state.ref.ID)
		if !ok || current != state.ref {
			m.setMode(ModeChat)
			m.Feedback.NotifyError("The attachment changed while it was loading; open it again")
			return
		}
		state.original, state.loaded = msg.content, true
		if msg.image != nil {
			state.screen.OpenImage(state.ref.ID, state.ref.DisplayName(), msg.image)
			state.screen.SetImageResource(msg.resource)
			m.configureReferenceGraphics(state.screen)
		} else if msg.readOnly || state.readOnly {
			state.screen.OpenReadOnly(state.ref.ID, state.ref.DisplayName(), msg.content)
		} else {
			state.screen.Open(state.ref.ID, state.ref.DisplayName(), msg.content)
		}
	}
	state.epoch = state.screen.OpenEpoch()
	m.markReferenceOverlay()
	state.screen.FocusInput()
}

func (m *App) saveReferenceFromOverlay(msg textoverlay.SaveMsg) tea.Cmd {
	state := m.referenceOverlay
	if !m.ownsReferenceOverlay(state) || !state.loaded || state.saving || state.screen.ReadOnly() {
		return nil
	}
	state.saving = true
	state.screen.SetBusy(true)
	m.markReferenceOverlay()
	return m.clientIOCommand(context.Background(), func(context.Context) tea.Msg {
		err := state.store.SaveReferenceContent(state.ref, state.original, msg.Text)
		return referenceContentSavedMsg{owner: state, err: err}
	})
}

func (m *App) applyReferenceSave(msg referenceContentSavedMsg) {
	if !m.ownsReferenceOverlay(msg.owner) {
		return
	}
	if msg.err != nil {
		msg.owner.saving = false
		msg.owner.screen.SetBusy(false)
		m.markReferenceOverlay()
		m.Feedback.NotifyError("Could not save attachment: " + msg.err.Error() + ". Your edits are kept.")
		return
	}
	m.setMode(ModeChat)
	m.persistComposerDraftIfChanged()
	m.Feedback.NotifyInfo("Pasted text saved")
}
