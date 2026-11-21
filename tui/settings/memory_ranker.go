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
	account := ui.SelectFieldState(fieldRankerAccount, "Ranking account", r.AccountID, m.accountOptions(r.AccountID),
		"Ranking uses only this account, not the Memory Agent's. It sends task, action and memory excerpts.")
	account.Placeholder = "Choose an account"
	model := ui.ModelFieldState(fieldRankerModel, "Ranking model", r.Model, nil,
		"A fast decision model or chat model. Choose one from the list or enter its exact ID.")
	model.Placeholder = "Choose or enter a model ID"
	// Ranking is a separate job: while an agent works, it picks saved
	// memories for the next step. It never writes memories.
	return []*ui.FieldState{ui.SeparatorFieldState("ranking", "Memory ranking"),
		ui.BoolFieldState("ranker-enabled", "Rank memories", r.Enabled == "on",
			"While an agent works, add the saved memories that help with its next step."),
		account, model, modelTypeField(fieldRankerModelType, r.ModelType), cadence, k}
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
