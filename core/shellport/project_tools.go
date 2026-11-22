package shellport

import (
	"context"

	"crude/types/insight"
)

// ProjectTools is the optional native project-setup surface. A generic control
// client or agent cannot impersonate the human decision through this interface.
type ProjectTools interface {
	ReviewProjectTools(context.Context, insight.ProjectToolSetupRequest) (insight.ProjectToolSetupReview, error)
	DecideProjectTools(context.Context, string, []string) (insight.ProjectToolSetupResult, error)
}
