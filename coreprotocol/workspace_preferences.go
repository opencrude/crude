package coreprotocol

import "crude/types"

// WorkspacePreferencesRequest changes only explicitly edited stored fields.
type WorkspacePreferencesRequest struct {
	Edits []types.SettingEdit `json:"edits"`
}

type WorkspacePreferencesResult struct {
	Effective types.AppSettings        `json:"effective"`
	Overrides types.WorkspaceOverrides `json:"overrides"`
}
