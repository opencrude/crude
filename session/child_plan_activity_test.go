package session

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"crude/agent"
	"crude/plan"
	"crude/provider"
	"crude/sessiondb"
)

func TestChildPlanActivityIsolatedRetainedAndRebound(t *testing.T) {
	s := newTestSessionForPlan(t)
	t.Cleanup(s.Close)
	r := NewSubAgentRunner(s, 2)
	params := func(text string) map[string]any {
		return map[string]any{"plan_id": "same-id", "objective": text, "steps": []any{map[string]any{"id": "work", "description": text, "status": "in_progress"}}}
	}
	if _, err := s.executePlanTool(t.Context(), "update_plan", params("Root work")); err != nil {
		t.Fatal(err)
	}
	root := s.PlanActivity()
	paths := make(map[string]string)
	makeChild := func(id string) *agent.SubAgentHandle {
		t.Helper()
		paths[id] = filepath.Join(t.TempDir(), id+".db")
		db, err := sessiondb.OpenAndMigrate(paths[id])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: id}); err != nil {
			t.Fatal(err)
		}
		return &agent.SubAgentHandle{Spec: agent.SubAgentSpec{ID: agent.AgentID(id)}, SessionID: id, DB: db}
	}
	a, b := makeChild("a"), makeChild("b")
	for _, h := range []*agent.SubAgentHandle{a, b} {
		if _, err := r.executeSubAgentPlanningTool(h, "update_plan", params(h.SessionID+" work")); err != nil {
			t.Fatal(err)
		}
	}
	first := s.ChildPlanActivity("a")
	other := s.ChildPlanActivity("b")
	if first.Objective != "a work" || other.Objective != "b work" || s.ChildPlanActivity("none") != nil {
		t.Fatal("child owner projection leaked")
	}
	replace := params("Replacement")
	replace["replace"], replace["revision"] = true, 1
	if _, err := r.executeSubAgentPlanningTool(a, "update_plan", replace); err != nil {
		t.Fatal(err)
	}
	if first.Objective != "a work" || s.ChildPlanActivity("a").Objective != "Replacement" || s.ChildPlanActivity("b") != other || s.PlanActivity() != root {
		t.Fatal("replacement changed immutable or foreign projection")
	}
	if _, err := r.executeSubAgentPlanningTool(a, "get_plan", map[string]any{"include_history": true}); err != nil {
		t.Fatal(err)
	}
	if s.ChildPlanActivity("b") != other || s.PlanActivity() != root {
		t.Fatal("inspection changed foreign owner")
	}
	if _, err := r.executeSubAgentPlanningTool(a, "update_plan", map[string]any{"plan_id": "same-id", "revision": 2, "status": "completed", "steps": []any{map[string]any{"id": "work", "description": "Replacement", "status": "completed"}}}); err != nil {
		t.Fatal(err)
	}
	if s.ChildPlanActivity("a") != nil || s.ChildPlanActivity("b") != other || s.PlanActivity() != root {
		t.Fatal("completion changed another owner")
	}
	if err := b.DB.Close(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if s.ChildPlanActivity("b") != other {
					t.Error("closed DB projection changed")
				}
			}
		}()
	}
	wg.Wait()
	// Explicit recovery binds the new DB authority; it must not reuse a closed store.
	reopened, err := sessiondb.OpenAndMigrate(paths["b"])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := s.bindChildPlanStore("b", "b", reopened); err != nil {
		t.Fatal(err)
	}
	b.DB = reopened
	update := params("Recovered work")
	update["revision"] = 1
	if _, err := r.executeSubAgentPlanningTool(b, "update_plan", update); err != nil {
		t.Fatal(err)
	}
	if s.ChildPlanActivity("b").Objective != "Recovered work" || other.Objective != "b work" {
		t.Fatal("recovery failed to replace authority")
	}
	// Readers may run alongside serialized child tool commits, not just after
	// settlement. Old snapshots must remain immutable throughout publication.
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 1000 {
				view := s.ChildPlanActivity("b")
				if view == nil || len(view.Steps) != 1 || view.Steps[0].Text == "" {
					t.Error("incomplete concurrent projection")
				}
			}
		}()
	}
	for revision := 2; revision < 12; revision++ {
		update := params("Concurrent work")
		update["revision"] = revision
		if _, err := r.executeSubAgentPlanningTool(b, "update_plan", update); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	if other.Objective != "b work" || s.PlanActivity() != root {
		t.Fatal("concurrent child commits changed immutable or root projection")
	}
}

func TestChildPlanActivityPassiveResume(t *testing.T) {
	home, sessions := t.TempDir(), t.TempDir()
	s, err := NewWithSessionDir("root", testConfig(), home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the real parent transcript across Close's empty-session pruning.
	s.msgs = []provider.ChatMessage{{Role: "user", Content: "Retain this session"}}
	if _, err := sessiondb.ReplaceMessages(s.db, s.sessionID, s.msgs, []int{1}); err != nil {
		t.Fatal(err)
	}
	r := NewSubAgentRunner(s, 2)
	for _, id := range []string{"child", "no-plan"} {
		path, err := r.subAgentDBPath(id)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sessiondb.OpenAndMigrate(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: id}); err != nil {
			t.Fatal(err)
		}
		if id == "child" {
			store := plan.NewStore(db, id)
			if _, _, err := store.Update(context.Background(), plan.UpdateRequest{PlanID: "p", Objective: "Child goal", Steps: []plan.PlanStep{{ID: "c", Description: "Child task", Status: plan.StepInProgress}}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := sessiondb.InsertSubAgentResult(s.db, sessiondb.SubAgentResultRow{ParentSessionID: s.sessionID, ChildSessionID: id, AgentID: id, Status: string(agent.SubAgentCompleted)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.executePlanTool(t.Context(), "update_plan", map[string]any{"plan_id": "root-p", "objective": "Root goal", "steps": []any{map[string]any{"id": "r", "description": "Root task", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	root := s.PlanActivity()
	path := s.dbPath
	s.Close()
	resumed, err := ResumeSessionWithDir(path, testConfig(), home, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root, resumed.PlanActivity()) || resumed.ChildPlanActivity("child").Objective != "Child goal" || resumed.ChildPlanActivity("no-plan") != nil || resumed.SubAgentRunner().Running() {
		t.Fatal("passive resume lost isolated plans or launched work")
	}
	retained := resumed.ChildPlanActivity("child")
	resumed.Close()
	if resumed.ChildPlanActivity("child") != retained {
		t.Fatal("session closure invalidated immutable projection")
	}
}
