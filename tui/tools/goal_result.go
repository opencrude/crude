package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/types"
)

// IsGoalTool identifies the session goal tools. Their results are JSON for
// the model; the transcript projects them as a goal, never as raw JSON.
func IsGoalTool(name string) bool {
	switch name {
	case "get_goal", "create_goal", "update_goal":
		return true
	}
	return false
}

// GoalStatusLabel is the user-facing goal state shared by the header, /goal
// and goal tool rows.
func GoalStatusLabel(goal types.Goal) string {
	switch {
	case goal.Status == types.GoalActive && goal.Verifying:
		return "checking"
	case goal.Status == types.GoalActive && goal.Waiting:
		return "waiting"
	case goal.Status == types.GoalComplete:
		return "complete"
	case goal.Status == types.GoalBudgetLimited:
		return "limited by budget"
	}
	return string(goal.Status)
}

func FormatGoalDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	switch {
	case minutes < 1:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case minutes < 60:
		return fmt.Sprintf("%dm", minutes)
	case minutes < 24*60:
		return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
	}
	return fmt.Sprintf("%dd %dh", minutes/(24*60), minutes/60%24)
}

func FormatGoalTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

func buildGoalResult(content string) StructuredResult {
	var payload struct {
		Goal            *types.Goal `json:"goal"`
		RemainingTokens *int64      `json:"remaining_tokens"`
		Status          string      `json:"status"`
		VerifierID      string      `json:"verifier_id"`
		Next            string      `json:"next"`
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	if decoder.Decode(&payload) != nil {
		return BuildReadableResult(content)
	}
	if payload.Status == "verifying" {
		section := ResultSection{Body: payload.Next}
		if payload.VerifierID != "" {
			section.Details = []ResultField{{"Verifier", payload.VerifierID}}
		}
		return StructuredResult{Summary: "verifier checking", Sections: []ResultSection{section}}
	}
	if payload.Goal == nil {
		return StructuredResult{Summary: "no goal", Sections: []ResultSection{{Body: "This session has no goal."}}}
	}
	goal := *payload.Goal
	var usage []string
	if goal.TimeUsed >= time.Second {
		usage = append(usage, FormatGoalDuration(goal.TimeUsed)+" active")
	}
	tokens := ""
	if goal.TokenBudget > 0 {
		tokens = FormatGoalTokens(goal.TokensUsed) + " / " + FormatGoalTokens(goal.TokenBudget)
	} else if goal.TokensUsed > 0 {
		tokens = FormatGoalTokens(goal.TokensUsed)
	}
	if tokens != "" {
		usage = append(usage, tokens+" tokens")
	}
	objective := ResultSection{Title: "Objective", Metadata: strings.Join(usage, " · "), Body: goal.Objective}
	for _, field := range []ResultField{
		{"Status", GoalStatusLabel(goal)},
		{"Active time", formatGoalTime(goal.TimeUsed)},
		{"Tokens", tokens},
		{"Remaining tokens", formatOptionalTokens(payload.RemainingTokens)},
		{"Goal ID", goal.ID},
		{"Updated", formatGoalStamp(goal.UpdatedAt)},
	} {
		if field.Value != "" {
			objective.Details = append(objective.Details, field)
		}
	}
	result := StructuredResult{Summary: GoalStatusLabel(goal), Sections: []ResultSection{objective}}
	if goal.Reason != "" {
		result.Sections = append(result.Sections, ResultSection{Title: "Reason", Body: goal.Reason})
	}
	return result
}

func formatGoalTime(d time.Duration) string {
	if d < time.Second {
		return ""
	}
	return FormatGoalDuration(d)
}

func formatOptionalTokens(n *int64) string {
	if n == nil {
		return ""
	}
	return FormatGoalTokens(*n)
}

func formatGoalStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
