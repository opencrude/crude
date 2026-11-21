package settings

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"crude/agent/provider"
	"crude/tui/ui"
	"crude/types"
)

// Errand's Suggestions tab is built like the Auto Review Agent's connection:
// an account, a model, a request format, a speed tier, a timeout and an output
// limit. Empty account and model use Errand's own. Crude always sends thinking
// "none"; it is not a choice.
const (
	fieldSuggestAccount  = "suggest-account"
	fieldSuggestModel    = "suggest-model"
	fieldSuggestFormat   = "suggest-format"
	fieldSuggestEndpoint = "suggest-endpoint"
	fieldSuggestTier     = "suggest-tier"
	fieldSuggestTimeout  = "suggest-timeout"
	fieldSuggestOutput   = "suggest-output"
)

func suggestionField(id string) bool { return strings.HasPrefix(id, "suggest-") }

// suggestionFields builds the Suggestions tab. Its Account and Model show
// Errand's choices the same way every agent editor shows inherited values;
// syncSuggestionConnection fills them once the Errand draft is open.
func (m *Model) suggestionFields(s types.SuggestionSettings) []*ui.FieldState {
	n := s.Normalized()
	tier := ui.SelectFieldState(fieldSuggestTier, "Speed", s.ServiceTier, nil,
		"Processing tier for Chat suggestion requests. The account's protocol decides which tiers exist.")
	fields := []*ui.FieldState{
		ui.SelectFieldState(fieldSuggestAccount, "Account", s.AccountID, m.accountOptions(s.AccountID),
			"Where suggestions go. Errand's account is the default."),
		ui.ModelFieldState(fieldSuggestModel, "Model", s.Model, nil,
			"A fast model without thinking. Empty uses Errand's model. Suggestions feel best under 300 ms."),
		ui.SelectFieldState(fieldSuggestFormat, "Request format", n.Format, []ui.FormOption{
			{ID: types.SuggestionFormatChat, Label: "Chat", Detail: "Inserts and edits near the caret; works with every account"},
			{ID: types.SuggestionFormatOpenAI, Label: "Completions with suffix", Detail: "Fill-in-the-middle servers; inserts only"},
			{ID: types.SuggestionFormatDeepSeek, Label: "DeepSeek FIM", Detail: "Inserts only"},
			{ID: types.SuggestionFormatCodestral, Label: "Codestral FIM", Detail: "Inserts only"},
		}, "Chat can also change nearby lines and point to the next place to edit."),
		ui.TextFieldState(fieldSuggestEndpoint, "Endpoint path", s.EndpointPath,
			"Fill-in-the-middle formats only. The path after the account's API address, for example completions. Crude never guesses it."),
		tier,
		ui.NumberFieldState(fieldSuggestTimeout, "Timeout", n.TimeoutSeconds, "seconds", "A suggestion that takes longer is dropped."),
		ui.NumberFieldState(fieldSuggestOutput, "Output limit", n.MaxOutputTokens, "tokens", "The most text one suggestion may use."),
	}
	tier.Placeholder = "Account default"
	for _, field := range fields {
		field.Validator = func(ui.FieldValue) []string { return nil }
	}
	fields[3].Validator = func(value ui.FieldValue) []string {
		path := strings.TrimSpace(value.String())
		if path != "" && !types.ValidSuggestionEndpointPath(path) {
			return []string{"Enter a path such as completions, without a host name."}
		}
		return nil
	}
	tier.Validator = func(value ui.FieldValue) []string {
		if id := value.String(); id != "" && !m.suggestionTierOffered(m.suggestionAccountID(), id) {
			return []string{"This account does not offer this speed. Choose another speed."}
		}
		return nil
	}
	fields[5].Validator = numberRange(types.SuggestionMinTimeoutSeconds, types.SuggestionMaxTimeoutSeconds)
	fields[6].Validator = numberRange(types.SuggestionMinOutputTokens, types.SuggestionMaxOutputTokens)
	return fields
}

// numberRange accepts a whole number from low to high, and nothing else.
func numberRange(low, high int) func(ui.FieldValue) []string {
	return func(value ui.FieldValue) []string {
		n, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || n < low || n > high {
			return []string{fmt.Sprintf("Enter a whole number from %d to %d.", low, high)}
		}
		return nil
	}
}

// suggestionAccountID is the account suggestions use in the open Errand
// draft: the chosen one, else Errand's.
func (m *Model) suggestionAccountID() string {
	form := m.categories[catAgents].Form
	if form == nil || form.Field(fieldSuggestAccount) == nil {
		return ""
	}
	id := form.Field(fieldSuggestAccount).Value.String()
	if id == "" {
		id = m.suggestionParent().account
	}
	if _, ok := m.accounts[id]; ok {
		return id
	}
	return ""
}

// syncSuggestionConnection shows what empty suggestion choices use. It runs
// when the Errand draft opens and when its Model tab changes.
func (m *Model) syncSuggestionConnection() {
	form := m.categories[catAgents].Form
	if form == nil || form.Field(fieldSuggestAccount) == nil {
		return
	}
	m.syncConnection(form, fieldSuggestAccount, fieldSuggestModel, m.suggestionParent())
	if tier := form.Field(fieldSuggestTier); tier != nil {
		tier.Options = m.suggestionTierOptions(m.suggestionAccountID(), tier.Value.String())
	}
}

// suggestionTiers lists the speeds that the account's protocol can send; an
// account whose protocol has no tiers offers only its default.
func (m *Model) suggestionTiers(accountID string) []string {
	account, ok := m.accounts[accountID]
	if !ok {
		return nil
	}
	proto, ok := provider.ProtocolByID(providerStandard(account.ProviderID, account.Connection))
	if !ok {
		return nil
	}
	return provider.ServiceTierChoicesFor(proto.Info().Options.ServiceTiers)
}

func (m *Model) suggestionTierOffered(accountID, tier string) bool {
	return slices.Contains(m.suggestionTiers(accountID), tier)
}

// suggestionTierOptions offers the account default and the protocol's own
// tiers. A saved tier that the account does not offer stays visible, marked,
// so the choice is never changed silently.
func (m *Model) suggestionTierOptions(accountID, current string) []ui.FormOption {
	options := []ui.FormOption{{ID: "", Label: "Account default"}}
	for _, tier := range m.suggestionTiers(accountID) {
		options = append(options, ui.FormOption{ID: tier, Label: tierLabel(tier)})
	}
	if current != "" && !m.suggestionTierOffered(accountID, current) {
		options = append(options, ui.FormOption{ID: current, Label: tierLabel(current) + " (not offered)"})
	}
	return options
}

// suggestionsFromForm reads the Suggestions tab of a draft.
func suggestionsFromForm(form *ui.FormModel, base types.SuggestionSettings) types.SuggestionSettings {
	value := func(id string) string {
		if field := form.Field(id); field != nil {
			return strings.TrimSpace(field.Value.String())
		}
		return ""
	}
	number := func(id string, fallback int) int {
		if n, err := strconv.Atoi(value(id)); err == nil {
			return n
		}
		return fallback
	}
	if form.Field(fieldSuggestFormat) == nil {
		return base
	}
	s := types.SuggestionSettings{AccountID: value(fieldSuggestAccount), Model: value(fieldSuggestModel),
		Format: value(fieldSuggestFormat), EndpointPath: value(fieldSuggestEndpoint), ServiceTier: value(fieldSuggestTier),
		TimeoutSeconds: number(fieldSuggestTimeout, 0), MaxOutputTokens: number(fieldSuggestOutput, 0)}
	if s.Format == types.SuggestionFormatChat {
		s.Format, s.EndpointPath = "", ""
	}
	defaults := types.SuggestionSettings{}.Normalized()
	if s.TimeoutSeconds == defaults.TimeoutSeconds {
		s.TimeoutSeconds = 0
	}
	if s.MaxOutputTokens == defaults.MaxOutputTokens {
		s.MaxOutputTokens = 0
	}
	return s
}

// validateSuggestionsForm reports the first field the user must fix.
func validateSuggestionsForm(form *ui.FormModel) (string, string) {
	s := suggestionsFromForm(form, types.SuggestionSettings{})
	switch {
	case s.AccountID != "" && s.Model == "":
		return fieldSuggestModel, "Choose a model for this account."
	case s.Format != "" && s.EndpointPath == "":
		return fieldSuggestEndpoint, "Enter the endpoint path for this format."
	}
	return "", ""
}
