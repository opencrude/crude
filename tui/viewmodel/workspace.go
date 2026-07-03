package viewmodel

import "crude/tui/workspace"

// Workspace types are re-exported from the workspace package so that existing
// viewmodel code with `m.FieldName` continues to compile exactly as before.
// The old Workspace struct (with unexported fields) has been deleted.
// App now embeds *workspace.Workspace, and the fields listed below are
// promoted from that struct.
//
// DO NOT add new types here. Import workspace directly in new code.
//
// Types that are referenced by other viewmodel files via package-qualified
// names (viewmodel.SomeType) need explicit aliases here. Types that are
// referenced via unqualified names (SomeType) are promoted through the
// App embedding or are defined directly in this package.

// Type aliases so that existing viewmodel code with
//   type Workspace = workspace.Workspace
// continues to compile. These are unexported aliases that let test helpers
// and other viewmodel code reference workspace types without importing
// the workspace package.

type Workspace = workspace.Workspace
type Session = workspace.Session

// NewSessionResult is aliased from workspace so that external consumers
// (e.g. tui/tui.go) can refer to viewmodel.NewSessionResult.
type NewSessionResult = workspace.NewSessionResult
