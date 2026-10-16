package types

import (
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
	Settings              map[string]any
	InitializationOptions map[string]any
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
	Server string
}

type LSPLocation struct {
	URI     string
	Path    string
	Line    int
	Col     int
	EndLine int
	EndCol  int
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
	Label               string
	Detail              string
	Documentation       string
	Kind                int
	InsertText          string
}

type ProjectInsights struct {
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
}
