package workspace

import (
	"errors"
	"testing"

	"crude/workspace/document"
)

func TestWorkspaceRecentActivityCountsOnlyRetainedResourceState(t *testing.T) {
	ws := &Workspace{
		AgentMgr: NewAgentManager(),
		TermMgr: &TerminalManager{items: map[string]*terminalSession{
			"open shell": {alive: true, interactive: true},
			"exited":     {alive: false},
		}},
		LiveDocuments: &DocumentService{docs: map[string]*liveDocument{
			"saved":   {snapshot: document.Snapshot{Path: "saved.txt"}},
			"edited":  {snapshot: document.Snapshot{Path: "edited.txt", Dirty: true}},
			"unnamed": {snapshot: document.Snapshot{Dirty: true}},
		}},
	}
	root := &AgentState{Running: true}
	waiting := &AgentState{PendingBlockID: "question"}
	ws.AgentMgr.AddOrchestrator("root", root)
	ws.AgentMgr.AddSubAgent("waiting", "root", "", "", "", waiting)
	ws.AgentMgr.AddSubAgent("done", "root", "", "", "", &AgentState{Completed: true})
	ws.AgentMgr.AddOrchestrator("idle", &AgentState{})

	if got := ws.RecentActivity(); got != (WorkspaceActivity{
		Agents: ActivityCount{2, true}, Terminals: ActivityCount{1, true}, Unsaved: ActivityCount{2, true},
	}) {
		t.Fatalf("retained activity = %+v", got)
	}
	root.Running, root.Completed, waiting.PendingBlockID = false, true, ""
	ws.TermMgr.items["open shell"].alive = false
	ws.LiveDocuments.docs["edited"].snapshot.Dirty = false
	ws.LiveDocuments.docs["unnamed"].snapshot.Dirty = false
	if got := ws.RecentActivity(); got != (WorkspaceActivity{
		Agents: ActivityCount{Observed: true}, Terminals: ActivityCount{Observed: true}, Unsaved: ActivityCount{Observed: true},
	}) {
		t.Fatalf("settled resources did not read as observed zero: %+v", got)
	}
}

func TestWorkspaceRecentActivityDoesNotWaitForResourceLocks(t *testing.T) {
	ws := &Workspace{AgentMgr: NewAgentManager(), TermMgr: &TerminalManager{}, LiveDocuments: &DocumentService{}}
	ws.TermMgr.mu.Lock()
	ws.LiveDocuments.mu.Lock()
	if got := ws.RecentActivity(); !got.Agents.Observed || got.Terminals.Observed || got.Unsaved.Observed {
		t.Fatalf("unavailable resources were reported as measured zero: %+v", got)
	}
	ws.TermMgr.mu.Unlock()
	ws.LiveDocuments.mu.Unlock()
	ws.LiveDocuments.stateErr = errors.New("recovery not available")
	if ws.RecentActivity().Unsaved.Observed {
		t.Fatal("failed recovery cannot establish that all files were saved")
	}
}
