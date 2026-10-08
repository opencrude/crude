package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

func (m *Model) collectLSP(cfg *types.LSPSettings) {
	advanced, _ := parseLSPAdvanced(m.value(catLSP, "advanced"))
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
		Languages:        advanced.Languages,
		Servers:          advanced.Servers, ServerArgs: advanced.ServerArgs,
		MaxServers:         parseOpt(m.value(catLSP, "max-servers"), 2),
		IdleTimeoutSeconds: parseOpt(m.value(catLSP, "idle-timeout"), 300),
		MaxDocumentBytes:   parseOpt(m.value(catLSP, "max-document"), 2*1024*1024),
		ServerThreads:      parseOpt(m.value(catLSP, "server-threads"), 2),
		ServerMemoryMB:     parseOpt(m.value(catLSP, "server-memory"), 1024),
	})
	for _, lang := range types.DefaultLSPLanguages() {
		enabled := m.value(catLSP, "lang-"+lang.ID+"-enabled") == "on"
		cmd := m.value(catLSP, "lang-"+lang.ID+"-cmd")
		var args []string
		_ = json.Unmarshal([]byte(m.value(catLSP, "lang-"+lang.ID+"-args")), &args)
		if cmd == "" {
			cmd = lang.DefaultCmd
		}
		cfg := lsp.Languages[lang.ID]
		cfg.Enabled, cfg.Command, cfg.Args = enabled, cmd, args
		lsp.Languages[lang.ID] = cfg
	}
	*cfg = lsp
}

func (m *Model) buildLSPForm(lsp types.LSPSettings) *ui.FormModel {
	lsp = types.NormalizeLSP(lsp)
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("language-intelligence", "Language intelligence"),
		ui.BoolFieldState("enabled", "Enabled", lsp.Enabled, "Master switch for language servers in this scope."),
		ui.BoolFieldState("auto-detect", "Auto-detect", lsp.AutoDetect, "Select built-in servers for matching files. Explicit external definitions work when off."),
		ui.BoolFieldState("start-on-open", "Start on open", lsp.StartOnOpen, "Launch only when an editor document opens; never scan/open all project files."),
		lspNumberField("debounce", "Update delay (ms)", lsp.UpdateDebounceMS, 60000, "Milliseconds after an edit; 0 inherits 300."),
		ui.SeparatorFieldState("resources", "Resource guidance"),
		lspNumberField("max-servers", "Maximum servers", lsp.MaxServers, 32, "Concurrent process ceiling; 0 inherits 2."),
		lspNumberField("idle-timeout", "Idle timeout (seconds)", lsp.IdleTimeoutSeconds, 86400, "Retire idle servers; 0 inherits 300."),
		lspNumberField("max-document", "Document limit (bytes)", lsp.MaxDocumentBytes, 64*1024*1024, "Skip oversized documents; 0 inherits 2097152."),
		lspNumberField("server-threads", "Threads per server", lsp.ServerThreads, 64, "Soft guidance to supported servers; 0 inherits 2. Explicit overrides win."),
		lspNumberField("server-memory", "Memory guidance (MiB)", lsp.ServerMemoryMB, 65536, "Soft Go heap / TS old-space guidance, not total RSS. 0 inherits 1024."),
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
			if cfg.Args == nil {
				cfg.Args = append([]string(nil), lang.DefaultArgs...)
			}
		}
		fields = append(fields,
			ui.BoolFieldState("lang-"+lang.ID+"-enabled", lang.Name, cfg.Enabled, "Allow this server for matching open editor documents."),
			ui.TextFieldState("lang-"+lang.ID+"-cmd", lang.ID+" command", cfg.Command, "Executable used for "+lang.Name+"."),
			lspArgsField("lang-"+lang.ID+"-args", lang.ID+" args", cfg.Args),
		)
	}
	advanced := ui.TextAreaFieldState("advanced", "Custom servers / advanced JSON", lspJSON(lspAdvanced{Languages: lsp.Languages, Servers: lsp.Servers, ServerArgs: lsp.ServerArgs}),
		`Object with languages, servers and server_args. Built-in enabled/command/args use controls above; advanced fields and custom entries use this editor. Example: {"languages":{"ruby":{"enabled":true,"command":"ruby-lsp","extensions":[".rb"],"language_id":"ruby","root_markers":["Gemfile"],"environment":{},"settings":{},"initialization_options":{}}}}`)
	advanced.Validator = func(value ui.FieldValue) []string {
		_, err := parseLSPAdvanced(value.String())
		if err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	fields = append(fields, ui.SeparatorFieldState("advanced-servers", "External server configuration"), advanced)
	return formModel(fields...)
}

type lspAdvanced struct {
	Languages  map[string]types.LSPLanguageSettings `json:"languages,omitempty"`
	Servers    map[string]string                    `json:"servers,omitempty"`
	ServerArgs map[string][]string                  `json:"server_args,omitempty"`
}

func lspJSON(value any) string {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	if string(bytes) == "null" {
		return "[]"
	}
	return string(bytes)
}

func parseLSPAdvanced(text string) (lspAdvanced, error) {
	var value lspAdvanced
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return value, fmt.Errorf("enter a JSON object with languages, servers and server_args")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, fmt.Errorf("enter exactly one JSON object")
	}
	cfg := types.LSPSettings{Languages: value.Languages, Servers: value.Servers, ServerArgs: value.ServerArgs}
	return value, cfg.Validate()
}

func lspNumberField(id, label string, value, maximum int, description string) *ui.FieldState {
	field := ui.TextFieldState(id, label, strconv.Itoa(value), description)
	field.Validator = intRangeValidator(0, maximum)
	return field
}

func lspArgsField(id, label string, args []string) *ui.FieldState {
	field := ui.TextFieldState(id, label, lspJSON(args), `JSON string array, e.g. ["--stdio", "a path with spaces"]. [] means no arguments.`)
	field.Validator = func(value ui.FieldValue) []string {
		var args []string
		if !strings.HasPrefix(strings.TrimSpace(value.String()), "[") {
			return []string{"Enter a JSON array of strings"}
		}
		if err := json.Unmarshal([]byte(value.String()), &args); err != nil {
			return []string{err.Error()}
		}
		return nil
	}
	return field
}
