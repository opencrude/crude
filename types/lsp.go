package types

import (
	"crude/types/jsondoc"
	"path/filepath"
	"strings"
)

// LSPServerSpec is the resolved launch contract shared by workspace and transport.
type LSPServerSpec struct {
	LanguageID            string
	Command               string
	Args                  []string
	RootMarkers           []string
	Environment           map[string]string
	Settings              jsondoc.Object
	ConfigurationScopes   []LSPConfigurationScope
	InitializationOptions jsondoc.Object
}

// LSPConfigurationScope is a complete effective object for a directory URI.
// The project-context owner resolves it; it is not a sparse preference overlay,
// an execution grant or evidence that a complete build context was established.
type LSPConfigurationScope struct {
	URI      string
	Settings jsondoc.Object
}

// ResolveServer returns detached launch settings. AutoDetect gates only built-in
// selection; explicit external definitions remain usable when it is off.
func (s LSPSettings) ResolveServer(path string) (LSPServerSpec, bool) {
	if !s.Enabled || s.Validate() != nil {
		return LSPServerSpec{}, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	id, found := s.languageForExtension(ext)
	cfg, configured := s.Languages[id]
	if configured && !cfg.Enabled {
		return LSPServerSpec{}, false
	}
	lang := languageByID(id)
	cmd, args, explicit := s.ServerOverride(ext)
	if !explicit {
		if !s.AutoDetect || !found || lang.DefaultCmd == "" {
			return LSPServerSpec{}, false
		}
		cmd, args = lang.DefaultCmd, append([]string(nil), lang.DefaultArgs...)
	}
	if cmd == "" {
		return LSPServerSpec{}, false
	}
	wireID := id
	switch ext {
	case ".c", ".h":
		wireID = "c"
	case ".js", ".mjs", ".cjs":
		wireID = "javascript"
	case ".jsx":
		wireID = "javascriptreact"
	case ".tsx":
		wireID = "typescriptreact"
	case ".scss":
		wireID = "scss"
	case ".less":
		wireID = "less"
	case ".cs":
		wireID = "csharp"
	}
	if cfg.LanguageID != "" {
		wireID = cfg.LanguageID
	}
	if wireID == "" {
		wireID = strings.TrimPrefix(ext, ".")
	}
	markers := lang.RootMarkers
	if cfg.RootMarkers != nil {
		markers = cfg.RootMarkers
	}
	spec := LSPServerSpec{LanguageID: wireID, Command: cmd, Args: args,
		RootMarkers: append([]string(nil), markers...), Environment: cloneStringMap(cfg.Environment),
		Settings: cloneJSONObject(cfg.Settings), InitializationOptions: cloneJSONObject(cfg.InitializationOptions)}
	applyServerHints(&spec, s)
	return spec, true
}

func CloneLSPSettings(s LSPSettings) LSPSettings {
	s.Languages = cloneLanguages(s.Languages)
	return s
}

type LSPStatus int

const (
	LSPIdle LSPStatus = iota
	LSPStarting
	LSPConnected
	LSPFailed
)

const (
	DiagError   = 1
	DiagWarning = 2
	DiagInfo    = 3
	DiagHint    = 4
)

type LSPDiagnostic struct {
	Line     int
	Col      int
	Message  string
	Severity int
	EndLine  int
	EndCol   int
	// Source names the producer (compiler, linter, "go vet"); Code is its
	// stable rule identifier when the server reports one.
	Source string
	Code   string
}

// LSPProbeView describes the text an agent code-intelligence request was
// answered for.
type LSPProbeView struct {
	// BufferDiffers is true when the file is open in the editor with text
	// that differs from disk; answers then describe the editor buffer.
	BufferDiffers bool
	// Server names the language server command that answered.
	Server     string
	Source     InsightSource
	SourceText string
}

type LSPSymbol struct {
	Name     string
	Detail   string
	Kind     int
	KindName string
	Line     int
	Col      int
	EndLine  int
	EndCol   int
	Children []LSPSymbol
}

// LSPTextEdit positions use UTF-16 code units, as negotiated on the wire.
type LSPTextEdit struct {
	Line, Col, EndLine, EndCol int
	NewText                    string
}

type CompletionItem struct {
	TextEdit            *LSPTextEdit
	AdditionalTextEdits []LSPTextEdit
	Snippet             *CompletionSnippet
	Label               string
	Detail              string
	Documentation       string
	Kind                int
	InsertText          string
	// HasInsertText distinguishes an explicitly empty insertion from an absent
	// field. Only absence permits using the label as a replacement.
	HasInsertText bool
}

// LanguageServiceState is the user-facing state of language services for the
// active document of a workspace. Only the LSP manager derives it.
type LanguageServiceState string

const (
	// LanguageRestricted: the folder is not trusted, so no server runs.
	LanguageRestricted LanguageServiceState = "restricted"
	// LanguageOff: turned off in settings, paused or stopped by the user.
	LanguageOff LanguageServiceState = "off"
	// LanguageNoServer: no enabled server matches the active file.
	LanguageNoServer LanguageServiceState = "no_server"
	// LanguageIdle: nothing to check yet (no source file open, automatic
	// start off). A quiet state.
	LanguageIdle LanguageServiceState = "idle"
	// LanguageStarting: a server is starting or catching up with edits.
	LanguageStarting LanguageServiceState = "starting"
	// LanguageReady: a server runs for the active file. A quiet state.
	LanguageReady LanguageServiceState = "ready"
	// LanguageFailed: a server could not start or stopped with an error.
	LanguageFailed LanguageServiceState = "failed"
)

// LanguageServiceAction names the next step a surface offers. Surfaces route
// it to one existing command; they never invent a second path.
type LanguageServiceAction string

const (
	LanguageActionNone          LanguageServiceAction = ""
	LanguageActionTrustFolder   LanguageServiceAction = "trust_folder"   // opens the trust dialog
	LanguageActionOpenSettings  LanguageServiceAction = "open_settings"  // Language servers settings
	LanguageActionStartServers  LanguageServiceAction = "start_servers"  // Start Language Servers
	LanguageActionRestartServer LanguageServiceAction = "restart_server" // retry the failed server
)

// LanguageServiceStatus is the single user-facing language status. Every
// surface (editor header, footer pill, Problems panel, Status Overview)
// renders it and composes no wording of its own. Text follows
// docs/reference/user-language.md.
type LanguageServiceStatus struct {
	State LanguageServiceState
	// Label is one short phrase for a header or pill. Empty in quiet states.
	Label string
	// Message is the state, then the next step, in STE sentences.
	Message string
	// Action and ActionLabel name the next step and its exact control name.
	Action      LanguageServiceAction
	ActionLabel string
}

type ProjectInsights struct {
	// Language is the owner-derived user-facing status. Prefer it over the
	// raw lifecycle fields below for any user-visible text.
	Language       LanguageServiceStatus
	ServerName     string
	ServerReady    bool
	ServerBusy     bool
	ServerStarting bool
	ServerSyncing  bool
	ServerFailed   bool
	ServerError    string
	Errors         int
	Warnings       int
	Info           int
	Hints          int
	FilesAffected  int
	FilesTracked   int
	SymbolsCount   int
	TopIssue       string
	LastSync       int64
	// These count observed diagnostic units, not all files in the project.
	// The denominator and complete build context may remain unknown.
	DiagnosticsObserved   int
	DiagnosticsStale      int
	DiagnosticsUnverified int
	DiagnosticsLimited    int
	CoverageDetail        string
}
