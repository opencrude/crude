package remote

import (
	"context"
	"crude/core/coreprotocol"
	"crude/workspace/fileio"
)

func (w *Workspace) PlanProjectReplace(ctx context.Context, matches []fileio.ProjectSearchMatch, query, replacement string, opts fileio.ProjectSearchOptions) ([]fileio.ProjectFileReplacement, int, error) {
	var out coreprotocol.ProjectReplaceResult
	err := w.call(ctx, coreprotocol.ProjectReplacePlan, &coreprotocol.ProjectReplaceRequest{Matches: matches, Query: query, Replacement: replacement, Options: opts}, &out)
	return out.Edits, out.Skipped, err
}
