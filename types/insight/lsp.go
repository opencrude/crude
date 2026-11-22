package insight

import (
	"crude/types/setting"
)

// LSPProjectProducer asks for one language server that checks a whole
// project root. It starts without an open document and reports problems for
// files that no editor has open. Root is a native directory path.
type LSPProjectProducer struct {
	Root string
	Spec setting.LSPServerSpec
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

// HighestDiagnosticSeverity selects the more important LSP severity. Zero
// means no diagnostic; smaller valid protocol values have higher priority.
func HighestDiagnosticSeverity(a, b int) int {
	if a < DiagError || a > DiagHint {
		a = 0
	}
	if b >= DiagError && b <= DiagHint && (a == 0 || b < a) {
		return b
	}
	return a
}

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
	Source     Source
	SourceText string
}

// LSPDiagnosticsResult is one file's answer in an ordered diagnostic batch.
// Error is a request failure, never a diagnostic reported for the source.
type LSPDiagnosticsResult struct {
	Diagnostics Diagnostics
	View        LSPProbeView
	Settled     bool
	Error       string
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
	// AdjustIndentation is the initial provider's explicit insertTextMode 2.
	// SnippetSource retains the validated original syntax for this mode only;
	// the editor reparses it after applying its private indentation policy.
	AdjustIndentation bool
	SnippetSource     string
	Label             string
	Detail            string
	Documentation     string
	Kind              int
	InsertText        string
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
	// The check of files that are not open: files checked and to check in
	// this pass, and why it waits ("while you type"), empty while it works.
	ProjectCheckDone  int
	ProjectCheckTotal int
	ProjectCheckWait  string
	// ProjectCheckUnchecked counts closed files whose server gave no usable
	// result; the check does not try them again until they or the
	// configuration change, or the user restarts the server.
	ProjectCheckUnchecked int
}
