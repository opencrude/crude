package shellport

import (
	"context"

	"crude/types"
)

// ProjectTools is the optional native project-setup surface. A generic control
// client or agent cannot impersonate the human decision through this interface.
type ProjectTools interface {
	ReviewProjectTools(context.Context, bool) (types.ProjectToolSetupReview, error)
	DecideProjectTools(context.Context, string, []string) (types.ProjectToolSetupResult, error)
}
