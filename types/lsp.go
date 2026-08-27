package types

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

type CompletionItem struct {
	Label         string
	Detail        string
	Documentation string
	Kind          int
	InsertText    string
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
