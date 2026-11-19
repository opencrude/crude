package settings

import (
	"math"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

func (m *Model) rankerFields() []*ui.FieldState {
	r := m.memoryRanker.WithDefaults()
	cadence := ui.NumberFieldState("ranker-every_actions", "Check every", r.EveryActions, "actions", "A parallel tool batch counts as one action.")
	cadence.Validator = numberRange(1, math.MaxInt)
	k := ui.NumberFieldState("ranker-top_k", "Add up to", r.TopK, "memories", "Add fewer memories when none of the others would help.")
	k.Validator = numberRange(1, types.MaxRankedMemories)
	return []*ui.FieldState{ui.BoolFieldState("ranker-enabled", "Rank after actions", r.Enabled == "on", "Send task, action and memory excerpts to the selected account."),
		ui.SelectFieldState("ranker-account_id", "Decision account", r.AccountID, m.connectionOptions(false), "Use this account only for ranking. No other account is used."),
		ui.TextFieldState("ranker-model", "Decision model ID", r.Model, "Enter the exact model ID. Chat model lists can omit decision models."),
		ui.SegmentedFieldState("ranker-endpoint", "Decision endpoint", []ui.FormOption{{ID: "systemone", Label: "System One"}, {ID: "decisions", Label: "Decisions"}, {ID: types.MemoryRankerChat, Label: "Chat"}}, r.Endpoint, "Choose Chat for a text model or a typed endpoint for a decision model."), cadence, k}
}

// Preserve unset or inherited values when the user edits a sibling field.
func (c *agentCatalog) rankerSettings() types.MemoryRankerSettings {
	r := c.model.memoryRanker
	for _, f := range c.draft.Fields() {
		if f.Value.Equal(f.Initial) {
			continue
		}
		v := strings.TrimSpace(f.Value.String())
		switch f.ID {
		case "ranker-enabled":
			r.Enabled = onOff(ui.BoolFieldValue(f))
		case "ranker-account_id":
			r.AccountID = v
		case "ranker-model":
			r.Model = v
		case "ranker-endpoint":
			r.Endpoint = v
		case "ranker-every_actions":
			r.EveryActions, _ = strconv.Atoi(v)
		case "ranker-top_k":
			r.TopK, _ = strconv.Atoi(v)
		}
	}
	return r
}
