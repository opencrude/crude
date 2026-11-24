package settingspages

import (
	"slices"
	"sort"
	"strings"
	"time"

	"crude/agent/provider"
	"crude/tui/ui"
	"crude/types/setting"
)

const (
	accountFieldSeed       = "account-seed"
	accountFieldEndpoint   = "account-endpoint"
	accountFieldAuthType   = "account-auth-type"
	accountFieldAuthHeader = "account-auth-header"
	accountFieldHeaders    = "account-extra-headers"
	accountFieldProtocol   = "account-wire-protocol"
)

func (m *Model) presetOptions() []ui.FormOption {
	templates := provider.DefaultTemplates()
	options := make([]ui.FormOption, 0, len(templates))
	for _, id := range provider.PresetIDs() {
		if template, ok := templates[id]; ok {
			options = append(options, ui.FormOption{ID: id, Label: template.Name})
		}
	}
	return options
}

func (m *Model) buildAccountEditorForm() *ui.FormModel {
	if m.accountEditor == nil {
		return formModel()
	}
	edit, account := m.accountEditor, provider.CloneAccount(m.accountEditor.draft)
	connection := provider.CloneTemplate(account.Connection)
	label := ui.TextFieldState(fieldAccountLabel, "Account name", account.Label, "Unique name shown in agent and account pickers.")
	label.Validator = func(value ui.FieldValue) []string {
		if strings.TrimSpace(value.String()) == "" {
			return []string{"Enter a name for this account."}
		}
		for id, current := range m.accounts {
			if id != account.ID && strings.EqualFold(strings.TrimSpace(current.Label), strings.TrimSpace(value.String())) {
				return []string{"Account names must be unique."}
			}
		}
		return nil
	}
	endpoint := ui.TextFieldState(accountFieldEndpoint, "Endpoint", connection.ProviderURL, "HTTP(S) endpoint for model and chat requests.")
	endpoint.Validator = func(value ui.FieldValue) []string {
		trial := provider.CloneTemplate(connection)
		trial.ProviderURL = strings.TrimSpace(value.String())
		if err := trial.Validate(); err != nil && strings.Contains(err.Error(), "endpoint") {
			return []string{err.Error()}
		}
		return nil
	}
	auth := ui.SelectFieldState(accountFieldAuthType, "Authentication", connection.AuthType, authTypeOptions(), "Credential transport for this account.")
	headers := ui.KeyValuesFieldState(accountFieldHeaders, "Extra headers", formatHeaders(connection.Headers), "Sent with every request made through this account.")
	headers.Placeholder, headers.Columns = "Add header", []string{"Header", "Value"}
	headers.Validator = headerMapValidator
	fields := []*ui.FieldState{ui.SeparatorFieldState("account-identity", "Account")}
	if edit.isNew {
		fields = append(fields, ui.SelectFieldState(accountFieldSeed, "Start from", edit.seedID, m.presetOptions(), "Copy an embedded preset into this independent account."))
	}
	fields = append(fields, label, endpoint, ui.SeparatorFieldState("account-authentication", "Authentication"), auth)
	if connection.AuthType == provider.AuthCustomHeader {
		header := ui.TextFieldState(accountFieldAuthHeader, "Credential header", connection.AuthHeader, "Header populated from this account's API key.")
		header.Validator = requiredHeaderNameValidator
		fields = append(fields, header)
	}
	switch connection.AuthType {
	case provider.AuthChatGPT:
		fields = append(fields, m.chatgptAuthFields(account)...)
	case provider.AuthNone:
		fields = append(fields, &ui.FieldState{ID: "account-auth-none", Label: "Credential", Type: ui.FieldText, Value: ui.StringValue("Not required"), ReadOnly: true})
	default:
		fields = append(fields, ui.PasswordFieldState(fieldAPIKey, "API key", account.APIKey, "Credential stored only on this application account."))
	}
	fields = append(fields, headers, ui.SeparatorFieldState("account-protocol", "Wire protocol"), ui.SelectFieldState(accountFieldProtocol, "Protocol", providerStandard(account.ProviderID, connection), standardOptions(nil), "Request and response wire format."))
	fields = append(fields, m.protocolFieldsFor(account.ProviderID, connection, "")...)
	if !edit.isNew && m.hasCompactionCheck(account.ID) {
		fields = append(fields, ui.BoolFieldState("forget-compaction", "Forget compaction checks", edit.forgetCompaction, "Save to check again at next use. Conversations stay unchanged."))
	}
	fields = append(fields, dialerFields(connection.Dialer)...)
	fields = append(fields, ui.SeparatorFieldState("account-request-options", "Request options"))
	request := m.extraBodyField(connection)
	request.ID, request.Label, request.Description = fieldExtraBody, "Extra JSON body", "Object merged into each request body. Blank means no extra fields."
	fields = append(fields, request)
	if m.accountError != "" {
		fields = append(fields, &ui.FieldState{ID: "account-error", Label: "Status", Type: ui.FieldText, Value: ui.StringValue(m.accountError), ReadOnly: true, Errors: []string{m.accountError}})
	}
	if m.accountBusy {
		for _, field := range fields {
			field.Disabled = true
		}
	}
	return formModel(fields...)
}

func (m *Model) protocolFieldsFor(providerID string, cfg provider.Template, prefix string) []*ui.FieldState {
	proto, ok := provider.ProtocolByID(providerStandard(providerID, cfg))
	if !ok {
		return nil
	}
	options, settings := proto.Info().Options, cfg.Protocol
	var fields []*ui.FieldState
	add := func(field *ui.FieldState) { field.ID = prefix + field.ID; fields = append(fields, field) }
	{
		method := "auto"
		if settings != nil && settings.Compaction != "" {
			method = settings.Compaction
		}
		hint := "Auto checks the model at first use. Errand is the fallback."
		if options.NativeCompaction == "" {
			hint = "Auto uses Errand. This wire has no bounded native check."
		}
		add(ui.SelectFieldState(fieldCompactionMethod, "Compaction", method, []ui.FormOption{{ID: "auto", Label: "Auto"}, {ID: provider.CompactionProviderOnly, Label: "Provider only"}, {ID: provider.CompactionErrandOnly, Label: "Errand only"}}, hint))
	}
	if options.Store {
		label := "Off (default)"
		if options.StoreDefault {
			label = "On (default)"
		}
		add(ui.TriStateFieldState(fieldProtocolStore, "Server storage", label, protocolStoreValue(settings), "Request retained context and delta uploads."))
	}
	if options.MediaReplayRecovery {
		add(ui.TriStateFieldState(fieldProtocolMediaReplay, "Inline image replay recovery", "On (default)", protocolMediaReplayValue(settings), "Retry once with full local context after exact stored-image replay rejection."))
	}
	if options.ZeroDataRetention {
		add(ui.ToggleFieldState(fieldProtocolZDR, "Zero data retention", settings != nil && settings.ZeroDataRetention, "Do not retain conversation data after a response."))
	}
	if options.ReasoningSummaries {
		add(ui.TriStateFieldState(fieldProtocolSummaries, "Reasoning summaries", "On (default)", protocolSummariesValue(settings), "Include summarized reasoning when supported."))
	}
	if options.Truncation {
		current := "fail"
		if settings != nil && settings.Truncation != nil && *settings.Truncation {
			current = "truncate"
		}
		field := ui.CycleFieldState(fieldProtocolTruncate, "Context overflow", []string{"fail", "truncate"}, current, "Truncate overflowing context rather than failing.")
		field.Options = []ui.FormOption{{ID: "fail", Label: "Fail (default)"}, {ID: "truncate", Label: "Truncate"}}
		add(field)
	}
	if len(options.ServiceTiers) > 0 {
		current := ""
		if settings != nil {
			current = settings.ServiceTier
		}
		add(ui.SelectFieldState(fieldProtocolTier, "Service tier", current, serviceTierOptions(options.ServiceTiers), "Processing tier for requests."))
	}
	return fields
}

func (m *Model) storeAccountEditorForm() {
	if m.Scope() != setting.ScopeApplication || m.accountEditor == nil || len(m.categories) <= catAccounts || m.categories[catAccounts].Form == nil {
		return
	}
	draft := provider.CloneAccount(m.accountEditor.draft)
	if m.fieldAt(catAccounts, "forget-compaction") != nil {
		m.accountEditor.forgetCompaction = m.value(catAccounts, "forget-compaction") == "on"
	}
	draft.Label = strings.TrimSpace(m.value(catAccounts, fieldAccountLabel))
	connection := provider.CloneTemplate(draft.Connection)
	oldAuth, oldEndpoint := connection.AuthType, connection.ProviderURL
	connection.ProviderURL, connection.AuthType, connection.AuthHeader = strings.TrimSpace(m.value(catAccounts, accountFieldEndpoint)), m.value(catAccounts, accountFieldAuthType), strings.TrimSpace(m.value(catAccounts, accountFieldAuthHeader))
	connection.Headers, connection.APIStandard = parseHeaderLines(m.value(catAccounts, accountFieldHeaders)), m.value(catAccounts, accountFieldProtocol)
	connection.Protocol = m.protocolSettingsFromForm(catAccounts)
	connection.Dialer = dialerFromForm(m.value(catAccounts, fieldDialTimeout), m.value(catAccounts, fieldDialConnect), m.value(catAccounts, fieldDialAttempts), m.value(catAccounts, fieldDialBackoff), m.value(catAccounts, fieldDialProgressStartup), m.value(catAccounts, fieldDialProgressFloor), m.value(catAccounts, fieldDialProgressCeiling))
	connection.ExtraBody = extraBodyFromForm(m.value(catAccounts, fieldExtraBody))
	draft.Connection = connection
	if oldAuth != connection.AuthType || (oldAuth == provider.AuthChatGPT && oldEndpoint != connection.ProviderURL) {
		draft.APIKey = ""
		draft.OAuth = nil
	}
	switch connection.AuthType {
	case provider.AuthChatGPT:
		draft.APIKey = ""
	case provider.AuthNone:
		draft.APIKey, draft.OAuth = "", nil
	default:
		if oldAuth == connection.AuthType {
			draft.APIKey = m.value(catAccounts, fieldAPIKey)
		}
		draft.OAuth = nil
	}
	m.accountEditor.draft = draft
}

func authTypeOptions() []ui.FormOption {
	return []ui.FormOption{{ID: provider.AuthProtocol, Label: "Protocol default"}, {ID: provider.AuthBearer, Label: "Bearer token"}, {ID: provider.AuthCustomHeader, Label: "Custom header"}, {ID: provider.AuthNone, Label: "No authentication"}, {ID: provider.AuthChatGPT, Label: "ChatGPT sign-in"}}
}
func formatHeaders(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+": "+headers[key])
	}
	return strings.Join(lines, "\n")
}
func parseHeaderLines(value string) map[string]string {
	headers := map[string]string{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(name) != "" {
			headers[strings.TrimSpace(name)] = strings.TrimSpace(v)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
func headerMapValidator(value ui.FieldValue) []string {
	text := strings.TrimSpace(value.String())
	if text == "" {
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(v) == "" {
			return []string{"Use one non-empty Name: value header per line."}
		}
	}
	return nil
}
func requiredHeaderNameValidator(value ui.FieldValue) []string {
	if strings.TrimSpace(value.String()) == "" {
		return []string{"Enter the credential header name."}
	}
	return nil
}

func (m *Model) hasCompactionCheck(account string) bool {
	for _, v := range m.layerBase.CompactionChecks {
		if v.AccountID == account && v.Until > time.Now().Unix() {
			return true
		}
	}
	return false
}

// compactionHint states the observed native support for the selected model.
func (m *Model) compactionHint(account, model string) string {
	a, ok := m.accounts[account]
	if !ok {
		return ""
	}
	if p, ok := provider.ProtocolByID(providerStandard(a.ProviderID, a.Connection)); !ok || p.Info().Options.NativeCompaction == "" {
		return ""
	}
	if o := a.Connection.Protocol; o != nil && o.Compaction == provider.CompactionErrandOnly {
		return "Errand compacts for this account."
	}
	for _, v := range m.layerBase.CompactionChecks {
		if v.AccountID != account || v.Model != model || v.Until <= time.Now().Unix() {
			continue
		}
		switch v.Status {
		case "verified":
			return "Native compaction verified."
		case "unavailable":
			return "No native compaction; Auto uses Errand."
		default:
			return "Native check failed; retrying later."
		}
	}
	return "Native compaction checked at first use."
}

// UpdateCompactionChecks refreshes the selected model's hint without touching drafts.
func (s *WorkspaceSettingsSurface) UpdateCompactionChecks(checks []provider.CompactionObservation) bool {
	if s == nil || s.model == nil || slices.Equal(s.model.layerBase.CompactionChecks, checks) {
		return false
	}
	m := s.model
	m.layerBase.CompactionChecks = slices.Clone(checks)
	for i := range m.categories {
		if m.isAgentCategory(i) {
			m.syncCompactionHint(i)
		}
	}
	return true
}
func (m *Model) syncCompactionHint(category int) {
	form := m.categories[category].Form
	if form == nil {
		return
	}
	field := form.Field(fieldContextCompaction)
	if field == nil {
		return
	}
	a, model := m.effectiveConnection(category)
	field.Description = compactAtDescription(m.compactionHint(a, model))
}

// compactAtDescription is the one help text of "Compact context at". The
// model's native compaction state comes first and the rest stays short enough
// for one hint line in a desktop-size Settings window.
func compactAtDescription(hint string) string {
	if hint == "" {
		return "Blank is Auto (cost and capacity aware). A percent compacts there; 0 is off."
	}
	return hint + " Blank is Auto; a percent compacts there; 0 is off."
}
