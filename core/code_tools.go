package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"crude/coreprotocol"
	"crude/toolstore"
	"crude/types"
)

const codeToolsPreviewLimit = 64
const codeToolsPreviewLife = 10 * time.Minute

type codeToolsOwner struct {
	owner     coreprotocol.Owner
	workspace string
}

type codeToolsScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type retainedCodeToolsPreview struct {
	scope   *codeToolsScope
	key     codeToolsOwner
	expires time.Time
	store   toolstore.RemovalPreview
	view    types.CodeToolRemovalPreview
}

// codeToolsState retains only native attached-shell previews. Its mutex guards
// consent identity, not filesystem work or workspace inspection.
type codeToolsState struct {
	mu       sync.Mutex
	scopes   map[codeToolsOwner]*codeToolsScope
	previews map[string]retainedCodeToolsPreview
}

func (s *codeToolsState) attach(key codeToolsOwner) *codeToolsScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scopes == nil {
		s.scopes = make(map[codeToolsOwner]*codeToolsScope)
	}
	if s.scopes[key] == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.scopes[key] = &codeToolsScope{ctx: ctx, cancel: cancel}
	}
	return s.scopes[key]
}

func (s *codeToolsState) detach(owner coreprotocol.Owner, workspace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := codeToolsOwner{owner, workspace}
	if scope := s.scopes[key]; scope != nil {
		scope.cancel()
	}
	delete(s.scopes, key)
	for token, p := range s.previews {
		if p.key == key {
			delete(s.previews, token)
		}
	}
}

func (s *codeToolsState) retain(key codeToolsOwner, scope *codeToolsScope, p toolstore.RemovalPreview, view types.CodeToolRemovalPreview) (types.CodeToolRemovalPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, old := range s.previews {
		if !now.Before(old.expires) {
			delete(s.previews, token)
		}
	}
	if s.scopes[key] != scope {
		return types.CodeToolRemovalPreview{}, errors.New("the Settings window changed; review the removal again")
	}
	if len(s.previews) >= codeToolsPreviewLimit {
		return types.CodeToolRemovalPreview{}, errors.New("too many removal reviews are open; close one and try again")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return types.CodeToolRemovalPreview{}, err
	}
	view.Token = hex.EncodeToString(random[:])
	if s.previews == nil {
		s.previews = make(map[string]retainedCodeToolsPreview)
	}
	s.previews[view.Token] = retainedCodeToolsPreview{scope, key, now.Add(codeToolsPreviewLife), p, cloneRemovalView(view)}
	return view, nil
}

func cloneRemovalView(view types.CodeToolRemovalPreview) types.CodeToolRemovalPreview {
	view.Paths = append([]string(nil), view.Paths...)
	view.Tools = append([]types.CodeTool(nil), view.Tools...)
	for i := range view.Tools {
		t := &view.Tools[i]
		t.Licenses = append([]string(nil), t.Licenses...)
		t.Workspaces = append([]string(nil), t.Workspaces...)
		t.FileEntries = append([]types.CodeToolFile(nil), t.FileEntries...)
	}
	return view
}

// Actor inspection pins each manager's lifetime while reading its immutable
// assembly. Store-owned executable leases close the start/remove race after this
// conservative check and refuse deletion until every actual process has joined.
func (r *Runtime) checkCodeToolsUnused(ctx context.Context, p toolstore.RemovalPreview) error {
	for _, a := range r.actors() {
		var used string
		if err := a.inspect(ctx, func() {
			if a.ws.LSPMgr == nil {
				return
			}
			for _, language := range a.ws.LSPMgr.Assembly().Languages {
				if language.Executable == "" {
					continue
				}
				for _, tool := range p.Tools {
					relative, err := filepath.Rel(tool.Dir, language.Executable)
					if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
						used = a.ws.RootPath
					}
				}
			}
		}); err != nil {
			return err
		}
		if used != "" {
			return fmt.Errorf("a workspace uses this tool: %s; turn off its language server and refresh before removal", used)
		}
	}
	return nil
}

func (s *codeToolsState) consume(key codeToolsOwner, view types.CodeToolRemovalPreview) (toolstore.RemovalPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[view.Token]
	if !ok || p.key != key || s.scopes[key] != p.scope {
		return toolstore.RemovalPreview{}, errors.New("this removal review is no longer available; review the removal again")
	}
	delete(s.previews, view.Token)
	if !time.Now().Before(p.expires) || !reflect.DeepEqual(view, p.view) {
		return toolstore.RemovalPreview{}, errors.New("this removal review changed or expired; review the removal again")
	}
	return p.store, nil
}

func (a *actor) prepareCodeTools(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.PreviewCodeToolRemoval, coreprotocol.RemoveCodeTools:
	default:
		return nil, false
	}
	if request.JSON || request.Owner.ClientID == "" || request.Owner.Epoch == 0 {
		return func(context.Context) coreprotocol.Response {
			return fault(request, "native_shell_required", "Review tool removal in Settings.")
		}, true
	}
	key := codeToolsOwner{request.Owner, a.id}
	state := &a.runtime.codeTools
	scope := state.attach(key)
	switch request.Method {
	case coreprotocol.PreviewCodeToolRemoval:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.PreviewCodeToolRemovalRequest) (coreprotocol.PreviewCodeToolRemovalResult, error) {
			store, err := a.runtime.toolStore()
			if err != nil {
				return coreprotocol.PreviewCodeToolRemovalResult{}, err
			}
			var p toolstore.RemovalPreview
			if in.Unused {
				if len(in.IDs) != 0 {
					return coreprotocol.PreviewCodeToolRemovalResult{}, errors.New("choose tools or Remove unused, not both")
				}
				p, err = store.PreviewRemoveUnused(ctx)
			} else {
				p, err = store.PreviewRemove(ctx, in.IDs)
			}
			if err != nil {
				return coreprotocol.PreviewCodeToolRemovalResult{}, err
			}
			if len(p.Tools) == 0 {
				return coreprotocol.PreviewCodeToolRemovalResult{}, nil
			}
			view := types.CodeToolRemovalPreview{Paths: append([]string(nil), p.Paths...), Bytes: p.Bytes}
			for _, tool := range p.Tools {
				value, err := inspectCodeTool(ctx, tool)
				if err != nil {
					return coreprotocol.PreviewCodeToolRemovalResult{}, err
				}
				view.Tools = append(view.Tools, value)
			}
			view, err = state.retain(key, scope, p, view)
			return coreprotocol.PreviewCodeToolRemovalResult{Preview: view}, err
		})
	default:
		return serviceCall(request, func(ctx context.Context, in coreprotocol.RemoveCodeToolsRequest) (coreprotocol.RemoveCodeToolsResult, error) {
			// A consumed review is not an everlasting grant. Detach must also
			// cancel a removal queued behind store work, not just unused previews.
			requestCtx := ctx
			ctx, cancel := context.WithCancel(scope.ctx)
			stop := context.AfterFunc(requestCtx, cancel)
			defer stop()
			defer cancel()
			if requestCtx.Err() != nil {
				cancel()
			}
			p, err := state.consume(key, in.Preview)
			if err != nil {
				return coreprotocol.RemoveCodeToolsResult{}, err
			}
			if err := a.runtime.checkCodeToolsUnused(ctx, p); err != nil {
				return coreprotocol.RemoveCodeToolsResult{}, err
			}
			store, err := a.runtime.toolStore()
			if err == nil {
				err = store.Remove(ctx, p, "user")
			}
			return coreprotocol.RemoveCodeToolsResult{}, err
		})
	}
}
