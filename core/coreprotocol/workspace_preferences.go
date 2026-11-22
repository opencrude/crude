package coreprotocol

import (
	"crude/types/setting"
)

// WorkspacePreferencesRequest changes only explicitly edited stored fields.
type WorkspacePreferencesRequest struct {
	Edits []setting.Edit `json:"edits"`
}

type WorkspacePreferencesResult struct {
	Effective setting.App                `json:"effective"`
	Overrides setting.WorkspaceOverrides `json:"overrides"`
}
