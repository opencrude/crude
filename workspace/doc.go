// Package workspace is the per-project state machine for the Crude IDE.
//
// One Workspace instance represents a single IDE session rooted at a project
// directory. It owns everything that is scoped to that project:
//
// - AI agent sessions and orchestrators
// - Editor buffers and tab state
// - LSP server connections and diagnostics cache
// - File tree model
// - Workspace-specific settings (inheriting from app config)
// - Per-workspace SQLite database
//
// Workspace follows the JetBrains model: each project gets a dedicated folder
// under ~/.crude/workspaces/<hash>/, identified by a SHA-256 hash of the
// absolute project directory path. This folder contains the workspace database,
// session data, and any workspace-specific configuration.
//
// Communication with the TUI layer happens via two mechanisms:
// - TUI → Workspace: direct method calls (e.g., ws.OpenFile(path))
// - Workspace → TUI: event bus publishes (e.g., bus.Publish(FileOpened{path}))
//
// The workspace package intentionally has NO knowledge of rendering or input
// handling; those concerns live in the tui/ layer.
package workspace
