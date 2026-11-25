package remote

import (
	"context"
	"errors"
	"time"

	"crude/agent/session"
	"crude/agent/sessiondb"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/types/setting"
	"crude/types/statuscenter"
	"crude/workspace"
)

func (w *Workspace) RootPath() string    { return w.root }
func (w *Workspace) WorkspaceID() string { return w.id }

// FileOnly reports this shell's entry, independent of a peer's project demand.
func (w *Workspace) FileOnly() bool {
	return w.EntryIntent().FileOnly
}

// EntryIntent returns the acknowledged routing choice for reconnect and handover.
func (w *Workspace) EntryIntent() coreprotocol.EntryIntent {
	if entry := w.entry.Load(); entry != nil {
		return *entry
	}
	return coreprotocol.EntryIntent{}
}

// OpenFolder promotes this attachment in place. No editor, draft or terminal changes owner.
func (w *Workspace) OpenFolder(ctx context.Context) error {
	client, id := w.conn()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if client == nil {
		return errDisconnected
	}
	if err := client.Call(ctx, id, coreprotocol.WorkspaceOpenFolder, &coreprotocol.Empty{}, &coreprotocol.Empty{}); err != nil {
		return err
	}
	w.connMu.Lock()
	defer w.connMu.Unlock()
	if w.client != client {
		return errors.New("connection changed; open the folder again")
	}
	w.entry.Store(&coreprotocol.EntryIntent{Explicit: true})
	return nil
}

// DraftWorkspaceID names private client data independently of the transport's
// workspace ID, which can be identical on unrelated SSH hosts.
func (w *Workspace) DraftWorkspaceID() string {
	if w.namespace == "" {
		return w.id
	}
	return w.namespace + ":" + w.id
}
func (w *Workspace) Username() string         { return w.view.Username }
func (w *Workspace) StartTime() time.Time     { return w.view.StartTime }
func (w *Workspace) Logger() workspace.Logger { return w.logger }

// InitialSettings returns the settings that a new shell starts with.
func (w *Workspace) InitialSettings() setting.Settings {
	value := w.view.InitialSettings
	settings := w.LoadAppSettings()
	value.Theme, value.Editor, value.UI = settings.Theme, settings.Editor, settings.UI
	return value
}
func (w *Workspace) InitialModelName() string {
	if w.view.InitialModelName != "" {
		return w.view.InitialModelName
	}
	return w.LoadAppSettings().Agents.Categories[session.CategoryDefault].Model
}
func (w *Workspace) Status() shellport.StatusService       { return w.status }
func (w *Workspace) WorkspaceDB() shellport.WorkspaceStore { return workspaceStore{w: w} }
func (w *Workspace) SetEventSender(session.EventSender)    {}
func (w *Workspace) SwitchProject(string) error {
	return errors.New("a connected shell must attach a replacement workspace, not re-root the core")
}
func (w *Workspace) AttachWorkspace(ctx context.Context, root string) (shellport.Workspace, error) {
	// Distinct root-qualified profile identities permit destination preparation
	// while the old connection is still live, without losing author undo on return.
	next, err := Attach(ctx, Options{Root: root, Entry: coreprotocol.EntryIntent{Explicit: true}, Dial: w.dial, Logger: w.logger, Namespace: w.namespace, ClientID: w.profileID})
	if err != nil {
		return nil, err
	}
	next.ConfigureInvocation(w.invocation)
	return next, nil
}

// TakeStartupIssues returns the startup issues and forgets them.
func (w *Workspace) TakeStartupIssues() []statuscenter.OperationIssue {
	issues := w.startup.Issues
	w.startup.Issues = nil
	return issues
}

// AddStartupIssues hands pre-attachment diagnostics to the ordinary shell
// recovery surface. Identical configuration evidence is not shown twice.
func (w *Workspace) AddStartupIssues(issues []statuscenter.OperationIssue) {
	for _, issue := range issues {
		if issue.Target == "" {
			w.Status().Notify(statuscenter.NotificationRecord{Severity: statuscenter.SeverityWarning, Source: "startup", Summary: issue.Title, Detail: issue.Detail, Transient: true})
			continue
		}
		found := false
		for _, prior := range w.startup.Issues {
			if prior == issue {
				found = true
				break
			}
		}
		if !found {
			w.startup.Issues = append(w.startup.Issues, issue)
		}
	}
}
func (w *Workspace) LastRegistryError() error {
	if w.startup.RegistryError != "" {
		return errors.New(w.startup.RegistryError)
	}
	return nil
}

type workspaceStore struct{ w *Workspace }

func (w *Workspace) reportFailure(operation string, err error) {
	if err != nil {
		w.send(shellport.ReplicaFailureMsg{Owner: w, Operation: operation, Error: err.Error()})
	}
}

func (s workspaceStore) ListSessions() ([]sessiondb.SessionMeta, error) {
	return s.w.ListSessions(), nil
}
