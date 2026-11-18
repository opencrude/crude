package settings

import (
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

// Aeron's Suggestions tab is built like the Auto Review Agent's connection:
// an account, a model, a request format, a speed tier, a timeout and an output
// limit. Empty account and model use Aeron's own. Crude always sends thinking
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

func (m *Model) suggestionFields(s types.SuggestionSettings) []*ui.FieldState {
	n := s.Normalized()
	accounts := append([]ui.FormOption{{ID: "", Label: "Aeron's account"}}, m.connectionOptions(false)...)
	var models []ui.FormOption
	if account, ok := m.accounts[s.AccountID]; ok {
		models = ui.ModelOptions(m.models[accountModelKey(account.ProviderID, s.AccountID)])
	}
	fields := []*ui.FieldState{
		ui.SelectFieldState(fieldSuggestAccount, "Account", s.AccountID, accounts,
			"Where suggestions go. Aeron's account is the default."),
		ui.ModelFieldState(fieldSuggestModel, "Model", s.Model, models,
			"A fast model without thinking. Empty uses Aeron's model. Suggestions feel best under 300 ms."),
		ui.SelectFieldState(fieldSuggestFormat, "Request format", n.Format, []ui.FormOption{
			{ID: types.SuggestionFormatChat, Label: "Chat", Detail: "Inserts and edits near the caret; works with every account"},
			{ID: types.SuggestionFormatOpenAI, Label: "Completions with suffix", Detail: "Fill-in-the-middle servers; inserts only"},
			{ID: types.SuggestionFormatDeepSeek, Label: "DeepSeek FIM", Detail: "Inserts only"},
			{ID: types.SuggestionFormatCodestral, Label: "Codestral FIM", Detail: "Inserts only"},
		}, "Chat can also change nearby lines and point to the next place to edit."),
		ui.TextFieldState(fieldSuggestEndpoint, "Endpoint path", s.EndpointPath,
			"Fill-in-the-middle formats only, for example /v1/completions. Crude never guesses it."),
		ui.SelectFieldState(fieldSuggestTier, "Speed", s.ServiceTier, []ui.FormOption{
			{ID: "", Label: "Account default"}, {ID: "standard", Label: "Standard"}, {ID: "fast", Label: "Fast"},
			{ID: "priority", Label: "Priority"}, {ID: "flex", Label: "Flex"},
		}, "Processing tier for suggestion requests."),
		ui.NumberFieldState(fieldSuggestTimeout, "Timeout", n.TimeoutSeconds, "seconds", "A suggestion that takes longer is dropped."),
		ui.NumberFieldState(fieldSuggestOutput, "Output limit", n.MaxOutputTokens, "tokens", "The most text one suggestion may use."),
	}
	for _, field := range fields {
		field.Validator = func(ui.FieldValue) []string { return nil }
	}
	fields[3].Validator = func(value ui.FieldValue) []string {
		path := strings.TrimSpace(value.String())
		if path != "" && !types.ValidSuggestionEndpointPath(path) {
			return []string{"Enter a path such as /v1/completions, without a host name."}
		}
		return nil
	}
	return fields
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
