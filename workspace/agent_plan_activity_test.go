package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"crude/agent"
	"crude/plan"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
)

func TestAgentActivityUsesExactPlanOwner(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	childDir := filepath.Join(directory, "root", "subagents")
	if err := os.MkdirAll(childDir, 0700); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(directory, "root.db")
	for _, id := range []string{"root", "child", "no-plan"} {
		path := rootPath
		if id != "root" {
			path = filepath.Join(childDir, id+".db")
		}
		db, err := sessiondb.OpenAndMigrate(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: id, AccountID: "test-account", Model: "root-model"}); err != nil {
			t.Fatal(err)
		}
		if id != "no-plan" {
			store := plan.NewStore(db, id)
			if _, _, err := store.Update(t.Context(), plan.UpdateRequest{PlanID: "same-id", Objective: id + " goal", Steps: []plan.PlanStep{{ID: "task", Description: id + " task", Status: plan.StepInProgress}}}); err != nil {
				t.Fatal(err)
			}
		}
		if id == "root" {
			if _, err := sessiondb.ReplaceMessages(db, id, []provider.ChatMessage{{Role: "user", Content: "Retained input"}}, []int{1}); err != nil {
				t.Fatal(err)
			}
			for _, child := range []string{"child", "no-plan"} {
				if err := sessiondb.InsertSubAgentResult(db, sessiondb.SubAgentResultRow{ParentSessionID: id, ChildSessionID: child, AgentID: child, Status: string(agent.SubAgentCompleted)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := session.ResumeSessionWithDir(rootPath, workspaceToolRootConfig(), home, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	mgr := NewAgentManager()
	mgr.AddOrchestrator("root", &AgentState{Messages: NewConversation()})
	mgr.AddSubAgent("child", "root", "Child", "model", "crafter", &AgentState{Messages: NewConversation()})
	mgr.AddSubAgent("no-plan", "root", "Empty", "model", "crafter", &AgentState{Messages: NewConversation()})
	owner := &Session{AgentID: "root", backend: backend}
	ws := &Workspace{AgentMgr: mgr, SessionsByAgent: map[string]*Session{"root": owner}}
	root := ws.AgentActivity("root").Plan
	child := ws.AgentActivity("child").Plan
	if root == nil || root.Objective != "root goal" || child == nil || child.Objective != "child goal" || len(child.Steps) != 1 || child.Steps[0].Text != "child task" {
		t.Fatal("status/hover activity borrowed the wrong owner plan")
	}
	if ws.AgentActivity("no-plan").Plan != nil || ws.AgentActivity("missing").Plan != nil {
		t.Fatal("child without plan borrowed root plan")
	}
	unrelated := &Session{AgentID: "unrelated"}
	ws.SessionsByAgent["unrelated"] = unrelated
	if ws.AgentActivity("child").Plan != child || ws.AgentActivity("root").Plan != root || ws.SessionForAgent("unrelated") != unrelated || ws.AgentActivity("unrelated").Plan != nil {
		t.Fatal("background inspection changed owner")
	}
	mgr.Remove("child")
	if ws.AgentActivity("child").Plan != nil || ws.AgentActivity("root").Plan != root {
		t.Fatal("removed child retained a visible plan or changed root")
	}
}
