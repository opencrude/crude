package settings

import (
	"math"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

const (
	fieldRankerAccount   = "ranker-account_id"
	fieldRankerModel     = "ranker-model"
	fieldRankerModelType = "ranker-model_type"
	fieldReviewModelType = "agent-model_type"
)

// modelTypeField is the one Model type control shared by Memory ranking and
// Auto Review. Automatic follows the account's model list.
func modelTypeField(id, value string) *ui.FieldState {
	return ui.SegmentedFieldState(id, "Model type", []ui.FormOption{
		{ID: types.ModelTypeAutomatic, Label: "Automatic"},
		{ID: types.ModelTypeChat, Label: "Chat"},
		{ID: types.ModelTypeDecision, Label: "Decision"},
	}, value, "Automatic reads the account's model list. Other models reply in chat.")
}

func (m *Model) rankerFields() []*ui.FieldState {
	r := m.memoryRanker.WithDefaults()
	cadence := ui.NumberFieldState("ranker-every_actions", "Check every", r.EveryActions, "actions", "A parallel tool batch counts as one action.")
	cadence.Validator = numberRange(1, math.MaxInt)
	k := ui.NumberFieldState("ranker-top_k", "Add up to", r.TopK, "memories", "Add fewer memories when none of the others would help.")
	k.Validator = numberRange(1, types.MaxRankedMemories)
	return []*ui.FieldState{ui.SeparatorFieldState("ranking", "Ranking"),
		ui.BoolFieldState("ranker-enabled", "Rank after actions", r.Enabled == "on", "Send task, action and memory excerpts to the selected account."),
		ui.SelectFieldState(fieldRankerAccount, "Account", r.AccountID, m.connectionOptions(false), "Use this account only for ranking. No other account is used."),
		ui.ModelFieldState(fieldRankerModel, "Model", r.Model, nil, "Choose a decision model or a chat model, or enter its exact ID."),
		modelTypeField(fieldRankerModelType, r.ModelType), cadence, k}
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
		case fieldRankerAccount:
			r.AccountID = v
		case fieldRankerModel:
			r.Model = v
		case fieldRankerModelType:
			r.ModelType = v
		case "ranker-every_actions":
			r.EveryActions, _ = strconv.Atoi(v)
		case "ranker-top_k":
			r.TopK, _ = strconv.Atoi(v)
		}
	}
	return r
}
