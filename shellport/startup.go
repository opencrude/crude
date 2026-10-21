package shellport

import (
	"context"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Startup is the pre-workspace navigation port. It owns no project resources.
type Startup interface {
	Inspect(context.Context, string) (coreprotocol.StartupSnapshot, error)
	Browse(context.Context, coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error)
	Catalog(context.Context, string, bool) (workspace.BrowserCatalog, error)
	Save(context.Context, []types.SettingEdit) (types.OnboardingSettings, error)
}
