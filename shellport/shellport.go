// Package shellport defines the seam between the terminal shell (tui/) and the
// workspace runtime.
//
// The shell depends only on Workspace. The in-process implementation is
// Local; a remote implementation can satisfy the same sub-interfaces over the
// core protocol. See docs/reference/shell-port.md for the inventory.
package shellport

// Workspace is the complete runtime surface the shell consumes. Each embedded
// interface is cohesive and independently implementable.
type Workspace interface {
	Runtime
	SavedWorkspaceManagement
	Agents
	Sessions
	Settings
	ToolLayoutStore
	Files
	SourceControl
	History
	Capabilities
	Collaboration
	Memory
	TerminalHost
	StatusHost
}
