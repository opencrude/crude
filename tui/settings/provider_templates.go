package settings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"crude/provider"
	"crude/tui/ui"
	"crude/types"
)

const (
	templateFieldSeed       = "template-seed"
	templateFieldName       = "template-name"
	templateFieldEndpoint   = "template-endpoint"
	templateFieldAuthType   = "template-auth-type"
	templateFieldAuthHeader = "template-auth-header"
	templateFieldHeaders    = "template-extra-headers"
	templateFieldProtocol   = "template-wire-protocol"
	accountOverridePrefix   = "account-override-"
	accountOverrideEndpoint = accountOverridePrefix + "endpoint"
	accountOverrideAuth     = accountOverridePrefix + "auth-type"
	accountOverrideHeader   = accountOverridePrefix + "auth-header"
	accountOverrideHeaders  = accountOverridePrefix + "headers"
	accountOverrideProtocol = accountOverridePrefix + "protocol"
	accountOverrideDialer   = accountOverridePrefix + "dialer"
	accountOverrideOptions  = accountOverridePrefix + "protocol-options"
	accountOverrideRequest  = accountOverridePrefix + "request-options"
)

type providerEdit struct {
	id        string
	draft     provider.Template
	isNew     bool
	seedID    string
	fromClone bool
}

func sortedTemplateIDs(templates map[string]types.ProviderConfig) []string {
	ids := make([]string, 0, len(templates))
	for id := range templates {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := provider.ResolveTemplate(ids[i], templates[ids[i]], provider.TemplateOverrides{}).Name
		right := provider.ResolveTemplate(ids[j], templates[ids[j]], provider.TemplateOverrides{}).Name
		if c := strings.Compare(strings.ToLower(left), strings.ToLower(right)); c != 0 {
			return c < 0
		}
		return ids[i] < ids[j]
	})
	return ids
}

func (m *Model) providerRows() []ui.TableRow {
	rows := make([]ui.TableRow, 0, len(m.providers))
	query := strings.ToLower(strings.TrimSpace(m.templateQuery))
	for _, id := range sortedTemplateIDs(m.providers) {
		template := provider.ResolveTemplate(id, m.providers[id], provider.TemplateOverrides{})
		if query != "" && !strings.Contains(strings.ToLower(template.Name+" "+template.ProviderURL+" "+protocolName(template.APIStandard)), query) {
			continue
		}
		auth := authTypeName(template.AuthType)
		if template.AuthType == provider.AuthProtocol {
			auth = "Built-in"
		}
		if template.AuthType == provider.AuthNone {
			auth = "None"
		}
		if template.AuthType == provider.AuthChatGPT {
			auth = "Codex login"
		}
		rows = append(rows, ui.TableRow{ID: id, Cells: []ui.TableCell{
			{Text: template.Name},
			{Text: template.ProviderURL},
			{Text: protocolName(template.APIStandard)},
			{Text: auth},
		}})
	}
	return rows
}

func (m *Model) providerTemplateOptions() []ui.FormOption {
	ids := sortedTemplateIDs(m.providers)
	options := make([]ui.FormOption, 0, len(ids))
	for _, id := range ids {
		template := provider.ResolveTemplate(id, m.providers[id], provider.TemplateOverrides{})
		options = append(options, ui.FormOption{ID: id, Label: template.Name})
	}
	return options
}

func (m *Model) buildProviderEditorForm() *ui.FormModel {
	if m.providerEditor == nil {
		return formModel()
	}
	editor := m.providerEditor
	template := provider.CloneTemplate(editor.draft)
	effective := provider.ResolveTemplate(editor.id, template, provider.TemplateOverrides{})
	name := ui.TextFieldState(templateFieldName, "Name", template.Name, "Unique label for this reusable provider template.")
	name.Validator = func(value ui.FieldValue) []string {
		if strings.TrimSpace(value.String()) == "" {
			return []string{"Enter a template name."}
		}
		for id, existing := range m.providers {
			if id == editor.id {
				continue
			}
			resolved := provider.ResolveTemplate(id, existing, provider.TemplateOverrides{})
			if strings.EqualFold(strings.TrimSpace(resolved.Name), strings.TrimSpace(value.String())) {
				return []string{"Template names must be unique."}
			}
		}
		return nil
	}
	endpoint := ui.TextFieldState(templateFieldEndpoint, "Endpoint", effective.ProviderURL, "HTTP(S) endpoint for model and chat requests.")
	endpoint.Validator = func(value ui.FieldValue) []string {
		trial := effective
		trial.ProviderURL = strings.TrimSpace(value.String())
		if err := trial.Validate(); err != nil && strings.Contains(err.Error(), "endpoint") {
			return []string{err.Error()}
		}
		return nil
	}
	auth := ui.SelectFieldState(templateFieldAuthType, "Authentication", effective.AuthType, authTypeOptions(), "The credential transport used by accounts based on this template.")
	headers := ui.KeyValuesFieldState(templateFieldHeaders, "Extra headers", formatHeaders(effective.Headers), "Sent with every request made through this template.")
	headers.Validator = headerMapValidator
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("template-identity", "Template"),
	}
	if editor.isNew && !editor.fromClone {
		seeds := []ui.FormOption{{ID: "", Label: "Blank template"}}
		defaults := provider.DefaultTemplates()
		for _, id := range provider.PresetIDs() {
			if template, ok := defaults[id]; ok {
				seeds = append(seeds, ui.FormOption{ID: id, Label: template.Name})
			}
		}
		fields = append(fields, ui.SelectFieldState(templateFieldSeed, "Start from", editor.seedID, seeds, "Copy a preset into a new, independently named template."))
	}
	fields = append(fields, name, endpoint, ui.SeparatorFieldState("template-authentication", "Authentication"), auth)
	if effective.AuthType == provider.AuthCustomHeader {
		authHeader := ui.TextFieldState(templateFieldAuthHeader, "Credential header", effective.AuthHeader, "Header name populated from the account credential.")
		authHeader.Validator = requiredHeaderNameValidator
		fields = append(fields, authHeader)
	}
	fields = append(fields,
		headers,
		ui.SeparatorFieldState("template-protocol", "Wire protocol"),
		ui.SelectFieldState(templateFieldProtocol, "Protocol", providerStandard(editor.id, effective), standardOptions(nil), "Request and response wire format."),
	)
	fields = append(fields, m.protocolFieldsFor(editor.id, effective, "")...)
	fields = append(fields, ui.SeparatorFieldState("template-transport", "Dial settings"))
	fields = append(fields, dialerFields(effective.Dialer)...)
	fields = append(fields, ui.SeparatorFieldState("template-request-options", "Request options"))
	request := m.extraBodyField(effective)
	request.ID = fieldExtraBody
	request.Label = "Extra JSON body"
	request.Description = "Object merged into each request body. Blank means no extra fields."
	fields = append(fields, request)
	form := formModel(fields...)
	if m.providerError != "" {
		form.AddField(&ui.FieldState{ID: "template-error", Label: "Status", Type: ui.FieldText, Value: ui.StringValue(m.providerError), ReadOnly: true, Errors: []string{m.providerError}})
	}
	if m.accountBusy {
		for _, field := range form.Fields() {
			field.Disabled = true
		}
	}
	return form
}

func (m *Model) protocolFieldsFor(providerID string, cfg provider.Template, prefix string) []*ui.FieldState {
	standard := providerStandard(providerID, cfg)
	proto, ok := provider.ProtocolByID(standard)
	if !ok {
		return nil
	}
	options := proto.Info().Options
	settings := cfg.Protocol
	var fields []*ui.FieldState
	add := func(field *ui.FieldState) {
		field.ID = prefix + field.ID
		fields = append(fields, field)
	}
	if options.Store {
		defaultLabel := "Off (default)"
		if options.StoreDefault {
			defaultLabel = "On (default)"
		}
		add(ui.TriStateFieldState(fieldProtocolStore, "Server-side caching", defaultLabel, protocolStoreValue(settings), "Keep context on the service between requests."))
	}
	if options.ZeroDataRetention {
		zdr := settings != nil && settings.ZeroDataRetention
		add(ui.ToggleFieldState(fieldProtocolZDR, "Zero data retention", zdr, "Do not retain conversation data after a response."))
	}
	if options.ReasoningSummaries {
		add(ui.TriStateFieldState(fieldProtocolSummaries, "Reasoning summaries", "On (default)", protocolSummariesValue(settings), "Include summarized reasoning when supported."))
	}
	if options.Truncation {
		current := "fail"
		if settings != nil && settings.Truncation != nil && *settings.Truncation {
			current = "truncate"
		}
		field := ui.CycleFieldState(fieldProtocolTruncate, "Context overflow", []string{"fail", "truncate"}, current, "Truncate overflowing context rather than failing the request.")
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

func (m *Model) storeProviderEditorForm() {
	if m.providerEditor == nil || len(m.categories) <= catProviders || m.categories[catProviders].Form == nil {
		return
	}
	draft := m.providerEditor.draft
	draft.Name = strings.TrimSpace(m.value(catProviders, templateFieldName))
	draft.ProviderURL = strings.TrimSpace(m.value(catProviders, templateFieldEndpoint))
	draft.AuthType = m.value(catProviders, templateFieldAuthType)
	draft.AuthHeader = strings.TrimSpace(m.value(catProviders, templateFieldAuthHeader))
	draft.Headers = parseHeaderLines(m.value(catProviders, templateFieldHeaders))
	draft.APIStandard = m.value(catProviders, templateFieldProtocol)
	draft.Protocol = m.protocolSettingsFromForm(catProviders)
	draft.Dialer = dialerFromForm(
		m.value(catProviders, fieldDialTimeout),
		m.value(catProviders, fieldDialConnect),
		m.value(catProviders, fieldDialAttempts),
		m.value(catProviders, fieldDialBackoff),
		m.value(catProviders, fieldDialProgressStartup),
		m.value(catProviders, fieldDialProgressFloor),
		m.value(catProviders, fieldDialProgressCeiling),
	)
	draft.ExtraBody = extraBodyFromForm(m.value(catProviders, fieldExtraBody))
	m.providerEditor.draft = draft
}

func (m *Model) beginProviderEdit(create, clone bool) error {
	if m.providerEditor != nil {
		return nil
	}
	editor := &providerEdit{}
	if create {
		editor.isNew = true
		if clone {
			editor.fromClone = true
			original, ok := m.providers[m.providerID]
			if !ok {
				return fmt.Errorf("select a template to clone")
			}
			editor.draft = provider.ResolveTemplate(m.providerID, original, provider.TemplateOverrides{})
			name := editor.draft.Name
			editor.draft.Name = "Copy of " + name
			editor.id = ""
		} else {
			standard := ""
			if standards := provider.StandardList(); len(standards) > 0 {
				standard = standards[0].ID
			}
			editor.draft = provider.Template{AuthType: provider.AuthProtocol, AuthHeader: "Authorization", APIStandard: standard}
		}
	} else {
		original, ok := m.providers[m.providerID]
		if !ok {
			return fmt.Errorf("select a template to edit")
		}
		editor.id = m.providerID
		editor.draft = provider.ResolveTemplate(m.providerID, original, provider.TemplateOverrides{})
	}
	m.providerEditor = editor
	m.providerError = ""
	m.categories[catProviders].Form = m.buildProvidersForm()
	m.categories[catProviders].Form.OnChanged(func(fieldID string) { m.handleFieldChanged(catProviders, fieldID) })
	return nil
}

func (m *Model) providerEditorDirty() bool {
	if m.providerEditor == nil {
		return false
	}
	return m.categories[catProviders].Form.AnyDirty()
}

func (m *Model) cancelProviderEdit() {
	if m.accountBusy {
		return
	}
	m.providerEditor = nil
	m.providerError = ""
	m.rebuildProvidersForm()
}

func (m *Model) providerEditorChange() (provider.TemplateChange, error) {
	if m.providerEditor == nil {
		return provider.TemplateChange{}, fmt.Errorf("no provider template editor is open")
	}
	m.storeProviderEditorForm()
	form := m.categories[catProviders].Form
	form.ValidateAll()
	if form.AnyErrors() {
		return provider.TemplateChange{}, fmt.Errorf("correct the highlighted template fields")
	}
	draft := provider.CloneTemplate(m.providerEditor.draft)
	draft.Name = strings.TrimSpace(draft.Name)
	for id, existing := range m.providers {
		if id != m.providerEditor.id && strings.EqualFold(strings.TrimSpace(provider.ResolveTemplate(id, existing, provider.TemplateOverrides{}).Name), draft.Name) {
			return provider.TemplateChange{}, fmt.Errorf("template names must be unique")
		}
	}
	if err := draft.Validate(); err != nil {
		return provider.TemplateChange{}, err
	}
	id := m.providerEditor.id
	if m.providerEditor.isNew {
		id = uniqueTemplateID(draft.Name, m.providers)
	}
	if id == "" {
		return provider.TemplateChange{}, fmt.Errorf("could not assign a template ID")
	}
	change := provider.TemplateChange{ID: id, After: &draft}
	if !m.providerEditor.isNew {
		previous := provider.CloneTemplate(m.providers[id])
		change.Before = &previous
	}
	return change, nil
}

func uniqueTemplateID(name string, templates map[string]types.ProviderConfig) string {
	var slug strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			separator = false
			slug.WriteRune(r)
		default:
			separator = true
		}
	}
	base := strings.Trim(slug.String(), "-")
	if base == "" {
		base = "provider-template"
	}
	id := base
	for suffix := 2; ; suffix++ {
		if _, exists := templates[id]; !exists {
			return id
		}
		id = base + "-" + strconv.Itoa(suffix)
	}
}

func (m *Model) templateInUse(id string) bool {
	for _, account := range m.accounts {
		if account.ProviderID == id {
			return true
		}
	}
	return false
}

func (m *Model) deleteProviderTemplate(id string) (provider.TemplateChange, error) {
	if m.templateInUse(id) {
		return provider.TemplateChange{}, fmt.Errorf("this template is used by one or more accounts")
	}
	configured, ok := m.providers[id]
	if !ok {
		return provider.TemplateChange{}, fmt.Errorf("the selected template no longer exists")
	}
	previous := provider.CloneTemplate(configured)
	return provider.TemplateChange{ID: id, Before: &previous}, nil
}

func (m *Model) applyTemplateChange(result TemplateChangeResultMsg) {
	m.accountBusy = false
	if result.Err != nil {
		m.providerError = result.Err.Error()
		m.rebuildProvidersForm()
		return
	}
	m.providers = provider.CloneTemplates(result.Settings.Providers)
	for id, account := range m.accounts {
		if account.ProviderID == result.Change.ID {
			key := accountModelKey(account.ProviderID, id)
			delete(m.models, key)
			m.fetchGeneration[key] = nextAccountRequest.Add(1)
		}
	}
	m.templateQuery = ""
	m.providerError = ""
	m.providerEditor = nil
	if result.Change.After != nil {
		m.providerID = result.Change.ID
	} else {
		ids := sortedTemplateIDs(m.providers)
		m.providerID = ""
		if len(ids) > 0 {
			m.providerID = ids[0]
		}
	}
	m.rebuildProvidersForm()
	m.rebuildAccountsForm()
	m.refreshAgentConnectionFields()
}

func (m *Model) buildAccountEditorForm() *ui.FormModel {
	if m.accountEditor == nil {
		return formModel()
	}
	edit := m.accountEditor
	account := edit.draft
	templateConfig, exists := m.providers[account.ProviderID]
	effective := provider.ResolveTemplate(account.ProviderID, templateConfig, account.Overrides)
	templateOptions := m.providerTemplateOptions()
	if !exists {
		templateOptions = append(templateOptions, ui.FormOption{ID: account.ProviderID, Label: "Missing template: " + account.ProviderID})
	}
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
	templateField := ui.SelectFieldState(fieldProvider, "Template", account.ProviderID, templateOptions, "Provider template for endpoint and request behavior.")
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("account-identity", "Account"),
		templateField,
		label,
		ui.SeparatorFieldState("account-credentials", "Credentials"),
	}
	switch effective.AuthType {
	case provider.AuthChatGPT:
		fields = append(fields, m.chatgptAuthFields(account)...)
	case provider.AuthNone:
		fields = append(fields, &ui.FieldState{ID: "account-auth-none", Label: "Credential", Type: ui.FieldText, Value: ui.StringValue("Not required by this template"), ReadOnly: true})
	default:
		fields = append(fields, ui.PasswordFieldState(fieldAPIKey, "API key", account.APIKey, "Credential stored only on this application account."))
	}
	fields = append(fields,
		ui.SeparatorFieldState("account-template-overrides", "Template overrides"),
		accountOverrideSwitch(accountOverrideEndpoint+"-enabled", "Override URL", account.Overrides.ProviderURL != nil, "Use an account-specific endpoint."),
		accountOverrideText(accountOverrideEndpoint, "URL", effective.ProviderURL, account.Overrides.ProviderURL != nil, "Endpoint inherited from the template when disabled."),
		accountOverrideSwitch(accountOverrideAuth+"-enabled", "Override auth", account.Overrides.AuthType != nil, "Use an account-specific authentication mode."),
		accountOverrideSelect(accountOverrideAuth, "Auth type", effective.AuthType, authTypeOptions(), account.Overrides.AuthType != nil, "Authentication mode inherited from the template when disabled."),
		accountOverrideSwitch(accountOverrideHeader+"-enabled", "Override key header", account.Overrides.AuthHeader != nil, "Use an account-specific credential header name."),
		accountOverrideText(accountOverrideHeader, "Key header", effective.AuthHeader, account.Overrides.AuthHeader != nil, "Header name inherited from the template when disabled."),
		accountOverrideSwitch(accountOverrideHeaders+"-enabled", "Override headers", account.Overrides.Headers != nil, "Replace the template's additional request headers."),
		accountOverrideHeadersTable(accountOverrideHeaders, "Headers", formatHeaders(effective.Headers), account.Overrides.Headers != nil, "Header set inherited from the template when disabled."),
		accountOverrideSwitch(accountOverrideProtocol+"-enabled", "Override wire", account.Overrides.APIStandard != nil, "Use an account-specific wire protocol."),
		accountOverrideSelect(accountOverrideProtocol, "Wire protocol", effective.APIStandard, standardOptions(nil), account.Overrides.APIStandard != nil, "Wire protocol inherited from the template when disabled."),
		accountOverrideSwitch(accountOverrideDialer+"-enabled", "Override dialer", account.Overrides.Dialer != nil, "Replace the template's connection timeouts, progress limits, and retry settings for this account."),
	)
	dialFields := dialerFields(effective.Dialer)
	for _, field := range dialFields {
		field.ID = accountOverrideDialer + "-" + field.ID
		field.ReadOnly = account.Overrides.Dialer == nil
		fields = append(fields, field)
	}
	fields = append(fields, accountOverrideSwitch(accountOverrideOptions+"-enabled", "Override options", account.Overrides.Protocol != nil, "Replace protocol request options for this account."))
	for _, field := range m.protocolFieldsFor(account.ProviderID, effective, accountOverrideOptions+"-") {
		field.ReadOnly = account.Overrides.Protocol == nil
		fields = append(fields, field)
	}
	fields = append(fields,
		accountOverrideSwitch(accountOverrideRequest+"-enabled", "Override body", account.Overrides.ExtraBody != nil, "Replace the template's extra JSON request fields."),
	)
	request := ui.TextAreaFieldState(accountOverrideRequest, "Extra JSON", formatExtraBody(effective.ExtraBody), "Request body inherited from the template when disabled.")
	request.Rows = 4
	request.ReadOnly = account.Overrides.ExtraBody == nil
	request.Validator = jsonObjectValidator
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

func (m *Model) storeAccountEditorForm() {
	if m.Scope() != types.SettingsScopeApplication || m.accountEditor == nil || len(m.categories) <= catAccounts || m.categories[catAccounts].Form == nil {
		return
	}
	edit := m.accountEditor
	draft := provider.CloneAccount(edit.draft)
	draft.ProviderID = m.value(catAccounts, fieldProvider)
	draft.Label = strings.TrimSpace(m.value(catAccounts, fieldAccountLabel))
	effective := provider.ResolveTemplate(draft.ProviderID, m.providers[draft.ProviderID], draft.Overrides)
	switch effective.AuthType {
	case provider.AuthChatGPT:
		draft.APIKey = ""
	case provider.AuthNone:
		draft.APIKey = ""
		draft.OAuth = nil
	default:
		draft.APIKey = m.value(catAccounts, fieldAPIKey)
		draft.OAuth = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideEndpoint+"-enabled")) {
		value := strings.TrimSpace(m.value(catAccounts, accountOverrideEndpoint))
		draft.Overrides.ProviderURL = &value
	} else {
		draft.Overrides.ProviderURL = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideAuth+"-enabled")) {
		value := m.value(catAccounts, accountOverrideAuth)
		draft.Overrides.AuthType = &value
	} else {
		draft.Overrides.AuthType = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideHeader+"-enabled")) {
		value := strings.TrimSpace(m.value(catAccounts, accountOverrideHeader))
		draft.Overrides.AuthHeader = &value
	} else {
		draft.Overrides.AuthHeader = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideHeaders+"-enabled")) {
		value := parseHeaderLines(m.value(catAccounts, accountOverrideHeaders))
		draft.Overrides.Headers = &value
	} else {
		draft.Overrides.Headers = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideProtocol+"-enabled")) {
		value := m.value(catAccounts, accountOverrideProtocol)
		draft.Overrides.APIStandard = &value
	} else {
		draft.Overrides.APIStandard = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideDialer+"-enabled")) {
		dialer := dialerFromForm(
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialTimeout),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialConnect),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialAttempts),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialBackoff),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialProgressStartup),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialProgressFloor),
			m.value(catAccounts, accountOverrideDialer+"-"+fieldDialProgressCeiling),
		)
		if dialer == nil {
			dialer = &provider.DialSettings{}
		}
		draft.Overrides.Dialer = dialer
	} else {
		draft.Overrides.Dialer = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideOptions+"-enabled")) {
		protocolSettings := m.protocolSettingsWithPrefix(catAccounts, accountOverrideOptions+"-")
		if protocolSettings == nil {
			protocolSettings = &provider.ProtocolSettings{}
		}
		draft.Overrides.Protocol = protocolSettings
	} else {
		draft.Overrides.Protocol = nil
	}
	if ui.ToggleFieldValue(m.fieldAt(catAccounts, accountOverrideRequest+"-enabled")) {
		value := extraBodyFromForm(m.value(catAccounts, accountOverrideRequest))
		draft.Overrides.ExtraBody = &value
	} else {
		draft.Overrides.ExtraBody = nil
	}
	edit.draft = draft
}

func (m *Model) protocolSettingsWithPrefix(category int, prefix string) *provider.ProtocolSettings {
	settings := provider.ProtocolSettings{
		Store:              m.triStateAt(category, prefix+fieldProtocolStore),
		ZeroDataRetention:  m.toggleAt(category, prefix+fieldProtocolZDR),
		ReasoningSummaries: m.triStateAt(category, prefix+fieldProtocolSummaries),
		Truncation:         m.truncationAt(category, prefix+fieldProtocolTruncate),
		ServiceTier:        m.value(category, prefix+fieldProtocolTier),
	}
	if settings == (provider.ProtocolSettings{}) {
		return nil
	}
	return &settings
}

func accountOverrideSwitch(id, label string, enabled bool, description string) *ui.FieldState {
	return ui.ToggleFieldState(id, label, enabled, description)
}

func accountOverrideText(id, label, value string, enabled bool, description string) *ui.FieldState {
	field := ui.TextFieldState(id, label, value, description)
	field.ReadOnly = !enabled
	return field
}

func accountOverrideSelect(id, label, value string, options []ui.FormOption, enabled bool, description string) *ui.FieldState {
	field := ui.SelectFieldState(id, label, value, options, description)
	field.ReadOnly = !enabled
	return field
}

// accountOverrideHeadersTable edits a keyed header set as a Name/value table.
// The stored value keeps the "Name: value" line protocol, so the table is a
// presentation and editing layer only.
func accountOverrideHeadersTable(id, label, value string, enabled bool, description string) *ui.FieldState {
	field := ui.KeyValuesFieldState(id, label, value, description)
	field.ReadOnly = !enabled
	field.Validator = headerMapValidator
	return field
}

func authTypeOptions() []ui.FormOption {
	return []ui.FormOption{
		{ID: provider.AuthProtocol, Label: "Protocol default"},
		{ID: provider.AuthBearer, Label: "Bearer token"},
		{ID: provider.AuthCustomHeader, Label: "Custom header"},
		{ID: provider.AuthNone, Label: "No authentication"},
		{ID: provider.AuthChatGPT, Label: "ChatGPT sign-in"},
	}
}

func authTypeName(id string) string {
	for _, option := range authTypeOptions() {
		if option.ID == id {
			return option.Label
		}
	}
	return "Protocol default"
}

func protocolName(id string) string {
	switch id {
	case "openai":
		return "Chat Completions"
	case "openai-responses":
		return "Responses"
	case "anthropic":
		return "Messages"
	case "codex":
		return "Codex"
	}
	for _, standard := range provider.StandardList() {
		if standard.ID == id {
			return standard.Name
		}
	}
	return id
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
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return nil
	}
	headers := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, headerValue, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name, headerValue = strings.TrimSpace(name), strings.TrimSpace(headerValue)
		if name != "" {
			headers[name] = headerValue
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
		name, headerValue, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(headerValue) == "" {
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

func jsonObjectValidator(value ui.FieldValue) []string {
	trimmed := strings.TrimSpace(value.String())
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil || decoded == nil {
		return []string{"Must be a JSON object."}
	}
	return nil
}
