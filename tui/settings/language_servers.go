package settings

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

// Code Intelligence owns behavior shared by every server; Language Servers
// owns the per-language inventory. Keeping them apart replaces the old
// monolith (switches, per-language rows and a raw JSON editor on one page).

const mebibyte = 1024 * 1024

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	documentMB := parseOpt(m.value(catLSP, "max-document"), 2)
	lsp := types.NormalizeLSP(types.LSPSettings{
		Enabled:            m.value(catLSP, "enabled") == "on",
		AutoDetect:         m.value(catLSP, "auto-detect") == "on",
		StartOnOpen:        m.value(catLSP, "start-on-open") == "on",
		Diagnostics:        m.value(catLSP, "diagnostics") == "on",
		Completions:        m.value(catLSP, "completions") == "on",
		Hover:              m.value(catLSP, "hover") == "on",
		Definition:         m.value(catLSP, "goto-definition") == "on",
		References:         m.value(catLSP, "find-references") == "on",
		Outline:            m.value(catLSP, "outline") == "on",
		Rename:             m.value(catLSP, "rename") == "on",
		DiagInEditor:       m.value(catLSP, "diag-editor") == "on",
		DiagInTree:         m.value(catLSP, "diag-tree") == "on",
		DiagInStatus:       m.value(catLSP, "diag-status") == "on",
		AutoComplete:       m.value(catLSP, "auto-complete") == "on",
		MinSeverity:        m.value(catLSP, "min-severity"),
		UpdateDebounceMS:   parseOpt(m.value(catLSP, "debounce"), 300),
		Languages:          cloneLSPLanguages(m.lspLanguages),
		MaxServers:         parseOpt(m.value(catLSP, "max-servers"), 2),
		IdleTimeoutSeconds: parseOpt(m.value(catLSP, "idle-timeout"), 300),
		MaxDocumentBytes:   documentMB * mebibyte,
		ServerThreads:      parseOpt(m.value(catLSP, "server-threads"), 2),
		ServerMemoryMB:     parseOpt(m.value(catLSP, "server-memory"), 1024),
	})
	*cfg = lsp
}

func (m *Model) buildLSPForm(lsp types.LSPSettings) *ui.FormModel {
	lsp = types.NormalizeLSP(lsp)
	severity := ui.SegmentedFieldState("min-severity", "Show diagnostics from", []ui.FormOption{
		{ID: "error", Label: "Errors"}, {ID: "warning", Label: "Warnings"}, {ID: "info", Label: "Info"}, {ID: "hint", Label: "Hints"},
	}, lsp.MinSeverity, "Lowest level shown: Hints includes all; Errors includes errors only.")
	return formModel(
		ui.SeparatorFieldState("language-intelligence", "General"),
		ui.BoolFieldState("enabled", "Language intelligence", lsp.Enabled, "Master switch for language servers in this scope. Off stops every server."),
		ui.BoolFieldState("auto-detect", "Detect servers", lsp.AutoDetect, "Choose built-in servers by file type; explicit servers work even when off."),
		ui.BoolFieldState("start-on-open", "Start on open", lsp.StartOnOpen, "Start a language server when its first document opens, not at startup."),
		lspNumberField("debounce", "Update delay", "ms", lsp.UpdateDebounceMS, 60000, "Pause after typing before the server re-analyzes the document."),
		&ui.FieldState{ID: "servers-nav", Label: "Language servers", Type: ui.FieldNav, Target: "settings.lsp.servers", Columns: []string{lspServerSummary(lsp.Languages)}, Description: "Commands, arguments and environment for each language server."},
		ui.SeparatorFieldState("features", "Features"),
		ui.BoolFieldState("diagnostics", "Diagnostics", lsp.Diagnostics, "Publish errors, warnings and hints from the server."),
		ui.BoolFieldState("completions", "Completions", lsp.Completions, "Offer completion items at the cursor."),
		ui.BoolFieldState("auto-complete", "Complete while typing", lsp.AutoComplete, "Open completions automatically as you type instead of on request."),
		ui.BoolFieldState("hover", "Hover documentation", lsp.Hover, "Show symbol documentation under the cursor."),
		ui.BoolFieldState("goto-definition", "Go to definition", lsp.Definition, "Jump to where a symbol is defined."),
		ui.BoolFieldState("find-references", "Find references", lsp.References, "List every reference to the symbol."),
		ui.BoolFieldState("outline", "Document outline", lsp.Outline, "Build the symbol outline for the open file."),
		ui.BoolFieldState("rename", "Rename symbol", lsp.Rename, "Rename a symbol across the project."),
		ui.SeparatorFieldState("diagnostic-display", "Diagnostics display"),
		severity,
		ui.BoolFieldState("diag-editor", "In the editor", lsp.DiagInEditor, "Underline issues in the open buffer."),
		ui.BoolFieldState("diag-tree", "In the file tree", lsp.DiagInTree, "Show issue counts on files in the sidebar."),
		ui.BoolFieldState("diag-status", "In the status bar", lsp.DiagInStatus, "Show project-wide error and warning totals."),
		ui.SeparatorFieldState("resources", "Resources"),
		lspNumberField("max-servers", "Running servers", "max", lsp.MaxServers, 32, "How many language servers may run at once. Idle ones retire first."),
		lspNumberField("idle-timeout", "Retire idle after", "seconds", lsp.IdleTimeoutSeconds, 86400, "Stop a server that has had no open documents for this long."),
		lspNumberField("max-document", "Skip documents over", "MB", max(1, lsp.MaxDocumentBytes/mebibyte), 64, "Larger files open without language intelligence to keep servers responsive."),
		lspNumberField("server-threads", "Threads per server", "threads", lsp.ServerThreads, 64, "Parallelism hint for supported servers; explicit arguments take priority."),
		lspNumberField("server-memory", "Memory per server", "MiB", lsp.ServerMemoryMB, 65536, "Heap hint for Go and TypeScript, not a hard process memory limit."),
	)
}

// lspServerSummary states how many servers are enabled, e.g. "11 of 12 on".
func lspServerSummary(languages map[string]types.LSPLanguageSettings) string {
	total, enabled := 0, 0
	for _, cfg := range languages {
		total++
		if cfg.Enabled {
			enabled++
		}
	}
	return fmt.Sprintf("%d of %d on", enabled, total)
}

// syncLSPSummary keeps Code Intelligence's drill-in row current after the
// server inventory changes.
func (m *Model) syncLSPSummary() {
	if form := m.categories[catLSP].Form; form != nil {
		if field := form.Field("servers-nav"); field != nil {
			field.Columns = []string{lspServerSummary(m.lspLanguages)}
		}
	}
}

func lspNumberField(id, label, unit string, value, maximum int, description string) *ui.FieldState {
	field := ui.NumberFieldState(id, label, value, unit, description)
	field.Validator = intRangeValidator(0, maximum)
	return field
}

func cloneLSPLanguages(src map[string]types.LSPLanguageSettings) map[string]types.LSPLanguageSettings {
	return types.CloneLSPSettings(types.LSPSettings{Languages: src}).Languages
}

// lspServer is one row of the Language Servers inventory: a built-in
// catalog language or a custom definition.
type lspServer struct {
	id      string
	name    string
	builtin bool
	def     types.LSPLanguage
	cfg     types.LSPLanguageSettings
}

// lspServers lists built-ins in catalog order, then custom servers by name.
func (m *Model) lspServers() []lspServer {
	var servers []lspServer
	known := map[string]bool{}
	for _, def := range types.DefaultLSPLanguages() {
		known[def.ID] = true
		cfg := m.lspLanguages[def.ID]
		if cfg.Command == "" {
			cfg.Command = def.DefaultCmd
			if cfg.Args == nil {
				cfg.Args = slices.Clone(def.DefaultArgs)
			}
		}
		servers = append(servers, lspServer{id: def.ID, name: def.Name, builtin: true, def: def, cfg: cfg})
	}
	custom := slices.Sorted(maps.Keys(m.lspLanguages))
	for _, id := range custom {
		if !known[id] {
			servers = append(servers, lspServer{id: id, name: id, cfg: m.lspLanguages[id]})
		}
	}
	return servers
}

func (m *Model) lspServer(id string) (lspServer, bool) {
	for _, server := range m.lspServers() {
		if server.id == id {
			return server, true
		}
	}
	return lspServer{}, false
}

// lspLanguagesDirty reports unsaved server inventory edits.
func (m *Model) lspLanguagesDirty() bool {
	return !reflect.DeepEqual(types.NormalizeLSP(types.LSPSettings{Languages: m.lspLanguages}).Languages,
		types.NormalizeLSP(types.LSPSettings{Languages: m.lspLanguagesBase}).Languages)
}

// Server editor field IDs.
const (
	lspFieldID          = "lsp-id"
	lspFieldEnabled     = "lsp-enabled"
	lspFieldCommand     = "lsp-command"
	lspFieldArgs        = "lsp-args"
	lspFieldExtensions  = "lsp-extensions"
	lspFieldLanguageID  = "lsp-language-id"
	lspFieldRootMarkers = "lsp-root-markers"
	lspFieldEnvironment = "lsp-environment"
	lspFieldSettings    = "lsp-settings"
	lspFieldInitOptions = "lsp-init-options"
)

// buildLSPServerForm is the editor for one server. Lists are typed the way
// people write them (shell-style arguments, comma-separated extensions);
// JSON appears only for the two protocol objects that are JSON by nature.
func buildLSPServerForm(server lspServer) *ui.FormModel {
	cfg := server.cfg
	fields := []*ui.FieldState{ui.SeparatorFieldState("lsp-server", "Server")}
	if !server.builtin {
		id := ui.TextFieldState(lspFieldID, "Identifier", server.id, "A short lowercase name for this server, such as ruby or elixir.")
		id.Required = true
		fields = append(fields, id)
	}
	command := ui.TextFieldState(lspFieldCommand, "Command", cfg.Command, "Executable to launch, found on PATH or given as an absolute path.")
	if server.builtin {
		command.Placeholder = server.def.DefaultCmd
		command.Description = "Executable on PATH; default is " + server.def.DefaultCmd + "."
	} else {
		command.Required = true
	}
	args := ui.TextFieldState(lspFieldArgs, "Arguments", joinShellWords(cfg.Args), `Command-line arguments; quote spaces: --stdio "a path with spaces".`)
	args.Validator = func(value ui.FieldValue) []string {
		if _, err := splitShellWords(value.String()); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	fields = append(fields, ui.BoolFieldState(lspFieldEnabled, "Enabled", cfg.Enabled, "Allow this server for matching editor documents."), command, args)
	fields = append(fields, ui.SeparatorFieldState("lsp-files", "Files"))
	if server.builtin {
		handles := ui.TextFieldState(lspFieldExtensions, "Handles", strings.Join(server.def.Exts, ", "), "File types this built-in server owns. Add a custom server for other extensions.")
		handles.ReadOnly = true
		fields = append(fields, handles)
	} else {
		extensions := ui.TextFieldState(lspFieldExtensions, "File extensions", strings.Join(cfg.Extensions, ", "), "Comma-separated, lowercase, with the dot: .rb, .rake")
		extensions.Required = true
		languageID := ui.TextFieldState(lspFieldLanguageID, "Language ID", cfg.LanguageID, "The LSP languageId sent with each document, such as ruby.")
		languageID.Required = true
		fields = append(fields, extensions, languageID)
	}
	markers := cfg.RootMarkers
	if server.builtin && len(markers) == 0 {
		markers = server.def.RootMarkers
	}
	rootMarkers := ui.TextFieldState(lspFieldRootMarkers, "Project markers", strings.Join(markers, ", "), "Files that mark a project root, comma-separated; globs such as *.sln work.")
	fields = append(fields, rootMarkers,
		ui.SeparatorFieldState("lsp-environment-section", "Environment"),
		lspEnvironmentField(cfg.Environment),
		ui.SeparatorFieldState("lsp-advanced", "Advanced"),
	)
	for _, object := range []struct {
		id, label, description string
		value                  map[string]any
	}{
		{lspFieldSettings, "Settings", "workspace/configuration object, as JSON. Leave empty for server defaults.", cfg.Settings},
		{lspFieldInitOptions, "Initialization options", "initializationOptions object, as JSON. Leave empty for server defaults.", cfg.InitializationOptions},
	} {
		field := ui.TextAreaFieldState(object.id, object.label, formatJSONObject(object.value), object.description)
		field.Rows = 3
		field.Validator = func(value ui.FieldValue) []string {
			if _, err := parseJSONObject(value.String()); err != nil {
				return []string{err.Error()}
			}
			return nil
		}
		fields = append(fields, field)
	}
	return formModel(fields...)
}

// lspServerFromForm reads an editor form back into a server definition.
// Invalid fields keep their previous values; the form shows the errors.
func lspServerFromForm(server lspServer, form *ui.FormModel) (string, types.LSPLanguageSettings) {
	value := func(id string) string {
		if field := form.Field(id); field != nil {
			return strings.TrimSpace(field.Value.String())
		}
		return ""
	}
	cfg := server.cfg
	id := server.id
	if !server.builtin {
		id = value(lspFieldID)
		cfg.Extensions = splitList(value(lspFieldExtensions))
		cfg.LanguageID = value(lspFieldLanguageID)
	}
	cfg.Enabled = value(lspFieldEnabled) == "on"
	cfg.Command = value(lspFieldCommand)
	if cfg.Command == "" && server.builtin {
		cfg.Command = server.def.DefaultCmd
	}
	if args, err := splitShellWords(value(lspFieldArgs)); err == nil {
		cfg.Args = args
	}
	cfg.RootMarkers = splitList(value(lspFieldRootMarkers))
	if server.builtin && slices.Equal(cfg.RootMarkers, server.def.RootMarkers) {
		cfg.RootMarkers = nil
	}
	cfg.Environment = parseEnvironment(value(lspFieldEnvironment))
	if object, err := parseJSONObject(value(lspFieldSettings)); err == nil {
		cfg.Settings = object
	}
	if object, err := parseJSONObject(value(lspFieldInitOptions)); err == nil {
		cfg.InitializationOptions = object
	}
	return id, cfg
}

func lspEnvironmentField(env map[string]string) *ui.FieldState {
	field := ui.KeyValuesFieldState(lspFieldEnvironment, "Variables", formatEnvironment(env), "Extra environment variables for this server process.")
	field.Placeholder = "Add variable"
	field.Columns = []string{"Variable", "Value"}
	return field
}

func splitList(text string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func formatEnvironment(env map[string]string) string {
	keys := slices.Sorted(maps.Keys(env))
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+": "+env[key])
	}
	return strings.Join(lines, "\n")
}

func parseEnvironment(text string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if name = strings.TrimSpace(name); ok && name != "" {
			env[name] = strings.TrimSpace(value)
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func formatJSONObject(object map[string]any) string {
	if len(object) == 0 {
		return ""
	}
	bytes, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return ""
	}
	return string(bytes)
}

func parseJSONObject(text string) (map[string]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(text), &object); err != nil {
		return nil, fmt.Errorf("enter a JSON object, for example {\"key\": true}")
	}
	return object, nil
}

// splitShellWords splits a command line with POSIX-style single and double
// quotes and backslash escapes, without any expansion.
func splitShellWords(text string) ([]string, error) {
	var words []string
	var current strings.Builder
	inWord, quote, escaped := false, rune(0), false
	for _, r := range text {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("close the %c quote", quote)
	}
	if escaped {
		return nil, fmt.Errorf("finish the trailing backslash escape")
	}
	if inWord {
		words = append(words, current.String())
	}
	return words, nil
}

// joinShellWords renders arguments so splitShellWords reads them back exactly.
func joinShellWords(words []string) string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		if word != "" && !strings.ContainsAny(word, " \t'\"\\") {
			out = append(out, word)
			continue
		}
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(word)
		out = append(out, `"`+escaped+`"`)
	}
	return strings.Join(out, " ")
}
