package core

import (
	"context"
	"crude/coreprotocol"
	"crude/fileio"
	"errors"
)

func (a *actor) prepareProjectReplace(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	if request.Method != coreprotocol.ProjectReplacePlan {
		return nil, false
	}
	var input coreprotocol.ProjectReplaceRequest
	if err := decodeRequest(request, &input); err != nil {
		return func(context.Context) coreprotocol.Response { return fault(request, "invalid_request", err.Error()) }, true
	}
	root, docs := a.ws.RootPath, a.ws.LiveDocuments
	return func(ctx context.Context) coreprotocol.Response {
		if len(input.Matches) > 10000 || len(input.Replacement) > 1<<20 {
			return fault(request, "invalid_request", "replacement plan exceeds bounded inputs")
		}
		matches := append([]fileio.ProjectSearchMatch(nil), input.Matches...)
		for i := range matches {
			p, err := workspaceFilePath(root, matches[i].Path, false)
			if err != nil {
				return fault(request, "files_failed", err.Error())
			}
			matches[i].Path = p
		}
		edits, skipped, err := fileio.PlanProjectReplaceWithReader(matches, input.Query, input.Replacement, input.Options, func(path string) ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if docs != nil {
				if s, ok := docs.SnapshotIfOpen(path); ok {
					return s.Text, nil
				}
			}
			chunk, err := readFileChunk(ctx, root, coreprotocol.FileReadRequest{Path: path, Limit: coreprotocol.MaxFileChunk})
			if err != nil {
				return nil, err
			}
			if chunk.Total > 16<<20 {
				return nil, errors.New("replacement document exceeds 16 MiB")
			}
			return chunk.Data, nil
		})
		if err != nil {
			return fault(request, "files_failed", err.Error())
		}
		return response(request, coreprotocol.ProjectReplaceResult{Edits: edits, Skipped: skipped})
	}, true
}
