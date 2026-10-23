package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/types"
)

// Every goal change the user should see becomes one transcript row in every
// shell; accounting-only updates do not.
func TestGoalTransitionRows(t *testing.T) {
	active := types.Goal{ID: "g", Objective: "Ship it", Status: types.GoalActive, TokenBudget: 100}
	with := func(change func(*types.Goal)) *types.Goal {
		goal := active
		change(&goal)
		return &goal
	}
	for _, test := range []struct {
		name          string
		before, after *types.Goal
		want          string
	}{
		{"set", nil, &active, "Goal set: Ship it"},
		{"cleared", &active, nil, "Goal cleared."},
		{"accounting", &active, with(func(g *types.Goal) { g.TokensUsed = 40 }), ""},
		{"paused", &active, with(func(g *types.Goal) { g.Status, g.Reason = types.GoalPaused, "Stopped." }), "Goal paused. Stopped."},
		{"budget", &active, with(func(g *types.Goal) {
			g.Status, g.Reason = types.GoalBudgetLimited, "The goal used its token budget of 100 tokens."
		}), "Goal stopped: The goal used its token budget of 100 tokens. Set a new goal with /goal or clear it."},
		{"complete", &active, with(func(g *types.Goal) { g.Status, g.Reason = types.GoalComplete, "Verified." }), "Goal complete. Verified."},
	} {
		if got := strings.TrimSpace(goalTransitionRow(test.before, test.after)); got != test.want {
			t.Errorf("%s: row %q, want %q", test.name, got, test.want)
		}
	}
}

// Plan mode, YOLO and goal changes each record one durable row, only when
// the value changes; a reopened session lists the same rows and its loaded
// goal produces no spurious "Goal set" row.
func TestStatusRowsAreRecordedOnceAndSurviveReopen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	dir := filepath.Join(home, "sessions")
	cfg := modelAttributionConfig(server.URL, "rows-model")
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	sender := newMockEventSender()
	sender.ch = make(chan agentEvent, 256)
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	// A saved session has at least one turn.
	if !o.SubmitInput(provider.PlainInput("hello")) {
		t.Fatal("submit rejected")
	}
	waitTurnDone(t, sender, o.ID)
	s.SetYolo(true)
	s.SetYolo(true)
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGoal("Ship it", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PauseGoal("Paused for review."); err != nil {
		t.Fatal(err)
	}
	want := []string{"yolo_enabled", "plan_mode_on", "goal", "goal"}
	phases := func(rows []sessiondb.StatusRow) []string {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.Phase
		}
		return out
	}
	rows := s.StatusRows()
	if got := phases(rows); strings.Join(got, ",") != strings.Join(want, ",") || rows[2].Content != "Goal set: Ship it" || rows[3].Content != "Goal paused. Paused for review." {
		t.Fatalf("rows = %+v", rows)
	}
	live := 0
	for len(sender.ch) > 0 {
		if event := <-sender.ch; event.typ == "status_row" {
			live++
		}
	}
	if live != len(want) {
		t.Fatalf("live status_row events = %d, want %d", live, len(want))
	}
	path := s.dbPath
	s.Close()
	reopened, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := phases(reopened.StatusRows()); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("reopened rows = %v", got)
	}
	if _, err := reopened.ResumeGoal(); err != nil {
		t.Fatal(err)
	}
	after := reopened.StatusRows()
	if len(after) != len(want)+1 || after[len(after)-1].Content != "Goal resumed." {
		t.Fatalf("rows after resuming a reopened goal = %+v", after)
	}
}
