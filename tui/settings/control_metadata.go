package settings

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// These are persisted JSON leaves from the collectors, not accessible labels.
var controlFieldPaths = map[int]map[string]string{
	catLSP:        {"enabled": "lsp.enabled", "auto-detect": "lsp.auto_detect", "start-on-open": "lsp.start_on_open", "debounce": "lsp.update_debounce_ms", "diagnostics": "lsp.diagnostics", "completions": "lsp.completions", "auto-complete": "lsp.auto_complete", "hover": "lsp.hover", "goto-definition": "lsp.goto_definition", "find-references": "lsp.find_references", "outline": "lsp.document_outline", "rename": "lsp.rename", "min-severity": "lsp.min_severity", "diag-editor": "lsp.diag_in_editor", "diag-tree": "lsp.diag_in_tree", "diag-status": "lsp.diag_in_status", "max-servers": "lsp.max_servers", "idle-timeout": "lsp.idle_timeout_seconds", "max-document": "lsp.max_document_bytes", "server-threads": "lsp.server_threads", "server-memory": "lsp.server_memory_mb"},
	catEditor:     {"tab-size": "editor.tab_size", "word-wrap": "editor.word_wrap", "line-numbers": "editor.line_numbers", "syntax-theme": "editor.syntax_theme", "render-whitespace": "editor.render_whitespace", "auto-save": "editor.auto_save", "auto-save-delay": "editor.auto_save_delay", "format-on-save": "editor.format_on_save"},
	catAppearance: {"preset": "theme.preset", "frame-top": "theme.pad_top", "frame-bottom": "theme.pad_bottom", "frame-left": "theme.pad_left", "frame-right": "theme.pad_right"},
	catUI:         {"context-help": "ui.context_help", "sidebar-width": "ui.sidebar_width", "show-hidden": "ui.show_hidden_files", "show-icons": "ui.show_icons", "nerd-font": "ui.use_nerd_font", "git-status": "ui.show_git_status", "diagnostics": "ui.show_diagnostics"},
	catMemory:     {"auto-recall": "memory.auto_recall", "recall-budget": "memory.recall_budget_tokens", "recall-limit": "memory.recall_limit", "auto-extract": "memory.auto_extract", "extract-every": "memory.extract_every_turns", "consolidation": "memory.consolidation"},
	catHistory:    {fieldHistoryEnabled: "local_history.enabled", fieldHistoryGitIgnore: "local_history.use_gitignore", fieldHistoryExternal: "local_history.capture_external_changes", fieldHistorySafety: "local_history.safety_snapshot_before_restore", fieldHistoryMaxFile: "local_history.max_file_size_mb", fieldHistoryDebounce: "local_history.debounce_ms", fieldHistoryReconcile: "local_history.reconcile_interval_seconds", fieldHistoryKeepHours: "local_history.retention_keep_hours", fieldHistoryKeepDaily: "local_history.retention_keep_daily", fieldHistoryMaxSnaps: "local_history.retention_max_snapshots", fieldHistoryBudget: "local_history.storage_budget_mb", fieldHistoryIgnores: "local_history.extra_ignore_patterns"},
	catTerminal:   {fieldTerminalShell: "terminal.shell", fieldTerminalCommand: "terminal.command", fieldTerminalArgs: "terminal.args", fieldTerminalLogin: "terminal.login_shell", fieldTerminalEnv: "terminal.environment", fieldTerminalPlacement: "terminal.placement", fieldTerminalScrollback: "terminal.scrollback_lines"},
}

func agentControlPath(category int, id string) string {
	role := map[int]string{catMain: "default", catResearcher: "researcher", catCrafter: "crafter", catAeron: "aeron"}[category]
	if role == "" {
		return ""
	}
	leaf := map[string]string{fieldConnection: "account_id", fieldModel: "Model", fieldThinkingEffort: "ThinkingEffort", "agent-description": "Description", "agent-instructions": "Instructions", fieldCtxWindow: "ModelContextWindow", fieldMaxOutput: "ModelMaxOutput", fieldTemperature: "ModelTemperature", fieldTopP: "ModelTopP", fieldContextCompaction: "context_compaction_percent", "context-retention-tokens": "context_retention_tokens", fieldMultiAgent: "multi_agent"}[id]
	if strings.HasPrefix(id, fieldModalityPrefix) {
		leaf = "ModelModalities"
	}
	if leaf == "" {
		return ""
	}
	return "agents.categories." + role + "." + leaf
}

func (s *WorkspaceSettingsSurface) controlField(id ui.ComponentID) (*ui.FieldState, string) {
	if s.model == nil {
		return nil, ""
	}
	if s.agents != nil && s.agents.draft != nil {
		for _, field := range s.agents.draft.Fields() {
			if id != ui.NewID(AgentSettingsFormID, field.ID) {
				continue
			}
			if s.agents.roleIndex == catAutoReviewPolicy {
				return field, "safety.auto_permission"
			}
			path := agentControlPath(catMain, field.ID)
			leaf := strings.TrimPrefix(path, "agents.categories.default.")
			if path == "" {
				return field, "agents.unknown"
			}
			container := "categories"
			if s.agents.roleIndex < 0 {
				container = "agent_profiles"
			}
			return field, "agents." + container + "." + s.agents.id + "." + leaf
		}
	}
	if s.lsp != nil && s.lsp.draft != nil {
		for _, field := range s.lsp.draft.Fields() {
			if id == ui.NewID(LanguageServerFormID, field.ID) {
				leaf := map[string]string{lspFieldID: "id", lspFieldEnabled: "enabled", lspFieldCommand: "command", lspFieldArgs: "args", lspFieldExtensions: "extensions", lspFieldLanguageID: "language_id", lspFieldRootMarkers: "root_markers", lspFieldEnvironment: "environment", lspFieldSettings: "settings", lspFieldInitOptions: "initialization_options"}[field.ID]
				if leaf == "" {
					return field, ""
				}
				return field, "lsp.languages." + s.lsp.editing + "." + leaf
			}
		}
	}
	for _, field := range s.model.categories[s.model.selected].Form.Fields() {
		if id != ui.NewID(s.form.ID(), field.ID) {
			continue
		}
		path := controlFieldPaths[s.model.selected][field.ID]
		if path == "" {
			path = agentControlPath(s.model.selected, field.ID)
		}
		// Sensitive collectors are closed containers; unknown ordinary leaves are
		// deliberately unavailable until their exact collector mapping is added.
		if path == "" {
			switch s.model.selected {
			case catAccounts:
				path = "accounts"
			case catSafety, catAutoReviewPolicy:
				path = "safety"
			case catNetworkConnection, catNetworkProxy, catNetworkCertificates:
				path = "network"
			case catAcquisition:
				path = "capabilities"
			}
		}
		return field, path
	}
	return nil, ""
}

func (s *WorkspaceSettingsSurface) agentDraftAuthority() bool {
	c := s.agents
	if c == nil || c.draft == nil {
		return false
	}
	if c.roleIndex == catAutoReviewPolicy || c.id == "" {
		return true
	}
	// Tool mode is policy even when an empty selection happens to serialize alike.
	if f := c.draft.Field("agent-tool-mode"); f != nil && !f.Value.Equal(f.Initial) {
		return true
	}
	before := s.model.AppSettings()
	raw, _ := json.Marshal(before)
	var after types.AppSettings
	if json.Unmarshal(raw, &after) != nil {
		return true
	}
	cfg := c.prospectiveCategory()
	if c.roleIndex == catMemory {
		after.Memory.MemoryCategory = cfg
	} else if c.roleIndex >= 0 {
		after.Agents.Categories[c.id] = cfg
		if c.roleIndex == catMain {
			after.Agents.Enabled = ui.BoolFieldValue(c.draft.Field(fieldMultiAgent))
		}
	} else {
		if after.Agents.AgentProfiles == nil {
			return true
		}
		after.Agents.AgentProfiles[c.id] = c.prospectiveProfile(cfg)
	}
	return control.SettingsChangesAuthority(before, after, "")
}

// ControlMetadata resolves the actual draft owner and all changes Save carries.
func (s *WorkspaceSettingsSurface) ControlMetadata(id ui.ComponentID, action string) (control.Effect, bool, error) {
	if s.model == nil {
		return "", false, fmt.Errorf("settings owner unavailable")
	}
	if id == "settings/agents/search" || id == "settings/language-servers/search" {
		return control.Write, false, nil
	}
	if field, path := s.controlField(id); field != nil {
		if path == "" {
			return "", false, fmt.Errorf("settings field has no canonical path")
		}
		human := field.Type == ui.FieldPassword || control.SettingsAuthority(path)
		if human {
			return control.Authority, true, nil
		}
		return control.Write, false, nil
	}
	switch action {
	case "settings.save":
		var before, after any
		if err := json.Unmarshal(s.model.preferencesBase, &before); err != nil {
			return "", false, err
		}
		if err := json.Unmarshal(s.model.preferenceBytes(), &after); err != nil {
			return "", false, err
		}
		// Accounts are trusted source context, never part of preference Save.
		for _, value := range []any{before, after} {
			if object, ok := value.(map[string]any); ok {
				raw, _ := json.Marshal(s.model.accounts)
				var accounts any
				_ = json.Unmarshal(raw, &accounts)
				object["accounts"] = accounts
			}
		}
		human := control.SettingsChangesAuthority(before, after, "")
		if s.model.accountEditor != nil || s.model.capabilityDraft != nil || s.agentDraftAuthority() {
			human = true
		}
		if human {
			return control.Authority, true, nil
		}
		return control.Write, false, nil
	case "settings.close", "settings.category.open", "settings.search", "settings.scope", "settings.lsp.servers":
		return control.Write, false, nil
	case "agents.edit", "agents.back", "tabs.activate", "lsp.edit", "lsp.back":
		return control.Write, false, nil
	case "agents.apply":
		if s.agents == nil || s.agents.draft == nil {
			return "", false, fmt.Errorf("agent draft unavailable")
		}
		if s.agentDraftAuthority() {
			return control.Authority, true, nil
		}
		return control.Write, false, nil
	}
	return "", false, nil
}

func (s *WorkspaceSettingsSurface) ControlValueAllowed(id ui.ComponentID) bool {
	field, path := s.controlField(id)
	// Protocol objects can contain opaque server-specific credentials. Their
	// field type is not a reliable secret annotation, so never release the blob.
	if strings.HasPrefix(path, "lsp.languages.") && (strings.HasSuffix(path, ".settings") || strings.HasSuffix(path, ".initialization_options")) {
		return false
	}
	return field != nil && path != "" && field.Type != ui.FieldPassword && !control.SettingsAuthority(path)
}

// ControlActionOutcome reports whether a synchronous entity decision actually
// completed. A handled validation failure is useful for human focus routing,
// but is not an applied semantic operation.
func (s *WorkspaceSettingsSurface) ControlActionOutcome(action string) error {
	if action != "agents.apply" {
		return nil
	}
	if s.agents == nil || s.agents.draft == nil {
		return nil
	}
	for _, field := range s.agents.draft.Fields() {
		if field.HasErrors() {
			return fmt.Errorf("agent Apply failed: %s: %s", field.Label, strings.Join(field.Errors, "; "))
		}
	}
	return fmt.Errorf("agent Apply did not stage the draft")
}

// ControlEditGeneration includes off-page drafts; Save is not page-local.
func (s *WorkspaceSettingsSurface) ControlEditGeneration() uint64 {
	if s.model == nil {
		return 0
	}
	var generation uint64
	for _, category := range s.model.categories {
		if category.Form != nil {
			generation += category.Form.EditGeneration()
		}
	}
	if s.agents.draft != nil {
		generation += s.agents.draft.EditGeneration()
	}
	if s.model.capabilityDraft != nil {
		generation += s.model.capabilityDraft.EditGeneration()
	}
	if s.lsp != nil && s.lsp.draft != nil {
		generation += s.lsp.draft.EditGeneration()
	}
	return generation
}
