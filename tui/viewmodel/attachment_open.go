package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/sessiondb"
	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Preparation and launch have separate Update admissions: a download/import
// cannot open an OS application after its session, pane or reference changes.
type attachmentOpenState struct {
	session     *workspace.Session
	store       workspace.SessionReferences
	ref         sessiondb.Reference
	generation  uint64
	agentID     string
	destination string
	ctx         context.Context
	cancel      context.CancelFunc
}

type attachmentPreparedMsg struct {
	owner    *attachmentOpenState
	ref      sessiondb.Reference
	path     string
	imported bool
	copied   bool
	err      error
}

type attachmentOpenedMsg struct {
	owner  *attachmentOpenState
	copied bool
	err    error
}

func (m *App) attachmentDestination() string {
	return m.ws.LoadAppSettings().UI.AttachmentOpenOrDefault()
}

func (m *App) cancelAttachmentOpen() {
	if m.attachmentOpen != nil {
		m.attachmentOpen.cancel()
		m.attachmentOpen = nil
	}
}

func (m *App) newAttachmentOpen(ref sessiondb.Reference, destination string) *attachmentOpenState {
	m.cancelAttachmentOpen()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	state := &attachmentOpenState{session: m.ActiveSession, store: m.RefStore(), ref: ref,
		generation: m.workspaceSwitchGen.Load(), agentID: m.activeAgentTabID(), destination: destination, ctx: ctx, cancel: cancel}
	m.attachmentOpen = state
	return state
}

func (m *App) ownsAttachmentOpen(state *attachmentOpenState) bool {
	return state != nil && m.attachmentOpen == state &&
		state.session == m.ActiveSession && state.store == m.RefStore() &&
		state.generation == m.workspaceSwitchGen.Load() && state.agentID == m.activeAgentTabID() && !m.asyncIOStopped.Load()
}

func (m *App) openHistoricalAttachment(key string) tea.Cmd {
	id, rawIndex, ok := strings.Cut(key, ":")
	index, err := strconv.Atoi(rawIndex)
	if !ok || err != nil || index < 0 || m.Prompts == nil {
		return nil
	}
	for _, entry := range m.Prompts.Entries() {
		if entry.ID != id || index >= len(entry.Attachments) {
			continue
		}
		attachment := entry.Attachments[index]
		if attachment.Unavailable {
			return nil
		}
		if m.RefStore() == nil {
			m.Feedback.NotifyError("Open an agent session to view attachments")
			return nil
		}
		state := m.newAttachmentOpen(attachment.Reference, m.attachmentDestination())
		history, connected := m.ws.(shellport.PromptHistoryAttachments)
		m.overlayHost.Close(ui.OverlayPromptHistory)
		return m.clientIOCommand(state.ctx, func(ctx context.Context) tea.Msg {
			var ref sessiondb.Reference
			var err error
			if connected {
				ref, err = history.ImportPromptHistoryAttachment(ctx, state.session.SessionID, entry.ID, index)
			} else {
				ref, err = state.store.ImportReference(state.ref)
			}
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return attachmentPreparedMsg{owner: state, ref: ref, imported: true, err: err}
		})
	}
	return nil
}

func (m *App) openAttachment(id string, reveal bool) tea.Cmd {
	destination := m.attachmentDestination()
	if reveal {
		destination = types.AttachmentOpenFileManager
	}
	return m.openAttachmentIn(id, destination)
}

// Explicit menu actions name their destination; only ordinary activation reads
// the preference. In particular "Open with default app" must never show Crude.
func (m *App) openAttachmentIn(id, destination string) tea.Cmd {
	store := m.RefStore()
	if store == nil {
		return nil
	}
	ref, ok := store.GetReference(id)
	if !ok || !ref.Active() || m.ActiveSession == nil || ref.SessionID != m.ActiveSession.SessionID {
		return nil
	}
	return m.openReferenceIn(ref, false, destination)
}

func (m *App) openReferenceIn(ref sessiondb.Reference, readOnly bool, destination string) tea.Cmd {
	if destination == types.AttachmentOpenCrude {
		m.cancelAttachmentOpen()
		return m.beginReferencePreview(ref, readOnly)
	}
	state := m.newAttachmentOpen(ref, destination)
	local := true
	if _, connected := m.ws.(shellport.ClientFiles); connected {
		host, ok := m.ws.(shellport.LocalHostAuthority)
		local = ok && host.LocalHost()
	}
	async := asyncIOStateFor(m)
	return m.clientIOCommand(state.ctx, func(ctx context.Context) tea.Msg {
		// Selected files on an authenticated local host remain real source files.
		// Captured/posted content is opened as a private snapshot, so another app
		// cannot silently edit the canonical attachment behind Crude's back.
		if local && ref.Kind == sessiondb.KindSelectedFile && ref.FilePath != "" {
			return attachmentPreparedMsg{owner: state, ref: ref, path: ref.FilePath}
		}
		path, err := prepareAttachmentCopy(ctx, async, state.store, ref)
		return attachmentPreparedMsg{owner: state, ref: ref, path: path, copied: true, err: err}
	})
}

func prepareAttachmentCopy(ctx context.Context, async *appAsyncIOState, store workspace.SessionReferences, ref sessiondb.Reference) (string, error) {
	data := []byte(ref.Content)
	name := ref.DisplayName() + ".txt"
	if ref.FilePath != "" {
		reader, ok := store.(interface {
			ReadReferenceMedia(context.Context, sessiondb.Reference) ([]byte, error)
		})
		if !ok {
			return "", fmt.Errorf("original attachment media is unavailable")
		}
		var err error
		data, err = reader.ReadReferenceMedia(ctx, ref)
		if err != nil {
			return "", err
		}
		name = ref.FilePath
	} else if ref.Kind == sessiondb.KindPastedImage {
		return "", fmt.Errorf("attachment has no captured image")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > 64<<20 {
		return "", fmt.Errorf("attachment exceeds 64 MiB")
	}
	cache, err := os.MkdirTemp("", "crude-attachment-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, desktopTargetName(name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(cache)
		return "", err
	}
	async.mu.Lock()
	defer async.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(cache)
		return "", err
	}
	async.desktopCaches = append(async.desktopCaches, cache)
	return path, nil
}

func (m *App) applyAttachmentPrepared(msg attachmentPreparedMsg) tea.Cmd {
	state := msg.owner
	if !m.ownsAttachmentOpen(state) {
		return nil
	}
	if msg.err != nil {
		m.applyAttachmentOpened(attachmentOpenedMsg{owner: state, err: msg.err})
		return nil
	}
	if msg.imported && msg.ref.Active() && msg.ref.SessionID == state.session.SessionID {
		state.store.AddExisting(msg.ref)
	}
	current, ok := state.store.GetReference(msg.ref.ID)
	if !ok || !current.Active() || current != msg.ref || current.SessionID != state.session.SessionID {
		m.applyAttachmentOpened(attachmentOpenedMsg{owner: state, err: fmt.Errorf("attachment changed while opening; open it again")})
		return nil
	}
	if msg.imported {
		return m.openReferenceIn(msg.ref, true, state.destination)
	}
	return m.clientIOCommand(state.ctx, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if current, ok := state.store.GetReference(msg.ref.ID); !ok || current != msg.ref {
			return attachmentOpenedMsg{owner: state, err: fmt.Errorf("attachment changed before opening; open it again")}
		}
		err := fileio.OpenDesktopPathContext(ctx, msg.path, state.destination == types.AttachmentOpenFileManager)
		return attachmentOpenedMsg{owner: state, copied: msg.copied, err: err}
	})
}

func (m *App) applyAttachmentOpened(msg attachmentOpenedMsg) {
	if !m.ownsAttachmentOpen(msg.owner) {
		return
	}
	m.cancelAttachmentOpen()
	if m.Feedback == nil {
		return
	}
	if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
		m.Feedback.NotifyError("Could not open attachment: " + msg.err.Error())
	} else if msg.copied && msg.err == nil {
		m.Feedback.NotifyInfo("Opened a local copy; the original attachment is unchanged")
	}
}
