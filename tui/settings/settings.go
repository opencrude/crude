package settings

import (
	"encoding/json"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// SaveMsg is returned as a tea.Msg on save.
type SaveMsg struct {
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Overrides   types.WorkspaceOverrides
}

// CancelMsg is returned as a tea.Msg on cancel.
type CancelMsg struct{}

// ThemePreviewMsg applies a theme preset immediately while Settings stays open.
type ThemePreviewMsg struct {
	Preset string
}

// FramePreviewMsg applies edited shell frame paddings immediately while
// Settings stays open, so the inset preview follows each keystroke. Nil
// fields are left unchanged.
type FramePreviewMsg struct {
	Top, Right, Bottom, Left *int
}

// FetchModelsMsg asks the workspace layer to load models for a provider.
type FetchModelsMsg struct {
	ProviderID string
	BaseURL    string
	APIKey     string
	StandardID string
}

// ModelsMsg and ModelsErrorMsg deliver an asynchronous model-list result.
type ModelsMsg struct {
	ProviderID string
	Models     []types.ModelEntry
}

type ModelsErrorMsg struct {
	ProviderID string
	Error      string
}

// ResetSectionMsg clears the workspace override for one settings page.
type ResetSectionMsg struct{ Key string }

// ScopeMsg switches the settings dialog between application and workspace.
type ScopeMsg struct{ Scope types.SettingsScope }

// ChatGPTSignInResultMsg delivers the outcome of a "Sign in with ChatGPT" run.
// On success Creds holds the tokens to persist; on failure Err is set (and may
// wrap a ChatGPTSignInError whose Fatal flag means the stored session is dead).
type ChatGPTSignInResultMsg struct {
	Creds *provider.OAuthCredentials
	Err   error
}

// ChatGPTSignOutMsg clears the stored ChatGPT tokens.
type ChatGPTSignOutMsg struct{}

// Category is a presentation-neutral settings section backed by the shared
// form model. It deliberately has no legacy tui/form state.
type Category struct {
	Name        string
	Group       string
	Description string
	Form        *ui.FormModel
}

type categorySnapshot struct{ values map[string]ui.FieldValue }

type modelSnapshot struct {
	categories    []categorySnapshot
	selected      int
	pane          int
	providerID    string
	providerModel string
}

// Model is the workspace settings screen state.
type Model struct {
	th            *theme.Theme
	categories    []Category
	selected      int
	pane          int // 0: category rail, 1: form
	snapshot      *modelSnapshot
	presets       []types.ProviderPreset
	providers     map[string]types.ProviderConfig
	providerID    string
	providerModel string
	models        map[string][]types.ModelEntry
	searchEd      *ui.TextEdit
	searchActive  bool
	scope         types.SettingsScope
	appDefaults   types.AppSettings
	// agents holds the working AgentsSettings so the main agent form's
	// Multi-Agent toggle reads and writes the real workspace flag.
	agents *types.AgentsSettings
}

const (
	fieldProvider       = "provider"
	fieldModel          = "model"
	fieldAPIKey         = "api-key"
	fieldBaseURL        = "base-url"
	fieldAPIStandard    = "api-standard"
	fieldThinkingEffort = "thinking-effort"
	fieldMultiAgent     = "multi-agent"
)

// New creates a settings model from current AppSettings (application scope).
func New(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset) *Model {
	return NewScoped(th, current, presets, types.SettingsScopeApplication, current, types.WorkspaceOverrides{})
}

// NewScoped creates a settings model for application or workspace scope.
func NewScoped(th *theme.Theme, current types.AppSettings, presets []types.ProviderPreset, scope types.SettingsScope, appDefaults types.AppSettings, _ types.WorkspaceOverrides) *Model {
	types.ApplyAppDefaults(&current)
	appearance := current.Theme
	editor := current.Editor
	lsp := current.LSP
	uiSettings := current.UI
	font := current.Font
	if len(presets) == 0 {
		for _, preset := range provider.PresetList() {
			presets = append(presets, types.ProviderPreset{
				ID: preset.ID, Name: preset.Name, HelpText: preset.HelpText,
				BaseURL: preset.BaseURL, StandardID: preset.StandardID,
			})
		}
	}
	providers := make(map[string]types.ProviderConfig, len(current.Providers))
	for id, cfg := range current.Providers {
		cfg.ModelStandards = cloneStandardMap(cfg.ModelStandards)
		providers[id] = cfg
	}
	active := current.ActiveProvider
	if active == "" {
		active = "openrouter"
	}
	if _, ok := providers[active]; !ok {
		providers[active] = types.ProviderConfig{}
	}
	m := &Model{
		th: th, presets: presets, providers: providers, providerID: active,
		providerModel: providers[active].Model,
		models:        make(map[string][]types.ModelEntry), scope: scope, appDefaults: appDefaults,
		agents: &current.Agents,
	}
	appearanceFields := append([]*ui.FieldState{
		ui.SeparatorFieldState("theme", "Theme"),
		ui.CycleFieldState("preset", "Preset", append([]string(nil), theme.AllPresetNames...), appearance.Preset, "Choose a complete visual palette for the workspace."),
		ui.SeparatorFieldState("palette", "Palette preview"),
	}, appearanceColorFields(appearance.Preset, appearance)...)
	appearanceFields = append(appearanceFields,
		// The form shows the effective value on each row; when the stored
		// value is unset (nil) the accessor resolves to the default. Blank
		// input means "inherit the default". Fields are Live so the frame
		// previews around the live shell while Settings stays open.
		ui.SeparatorFieldState("frame", "Shell frame"),
		liveFramePadField("frame-top", "Top padding", appearance.PadTopOrDefault(), "Blank rows reserved above the header."),
		liveFramePadField("frame-bottom", "Bottom padding", appearance.PadBottomOrDefault(), "Blank rows reserved below the status bar."),
		liveFramePadField("frame-left", "Left padding", appearance.PadLeftOrDefault(), "Blank columns reserved left of the activity rail."),
		liveFramePadField("frame-right", "Right padding", appearance.PadRightOrDefault(), "Blank columns reserved right of the content."),
	)
	m.categories = []Category{
		{Name: "Providers", Group: "AI", Description: "Connections, credentials, and communication protocol.", Form: m.buildProvidersForm()},
		{Name: "Main Agent", Group: "AI", Description: "Workspace defaults for planning and delegation.", Form: m.buildAgentForm(current.Agents.Categories["default"], true)},
		{Name: "Researcher", Group: "AI", Description: "Workspace defaults for research and context gathering.", Form: m.buildAgentForm(current.Agents.Categories["researcher"], false)},
		{Name: "Crafter", Group: "AI", Description: "Workspace defaults for implementation work.", Form: m.buildAgentForm(current.Agents.Categories["crafter"], false)},
		{Name: "Aeron", Group: "AI", Description: "Workspace defaults for mechanical tasks.", Form: m.buildAgentForm(current.Agents.Categories["aeron"], false)},
		{Name: "Memory", Group: "AI", Description: "Durable workspace memory: recall, extraction, consolidation.", Form: m.buildMemoryForm(current.Memory)},
		{Name: "Appearance", Group: "IDE", Description: "Theme palette and surface colors.", Form: formModel(appearanceFields...)},
		{Name: "Editor", Group: "IDE", Description: "Editing, wrapping, syntax, and save behavior.", Form: formModel(
			ui.SeparatorFieldState("editing", "Editing"),
			ui.TextFieldState("tab-size", "Tab size", fmt.Sprintf("%d", editor.TabSize), "Number of spaces inserted when a tab is used."),
			ui.BoolFieldState("word-wrap", "Word wrap", editor.WordWrap, "Wrap long lines inside the editor viewport."),
			ui.BoolFieldState("line-numbers", "Line numbers", editor.LineNumbers, "Show the document line-number gutter."),
			ui.CycleFieldState("syntax-theme", "Syntax theme", []string{"catppuccin", "dracula", "github", "monokai", "solarized"}, editor.SyntaxTheme, "Color grammar used for source code."),
			ui.CycleFieldState("render-whitespace", "Render whitespace", []string{"none", "boundary", "all"}, editor.RenderWhitespace, "Show spaces and tabs when inspecting layout."),
			ui.TextFieldState("auto-save-delay", "Auto-save delay", fmt.Sprintf("%d", editor.AutoSaveDelay), "Delay in milliseconds; zero disables automatic saves."),
			ui.BoolFieldState("format-on-save", "Format on save", editor.FormatOnSave, "Run the language server formatter when a file is saved."),
		)},
		{Name: "Language Servers", Group: "IDE", Description: "Language intelligence and diagnostics.", Form: m.buildLSPForm(lsp)},
		{Name: "Interface", Group: "IDE", Description: "File tree, sidebar, icons, and diagnostics.", Form: formModel(
			ui.SeparatorFieldState("workspace-chrome", "Workspace chrome"),
			ui.TextFieldState("sidebar-width", "Sidebar width", fmt.Sprintf("%d", uiSettings.SidebarWidth), "Width of the file and workspace navigation rail."),
			ui.BoolFieldState("show-hidden", "Show hidden files", uiSettings.ShowHiddenFiles, "Include dotfiles in the workspace tree."),
			ui.BoolFieldState("show-icons", "Show file icons", uiSettings.ShowIcons, "Display file-type icons in navigation."),
			ui.BoolFieldState("nerd-font", "Nerd font", uiSettings.UseNerdFont, "Use extended icon glyphs when the font supports them."),
			ui.BoolFieldState("git-status", "Git status", uiSettings.ShowGitStatus, "Show changed-file markers in the workspace tree."),
			ui.BoolFieldState("diagnostics", "Diagnostics", uiSettings.ShowDiagnostics, "Show language diagnostics in navigation."),
		)},
		{Name: "Font", Group: "IDE", Description: "Terminal font hints and ligatures.", Form: formModel(
			ui.SeparatorFieldState("typography", "Typography"),
			ui.TextFieldState("family", "Family", font.Family, "Font family is controlled by the host terminal."),
			ui.TextFieldState("size", "Size", fmt.Sprintf("%d", font.Size), "Preferred terminal font size in points."),
			ui.BoolFieldState("ligatures", "Ligatures", font.Ligatures, "Allow programming ligatures when supported by the terminal."),
		)},
	}
	for index := range m.categories {
		category := index
		m.categories[index].Form.OnChanged(func(fieldID string) { m.handleFieldChanged(category, fieldID) })
	}
	return m
}

func formModel(fields ...*ui.FieldState) *ui.FormModel {
	m := ui.NewFormModel()
	for _, field := range fields {
		m.AddField(field)
	}
	return m
}

// appearanceColorFields builds read-only hex+swatch rows reflecting the active
// preset (or an explicit override string when set). They are not editable —
// palette changes happen by cycling the preset.
func appearanceColorFields(preset string, overrides types.ThemeSettings) []*ui.FieldState {
	th := theme.FromPreset(preset)
	resolve := func(override string, col color.Color) string {
		if override != "" {
			return override
		}
		return rendering.ThemeColorToHex(col)
	}
	c := th.Colors
	return []*ui.FieldState{
		readOnlyColorField("background", "Background", resolve(overrides.Bg, c.Bg)),
		readOnlyColorField("foreground", "Foreground", resolve(overrides.Fg, c.Fg)),
		readOnlyColorField("accent", "Accent", resolve(overrides.Accent, c.Accent)),
		readOnlyColorField("accent2", "Secondary accent", resolve(overrides.Accent2, c.Accent2)),
		readOnlyColorField("subpane-bg", "Panel background", resolve(overrides.SubPaneBg, c.SubPaneBg)),
		readOnlyColorField("border", "Border", resolve(overrides.Border, c.Border)),
		readOnlyColorField("selection", "Selection", resolve(overrides.Selection, c.Selection)),
	}
}

func readOnlyColorField(id, label, hex string) *ui.FieldState {
	v := ui.StringValue(hex)
	return &ui.FieldState{
		ID: id, Label: label, Type: ui.FieldText,
		Value: v, Initial: v, ReadOnly: true,
	}
}

func (m *Model) buildProvidersForm() *ui.FormModel {
	cfg := m.providers[m.providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(m.providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("connection", "Connection"),
		ui.SelectFieldState(fieldProvider, "Provider", m.providerID, ui.ProviderOptions(m.presets), "The service used for model requests."),
	}
	// The ChatGPT subscription provider authenticates via OAuth, not an API
	// key, so it gets a status row plus a sign-in / sign-out drill-in instead
	// of the masked key field. The field set differs per provider, which is
	// why the form is rebuilt on provider switch (see rebuildProvidersForm).
	if m.providerID == "chatgpt" {
		fields = append(fields, m.chatgptAuthFields(cfg)...)
	} else {
		fields = append(fields, ui.PasswordFieldState(fieldAPIKey, "API key", cfg.APIKey, "Stored locally and masked while editing."))
	}
	fields = append(fields,
		ui.TextFieldState(fieldBaseURL, "Base URL", baseURL, "Override the endpoint when using a proxy or gateway."),
		ui.SeparatorFieldState("protocol", "Communication protocol"),
		ui.SelectFieldState(
			fieldAPIStandard, "Provider default", providerStandard(m.providerID, cfg),
			standardOptions(nil),
			"Wire protocol used by this provider.",
		),
	)
	fields = append(fields, m.protocolOptionFields(cfg)...)
	fields = append(fields, dialerFields(cfg.Dialer)...)
	fields = append(fields,
		ui.SeparatorFieldState("advanced", "Advanced"),
		m.extraBodyField(cfg),
	)
	return formModel(fields...)
}

// Field IDs for the protocol option, capability, and sampling rows.
const (
	fieldProtocolStore     = "protocol-store"
	fieldProtocolZDR       = "protocol-zdr"
	fieldProtocolSummaries = "protocol-summaries"
	fieldProtocolTruncate  = "protocol-truncation"
	fieldProtocolTier      = "protocol-tier"
	fieldExtraBody         = "extra-body"
	fieldCtxWindow         = "model-context-window"
	fieldMaxOutput         = "model-max-output"
	fieldModalityPrefix    = "modality-"
	fieldTemperature       = "model-temperature"
	fieldTopP              = "model-top-p"
)

// protocolOptionFields renders the protocol-level request switches the
// resolved protocol declares via ProtocolInfo.Options. Protocols without a
// knob render nothing, so the section is data-driven, not type-switched.
func (m *Model) protocolOptionFields(cfg types.ProviderConfig) []*ui.FieldState {
	proto, ok := provider.ProtocolByID(providerStandard(m.providerID, cfg))
	if !ok {
		return nil
	}
	options := proto.Info().Options
	settings := cfg.Protocol
	var fields []*ui.FieldState

	if options.Store {
		defaultLabel := "Off (default)"
		if options.StoreDefault {
			defaultLabel = "On (default)"
		}
		fields = append(fields, ui.TriStateFieldState(fieldProtocolStore, "Server-side caching", defaultLabel,
			protocolStoreValue(settings),
			"Cache context server-side instead of re-uploading it each request."))
	}
	if options.ZeroDataRetention {
		zdr := false
		if settings != nil {
			zdr = settings.ZeroDataRetention
		}
		fields = append(fields, ui.ToggleFieldState(fieldProtocolZDR, "Zero data retention", zdr,
			"Never store the conversation server-side; reasoning stays encrypted."))
	}
	if options.ReasoningSummaries {
		fields = append(fields, ui.TriStateFieldState(fieldProtocolSummaries, "Reasoning summaries", "On (default)",
			protocolSummariesValue(settings),
			"Include summarized reasoning alongside the final answer."))
	}
	if options.Truncation {
		current := "fail"
		if settings != nil && settings.Truncation != nil && *settings.Truncation {
			current = "truncate"
		}
		f := ui.CycleFieldState(fieldProtocolTruncate, "Context overflow", []string{"fail", "truncate"}, current,
			"Truncate context beyond the model window instead of failing.")
		f.Options = []ui.FormOption{{ID: "fail", Label: "Fail (default)"}, {ID: "truncate", Label: "Truncate"}}
		fields = append(fields, f)
	}
	if len(options.ServiceTiers) > 0 {
		current := ""
		if settings != nil {
			current = settings.ServiceTier
		}
		fields = append(fields, ui.SelectFieldState(fieldProtocolTier, "Service tier", current,
			serviceTierOptions(options.ServiceTiers),
			"Processing tier: flex is cheaper, priority is faster."))
	}
	return fields
}

// serviceTierOptions turns the protocol's declared tiers into select options.
// The first tier is the wire default and carries the empty ID (inherit).
func serviceTierOptions(tiers []string) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(tiers))
	for i, tier := range tiers {
		id := tier
		if i == 0 {
			id = ""
		}
		label := tierLabel(tier)
		if i == 0 {
			label += " (default)"
		}
		options = append(options, ui.FormOption{ID: id, Label: label})
	}
	return options
}

// tierLabel humanizes a tier identifier ("flex" → "Flex", "standard_only" →
// "Standard only").
func tierLabel(tier string) string {
	words := strings.Split(strings.ReplaceAll(tier, "-", "_"), "_")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func protocolStoreValue(settings *provider.ProtocolSettings) *bool {
	if settings == nil {
		return nil
	}
	return settings.Store
}

func protocolSummariesValue(settings *provider.ProtocolSettings) *bool {
	if settings == nil {
		return nil
	}
	return settings.ReasoningSummaries
}

// extraBodyField builds the raw-JSON power-user field: a multi-line editor
// (JSON blobs do not fit one row) validating that the input is a JSON object.
func (m *Model) extraBodyField(cfg types.ProviderConfig) *ui.FieldState {
	f := ui.TextAreaFieldState(fieldExtraBody, "Extra JSON body", formatExtraBody(cfg.ExtraBody),
		"Raw JSON object merged into every request body sent.")
	f.Validator = func(value ui.FieldValue) []string {
		trimmed := strings.TrimSpace(value.String())
		if trimmed == "" {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return []string{"Must be a JSON object, e.g. {\"key\": \"value\"}"}
		}
		return nil
	}
	return f
}

// formatExtraBody renders stored extras as compact JSON for editing.
func formatExtraBody(extra map[string]any) string {
	if len(extra) == 0 {
		return ""
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Dialer field IDs. Each row offers a ladder of concrete values; the built-in
// default leads the ladder and is labeled "(default)" so the inherited
// behavior is always visible in the control itself.
const (
	fieldDialTimeout  = "dial-request-timeout"
	fieldDialConnect  = "dial-connect-timeout"
	fieldDialAttempts = "dial-max-attempts"
	fieldDialBackoff  = "dial-max-backoff"
)

var (
	dialTimeoutLadder  = []int{30, 60, 120, 300, 600} // default: unlimited (0)
	dialConnectLadder  = []int{5, 10, 60}             // default: 30s
	dialAttemptsLadder = []int{1, 2, 3, 5}            // default: 10
	dialBackoffLadder  = []int{15, 30, 60, 300}       // default: 120s
)

// dialerFields builds the transport-tuning rows for one provider. A nil dialer
// selects the built-in defaults; nothing is persisted unless a row moves off
// its default.
func dialerFields(dialer *provider.DialSettings) []*ui.FieldState {
	d := dialerValue(dialer)
	return []*ui.FieldState{
		ui.SeparatorFieldState("dialer", "Connection tuning"),
		dialSecondsField(fieldDialTimeout, "Request timeout", d.RequestTimeoutSeconds, 0, dialTimeoutLadder,
			"Overall cap per request, including streaming."),
		dialSecondsField(fieldDialConnect, "Connect timeout", d.ConnectTimeoutSeconds,
			int(provider.DefaultConnectTimeout.Seconds()), dialConnectLadder,
			"Budget for establishing each connection (TCP + TLS)."),
		dialCountField(fieldDialAttempts, "Max attempts", d.MaxAttempts, provider.DefaultMaxAttempts, dialAttemptsLadder,
			"Tries per request; failures retry with exponential backoff."),
		dialSecondsField(fieldDialBackoff, "Max backoff", d.MaxBackoffSeconds,
			int(provider.DefaultMaxBackoff.Seconds()), dialBackoffLadder,
			"Upper bound on the exponential retry delay."),
	}
}

// dialSecondsField renders one timeout row.
func dialSecondsField(id, label string, value, builtinDefault int, ladder []int, description string) *ui.FieldState {
	f := ui.CycleFieldState(id, label, nil, dialChoiceID(value, builtinDefault), description)
	f.Options = dialFieldOptions(value, builtinDefault, ladder, "s")
	return f
}

// dialCountField renders one unit-less count row.
func dialCountField(id, label string, value, builtinDefault int, ladder []int, description string) *ui.FieldState {
	f := ui.CycleFieldState(id, label, nil, dialChoiceID(value, builtinDefault), description)
	f.Options = dialFieldOptions(value, builtinDefault, ladder, "")
	return f
}

// dialChoiceID maps a stored value to its option ID; unset values select the
// built-in default.
func dialChoiceID(value, builtinDefault int) string {
	if value <= 0 {
		return strconv.Itoa(builtinDefault)
	}
	return strconv.Itoa(value)
}

// dialFieldOptions builds a ladder of concrete choices led by the built-in
// default. A stored value outside the ladder is inserted so existing settings
// stay visible and editable.
func dialFieldOptions(current, builtinDefault int, ladder []int, unit string) []ui.FormOption {
	values := append([]int{builtinDefault}, ladder...)
	if current > 0 && !slices.Contains(values, current) {
		values = append(values, current)
		slices.Sort(values[1:])
	}
	options := make([]ui.FormOption, 0, len(values))
	for _, value := range values {
		options = append(options, ui.FormOption{
			ID:    strconv.Itoa(value),
			Label: dialChoiceLabel(value, builtinDefault, unit),
		})
	}
	return options
}

// dialChoiceLabel renders one concrete value; the built-in default is marked.
func dialChoiceLabel(value, builtinDefault int, unit string) string {
	label := strconv.Itoa(value) + unit
	if value == 0 {
		label = "Unlimited"
	}
	if value == builtinDefault {
		label += " (default)"
	}
	return label
}

// dialerValue returns a non-nil view of the stored dialer so field rendering
// never needs nil checks.
func dialerValue(dialer *provider.DialSettings) *provider.DialSettings {
	if dialer == nil {
		return &provider.DialSettings{}
	}
	return dialer
}

// dialSecondsFromChoice parses an option ID into seconds. Values equal to the
// built-in default persist as 0 (unset); 0 for the request timeout row means
// unlimited, which is itself the built-in default.
func dialSecondsFromChoice(choice string, builtinDefault int) int {
	seconds, err := strconv.Atoi(choice)
	if err != nil || seconds == builtinDefault {
		return 0
	}
	return seconds
}

// dialAttemptsFromChoice parses the attempts option ID; the built-in default
// persists as 0 (unset).
func dialAttemptsFromChoice(choice string) int {
	return dialSecondsFromChoice(choice, provider.DefaultMaxAttempts)
}

// dialerFromForm folds the four dialer rows back into persisted settings.
// Rows left on the built-in default persist as unset, and an all-default form
// stores nil — defaults, not a frozen copy of them.
func dialerFromForm(timeout, connect, attempts, backoff string) *provider.DialSettings {
	dialer := provider.DialSettings{
		RequestTimeoutSeconds: dialSecondsFromChoice(timeout, 0),
		ConnectTimeoutSeconds: dialSecondsFromChoice(connect, int(provider.DefaultConnectTimeout.Seconds())),
		MaxAttempts:           dialAttemptsFromChoice(attempts),
		MaxBackoffSeconds:     dialSecondsFromChoice(backoff, int(provider.DefaultMaxBackoff.Seconds())),
	}
	if dialer == (provider.DialSettings{}) {
		return nil
	}
	return &dialer
}

// chatgptAuthFields builds the OAuth status + sign-in/sign-out rows shown in
// place of the API-key field for the ChatGPT subscription provider.
func (m *Model) chatgptAuthFields(cfg types.ProviderConfig) []*ui.FieldState {
	signedIn := cfg.OAuth != nil && cfg.OAuth.AccessToken != ""
	status := "Not signed in"
	if signedIn {
		switch {
		case cfg.OAuth.Email != "":
			status = "Signed in as " + cfg.OAuth.Email
		case cfg.OAuth.AccountID != "":
			status = "Signed in (account " + cfg.OAuth.AccountID + ")"
		default:
			status = "Signed in"
		}
	}
	statusField := ui.TextFieldState("chatgpt-status", "Account", status,
		"ChatGPT subscription account; authenticates via browser, no API key.")
	statusField.ReadOnly = true
	if signedIn {
		return []*ui.FieldState{
			statusField,
			{ID: "chatgpt-signout", Label: "Sign out", Type: ui.FieldNav, Target: "settings.chatgpt.signout",
				Button:      true,
				Description: "Forget the stored ChatGPT tokens. You can sign in again any time."},
		}
	}
	return []*ui.FieldState{
		statusField,
		{ID: "chatgpt-signin", Label: "Sign in with ChatGPT", Type: ui.FieldNav, Target: "settings.chatgpt.signin",
			Button:      true,
			Description: "Opens a browser to authorize your ChatGPT subscription."},
	}
}

// rebuildProvidersForm rebuilds the provider form for the current providerID.
// The field set is provider-specific (ChatGPT swaps the API-key row for OAuth
// rows), so switching providers must replace the form model, not just its
// values. The surface rebinds its form widget in the form.changed handler.
func (m *Model) rebuildProvidersForm() {
	form := m.buildProvidersForm()
	m.categories[catProviders].Form = form
	category := catProviders
	form.OnChanged(func(fieldID string) { m.handleFieldChanged(category, fieldID) })
}

// buildAgentForm builds an agent's behavior form. When isMain is true it also
// carries the Multi-Agent enable/disable toggle so the main settings surface
// mirrors the /settings deck. Communication protocol is deliberately not an
// agent property — it is resolved from the provider at use time.
func (m *Model) buildAgentForm(cfg types.CategorySettings, isMain bool) *ui.FormModel {
	providerID := cfg.Provider
	if providerID == "" {
		providerID = m.providerID
	}
	providerCfg := m.providers[providerID]
	model := cfg.Model
	if model == "" {
		model = providerCfg.Model
	}
	thinking := cfg.ThinkingEffort
	if thinking == "" {
		thinking = providerCfg.ThinkingEffort
	}
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("agent", "Agent behavior"),
		ui.SelectFieldState(fieldProvider, "Provider", providerID, ui.ProviderOptions(m.presets), "Override the workspace provider for this agent."),
		ui.ModelFieldState(fieldModel, "Model", model, ui.ModelOptions(m.models[providerID]), "Override the workspace default model."),
		ui.ThinkingFieldState(fieldThinkingEffort, thinking, "Override the workspace reasoning budget."),
	}
	fields = append(fields, m.capabilityFields(cfg)...)
	fields = append(fields, m.samplingFields(cfg)...)
	if isMain {
		fields = append(fields,
			ui.SeparatorFieldState("delegation", "Delegation"),
			ui.BoolFieldState(fieldMultiAgent, "Multi Agent",
				m.agentsEnabled(), "Allow spawning researcher, crafter, and aeron sub-agents."),
		)
	}
	return formModel(fields...)
}

// capabilityFields builds the Model capabilities section: window limits and
// modality toggles for the agent's model. Values left blank inherit whatever
// the model registry reports at use time, so they never freeze stale numbers.
func (m *Model) capabilityFields(cfg types.CategorySettings) []*ui.FieldState {
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("capabilities", "Model capabilities"),
		numericField(fieldCtxWindow, "Context window", cfg.ModelContextWindow,
			"Max input tokens; blank uses the model registry value."),
		numericField(fieldMaxOutput, "Max output", cfg.ModelMaxOutput,
			"Max output tokens; blank uses the model registry value."),
	}
	for _, modality := range provider.KnownModalities() {
		fields = append(fields, ui.ToggleFieldState(
			fieldModalityPrefix+modality,
			modalityLabel(modality),
			slices.Contains(cfg.ModelModalities, modality),
			modalityDescription(modality),
		))
	}
	return fields
}

// samplingFields builds the Sampling section: temperature and top-p overrides
// in their standard 0–2 / 0–1 ranges. Blank inherits the provider default.
func (m *Model) samplingFields(cfg types.CategorySettings) []*ui.FieldState {
	temperature, topP := "", ""
	if cfg.ModelTemperature != nil {
		temperature = strconv.FormatFloat(*cfg.ModelTemperature, 'f', -1, 64)
	}
	if cfg.ModelTopP != nil {
		topP = strconv.FormatFloat(*cfg.ModelTopP, 'f', -1, 64)
	}
	temperatureField := ui.TextFieldState(fieldTemperature, "Temperature", temperature,
		"Randomness from 0 to 2. Blank = default.")
	temperatureField.Validator = floatRangeValidator(0, 2)
	topPField := ui.TextFieldState(fieldTopP, "Top P", topP,
		"Nucleus sampling cutoff, 0 to 1. Blank = default.")
	topPField.Validator = floatRangeValidator(0, 1)
	return []*ui.FieldState{
		ui.SeparatorFieldState("sampling", "Sampling"),
		temperatureField,
		topPField,
	}
}

// floatRangeValidator builds a validator accepting blank input or a float in
// [min, max].
func floatRangeValidator(min, max float64) func(ui.FieldValue) []string {
	return func(value ui.FieldValue) []string {
		trimmed := strings.TrimSpace(value.String())
		if trimmed == "" {
			return nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || parsed < min || parsed > max {
			return []string{fmt.Sprintf("Must be a number between %s and %s.",
				strconv.FormatFloat(min, 'f', -1, 64), strconv.FormatFloat(max, 'f', -1, 64))}
		}
		return nil
	}
}

// numericField renders an optional integer field; 0 and blank mean inherit.
func numericField(id, label string, value int, description string) *ui.FieldState {
	current := ""
	if value > 0 {
		current = strconv.Itoa(value)
	}
	f := ui.TextFieldState(id, label, current, description)
	f.Validator = func(v ui.FieldValue) []string {
		trimmed := strings.TrimSpace(v.String())
		if trimmed == "" {
			return nil
		}
		if n, err := strconv.Atoi(trimmed); err != nil || n <= 0 {
			return []string{"Must be a positive whole number."}
		}
		return nil
	}
	return f
}

// modalityLabel humanizes a modality constant for the toggle label.
func modalityLabel(modality string) string {
	switch modality {
	case provider.ModalityImage:
		return "Images"
	case provider.ModalityPDF:
		return "PDF documents"
	case provider.ModalityVideo:
		return "Video"
	case provider.ModalityAudio:
		return "Audio"
	}
	return strings.ToUpper(modality[:1]) + modality[1:]
}

// modalityDescription explains what each input modality toggle enables.
func modalityDescription(modality string) string {
	switch modality {
	case provider.ModalityImage:
		return "The model accepts image attachments."
	case provider.ModalityPDF:
		return "The model accepts PDF attachments."
	case provider.ModalityVideo:
		return "The model accepts video attachments."
	case provider.ModalityAudio:
		return "The model accepts audio attachments."
	}
	return "The model accepts " + modality + " attachments."
}

// agentsEnabled is a placeholder that is replaced by NewScoped wiring below;
// it reads the controller-level multi-agent flag from the model.
func (m *Model) agentsEnabled() bool {
	if m.agents != nil {
		return m.agents.Enabled
	}
	return true
}

// buildMemoryForm builds the Memory settings form: all the recall/extract/
// consolidation controls plus an Agent behavior section so the memory agent
// can pick its own provider/model/thinking like any other agent. Empty fields
// inherit the main agent at use time.
func (m *Model) buildMemoryForm(mem types.MemorySettings) *ui.FormModel {
	cat := mem.MemoryCategory
	providerID := cat.Provider
	if providerID == "" {
		providerID = m.providerID
	}
	providerCfg := m.providers[providerID]
	model := cat.Model
	if model == "" {
		model = mem.Model // legacy bare override
	}
	if model == "" {
		model = providerCfg.Model
	}
	thinking := cat.ThinkingEffort
	if thinking == "" {
		thinking = providerCfg.ThinkingEffort
	}
	return formModel(
		ui.SeparatorFieldState("memory-recall", "Recall"),
		ui.BoolFieldState("auto-recall", "Auto recall", mem.AutoRecallEnabled(), "Inject relevant workspace memories into context each turn."),
		ui.TextFieldState("recall-budget", "Recall budget", fmt.Sprintf("%d", mem.RecallBudgetOrDefault(types.DefaultMemoryRecallBudget)), "Token budget for the injected memory block."),
		ui.TextFieldState("recall-limit", "Recall limit", fmt.Sprintf("%d", mem.RecallLimitOrDefault(types.DefaultMemoryRecallLimit)), "Maximum memories injected per turn."),
		ui.SeparatorFieldState("memory-background", "Background curation"),
		ui.BoolFieldState("auto-extract", "Auto extract", mem.AutoExtractEnabled(), "Extract durable memories and summaries in a background job."),
		ui.TextFieldState("extract-every", "Extract cadence", fmt.Sprintf("%d", mem.ExtractCadenceOrDefault(types.DefaultMemoryExtractCadence)), "Run extraction every N turns."),
		ui.BoolFieldState("consolidation", "Consolidation", mem.ConsolidationEnabled(), "Background dedup, contradiction resolution, and memory decay."),
		ui.SeparatorFieldState("memory-agent", "Memory agent"),
		ui.SelectFieldState(fieldProvider, "Provider", providerID, ui.ProviderOptions(m.presets), "Override the workspace provider for the memory agent."),
		ui.ModelFieldState(fieldModel, "Model", model, ui.ModelOptions(m.models[providerID]), "Model for background memory jobs. Blank inherits the main agent."),
		ui.ThinkingFieldState(fieldThinkingEffort, thinking, "Override the memory agent's reasoning budget."),
	)
}

type standardDefault struct {
	ID     string
	Label  string
	Detail string
}

func standardOptions(def *standardDefault) []ui.FormOption {
	options := make([]ui.FormOption, 0, len(provider.StandardList())+1)
	if def != nil {
		options = append(options, ui.FormOption{ID: def.ID, Label: def.Label, Detail: def.Detail})
	}
	for _, standard := range provider.StandardList() {
		options = append(options, ui.FormOption{
			ID: standard.ID, Label: standard.Name, Detail: standard.Description,
		})
	}
	return options
}

func providerStandard(providerID string, cfg types.ProviderConfig) string {
	if cfg.APIStandard != "" {
		return cfg.APIStandard
	}
	return provider.DefaultStandardID(providerID)
}

func cloneStandardMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for model, standard := range src {
		out[model] = standard
	}
	return out
}

func parseOpt(value string, defaultVal int) int {
	if value == "" {
		return defaultVal
	}
	if _, err := fmt.Sscanf(value, "%d", &defaultVal); err != nil {
		return defaultVal
	}
	return defaultVal
}

func (m *Model) currentForm() *ui.FormModel {
	if m == nil || m.selected < 0 || m.selected >= len(m.categories) {
		return nil
	}
	return m.categories[m.selected].Form
}

func (m *Model) value(category int, fieldID string) string {
	if category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return ""
	}
	field := m.categories[category].Form.Field(fieldID)
	if field == nil {
		return ""
	}
	return field.Value.String()
}

// ValueForTest exposes a form field's string value for tests.
func (m *Model) ValueForTest(category int, fieldID string) string {
	return m.value(category, fieldID)
}

// SetValueForTest sets a form field value, firing the same onChanged path a
// user edit would.
func (m *Model) SetValueForTest(category int, fieldID, value string) {
	if category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return
	}
	m.categories[category].Form.SetValue(fieldID, ui.StringValue(value))
}

// ProviderIDForTest exposes the current provider selection for tests.
func (m *Model) ProviderIDForTest() string {
	if m == nil {
		return ""
	}
	return m.providerID
}

func (m *Model) isAgentCategory(index int) bool {
	return index == catMain || index == catResearcher || index == catCrafter || index == catAeron
}

func (m *Model) handleFieldChanged(category int, fieldID string) {
	if category == catAppearance && fieldID == "preset" {
		m.syncAppearanceColors()
		return
	}
	if category == catProviders && fieldID == fieldProvider {
		providerID := m.value(catProviders, fieldProvider)
		if providerID != "" && providerID != m.providerID {
			m.storeProviderForm(m.providerID)
			m.providerID = providerID
			m.providerModel = m.providers[providerID].Model
			// The field set is provider-specific (ChatGPT swaps the API-key row
			// for OAuth rows), so rebuild rather than just repopulating values.
			m.rebuildProvidersForm()
		}
		return
	}
	if category == catProviders && fieldID == fieldAPIStandard {
		// Persist the switch, then rebuild: the protocol option rows are
		// capability-driven by the selected protocol and must change with it.
		m.storeProviderForm(m.providerID)
		m.rebuildProvidersForm()
		return
	}
	if (m.isAgentCategory(category) || category == catMemory) && fieldID == fieldProvider {
		providerID := m.value(category, fieldProvider)
		cfg := m.providers[providerID]
		form := m.categories[category].Form
		form.SetValue(fieldModel, ui.StringValue(cfg.Model))
		if field := form.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(m.models[providerID])
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
		return
	}
	if (m.isAgentCategory(category) || category == catMemory) && fieldID == fieldModel {
		m.autofillCapabilities(category)
		return
	}
}

// autofillCapabilities pre-fills an agent form's capability and sampling rows
// from the selected model's registry entry. Fields the registry doesn't know
// are left untouched so previously saved overrides survive a model switch.
func (m *Model) autofillCapabilities(category int) {
	form := m.categories[category].Form
	if form == nil {
		return
	}
	modelID := m.value(category, fieldModel)
	providerID := m.value(category, fieldProvider)
	var entry types.ModelEntry
	var found bool
	for _, model := range m.models[providerID] {
		if model.ID == modelID {
			entry, found = model, true
			break
		}
	}
	if !found {
		return
	}
	if entry.ContextWindow > 0 {
		form.SetValue(fieldCtxWindow, ui.StringValue(strconv.Itoa(entry.ContextWindow)))
	}
	if entry.MaxOutput > 0 {
		form.SetValue(fieldMaxOutput, ui.StringValue(strconv.Itoa(entry.MaxOutput)))
	}
	for _, modality := range provider.KnownModalities() {
		form.SetValue(fieldModalityPrefix+modality, ui.StringValue(modalityChoice(slices.Contains(entry.Modalities, modality))))
	}
}

// modalityChoice renders a modality toggle's cycle value.
func modalityChoice(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

// syncAppearanceColors refreshes read-only colour rows after a preset cycle.
func (m *Model) syncAppearanceColors() {
	if m == nil || catAppearance >= len(m.categories) || m.categories[catAppearance].Form == nil {
		return
	}
	preset := m.value(catAppearance, "preset")
	form := m.categories[catAppearance].Form
	for _, field := range appearanceColorFields(preset, types.ThemeSettings{}) {
		if f := form.Field(field.ID); f != nil {
			f.Value = field.Value
			f.Initial = field.Value
			f.Dirty = false
		}
	}
}

// Update accepts asynchronous workspace results. User interaction is owned by
// the mounted component tree rather than a parallel legacy form event loop.
func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ModelsMsg:
		m.setModels(msg.ProviderID, msg.Models)
	case ModelsErrorMsg:
		m.setModelsError(msg.ProviderID, msg.Error)
	case ChatGPTSignInResultMsg:
		if msg.Err == nil && msg.Creds != nil {
			m.setChatGPTAuth(msg.Creds, "")
		} else {
			// Surface the failure on the status row; keep the sign-in button.
			msg2 := ""
			if msg.Err != nil {
				msg2 = msg.Err.Error()
			}
			m.setChatGPTAuth(nil, msg2)
		}
	case ChatGPTSignOutMsg:
		m.setChatGPTAuth(nil, "")
	}
	return *m, nil
}

// setChatGPTAuth updates the chatgpt provider's OAuth state and refreshes the
// provider form when chatgpt is the selected provider. A nil creds clears the
// tokens (sign-out or fatal sign-in failure); errMsg is shown on the status
// row so the user sees why a sign-in attempt failed.
func (m *Model) setChatGPTAuth(creds *provider.OAuthCredentials, errMsg string) {
	cfg := m.providers["chatgpt"]
	cfg.OAuth = creds
	m.providers["chatgpt"] = cfg
	if m.providerID != "chatgpt" {
		return
	}
	m.rebuildProvidersForm()
	if errMsg != "" {
		if f := m.categories[catProviders].Form.Field("chatgpt-status"); f != nil {
			f.Errors = []string{errMsg}
		}
	}
}

func (m *Model) OpenSelect(fieldID string) tea.Cmd {
	if fieldID != fieldModel || m.currentForm() == nil {
		return nil
	}
	field := m.currentForm().Field(fieldModel)
	if field == nil || len(field.Options) > 0 {
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models..."
	if m.selected == catProviders {
		return m.fetchModelsCmd()
	}
	if m.isAgentCategory(m.selected) || m.selected == catMemory {
		return m.fetchModelsForProviderCmd(m.value(m.selected, fieldProvider))
	}
	return nil
}

func (m *Model) fetchModelsCmd() tea.Cmd {
	return func() tea.Msg {
		return FetchModelsMsg{
			ProviderID: m.providerID, BaseURL: m.value(catProviders, fieldBaseURL),
			APIKey:     m.value(catProviders, fieldAPIKey),
			StandardID: m.value(catProviders, fieldAPIStandard),
		}
	}
}

func (m *Model) fetchModelsForProviderCmd(providerID string) tea.Cmd {
	cfg := m.providers[providerID]
	baseURL := cfg.ProviderURL
	if baseURL == "" {
		if preset := provider.GetPreset(providerID); preset != nil {
			baseURL = preset.BaseURL
		}
	}
	return func() tea.Msg {
		return FetchModelsMsg{
			ProviderID: providerID, BaseURL: baseURL, APIKey: cfg.APIKey,
			StandardID: providerStandard(providerID, cfg),
		}
	}
}

func (m *Model) setModels(providerID string, models []types.ModelEntry) {
	m.models[providerID] = models
	for index := range m.categories {
		if index == catProviders && m.providerID != providerID {
			continue
		}
		if m.isAgentCategory(index) && m.value(index, fieldProvider) != providerID {
			continue
		}
		if field := m.categories[index].Form.Field(fieldModel); field != nil {
			field.Options = ui.ModelOptions(models)
			field.Errors, field.Loading, field.LoadMessage = nil, false, ""
		}
	}
}

func (m *Model) setModelsError(providerID, message string) {
	for index := range m.categories {
		if (index == catProviders && m.providerID == providerID) || (m.isAgentCategory(index) && m.value(index, fieldProvider) == providerID) {
			if field := m.categories[index].Form.Field(fieldModel); field != nil {
				field.Errors, field.Loading, field.LoadMessage = []string{message}, false, ""
			}
		}
	}
}

func (m *Model) searchText() string {
	if m == nil || m.searchEd == nil {
		return ""
	}
	return m.searchEd.Value()
}

func (m *Model) SetSearchQuery(query string) {
	if m.searchEd == nil {
		m.searchEd = ui.NewTextEdit()
	}
	m.searchEd.SetValue(query)
	m.ensureVisibleSelection()
}

func (m *Model) SetSearchActive(active bool) { m.searchActive = active }
func (m *Model) SelectedCategory() int       { return m.selected }
func (m *Model) Pane() int                   { return m.pane }
func (m *Model) Scope() types.SettingsScope {
	if m == nil || m.scope == "" {
		return types.SettingsScopeApplication
	}
	return m.scope
}
func (m *Model) SetScope(scope types.SettingsScope) { m.scope = scope }
func (m *Model) Overrides() types.WorkspaceOverrides {
	if m.Scope() != types.SettingsScopeWorkspace {
		return types.WorkspaceOverrides{}
	}
	return types.WorkspaceOverridesFromDiff(m.appDefaults, m.AppSettings())
}

func (m *Model) SelectCategory(index int) {
	if index >= 0 && index < len(m.categories) {
		m.selected, m.pane = index, 0
	}
}

// MoveCategory shifts the selected category by delta within the currently
// visible (search-filtered) list. Returns false when nothing changed.
func (m *Model) MoveCategory(delta int) bool {
	visible := m.visibleCategoryIndices()
	if len(visible) == 0 {
		return false
	}
	pos := 0
	for i, index := range visible {
		if index == m.selected {
			pos = i
			break
		}
	}
	next := pos + delta
	if next < 0 {
		next = 0
	}
	if next >= len(visible) {
		next = len(visible) - 1
	}
	if visible[next] == m.selected {
		return false
	}
	m.SelectCategory(visible[next])
	return true
}

func (m *Model) EnterForm() {
	m.searchActive, m.pane = false, 1
}

func (m *Model) CategoryItems() []ui.ListItem {
	visible := m.visibleCategoryIndices()
	items := make([]ui.ListItem, 0, len(visible)*2)
	lastGroup := ""
	for _, index := range visible {
		category := m.categories[index]
		if category.Group != lastGroup {
			if lastGroup != "" {
				items = append(items, ui.ListItem{ID: "settings/gap/" + strings.ToLower(category.Group), Spacer: true})
			}
			items = append(items, ui.ListItem{ID: "settings/group/" + strings.ToLower(category.Group), Label: category.Group, Header: true})
			lastGroup = category.Group
		}
		items = append(items, ui.ListItem{ID: fmt.Sprintf("settings/category/%d", index), Label: category.Name, Payload: index})
	}
	return items
}

func (m *Model) visibleCategoryIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.searchText()))
	indices := make([]int, 0, len(m.categories))
	for index, category := range m.categories {
		if query == "" || strings.Contains(strings.ToLower(category.Name), query) || strings.Contains(strings.ToLower(category.Group), query) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (m *Model) ensureVisibleSelection() {
	visible := m.visibleCategoryIndices()
	if len(visible) == 0 {
		return
	}
	for _, index := range visible {
		if index == m.selected {
			return
		}
	}
	m.selected = visible[0]
}

const (
	catProviders = iota
	catMain
	catResearcher
	catCrafter
	catAeron
	catMemory
	catAppearance
	catEditor
	catLSP
	catUI
	catFont
)

func (m *Model) currentOverrideKey() string {
	switch m.selected {
	case catAppearance:
		return "appearance"
	case catEditor:
		return "editor"
	case catLSP:
		return "lsp"
	case catUI:
		return "interface"
	case catFont:
		return "font"
	case catProviders:
		return "providers"
	case catMemory:
		return "memory"
	default:
		return "agents"
	}
}

// AppSettings returns the values held by the shared form models.
func (m *Model) AppSettings() types.AppSettings {
	var settings types.AppSettings
	m.collectProviders(&settings)
	m.collectAgents(&settings.Agents)
	m.collectMemory(&settings.Memory)
	m.collectAppearance(&settings.Theme)
	m.collectEditor(&settings.Editor)
	m.collectLSP(&settings.LSP)
	m.collectUI(&settings.UI)
	m.collectFont(&settings.Font)
	return settings
}

func (m *Model) collectMemory(cfg *types.MemorySettings) {
	*cfg = types.MemorySettings{
		AutoRecall:         onOff(m.value(catMemory, "auto-recall") == "on"),
		AutoExtract:        onOff(m.value(catMemory, "auto-extract") == "on"),
		Consolidation:      onOff(m.value(catMemory, "consolidation") == "on"),
		RecallBudgetTokens: parseOpt(m.value(catMemory, "recall-budget"), types.DefaultMemoryRecallBudget),
		RecallLimit:        parseOpt(m.value(catMemory, "recall-limit"), types.DefaultMemoryRecallLimit),
		ExtractEveryTurns:  parseOpt(m.value(catMemory, "extract-every"), types.DefaultMemoryExtractCadence),
		MemoryCategory: types.CategorySettings{
			Provider:       m.value(catMemory, fieldProvider),
			Model:          m.value(catMemory, fieldModel),
			ThinkingEffort: m.value(catMemory, fieldThinkingEffort),
		},
	}
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func (m *Model) collectProviders(settings *types.AppSettings) {
	m.storeProviderForm(m.providerID)
	settings.ActiveProvider = m.providerID
	settings.Providers = make(map[string]types.ProviderConfig, len(m.providers))
	for id, cfg := range m.providers {
		cfg.ModelStandards = cloneStandardMap(cfg.ModelStandards)
		settings.Providers[id] = cfg
	}
}

func (m *Model) storeProviderForm(providerID string) {
	if providerID == "" || len(m.categories) == 0 {
		return
	}
	cfg := m.providers[providerID]
	cfg.APIKey = m.value(catProviders, fieldAPIKey)
	cfg.ProviderURL = m.value(catProviders, fieldBaseURL)
	cfg.APIStandard = m.value(catProviders, fieldAPIStandard)
	cfg.Dialer = dialerFromForm(
		m.value(catProviders, fieldDialTimeout),
		m.value(catProviders, fieldDialConnect),
		m.value(catProviders, fieldDialAttempts),
		m.value(catProviders, fieldDialBackoff),
	)
	cfg.Protocol = m.protocolSettingsFromForm(catProviders)
	cfg.ExtraBody = extraBodyFromForm(m.value(catProviders, fieldExtraBody))
	m.providers[providerID] = cfg
}

// protocolSettingsFromForm folds the protocol option rows back into persisted
// settings. A form left entirely on defaults stores nil — protocol defaults,
// not a frozen copy of them. Fields the protocol does not expose never render,
// so their form lookups read empty and stay unset.
func (m *Model) protocolSettingsFromForm(category int) *provider.ProtocolSettings {
	settings := provider.ProtocolSettings{
		Store:              m.triStateAt(category, fieldProtocolStore),
		ZeroDataRetention:  m.toggleAt(category, fieldProtocolZDR),
		ReasoningSummaries: m.triStateAt(category, fieldProtocolSummaries),
		Truncation:         m.truncationAt(category, fieldProtocolTruncate),
		ServiceTier:        m.value(category, fieldProtocolTier),
	}
	if settings == (provider.ProtocolSettings{}) {
		return nil
	}
	return &settings
}

// triStateAt reads an optional-boolean row; nil means the protocol default.
func (m *Model) triStateAt(category int, fieldID string) *bool {
	return ui.TriStateFieldValue(m.fieldAt(category, fieldID))
}

// toggleAt reads a yes/no row as a plain bool.
func (m *Model) toggleAt(category int, fieldID string) bool {
	return ui.ToggleFieldValue(m.fieldAt(category, fieldID))
}

// truncationAt reads the context-overflow row; "truncate" persists true.
func (m *Model) truncationAt(category int, fieldID string) *bool {
	if m.value(category, fieldID) == "truncate" {
		on := true
		return &on
	}
	return nil
}

// fieldAt returns a form field, tolerating a missing form.
func (m *Model) fieldAt(category int, fieldID string) *ui.FieldState {
	if category < 0 || category >= len(m.categories) || m.categories[category].Form == nil {
		return nil
	}
	return m.categories[category].Form.Field(fieldID)
}

// extraBodyFromForm parses the extra-body JSON field; blank is nil and
// invalid JSON is nil (the field validator blocks saving invalid input).
func extraBodyFromForm(value string) map[string]any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil
	}
	return decoded
}

func (m *Model) collectAgents(agents *types.AgentsSettings) {
	if agents.Categories == nil {
		agents.Categories = make(map[string]types.CategorySettings)
	}
	for _, entry := range []struct {
		index int
		key   string
	}{{catMain, "default"}, {catResearcher, "researcher"}, {catCrafter, "crafter"}, {catAeron, "aeron"}} {
		category := types.CategorySettings{
			Provider:       m.value(entry.index, fieldProvider),
			Model:          m.value(entry.index, fieldModel),
			ThinkingEffort: m.value(entry.index, fieldThinkingEffort),
		}
		// Registry facts (pricing, tool support) mirror onto the category when
		// the model entry is known. User-editable capabilities come from the
		// form, where blank means inherit from the registry at use time.
		for _, model := range m.models[category.Provider] {
			if model.ID == category.Model {
				category.ModelInputPrice, category.ModelOutputPrice, category.ModelCachePrice = model.InputPrice, model.OutputPrice, model.CacheReadPrice
				category.ModelSupportsTools = model.SupportsTools
				break
			}
		}
		category.ModelContextWindow = parsePositiveInt(m.value(entry.index, fieldCtxWindow))
		category.ModelMaxOutput = parsePositiveInt(m.value(entry.index, fieldMaxOutput))
		for _, modality := range provider.KnownModalities() {
			if m.value(entry.index, fieldModalityPrefix+modality) == "yes" {
				category.ModelModalities = append(category.ModelModalities, modality)
			}
		}
		category.ModelTemperature = parseFloatPtr(m.value(entry.index, fieldTemperature))
		category.ModelTopP = parseFloatPtr(m.value(entry.index, fieldTopP))
		agents.Categories[entry.key] = category
	}
	// The main agent form owns the Multi-Agent toggle; mirror it into the
	// collected agents settings.
	if field := m.categories[catMain].Form.Field(fieldMultiAgent); field != nil {
		agents.Enabled = ui.BoolFieldValue(field)
	}
}

// parsePositiveInt parses an optional integer field; blank or invalid is 0.
func parsePositiveInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// parseFloatPtr parses an optional float field; blank or invalid is nil.
func parseFloatPtr(value string) *float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

// framePadFieldState renders one shell-frame padding input. Values are whole
// numbers of cells including zero; blank means "inherit the default" (the
// initial value shows the effective pad so the row reads as a plain number).
func framePadFieldState(id, label string, value int, description string) *ui.FieldState {
	f := ui.TextFieldState(id, label, strconv.Itoa(value), description)
	f.Validator = func(v ui.FieldValue) []string {
		trimmed := strings.TrimSpace(v.String())
		if trimmed == "" {
			return nil
		}
		if n, err := strconv.Atoi(trimmed); err != nil || n < 0 {
			return []string{"Must be a whole number of cells (0 or more)."}
		}
		return nil
	}
	return f
}

// liveFramePadField is a shell-frame padding row wired for live preview:
// every keystroke emits form.changed so the shell re-insets immediately.
func liveFramePadField(id, label string, value int, description string) *ui.FieldState {
	f := framePadFieldState(id, label, value, description)
	f.Live = true
	return f
}

// framePadField parses one shell-frame padding row. A blank or invalid field
// returns nil so the leaf stays unset and inherits the theme default.
func framePadField(value string) *int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// framePreview reports whether a changed field is one of the live shell-frame
// padding rows and returns the complete frame for the preview message. The
// message is SAVE-FAITHFUL: it mirrors exactly what collectAppearance would
// persist, so what the user previews is what a save produces. The settings
// model resolves a blank field the same way collectAppearance does (a zero
// pointer), which applyFramePreview maps to the configured default.
func (m *Model) framePreview(fieldID string) (FramePreviewMsg, bool) {
	if m == nil || m.SelectedCategory() != catAppearance {
		return FramePreviewMsg{}, false
	}
	switch fieldID {
	case "frame-top", "frame-bottom", "frame-left", "frame-right":
		return FramePreviewMsg{
			Top:    framePadField(m.value(catAppearance, "frame-top")),
			Bottom: framePadField(m.value(catAppearance, "frame-bottom")),
			Left:   framePadField(m.value(catAppearance, "frame-left")),
			Right:  framePadField(m.value(catAppearance, "frame-right")),
		}, true
	}
	return FramePreviewMsg{}, false
}

func (m *Model) collectAppearance(cfg *types.ThemeSettings) {
	// Colour rows are read-only preset reflections; the preset name and the
	// shell frame paddings persist.
	*cfg = types.ThemeSettings{
		Preset:    m.value(catAppearance, "preset"),
		PadTop:    framePadField(m.value(catAppearance, "frame-top")),
		PadBottom: framePadField(m.value(catAppearance, "frame-bottom")),
		PadLeft:   framePadField(m.value(catAppearance, "frame-left")),
		PadRight:  framePadField(m.value(catAppearance, "frame-right")),
	}
}

func (m *Model) collectEditor(cfg *types.EditorSettings) {
	*cfg = types.EditorSettings{TabSize: parseOpt(m.value(catEditor, "tab-size"), 4), WordWrap: m.value(catEditor, "word-wrap") == "on", LineNumbers: m.value(catEditor, "line-numbers") == "on", SyntaxTheme: m.value(catEditor, "syntax-theme"), RenderWhitespace: m.value(catEditor, "render-whitespace"), AutoSaveDelay: parseOpt(m.value(catEditor, "auto-save-delay"), 0), FormatOnSave: m.value(catEditor, "format-on-save") == "on"}
}

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	lsp := types.NormalizeLSP(types.LSPSettings{
		Enabled:          m.value(catLSP, "enabled") == "on",
		AutoDetect:       m.value(catLSP, "auto-detect") == "on",
		StartOnOpen:      m.value(catLSP, "start-on-open") == "on",
		Diagnostics:      m.value(catLSP, "diagnostics") == "on",
		Completions:      m.value(catLSP, "completions") == "on",
		Hover:            m.value(catLSP, "hover") == "on",
		Definition:       m.value(catLSP, "goto-definition") == "on",
		References:       m.value(catLSP, "find-references") == "on",
		Outline:          m.value(catLSP, "outline") == "on",
		Rename:           m.value(catLSP, "rename") == "on",
		DiagInEditor:     m.value(catLSP, "diag-editor") == "on",
		DiagInTree:       m.value(catLSP, "diag-tree") == "on",
		DiagInStatus:     m.value(catLSP, "diag-status") == "on",
		AutoComplete:     m.value(catLSP, "auto-complete") == "on",
		MinSeverity:      m.value(catLSP, "min-severity"),
		UpdateDebounceMS: parseOpt(m.value(catLSP, "debounce"), 300),
		Languages:        make(map[string]types.LSPLanguageSettings),
	})
	for _, lang := range types.DefaultLSPLanguages() {
		enabled := m.value(catLSP, "lang-"+lang.ID+"-enabled") == "on"
		cmd := m.value(catLSP, "lang-"+lang.ID+"-cmd")
		args := splitArgs(m.value(catLSP, "lang-"+lang.ID+"-args"))
		if cmd == "" {
			cmd = lang.DefaultCmd
		}
		if len(args) == 0 {
			args = append([]string(nil), lang.DefaultArgs...)
		}
		lsp.Languages[lang.ID] = types.LSPLanguageSettings{Enabled: enabled, Command: cmd, Args: args}
	}
	*cfg = lsp
}

func splitArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func (m *Model) buildLSPForm(lsp types.LSPSettings) *ui.FormModel {
	lsp = types.NormalizeLSP(lsp)
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("language-intelligence", "Language intelligence"),
		ui.BoolFieldState("enabled", "Enabled", lsp.Enabled, "Master switch for language servers in this scope."),
		ui.BoolFieldState("auto-detect", "Auto-detect", lsp.AutoDetect, "Start a server from files present in the project."),
		ui.BoolFieldState("start-on-open", "Start on open", lsp.StartOnOpen, "Launch detected servers when the workspace opens."),
		ui.TextFieldState("debounce", "Update delay", fmt.Sprintf("%d", lsp.UpdateDebounceMS), "Milliseconds to wait after an edit before notifying the server."),
		ui.SeparatorFieldState("features", "Features"),
		ui.BoolFieldState("diagnostics", "Diagnostics", lsp.Diagnostics, "Publish errors, warnings, and hints from the server."),
		ui.BoolFieldState("completions", "Completions", lsp.Completions, "Offer completion items at the cursor."),
		ui.BoolFieldState("auto-complete", "Auto-complete", lsp.AutoComplete, "Trigger completions while typing."),
		ui.BoolFieldState("hover", "Hover", lsp.Hover, "Show symbol documentation under the cursor."),
		ui.BoolFieldState("goto-definition", "Go to definition", lsp.Definition, "Jump to the symbol definition."),
		ui.BoolFieldState("find-references", "Find references", lsp.References, "List every reference to the symbol."),
		ui.BoolFieldState("outline", "Document outline", lsp.Outline, "Build the file symbol outline."),
		ui.BoolFieldState("rename", "Rename", lsp.Rename, "Rename the symbol across the project."),
		ui.SeparatorFieldState("diagnostic-display", "Diagnostic display"),
		ui.BoolFieldState("diag-editor", "In editor", lsp.DiagInEditor, "Underline issues in the open buffer."),
		ui.BoolFieldState("diag-tree", "In file tree", lsp.DiagInTree, "Show issue counts on files in the sidebar."),
		ui.BoolFieldState("diag-status", "In status bar", lsp.DiagInStatus, "Show project-wide error and warning totals."),
		ui.CycleFieldState("min-severity", "Minimum severity", []string{"error", "warning", "info", "hint"}, lsp.MinSeverity, "Hide diagnostics weaker than this level."),
		ui.SeparatorFieldState("servers", "Language servers"),
	}
	for _, lang := range types.DefaultLSPLanguages() {
		cfg := lsp.Languages[lang.ID]
		if cfg.Command == "" {
			cfg.Command = lang.DefaultCmd
			cfg.Args = append([]string(nil), lang.DefaultArgs...)
			cfg.Enabled = true
		}
		fields = append(fields,
			ui.BoolFieldState("lang-"+lang.ID+"-enabled", lang.Name, cfg.Enabled, "Start this server when matching files are present."),
			ui.TextFieldState("lang-"+lang.ID+"-cmd", lang.ID+" command", cfg.Command, "Executable used for "+lang.Name+"."),
			ui.TextFieldState("lang-"+lang.ID+"-args", lang.ID+" args", strings.Join(cfg.Args, " "), "Command arguments, space-separated."),
		)
	}
	return formModel(fields...)
}

func (m *Model) collectUI(cfg *types.UISettings) {
	*cfg = types.UISettings{SidebarWidth: parseOpt(m.value(catUI, "sidebar-width"), 30), ShowHiddenFiles: m.value(catUI, "show-hidden") == "on", ShowIcons: m.value(catUI, "show-icons") == "on", UseNerdFont: m.value(catUI, "nerd-font") == "on", ShowGitStatus: m.value(catUI, "git-status") == "on", ShowDiagnostics: m.value(catUI, "diagnostics") == "on"}
}

func (m *Model) collectFont(cfg *types.FontSettings) {
	*cfg = types.FontSettings{Family: m.value(catFont, "family"), Size: parseOpt(m.value(catFont, "size"), 12), Ligatures: m.value(catFont, "ligatures") == "on"}
}

// Snapshot records values without retaining component interaction state.
func (m *Model) Snapshot() {
	snapshot := &modelSnapshot{
		categories: make([]categorySnapshot, len(m.categories)),
		selected:   m.selected, pane: m.pane,
		providerID: m.providerID, providerModel: m.providerModel,
	}
	for index, category := range m.categories {
		values := make(map[string]ui.FieldValue, category.Form.FieldCount())
		for _, field := range category.Form.Fields() {
			values[field.ID] = field.Value
		}
		snapshot.categories[index] = categorySnapshot{values: values}
	}
	m.snapshot = snapshot
}

func (m *Model) Restore() {
	if m.snapshot == nil {
		return
	}
	for index, snapshot := range m.snapshot.categories {
		if index >= len(m.categories) {
			break
		}
		for fieldID, value := range snapshot.values {
			field := m.categories[index].Form.Field(fieldID)
			if field != nil {
				field.Value, field.Dirty = value, false
				field.Validate()
			}
		}
	}
	m.selected, m.pane = m.snapshot.selected, m.snapshot.pane
	m.providerID, m.providerModel = m.snapshot.providerID, m.snapshot.providerModel
}
