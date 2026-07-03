package viewmodel

import "crude/tui/workspace"

// referenceStore and newReferenceStore are unexported aliases of exported
// workspace types, so the 34+ call sites in viewmodel compile unchanged.
type referenceStore = workspace.ReferenceStore
var newReferenceStore = workspace.NewReferenceStore
