package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/editor"
	remotesscreen "crude/tui/screens/remotes"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
)

const remoteEditorOverlay ui.OverlayID = "remote-connection"

type remoteManagerState struct {
	manager  shellport.RemoteManager
	current  string
	snapshot remoteconn.Snapshot
	status   map[string]shellport.RemoteStatus
	errors   map[string]string
	expanded map[string]bool
	loading  bool
	sequence uint64
	latest   map[string]uint64
	hosts    []string
	jobs     map[*remoteManagerJob]struct{}
	editor   *remotesscreen.ConnectionEditor
	editorID string
	cancel   context.CancelFunc
	stopped  bool
	notice   string
	install  *remoteInstallConsent
}
type remoteManagerJob struct {
	cancel context.CancelFunc
	start  tea.Cmd
	done   chan struct{}
	editor *remotesscreen.ConnectionEditor
	action string
}
type remoteManagerResult struct {
	owner      shellport.Workspace
	generation uint64
	state      *remoteManagerState
	sequence   uint64
	job        *remoteManagerJob
	editor     *remotesscreen.ConnectionEditor
	action, id string
	snapshot   remoteconn.Snapshot
	hosts      []string
	status     shellport.RemoteStatus
	err        error
	draft      shellport.RemoteConnectionDraft
}

func (remoteManagerResult) String() string     { return "[remote manager result redacted]" }
func (r remoteManagerResult) GoString() string { return r.String() }

type remoteRecordUseMsg struct {
	manager shellport.RemoteManager
	err     error
}

// SetRemoteManager is configured once by the client host, alongside its private
// draft profile. It never imports the selected core's files or credentials.
func (m *App) SetRemoteManager(manager shellport.RemoteManager, initialBackend string) {
	retainedJobs := map[*remoteManagerJob]struct{}{}
	if old := m.remotes; old != nil {
		m.cancelRemoteEditor()
		old.stopped = true
		for job := range old.jobs {
			job.cancel()
			retainedJobs[job] = struct{}{}
		}
	}
	if initialBackend == "" {
		initialBackend = remoteconn.Local
	}
	m.remotes = &remoteManagerState{manager: manager, current: initialBackend, status: map[string]shellport.RemoteStatus{}, errors: map[string]string{}, expanded: map[string]bool{}, jobs: retainedJobs, latest: map[string]uint64{}}
}
func (m *App) StopRemotes() {
	m.cancelRemoteSettings()
	m.cancelRemoteEditor()
	s := m.remotes
	if s == nil {
		return
	}
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	for job := range s.jobs {
		job.cancel()
	}
	for job := range s.jobs {
		job.start()
		<-job.done
		delete(s.jobs, job)
	}
}
func (m *App) remoteJob(action, id string, p *remotesscreen.ConnectionEditor, work func(context.Context, *remoteManagerResult)) tea.Cmd {
	s := m.remotes
	if s == nil || s.stopped {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	job := &remoteManagerJob{cancel: cancel, done: make(chan struct{}), editor: p, action: action}
	s.jobs[job] = struct{}{}
	s.sequence++
	sequence := s.sequence
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	s.latest[action+":"+id] = sequence
	var once sync.Once
	var result remoteManagerResult
	job.start = func() tea.Msg {
		once.Do(func() {
			defer close(job.done)
			defer cancel()
			result = remoteManagerResult{owner: owner, generation: generation, state: s, sequence: sequence, job: job, editor: p, action: action, id: id}
			if err := ctx.Err(); err != nil {
				result.err = err
			} else {
				work(ctx, &result)
				if ctx.Err() != nil {
					result.err = ctx.Err()
				}
			}
		})
		return result
	}
	s.cancel = cancel
	return job.start
}
func (m *App) OpenRemotes() tea.Cmd {
	m.setActivity(activityRemotes, true)
	if m.FocusStack != nil {
		m.FocusStack.Clear()
		m.FocusStack.Push(FocusTree)
	}
	return m.refreshRemotes()
}
func (m *App) refreshRemotes() tea.Cmd {
	s := m.remotes
	if s == nil || s.manager == nil || s.loading || s.stopped {
		return nil
	}
	s.loading = true
	manager := s.manager
	return m.remoteJob("snapshot", "", nil, func(ctx context.Context, r *remoteManagerResult) {
		r.snapshot, r.err = manager.Snapshot(ctx)
		if source, ok := manager.(shellport.SSHConfigHostSource); ok && r.err == nil {
			r.hosts, _ = source.SSHConfigHosts(ctx)
		}
	})
}
func (m *App) RemoteBackendLabel() string {
	s := m.remotes
	if s == nil {
		return "Local"
	}
	name := "Local"
	if s.current != remoteconn.Local {
		name = s.current
		for _, c := range s.snapshot.Connections {
			if c.ID == s.current {
				name = c.Name
			}
		}
	}
	if link, ok := m.ws.(shellport.BackendLink); ok {
		status := link.BackendStatus()
		return name + " · " + remoteStatusText(status)
	}
	return name
}
func remoteStatusText(s shellport.RemoteStatus) string {
	text := s.State
	if text == "" {
		text = "offline"
	}
	if s.Carrier != "" {
		text += " · " + s.Carrier
	}
	if s.RTT > 0 {
		text += fmt.Sprintf(" · %d ms", s.RTT.Milliseconds())
	}
	return text
}

type remoteSidebarAction struct{ action, id, root string }

func (m *App) remoteSidebarItems() []ui.ListItem {
	s := m.remotes
	if s == nil {
		return []ui.ListItem{{ID: "remote.unavailable", Label: "Connection manager unavailable", Disabled: true}}
	}
	items := []ui.ListItem{{ID: "remote.add", Label: "Add connection", Icon: "+", Command: true, Payload: remoteSidebarAction{action: "add"}}}
	if s.notice != "" {
		items = append(items, ui.ListItem{ID: "remote.notice", Label: s.notice, Disabled: true})
	}
	appendConnection := func(id, name, host string) {
		status := s.status[id]
		if id == s.current {
			if link, ok := m.ws.(shellport.BackendLink); ok {
				status = link.BackendStatus()
			}
		}
		detail := remoteStatusText(status)
		if reason := s.errors[id]; reason != "" {
			detail += " · " + reason
		}
		items = append(items, ui.ListItem{ID: "remote:" + id, Label: name, Subtitle: host + " · " + detail, Active: id == s.current, Payload: remoteSidebarAction{action: "expand", id: id}})
		if !s.expanded[id] {
			return
		}
		command := func(action, label, root string) {
			items = append(items, ui.ListItem{ID: "remote:" + id + ":" + action + ":" + root, Label: label, Depth: 1, Command: true, Payload: remoteSidebarAction{action: action, id: id, root: root}})
		}
		command("open", "Connect / Open workspace…", "")
		for _, recent := range s.snapshot.Recent[id] {
			command("open", "Open "+recent.Path, recent.Path)
		}
		if id == s.current && id != remoteconn.Local {
			command("disconnect", "Disconnect / Switch to Local…", "")
		}
		if id != remoteconn.Local {
			command("test", "Test connection", "")
			command("edit", "Edit / Pair…", "")
			command("remove", "Remove…", "")
		}
	}
	appendConnection(remoteconn.Local, "Local", "This client machine")
	for _, c := range s.snapshot.Connections {
		appendConnection(c.ID, c.Name, c.Kind+" · "+c.Host)
	}
	items = append(items, ui.ListItem{ID: "remote.refresh", Label: "Refresh connections", Command: true, Payload: remoteSidebarAction{action: "refresh"}})
	return items
}
func (m *App) remoteConnection(id string) (remoteconn.Connection, bool) {
	if m.remotes != nil {
		for _, c := range m.remotes.snapshot.Connections {
			if c.ID == id {
				return c, true
			}
		}
	}
	return remoteconn.Connection{}, false
}
func (m *App) handleRemoteSidebar(item ui.ListItem) tea.Cmd {
	a, ok := item.Payload.(remoteSidebarAction)
	s := m.remotes
	if !ok || s == nil || s.stopped {
		return nil
	}
	switch a.action {
	case "expand":
		s.expanded[a.id] = !s.expanded[a.id]
		m.refreshActivityList()
	case "refresh":
		return m.refreshRemotes()
	case "add":
		m.openRemoteEditor(remoteconn.Connection{ID: fmt.Sprintf("connection-%d", time.Now().UnixNano()), Kind: "ssh", Carrier: "auto"}, false, "Add connection", "")
	case "edit", "remove":
		if c, ok := m.remoteConnection(a.id); ok {
			m.openRemoteEditor(c, false, "Edit connection · "+c.Name, "")
			if a.action == "remove" {
				s.editor.ConfirmRemove()
				if fw := m.overlayHost.Window(remoteEditorOverlay); fw != nil {
					if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
						frame.Host().Focus(remotesscreen.ActionsID)
					}
				}
				m.dirtyRemoteEditor()
			}
		}
	case "open", "disconnect":
		id := a.id
		if a.action == "disconnect" {
			id = remoteconn.Local
		}
		name := "Local"
		if c, ok := m.remoteConnection(id); ok {
			name = c.Name
		}
		m.openRemoteEditor(remoteconn.Connection{ID: id}, true, "Open workspace · "+name, a.root)
	case "test":
		if c, ok := m.remoteConnection(a.id); ok {
			manager := s.manager
			return m.remoteJob("test", a.id, nil, func(ctx context.Context, r *remoteManagerResult) {
				r.draft = shellport.RemoteConnectionDraft{Connection: c}
				r.status, r.err = manager.Test(ctx, c)
			})
		}
	}
	return nil
}
func (m *App) openRemoteEditor(c remoteconn.Connection, open bool, title, root string) {
	s := m.remotes
	if s == nil || m.overlayHost == nil {
		return
	}
	m.cancelRemoteEditor()
	p := remotesscreen.NewConnection(m.Theme, c, open)
	if !open {
		p.SetSSHHosts(s.hosts)
	}
	s.editor, s.editorID = p, c.ID
	if open {
		p.SetRoot(root)
	}
	w, h := min(90, max(1, m.width-4)), min(32, max(1, m.height-4))
	if open {
		h = min(13, h)
	}
	frame := ui.NewWindowFrameWidget(m.Theme, "remotes/frame", ui.FrameDialog, title, p, nil, false, w, h)
	frame.Host().Focus(remotesscreen.FormID)
	if open {
		frame.Host().Focus(remotesscreen.ActionsID)
		p.Actions.FocusLast()
	}
	fw := wm.NewFloatingWindow(string(remoteEditorOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(remoteEditorOverlay, fw, ui.ModalDialogPolicy(), func() {
		if s.editor == p {
			m.overlayHost.Close(remoteInstallOverlay)
			s.editor = nil
			p.ClearPairCode()
		}
		for job := range s.jobs {
			if job.editor == p {
				job.cancel()
			}
		}
	})
}
func (m *App) dirtyRemoteEditor() {
	if fw := m.overlayHost.Window(remoteEditorOverlay); fw != nil {
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
			frame.Host().MarkLayout()
			frame.MarkContentDirty()
		}
	}
	m.forceFrame()
}

// HandleRemoteMessage returns true only for the manager's own message family.
// The host calls this before normal Update dispatch so queued results cannot be
// interpreted as chat input or applied to a replacement editor.
func (m *App) HandleRemoteMessage(msg tea.Msg) (bool, tea.Cmd) {
	s := m.remotes
	switch r := msg.(type) {
	case remoteRecordUseMsg:
		if s != nil && s.manager == r.manager {
			m.Feedback.NotifyError("Connection recency was not saved: " + r.err.Error())
		}
		return true, nil
	case remotesscreen.ActionMsg:
		if s == nil || s.stopped || s.editor != r.Target {
			return true, nil
		}
		p := r.Target
		if r.Action == "cancel" {
			m.overlayHost.Close(remoteEditorOverlay)
			return true, nil
		}
		if r.Action == "open" {
			root, id := p.Root(), s.editorID
			if strings.TrimSpace(root) == "" {
				p.SetStatus("Enter the workspace path on the destination backend.")
				m.dirtyRemoteEditor()
				return true, nil
			}
			m.overlayHost.Close(remoteEditorOverlay)
			return true, m.switchRemoteBackend(id, root)
		}
		if s.install != nil {
			return true, nil
		}
		for job := range s.jobs {
			if job.editor == p {
				return true, nil
			}
		}
		id, manager := s.editorID, s.manager
		if r.Action == "remove" {
			if id == s.current {
				p.SetStatus("Switch to another backend before removing the current connection.")
				m.dirtyRemoteEditor()
				return true, nil
			}
			p.SetBusy(true)
			m.dirtyRemoteEditor()
			return true, m.remoteJob("remove", id, p, func(ctx context.Context, r *remoteManagerResult) { r.err = manager.Remove(ctx, id) })
		}
		draft, err := p.Draft()
		if err != nil {
			p.SetStatus(err.Error())
			m.dirtyRemoteEditor()
			return true, nil
		}
		if r.Action == "test" && draft.PairCode != "" {
			p.SetStatus("Use Test & save to pair once and validate the new device. Test alone does not consume a pairing code.")
			m.dirtyRemoteEditor()
			return true, nil
		}
		p.SetBusy(true)
		p.SetStatus("Testing connection…")
		m.dirtyRemoteEditor()
		action := r.Action
		return true, m.remoteJob(action, id, p, func(ctx context.Context, r *remoteManagerResult) {
			r.draft = draft
			if action == "save" {
				r.status, r.err = manager.Save(ctx, draft)
			} else {
				r.status, r.err = manager.Test(ctx, draft.Connection)
			}
		})
	case remoteManagerResult:
		if r.state != nil {
			delete(r.state.jobs, r.job)
			if s != nil {
				delete(s.jobs, r.job)
			}
		}
		if s == nil || s != r.state || s.stopped {
			return true, nil
		}
		if r.owner != m.ws || r.generation != m.workspaceSwitchGen.Load() {
			if r.action == "snapshot" && s.latest["snapshot:"] == r.sequence {
				s.loading = false
			}
			return true, nil
		}
		if s.latest[r.action+":"+r.id] != r.sequence {
			return true, nil
		}
		if r.editor != nil && s.editor != r.editor {
			return true, nil
		}
		if r.action == "snapshot" {
			s.loading = false
			if r.err == nil {
				s.snapshot = r.snapshot
				s.hosts = r.hosts
			} else {
				s.notice = "Connections could not be loaded: " + r.err.Error()
			}
			m.refreshActivityList()
			return true, nil
		}
		if handled := m.offerRemoteInstall(r); handled {
			return true, nil
		}
		if r.action == "install" {
			return true, m.finishRemoteInstall(r)
		}
		if r.status.Notice != "" {
			s.notice = r.status.Notice
		}
		if r.err != nil {
			s.errors[r.id] = remoteSafeError(r.err, r.draft)
			s.status[r.id] = shellport.RemoteStatus{State: "offline"}
		} else {
			delete(s.errors, r.id)
			s.status[r.id] = r.status
		}
		if r.editor != nil && s.editor == r.editor {
			r.editor.SetBusy(false)
			if r.action == "save" && r.err == nil {
				r.editor.ClearPairCode()
			}
			if r.err != nil {
				r.editor.SetStatus("Connection not saved. " + remoteSafeError(r.err, r.draft))
			} else {
				r.editor.SetStatus("Connection test succeeded · " + remoteStatusText(r.status))
				if r.action == "save" || r.action == "remove" {
					m.overlayHost.Close(remoteEditorOverlay)
				}
			}
			m.dirtyRemoteEditor()
		}
		m.refreshActivityList()
		if r.err == nil && (r.action == "save" || r.action == "remove") {
			// A pre-mutation catalog may still be in flight. Cancel its exact
			// owner and supersede it before observing the just-saved inventory.
			for job := range s.jobs {
				if job.action == "snapshot" {
					job.cancel()
				}
			}
			s.loading = false
			return true, m.refreshRemotes()
		}
		return true, nil
	}
	return false, nil
}

func (m *App) switchRemoteBackend(id, root string) tea.Cmd {
	s := m.remotes
	if s == nil || s.manager == nil || s.stopped || m.asyncIOStopped.Load() {
		return nil
	}
	if id == s.current {
		return m.attachRemoteWorkspace(root, "")
	}
	m.pruneRemoteWorkspacePreparations()
	if m.workspaceSwitchIntent != nil || m.workspaceAttachPending {
		return nil
	}
	if _, ok := m.ws.(shellport.Connection); !ok {
		m.showOperationFailure("Backend could not be opened", "This shell has no connected workspace attachment.")
		return nil
	}
	if m.DirtyPromptPath != "" || len(m.workspaceSwitchPreparations) >= 4 {
		m.showOperationFailure("Backend could not be opened", "Finish the current editor decision or cancelled attachment cleanup first.")
		return nil
	}
	manager := s.manager
	m.workspaceAttachSequence++
	m.workspaceSwitchIntent = &remoteWorkspaceSwitchIntent{owner: m.ws, sequence: m.workspaceAttachSequence, root: root, keepShared: map[*editor.Editor]workspaceSwitchEditorSeal{}, backendID: id, prepare: func(ctx context.Context) (shellport.Workspace, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return manager.Attach(ctx, id, root)
	}}
	m.workspaceAttachPending = true
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	return m.continueRemoteWorkspaceSwitch()
}

// Remote UI commands are drained once by App.Update. Opening a retained surface
// never starts I/O or sends synchronously through the workspace event bus.
func (m *App) drainRemoteUICommands() tea.Cmd {
	cmds := m.remoteUICommands
	m.remoteUICommands = nil
	return tea.Batch(cmds...)
}
func (m *App) bindRemoteSettings(body *settingsui.WorkspaceSettingsSurface) {
	admin, _ := m.ws.(shellport.RemoteAccessAdmin)
	authority, ok := m.ws.(shellport.LocalHostAuthority)
	local := ok && authority.LocalHost()
	if cmd := body.SetRemoteAccessAdmin(admin, local); cmd != nil {
		m.remoteUICommands = append(m.remoteUICommands, cmd)
	}
}
func (m *App) cancelRemoteSettings() {
	if m.overlayHost != nil {
		if body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil {
			body.CancelRemoteAccess()
		}
	}
}
func (m *App) cancelRemoteEditor() {
	if m.overlayHost != nil {
		m.overlayHost.Close(remoteInstallOverlay)
		m.overlayHost.Close(remoteEditorOverlay)
	}
}
func (m *App) applyRemoteSettingsMessage(target *settingsui.Model, msg tea.Msg) tea.Cmd {
	if m.overlayHost == nil {
		return nil
	}
	fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := m.settingsSurface(fw)
	if body == nil || body.Model() != target {
		return nil
	}
	_, cmd := target.Update(msg)
	body.SetModel(target)
	if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
	}
	fw.MarkContentDirty()
	m.forceFrame()
	return cmd
}
