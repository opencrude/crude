package coreprotocol

import (
	"crude/types"
	"crude/workspace"
)

// Startup methods require a connected core, not an attached workspace.
const (
	StartupInspect    = "startup.inspect"
	StartupBrowse     = "startup.browse"
	StartupCatalog    = "startup.catalog"
	StartupSave       = "startup.save"
	StartupOnboarding = "startup.onboarding"
)

type StartupInspectRequest struct {
	Root string `json:"root"`
}

// StartupSnapshot contains detached metadata only. An empty error-free FolderKind
// identifies a project candidate; no project services have been constructed.
type StartupSnapshot struct {
	Root             string                    `json:"root"`
	FolderKind       string                    `json:"folder_kind,omitempty"`
	PathError        string                    `json:"path_error,omitempty"`
	ShowIntroduction bool                      `json:"show_introduction"`
	Onboarding       types.OnboardingSettings  `json:"onboarding"`
	Theme            types.ThemeSettings       `json:"theme"`
	Workspaces       []workspace.WorkspaceInfo `json:"workspaces,omitempty"`
	WorkspacesError  string                    `json:"workspaces_error,omitempty"`
	Issues           []types.OperationIssue    `json:"issues,omitempty"`
}

type StartupBrowseRequest struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
}

type StartupBrowseResult struct {
	Path       string                 `json:"path"`
	Entries    []ClientDirectoryEntry `json:"entries"`
	NextOffset int                    `json:"next_offset"`
	Truncated  bool                   `json:"truncated"`
}

type StartupCatalogRequest struct {
	Root          string `json:"root"`
	IncludeHidden bool   `json:"include_hidden"`
}

type StartupSaveRequest struct {
	Edits []types.SettingEdit `json:"edits"`
}

type OnboardingResult struct {
	Application types.AppSettings      `json:"application"`
	Accounts    map[string]AccountView `json:"accounts"`
}
