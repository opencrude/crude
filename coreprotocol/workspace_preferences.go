package coreprotocol

import "crude/types"

// WorkspacePreferencesRequest fences the sparse overlay originally observed by
// this shell; its opaque revision includes redacted capability containers.
type WorkspacePreferencesRequest struct {
	ExpectedRevision string                   `json:"expected_revision"`
	Overrides        types.WorkspaceOverrides `json:"overrides"`
}

type WorkspacePreferencesResult struct {
	Effective types.AppSettings `json:"effective"`
}
