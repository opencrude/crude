package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"crude/agent"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	"crude/tools"
	"crude/types"
)

// A session's /goal is one objective its main agent pursues across turns.
// The rules follow the established goal contract: the runtime continues an
// active goal whenever the agent is idle, the user's own input always comes
// first, Stop pauses, a terminal turn failure or repeated empty or failing
// automatic turns block, a goal that used its token budget stops, and a
// reopened or forked session waits for the user.
// Completion is never the worker's word: its claim starts an independent
// verifier child, and only that verdict completes the goal.

const (
	goalContinuationPrefix = "goal-continuation:"
	goalObjectiveLimit     = 4000
	// goalStrikeLimit is how many consecutive automatic turns may make no
	// progress, or a verifier may end without a verdict, before the goal
	// blocks instead of spending more tokens.
	goalStrikeLimit   = 3
	goalFindingsLimit = 6000
)

var errNoGoal = errors.New("this session has no goal")

type goalRuntime struct {
	record  sessiondb.GoalRecord
	present bool
	// activeSince and tokenBase account time and tokens while the goal is
	// active and not waiting; zero activeSince means not accounting.
	activeSince      time.Time
	tokenBase        int64
	emptyTurns       int
	failingTurns     int
	verifierFailures int
	// findings carry the verifier's latest "not yet" into the next turn.
	findings string
	notices  []provider.ChatMessage
}

// goalTurn is one turn run while the goal was active.
type goalTurn struct {
	goalID    string
	automatic bool
	toolCalls int
	toolFails int
	reply     string
}

func newGoalID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("session: entropy source failed: %v", err))
	}
	return "goal-" + hex.EncodeToString(b)
}

// loadGoal restores the goal on resume. Work never restarts by itself in a
// new runtime: an active goal waits for the user, and a verifier from the
// previous runtime cannot report into this one, so its claim is checked again
// when the work resumes.
func (s *Session) loadGoal() error {
	record, ok, err := sessiondb.LoadGoal(s.db, s.sessionID)
	if err != nil || !ok {
		return err
	}
	if record.Goal.Status == types.GoalActive && (!record.Goal.Waiting || record.VerifierID != "") {
		record.Goal.Waiting = true
		if record.VerifierID != "" {
			record.VerifierID = ""
			record.Goal.Verifying = false
			record.Goal.Reason = "The verifier was interrupted; the claim is checked again when the goal resumes."
		}
		if err := sessiondb.SaveGoal(s.db, s.sessionID, record); err != nil {
			return err
		}
	}
	s.goalMu.Lock()
	s.goal = goalRuntime{record: record, present: true}
	s.goalMu.Unlock()
	shown := record.Goal
	s.goalShownMu.Lock()
	s.goalShown = &shown
	s.goalShownMu.Unlock()
	return nil
}

func (s *Session) tokenTotal() int64 {
	snapshot := s.CostSnapshot()
	return int64(snapshot.TotalPromptTokens + snapshot.TotalCompletionTokens)
}

// Goal returns the session's goal with time and tokens accounted up to now.
func (s *Session) Goal() (types.Goal, bool) {
	if s == nil {
		return types.Goal{}, false
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	if !s.goal.present {
		return types.Goal{}, false
	}
	goal := s.goal.record.Goal
	if !s.goal.activeSince.IsZero() {
		goal.TimeUsed += time.Since(s.goal.activeSince)
		goal.TokensUsed += max(0, total-s.goal.tokenBase)
	}
	return goal, true
}

// goalAccountLocked folds the running interval into the record and keeps
// accounting only while the goal is active and not waiting.
func (s *Session) goalAccountLocked(total int64) {
	g := &s.goal
	now := time.Now()
	if !g.activeSince.IsZero() {
		g.record.Goal.TimeUsed += now.Sub(g.activeSince)
		g.record.Goal.TokensUsed += max(0, total-g.tokenBase)
		g.activeSince = time.Time{}
	}
	if g.present && g.record.Goal.Status == types.GoalActive && !g.record.Goal.Waiting {
		g.activeSince, g.tokenBase = now, total
	}
}

// commitGoalLocked persists the record and returns the view to publish.
func (s *Session) commitGoalLocked(total int64) (types.Goal, error) {
	s.goalAccountLocked(total)
	s.goal.record.Goal.UpdatedAt = time.Now()
	s.goal.record.Goal.Verifying = s.goal.record.VerifierID != ""
	if err := sessiondb.SaveGoal(s.db, s.sessionID, s.goal.record); err != nil {
		return types.Goal{}, err
	}
	return s.goal.record.Goal, nil
}

// publishGoal tells the session's shells, and records the change's row;
// present false means cleared.
func (s *Session) publishGoal(goal types.Goal, present bool) {
	var next *types.Goal
	if present {
		next = &goal
	}
	s.goalShownMu.Lock()
	if row := strings.TrimSpace(goalTransitionRow(s.goalShown, next)); row != "" {
		s.recordStatusRow("goal", row)
	}
	s.goalShown = next
	if o := s.liveOrchestrator(); o != nil && o.sender != nil {
		var data any
		if present {
			data = goal
		}
		o.sender.SendAgentEventWithID("goal", data, o.ID)
	}
	s.goalShownMu.Unlock()
	select {
	case s.goalWake <- struct{}{}:
	default:
	}
}

func (s *Session) liveOrchestrator() *Orchestrator {
	s.orchMu.Lock()
	defer s.orchMu.Unlock()
	for _, o := range s.orchestrators {
		if o != nil && !o.closing.Load() {
			return o
		}
	}
	return nil
}

func validGoalObjective(objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", errors.New("a goal needs an objective")
	}
	if utf8.RuneCountInString(objective) > goalObjectiveLimit {
		return "", fmt.Errorf("a goal objective is limited to %d characters; put longer detail in a file and name it", goalObjectiveLimit)
	}
	return objective, nil
}

// SetGoal starts pursuing objective, within tokenBudget tokens when it is
// positive. An unfinished goal is replaced only when replace is set; a
// completed one is simply superseded.
func (s *Session) SetGoal(objective string, tokenBudget int64, replace bool) (types.Goal, error) {
	objective, err := validGoalObjective(objective)
	if err != nil {
		return types.Goal{}, err
	}
	if tokenBudget < 0 {
		return types.Goal{}, errors.New("a token budget must be positive")
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	if s.goal.present && s.goal.record.Goal.Unfinished() && !replace {
		s.goalMu.Unlock()
		return types.Goal{}, errors.New("an unfinished goal exists; replace it with /goal")
	}
	previousVerifier := ""
	if s.goal.present {
		previousVerifier = s.goal.record.VerifierID
	}
	now := time.Now()
	s.goal = goalRuntime{present: true, record: sessiondb.GoalRecord{Goal: types.Goal{
		ID: newGoalID(), Objective: objective, Status: types.GoalActive, TokenBudget: tokenBudget, CreatedAt: now,
	}}}
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		return types.Goal{}, err
	}
	s.cancelGoalVerifier(previousVerifier)
	s.publishGoal(goal, true)
	return goal, nil
}

// EditGoal changes the objective without restarting the goal; a running
// turn learns it at its next step.
func (s *Session) EditGoal(objective string) (types.Goal, error) {
	objective, err := validGoalObjective(objective)
	if err != nil {
		return types.Goal{}, err
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	if !s.goal.present {
		s.goalMu.Unlock()
		return types.Goal{}, errNoGoal
	}
	s.goal.record.Goal.Objective = objective
	notice := provider.NewTextMessage("user", rootprompt.GoalObjectiveUpdated(objective))
	notice.Origin, notice.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
	notice.InternalID = fmt.Sprintf("goal-objective-%d", time.Now().UnixNano())
	s.goal.notices = append(s.goal.notices, notice)
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		return types.Goal{}, err
	}
	s.publishGoal(goal, true)
	return goal, nil
}

// PauseGoal stops automatic work on an active goal until it is resumed.
func (s *Session) PauseGoal(reason string) (types.Goal, error) {
	return s.settleGoal(func(g *goalRuntime) error {
		if g.record.Goal.Status != types.GoalActive {
			return errors.New("only an active goal can be paused")
		}
		g.record.Goal.Status, g.record.Goal.Reason = types.GoalPaused, reason
		return nil
	})
}

// ResumeGoal makes a paused, blocked or waiting goal active again. Its stop
// rules start counting afresh, as after a user turn.
func (s *Session) ResumeGoal() (types.Goal, error) {
	return s.settleGoal(func(g *goalRuntime) error {
		switch {
		case g.record.Goal.Status == types.GoalComplete:
			return errors.New("this goal is complete; set a new one with /goal")
		case g.record.Goal.Status == types.GoalActive && !g.record.Goal.Waiting:
			return errors.New("this goal is already active")
		case g.record.Goal.TokenBudget > 0 && g.record.Goal.TokensUsed >= g.record.Goal.TokenBudget:
			return errors.New("this goal used its token budget; set a new goal with /goal or clear it")
		}
		g.record.Goal.Status, g.record.Goal.Waiting, g.record.Goal.Reason = types.GoalActive, false, ""
		g.emptyTurns, g.failingTurns, g.verifierFailures = 0, 0, 0
		return nil
	})
}

// ClearGoal removes the goal and stops any verifier it started.
func (s *Session) ClearGoal() error {
	s.goalMu.Lock()
	if !s.goal.present {
		s.goalMu.Unlock()
		return errNoGoal
	}
	if err := sessiondb.DeleteGoal(s.db, s.sessionID); err != nil {
		s.goalMu.Unlock()
		return err
	}
	verifier := s.goal.record.VerifierID
	s.goal = goalRuntime{}
	s.goalMu.Unlock()
	s.cancelGoalVerifier(verifier)
	s.publishGoal(types.Goal{}, false)
	return nil
}

// settleGoal applies one status change and its stop of any running verifier.
func (s *Session) settleGoal(change func(*goalRuntime) error) (types.Goal, error) {
	total := s.tokenTotal()
	s.goalMu.Lock()
	if !s.goal.present {
		s.goalMu.Unlock()
		return types.Goal{}, errNoGoal
	}
	if err := change(&s.goal); err != nil {
		s.goalMu.Unlock()
		return types.Goal{}, err
	}
	verifier := ""
	if s.goal.record.Goal.Status != types.GoalActive {
		verifier, s.goal.record.VerifierID = s.goal.record.VerifierID, ""
	}
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		return types.Goal{}, err
	}
	s.cancelGoalVerifier(verifier)
	s.publishGoal(goal, true)
	return goal, nil
}

func (s *Session) cancelGoalVerifier(id string) {
	if id == "" {
		return
	}
	if runner := s.SubAgentRunner(); runner != nil {
		runner.cancelActive(func(handle *agent.SubAgentHandle) bool { return string(handle.Spec.ID) == id })
	}
}

func (s *Session) takeGoalNotices() []provider.ChatMessage {
	s.goalMu.Lock()
	defer s.goalMu.Unlock()
	notices := s.goal.notices
	s.goal.notices = nil
	return notices
}

// enforceGoalBudget stops an active goal once it has used its token budget.
// The running turn is told once, at its next step, to wrap up; no further
// goal turn starts and a running verifier stops, since it spends tokens too.
func (s *Session) enforceGoalBudget() {
	total := s.tokenTotal()
	s.goalMu.Lock()
	g := &s.goal
	if !g.present || g.record.Goal.Status != types.GoalActive || g.record.Goal.TokenBudget <= 0 {
		s.goalMu.Unlock()
		return
	}
	s.goalAccountLocked(total)
	goal := g.record.Goal
	if goal.TokensUsed < goal.TokenBudget {
		s.goalMu.Unlock()
		return
	}
	g.record.Goal.Status = types.GoalBudgetLimited
	g.record.Goal.Reason = fmt.Sprintf("The goal used its token budget of %d tokens.", goal.TokenBudget)
	notice := provider.NewTextMessage("user", rootprompt.GoalBudgetLimit(goal.Objective, goal.TokensUsed, goal.TokenBudget, goal.TimeUsed))
	notice.Origin, notice.Visibility = provider.OriginRuntimeContext, provider.VisibilityContextOnly
	notice.InternalID = fmt.Sprintf("goal-budget-%d", time.Now().UnixNano())
	g.notices = append(g.notices, notice)
	verifier := g.record.VerifierID
	g.record.VerifierID = ""
	limited, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		s.logger.Warn("GOAL", "budget_limit_save_failed", "error", err.Error())
		return
	}
	s.cancelGoalVerifier(verifier)
	s.publishGoal(limited, true)
}

// goalTurnBegin starts accounting a turn against the active goal. Plan mode
// turns plan instead of doing the work, so they never count. A user turn
// ends a reopened or forked goal's wait.
func (s *Session) goalTurnBegin(input provider.UserInput, planMode bool) *goalTurn {
	if planMode {
		return nil
	}
	automatic := strings.HasPrefix(input.InternalID, goalContinuationPrefix)
	total := s.tokenTotal()
	s.goalMu.Lock()
	if !s.goal.present || s.goal.record.Goal.Status != types.GoalActive {
		s.goalMu.Unlock()
		return nil
	}
	var published *types.Goal
	if s.goal.record.Goal.Waiting && !automatic {
		s.goal.record.Goal.Waiting = false
		if goal, err := s.commitGoalLocked(total); err == nil {
			published = &goal
		}
	}
	turn := &goalTurn{goalID: s.goal.record.Goal.ID, automatic: automatic}
	s.goalMu.Unlock()
	if published != nil {
		s.publishGoal(*published, true)
	}
	return turn
}

func (t *goalTurn) observe(event agent.AgentEvent) {
	if t == nil {
		return
	}
	data, _ := event.Data.(map[string]any)
	switch event.Type {
	case "tool_call":
		t.toolCalls++
	case "tool_result":
		if data["error"] == "true" {
			t.toolFails++
		}
	case "done":
		t.reply, _ = data["content"].(string)
	}
}

// goalTurnEnd applies the stop rules to a finished turn. A turn parked for
// recovery changes nothing: recovery owns it, and no goal turn can start
// until it settles.
func (s *Session) goalTurnEnd(turn *goalTurn, stopped, failed bool, failure string, parked bool) {
	if turn == nil || parked {
		return
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	g := &s.goal
	// Notices meant for this turn (a budget stop) are stale once it ends.
	g.notices = nil
	if !g.present || g.record.Goal.ID != turn.goalID || g.record.Goal.Status != types.GoalActive {
		s.goalMu.Unlock()
		return
	}
	switch {
	case stopped:
		g.record.Goal.Status, g.record.Goal.Reason = types.GoalPaused, "Paused because the turn was stopped. Resume it with /goal resume."
	case failed:
		g.record.Goal.Status, g.record.Goal.Reason = types.GoalBlocked, "The last turn failed. Fix the cause, then resume with /goal resume."
		if failure != "" {
			g.record.Goal.Reason = "The last turn failed: " + failure + ". Fix the cause, then resume with /goal resume."
		}
	case turn.automatic:
		if turn.toolCalls == 0 && strings.TrimSpace(turn.reply) == "" {
			g.emptyTurns++
		} else {
			g.emptyTurns = 0
		}
		if turn.toolCalls > 0 && turn.toolFails == turn.toolCalls {
			g.failingTurns++
		} else {
			g.failingTurns = 0
		}
		switch {
		case g.emptyTurns >= goalStrikeLimit:
			g.record.Goal.Status, g.record.Goal.Reason = types.GoalBlocked, fmt.Sprintf("The agent made no progress in %d consecutive goal turns.", goalStrikeLimit)
		case g.failingTurns >= goalStrikeLimit:
			g.record.Goal.Status, g.record.Goal.Reason = types.GoalBlocked, fmt.Sprintf("Every tool call failed in %d consecutive goal turns.", goalStrikeLimit)
		}
	default:
		g.emptyTurns, g.failingTurns = 0, 0
	}
	verifier := ""
	if g.record.Goal.Status != types.GoalActive {
		verifier, g.record.VerifierID = g.record.VerifierID, ""
	}
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		s.logger.Warn("GOAL", "turn_settle_failed", "error", err.Error())
		return
	}
	s.cancelGoalVerifier(verifier)
	s.publishGoal(goal, true)
}

// goalContinuation is the next automatic turn's input, if the goal should
// continue now. The caller owns idleness; queued human input goes first.
func (s *Session) goalContinuation() (provider.UserInput, bool) {
	if s.PlanMode() {
		return provider.UserInput{}, false
	}
	s.queueMu.Lock()
	queued := len(s.queued) > 0
	s.queueMu.Unlock()
	if queued {
		return provider.UserInput{}, false
	}
	goal, ok := s.Goal()
	if !ok || goal.Status != types.GoalActive || goal.Waiting || goal.Verifying {
		return provider.UserInput{}, false
	}
	s.goalMu.Lock()
	findings := s.goal.findings
	s.goal.findings = ""
	s.goalMu.Unlock()
	return provider.UserInput{
		InternalID: fmt.Sprintf("%s%d", goalContinuationPrefix, time.Now().UnixNano()),
		Text:       rootprompt.GoalContinuation(goal.Objective, goal.TokensUsed, goal.TokenBudget, goal.TimeUsed, findings),
		Origin:     provider.OriginRuntimeContext,
	}, true
}

// claimGoalComplete starts the independent verifier of the worker's claim.
func (s *Session) claimGoalComplete(ctx context.Context, evidence string) (string, error) {
	s.goalMu.Lock()
	if !s.goal.present || s.goal.record.Goal.Status != types.GoalActive {
		s.goalMu.Unlock()
		return "", errors.New("there is no active goal to complete")
	}
	if s.goal.record.VerifierID != "" {
		s.goalMu.Unlock()
		return "", errors.New("a verifier is already checking this goal; end your turn and wait for its verdict")
	}
	objective := s.goal.record.Goal.Objective
	s.goalMu.Unlock()
	if !s.initSubAgentRunnerFor(true) {
		return "", errors.New("the goal verifier could not start: this session is closing")
	}
	id, err := s.SubAgentRunner().StartGoalVerifier(ctx, rootprompt.GoalVerifierAssignment(objective, evidence))
	if err != nil {
		return "", fmt.Errorf("the goal verifier could not start: %w", err)
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	if !s.goal.present || s.goal.record.Goal.Status != types.GoalActive || s.goal.record.Goal.Objective != objective {
		s.goalMu.Unlock()
		s.cancelGoalVerifier(string(id))
		return "", errors.New("the goal changed while its verifier was starting")
	}
	s.goal.record.VerifierID = string(id)
	s.goal.record.Goal.Reason = "An independent verifier is checking the completion claim."
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		s.cancelGoalVerifier(string(id))
		return "", err
	}
	s.publishGoal(goal, true)
	return string(id), nil
}

// observeGoalVerifier applies a finished child's verdict when it is the
// goal's verifier. The verdict is the report's goal_verdict metadata; a
// verifier that ends without one counts as not verified.
func (s *Session) observeGoalVerifier(childID agent.AgentID, result agent.SubAgentResult) {
	s.goalMu.Lock()
	match := s.goal.present && s.goal.record.VerifierID == string(childID)
	s.goalMu.Unlock()
	if !match {
		return
	}
	verdict, summary, body := "", "", ""
	if store := s.CollaborationStore(); store != nil && result.ReportID != "" {
		if report, err := store.Artifact(context.Background(), result.ReportID, result.ReportRevision); err == nil {
			verdict, _ = report.Metadata["goal_verdict"].(string)
			summary, body = report.Summary, report.Body
		}
	}
	total := s.tokenTotal()
	s.goalMu.Lock()
	g := &s.goal
	if !g.present || g.record.VerifierID != string(childID) {
		s.goalMu.Unlock()
		return
	}
	g.record.VerifierID = ""
	switch verdict {
	case "achieved":
		g.record.Goal.Status, g.record.Goal.Reason = types.GoalComplete, firstNonEmpty(summary, "The verifier confirmed the objective.")
		g.verifierFailures = 0
	case "not_achieved":
		g.record.Goal.Reason = "Not yet: " + firstNonEmpty(summary, "the verifier found remaining work.")
		g.findings = truncateRunes(firstNonEmpty(body, summary), goalFindingsLimit)
		g.verifierFailures = 0
	default:
		g.verifierFailures++
		g.record.Goal.Reason = "The verifier ended without a verdict; completion is unconfirmed."
		if g.verifierFailures >= goalStrikeLimit {
			g.record.Goal.Status = types.GoalBlocked
			g.record.Goal.Reason = fmt.Sprintf("The verifier ended without a verdict %d times in a row.", goalStrikeLimit)
		}
	}
	goal, err := s.commitGoalLocked(total)
	s.goalMu.Unlock()
	if err != nil {
		s.logger.Warn("GOAL", "verdict_save_failed", "error", err.Error())
		return
	}
	s.publishGoal(goal, true)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

// executeGoalTool serves the main agent's goal tools.
func (s *Session) executeGoalTool(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	encode := func(value any) (tools.ToolResult, error) {
		raw, err := json.Marshal(value)
		return tools.ToolResult{Content: string(raw)}, err
	}
	switch tool {
	case "get_goal":
		goal, ok := s.Goal()
		if !ok {
			return encode(map[string]any{"goal": nil})
		}
		result := map[string]any{"goal": goal}
		if goal.TokenBudget > 0 {
			result["remaining_tokens"] = max(0, goal.TokenBudget-goal.TokensUsed)
		}
		return encode(result)
	case "create_goal":
		budget := int64(0)
		if raw, ok := params["token_budget"]; ok && raw != nil {
			value, ok := raw.(float64)
			if !ok || value < 1 || value != float64(int64(value)) {
				return tools.ToolResult{}, errors.New("token_budget must be a positive whole number of tokens")
			}
			budget = int64(value)
		}
		goal, err := s.SetGoal(getStrParam(params, "objective"), budget, false)
		if err != nil {
			return tools.ToolResult{}, err
		}
		return encode(map[string]any{"goal": goal})
	case "update_goal":
		switch getStrParam(params, "status") {
		case "complete":
			id, err := s.claimGoalComplete(ctx, getStrParam(params, "evidence"))
			if err != nil {
				return tools.ToolResult{}, err
			}
			return encode(map[string]any{"status": "verifying", "verifier_id": id,
				"next": "An independent verifier is checking the current state. End your turn; its report arrives as a delivery, and the goal completes only if it confirms the objective."})
		case "blocked":
			reason := strings.TrimSpace(getStrParam(params, "reason"))
			if reason == "" {
				return tools.ToolResult{}, errors.New("blocked requires a reason the user can act on")
			}
			goal, err := s.settleGoal(func(g *goalRuntime) error {
				if g.record.Goal.Status != types.GoalActive {
					return errors.New("only an active goal can be blocked")
				}
				g.record.Goal.Status, g.record.Goal.Reason = types.GoalBlocked, reason
				return nil
			})
			if err != nil {
				return tools.ToolResult{}, err
			}
			return encode(map[string]any{"goal": goal})
		case "paused":
			goal, err := s.PauseGoal(firstNonEmpty(getStrParam(params, "reason"), "Paused at the user's request."))
			if err != nil {
				return tools.ToolResult{}, err
			}
			return encode(map[string]any{"goal": goal})
		}
		return tools.ToolResult{}, errors.New(`status must be "complete", "blocked" or "paused"`)
	}
	return tools.ToolResult{}, fmt.Errorf("unknown goal tool %q", tool)
}
