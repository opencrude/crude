package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"crude/agent"
	"crude/collaboration"
	"crude/coreprotocol"
	"crude/types/control"
	"crude/workspace"
	"crude/worktree"
)

// A checkout is resolved from immutable request identity and the artifact
// owner's current pin. Session selection is only a next-input preference.
func (a *actor) controlDomainRoot(ctx context.Context, in control.Request) (string, error) {
	if a.ws == nil {
		return "", errors.New("workspace authority unavailable")
	}
	root := a.ws.RootPath
	if in.Caller.Kind == control.AgentCaller {
		store := a.ws.Collaboration
		if store == nil {
			return "", errors.New("collaboration authority unavailable")
		}
		participant, err := store.Agent(ctx, in.Caller.AgentID)
		if err != nil || participant.CurrentRunID != in.Caller.RunID || in.Caller.RunID == "" {
			return "", collaboration.ErrStale
		}
		run, err := store.Run(ctx, in.Caller.RunID)
		if err != nil || run.AgentID != participant.ID || run.State.Terminal() {
			return "", collaboration.ErrStale
		}
		if in.Target.ExecutionID == "" && a.ws.Executions != nil {
			for _, c := range a.ws.Executions.List() {
				if slices.Contains(c.Pins, worktree.Owner{AgentID: participant.ID, RunID: run.ID}) {
					return "", errors.New("caller cannot clear its pinned execution binding")
				}
			}
		}
	}
	if in.Target.ExecutionID == "" {
		return root, nil
	}
	if a.ws.Executions == nil {
		return "", errors.New("execution authority unavailable")
	}
	c, err := a.ws.Executions.Inspect(in.Target.ExecutionID)
	if err != nil || !c.Ready || c.Removed || c.LogicalRoot != root || !filepath.IsAbs(c.Root) {
		return "", errors.New("execution unavailable or foreign")
	}
	if in.Caller.Kind == control.AgentCaller && !slices.Contains(c.Pins, worktree.Owner{AgentID: in.Caller.AgentID, RunID: in.Caller.RunID}) {
		return "", errors.New("execution is not pinned to the exact caller run")
	}
	if strings.HasPrefix(in.CommandID, "history.") && c.Root != root {
		return "", errors.New("isolated history requires an execution-root recovery adapter")
	}
	return c.Root, nil
}

func controlCheckedPath(root, raw string, missing bool) (string, error) {
	path, err := workspaceFilePath(root, raw, missing)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(component, ".git") {
			return "", errors.New("git administrative paths are protected")
		}
	}
	return path, nil
}

// Resolve every payload path, including document IDs, before choosing a native
// owner. Absolute logical paths are not implicit authorization for external I/O.
func (a *actor) controlDomainPaths(request coreprotocol.Request, root string) (coreprotocol.Request, []string, error) {
	var paths []string
	encode := func(v any) error { raw, err := json.Marshal(v); request.Payload = raw; return err }
	check := func(raw string, missing bool) (string, error) { return controlCheckedPath(root, raw, missing) }
	switch request.Method {
	case coreprotocol.DocumentOpen:
		var in coreprotocol.DocumentOpenRequest
		if err := decode(request.Payload, &in); err != nil {
			return request, nil, err
		}
		path, err := check(in.Path, false)
		if err != nil {
			return request, nil, err
		}
		in.Path = path
		return requestWithPayload(request, in)
	case coreprotocol.DocumentRead, coreprotocol.DocumentEdit, coreprotocol.DocumentSave, coreprotocol.DocumentUndo, coreprotocol.DocumentRedo, coreprotocol.DocumentFormat:
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Payload, &in); err != nil {
			return request, nil, err
		}
		if a.ws.LiveDocuments == nil {
			return request, nil, errors.New("document authority unavailable")
		}
		snapshot, err := a.ws.LiveDocuments.Snapshot(in.ID)
		if err != nil {
			return request, nil, err
		}
		path, err := check(snapshot.Path, false)
		if err != nil {
			return request, nil, err
		}
		if request.Method != coreprotocol.DocumentRead {
			paths = []string{path}
		}
	case coreprotocol.FilesBrowse, coreprotocol.FilesCreate, coreprotocol.FilesRename, coreprotocol.FilesDelete:
		var in coreprotocol.FilePathRequest
		if err := decode(request.Payload, &in); err != nil {
			return request, nil, err
		}
		path, err := check(in.Path, request.Method == coreprotocol.FilesCreate)
		if err != nil {
			return request, nil, err
		}
		in.Path = path
		if request.Method != coreprotocol.FilesBrowse {
			paths = append(paths, path)
		}
		if request.Method == coreprotocol.FilesRename {
			target, err := check(in.NewPath, true)
			if err != nil {
				return request, nil, err
			}
			in.NewPath = target
			paths = append(paths, target)
		}
		if err := encode(in); err != nil {
			return request, nil, err
		}
	case coreprotocol.FilesRead:
		var in coreprotocol.FileReadRequest
		if err := decode(request.Payload, &in); err != nil {
			return request, nil, err
		}
		path, err := check(in.Path, false)
		if err != nil {
			return request, nil, err
		}
		in.Path = path
		if err := encode(in); err != nil {
			return request, nil, err
		}
	case coreprotocol.TerminalCreate:
		var in workspace.TerminalCreateOpts
		if err := decode(request.Payload, &in); err != nil {
			return request, nil, err
		}
		path, err := check(in.CWD, false)
		if err != nil {
			return request, nil, err
		}
		in.CWD = path
		if err := encode(in); err != nil {
			return request, nil, err
		}
	case coreprotocol.GitCompare, coreprotocol.PreviewGitDiscard:
		var in coreprotocol.GitCompareRequest
		if err := decode(request.Payload, &in); err != nil {
			return request, nil, err
		}
		path := in.Change.Path
		if in.Change.AbsolutePath != "" {
			path = in.Change.AbsolutePath
		}
		if _, err := check(path, true); err != nil {
			return request, nil, err
		}
	case coreprotocol.GitStageReviewed, coreprotocol.DiscardGitReviewed:
		var comparison workspace.GitComparison
		if request.Method == coreprotocol.GitStageReviewed {
			var in coreprotocol.GitStageReviewedRequest
			if err := decode(request.Payload, &in); err != nil {
				return request, nil, err
			}
			comparison = in.Comparison
		} else {
			var in coreprotocol.DiscardGitReviewedRequest
			if err := decode(request.Payload, &in); err != nil {
				return request, nil, err
			}
			comparison = in.Preview.Comparison
		}
		if comparison.RepositoryRoot != root {
			return request, nil, errors.New("git review belongs to another checkout")
		}
		path, err := check(comparison.AbsolutePath, true)
		if err != nil {
			return request, nil, err
		}
		paths = []string{path}
		if comparison.OriginalPath != "" {
			old, err := check(comparison.OriginalPath, true)
			if err != nil {
				return request, nil, err
			}
			paths = append(paths, old)
		}
	}
	return request, paths, nil
}

func requestWithPayload(request coreprotocol.Request, value any) (coreprotocol.Request, []string, error) {
	raw, err := json.Marshal(value)
	request.Payload = raw
	return request, nil, err
}

func (a *actor) controlDomainClaims(ctx context.Context, in control.Request, paths []string) (func(), error) {
	noop := func() {}
	if in.Caller.Kind != control.AgentCaller || len(paths) == 0 {
		return noop, nil
	}
	store, ownership := a.ws.Collaboration, a.ws.FileOwnership
	if store == nil || ownership == nil || in.ID == "" {
		return nil, errors.New("native claim authority or request identity unavailable")
	}
	participant, err := store.Agent(ctx, in.Caller.AgentID)
	if err != nil || participant.CurrentRunID != in.Caller.RunID {
		return nil, collaboration.ErrStale
	}
	id := agent.AgentID(participant.ID)
	acquire := func() error {
		_, err := store.AcquireClaims(ctx, collaboration.AcquireClaimsRequest{AgentID: participant.ID, RunID: in.Caller.RunID, RequestID: in.ID, Paths: paths})
		return err
	}
	if participant.ParentID != "" {
		// A child must already own every path; never opportunistically expand its
		// assignment. Hold the same lease used by native tools through the effect.
		release, err := ownership.BeginWrite(id, paths)
		if err != nil {
			return nil, err
		}
		if err := acquire(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}
	if err := ownership.CommitClaimAll(id, participant.Name, paths, true, acquire); err != nil {
		return nil, err
	}
	return ownership.BeginWrite(id, paths)
}
