package types

// AssemblyPlan is the workspace's code-intelligence assembly: which languages
// the project contains, which tool serves each one, where that tool comes
// from, and what each tool checks. One owner (the workspace language-server
// manager) publishes it to shells, agents and the control API. It is data
// derived from the catalog, the user's settings, installed tools and a
// bounded file listing; producing it runs no project code.
type AssemblyPlan struct {
	// Generation increases with every published plan.
	Generation uint64
	// State is one of the Assembly* states.
	State string
	// Message is user text: the state and the next step.
	Message string
	// FilesSeen is the number of files the bounded listing looked at.
	FilesSeen int
	// Limited is true when the listing stopped at its bound, so some files
	// were not seen.
	Limited   bool
	Languages []AssemblyLanguage
}

// Assembly plan states.
const (
	AssemblyScanning = "scanning"
	AssemblyReady    = "ready"
	AssemblyFailed   = "failed"
)

// AssemblyLanguage is one language found in the project.
type AssemblyLanguage struct {
	ID   string
	Name string
	// Files is how many files of this language the listing saw.
	Files int
	// Sample is the file closest to the workspace root; it selects the
	// project root and the server.
	Sample string
	// ServerID names the catalog server; empty for a custom command.
	ServerID string
	Command  string
	// Executable is the resolved program; empty when it is not installed.
	Executable string
	// Source is where the program comes from: ToolSource*.
	Source string
	// Status is one of the Tool* statuses.
	Status string
	// Scope is CoverageProject or CoverageOpenFiles when a server runs.
	Scope string
	// Root is the project root a whole-project check uses.
	Root string
	// Package names the catalog package that can provide the server, and
	// Installable says whether Crude can install it on this computer
	// (catalog Package.DownloadFor).
	Package     string
	Installable bool
	// Reason is user text that states the result and the next step.
	Reason string
}

// Tool statuses of an assembled language.
const (
	ToolReady    = "ready"
	ToolMissing  = "missing"
	ToolOff      = "off"
	ToolNoServer = "no-server"
)

// Tool sources.
const (
	ToolSourcePath     = "path"
	ToolSourceStore    = "tool-store"
	ToolSourceSettings = "settings"
)

// Coverage scopes of a server.
const (
	CoverageProject   = "project"
	CoverageOpenFiles = "open-files"
)

// CloneAssemblyPlan returns a detached copy.
func CloneAssemblyPlan(plan AssemblyPlan) AssemblyPlan {
	plan.Languages = append([]AssemblyLanguage(nil), plan.Languages...)
	return plan
}
