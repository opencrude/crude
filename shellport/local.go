package shellport

import (
	"time"

	"crude/types"
	"crude/workspace"
)

// local is the in-process Workspace: a thin delegating adapter over the real
// runtime. The core side and existing tests run through it. Method delegates
// live in local_delegates.go; this file holds the accessors and the lazy
// service constructors that cannot be written as plain delegation.
type local struct{ ws *workspace.Workspace }

var _ Workspace = (*local)(nil)

// Local adapts a concrete workspace to the shell port. The adapter is a
// pointer, so two values returned by one Local call compare equal and the
// shell may use a Workspace as an ownership token. A nil workspace yields a
// nil Workspace.
func Local(ws *workspace.Workspace) Workspace {
	if ws == nil {
		return nil
	}
	return &local{ws: ws}
}

// Unwrap returns the concrete workspace behind a Local adapter, or nil for any
// other implementation. It exists for the core side and for tests that must
// reach runtime internals; the shell never calls it.
func Unwrap(w Workspace) *workspace.Workspace {
	if l, ok := w.(*local); ok {
		return l.ws
	}
	return nil
}

func (l *local) RootPath() string                { return l.ws.RootPath }
func (l *local) Username() string                { return l.ws.Username }
func (l *local) StartTime() time.Time            { return l.ws.StartTime }
func (l *local) InitialSettings() types.Settings { return l.ws.InitialSettings }
func (l *local) InitialModelName() string        { return l.ws.InitialModelName }
func (l *local) Logger() workspace.Logger        { return l.ws.Logger }

func (l *local) AgentMgr() *workspace.AgentManager { return l.ws.AgentMgr }
func (l *local) PaneMgr() *workspace.PaneManager   { return l.ws.PaneMgr }

func (l *local) Sessions() []*workspace.Session { return l.ws.Sessions }
func (l *local) SessionsByID() map[string]*workspace.Session {
	return l.ws.SessionsByID
}
func (l *local) SessionsByAgent() map[string]*workspace.Session {
	return l.ws.SessionsByAgent
}

func (l *local) WorkspaceDB() WorkspaceStore {
	if l.ws.WorkspaceDB == nil {
		return nil
	}
	return l.ws.WorkspaceDB
}

func (l *local) Documents() DocumentService {
	if l.ws.Documents == nil {
		return nil
	}
	return l.ws.Documents
}

func (l *local) EnsureDocuments(bus *workspace.EventBus) DocumentService {
	if l.ws.Documents == nil {
		l.ws.Documents = workspace.NewDocumentSaveService()
		l.ws.Documents.SetEventBus(bus)
	}
	return l.ws.Documents
}

func (l *local) LSPMgr() LSPService {
	if l.ws.LSPMgr == nil {
		return nil
	}
	return &localLSP{LSPManager: l.ws.LSPMgr}
}

func (l *local) Git() GitService {
	if l.ws.Git == nil {
		return nil
	}
	return l.ws.Git
}

func (l *local) EnsureGit() GitService {
	if l.ws.Git == nil {
		l.ws.Git = workspace.NewGitService(l.ws.RootPath)
	}
	return l.ws.Git
}

func (l *local) LocalHistory() LocalHistoryStore {
	if l.ws.LocalHistory == nil {
		return nil
	}
	return l.ws.LocalHistory
}

func (l *local) HistoryService() LocalHistoryService {
	if l.ws.HistoryService == nil {
		return nil
	}
	return l.ws.HistoryService
}

func (l *local) Terminals() TerminalService {
	if l.ws.Terminals == nil {
		return nil
	}
	return l.ws.Terminals
}

func (l *local) EnsureTerminals() (TerminalService, error) {
	if l.ws.Terminals == nil && l.ws.TermMgr != nil {
		service, err := workspace.NewTerminalService(l.ws.TermMgr)
		if err != nil {
			return nil, err
		}
		l.ws.Terminals = service
	}
	return l.Terminals(), nil
}

func (l *local) TermMgr() TerminalManager {
	if l.ws.TermMgr == nil {
		return nil
	}
	return l.ws.TermMgr
}

func (l *local) Status() StatusService {
	if l.ws.Status == nil {
		return nil
	}
	return l.ws.Status
}

// BindToolLayout and BindMemory capture only the workspace database, so work
// queued before a workspace switch keeps writing to the database it started
// with.
func (l *local) BindToolLayout() ToolLayoutStore {
	return &workspace.Workspace{WorkspaceDB: l.ws.WorkspaceDB}
}

func (l *local) BindMemory() MemoryStore {
	return &workspace.Workspace{WorkspaceDB: l.ws.WorkspaceDB}
}
