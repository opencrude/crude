package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Startup is the pre-workspace navigation port. It owns no project resources.
type Startup interface {
	WorkspaceFolderCreator
	Inspect(context.Context, string) (coreprotocol.StartupSnapshot, error)
	Browse(context.Context, coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error)
	Catalog(context.Context, string, bool) (workspace.BrowserCatalog, error)
	Save(context.Context, []types.SettingEdit) (types.OnboardingSettings, error)
}

// WorkspaceFolderCreator creates one new folder on the selected core host,
// independently of an attached project's file-operation boundary.
type WorkspaceFolderCreator interface {
	CreateWorkspaceFolder(context.Context, string, string) (string, error)
}
