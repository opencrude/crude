package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"crude/session"
	"crude/worktree"
)

// Capture session handles on the actor; the worker never reads workspace maps.
func (a *actor) executionRetentionCheck() func(context.Context, worktree.Owner, string, string) error {
	store, directory := a.ws.Collaboration, a.ws.WorkspaceDir
	live := map[string]*session.Session{}
	for _, frontend := range a.ws.Sessions {
		if backend := a.ws.BackendSession(frontend.SessionID); backend != nil {
			live[frontend.SessionID] = backend
		}
	}
	terminals := a.ws.TermMgr
	return func(ctx context.Context, owner worktree.Owner, id, root string) error {
		if terminals != nil {
			for _, terminal := range terminals.List() {
				rel, err := filepath.Rel(root, terminal.CWD)
				if terminal.Alive && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return fmt.Errorf("live terminal %s uses the checkout", terminal.ID)
				}
			}
		}
		if !strings.HasPrefix(owner.AgentID, "client:") {
			if store == nil {
				return errors.New("execution run authority unavailable")
			}
			run, err := store.Run(ctx, owner.RunID)
			if err != nil || run.AgentID != owner.AgentID || !run.State.Terminal() {
				return errors.New("execution run is not proven settled")
			}
		}
		for _, backend := range live {
			if err := backend.ExecutionRetention(id, root); err != nil {
				return err
			}
		}
		paths, err := executionSessionDatabases(filepath.Join(directory, "sessions"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
			db, err := sql.Open("sqlite", dsn.String())
			if err != nil {
				return err
			}
			err = session.ExecutionDatabaseRetention(db, id)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				return fmt.Errorf("retained session %s: %w", filepath.Base(path), errors.Join(err, closeErr))
			}
		}
		return nil
	}
}

// Only the session database layout is enumerated, not transcript/log/temp trees.
// Cold consumers matter just as much as the execution creator's own session.
func executionSessionDatabases(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".db") {
			paths = append(paths, path)
		}
		if !entry.IsDir() {
			continue
		}
		children, err := os.ReadDir(filepath.Join(path, "subagents"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.Type().IsRegular() && strings.HasSuffix(child.Name(), ".db") {
				paths = append(paths, filepath.Join(path, "subagents", child.Name()))
			}
		}
	}
	return paths, nil
}

type executionReconciliation struct {
	ID       string                 `json:"id"`
	Released []worktree.Owner       `json:"released"`
	Retained []executionRetainedPin `json:"retained"`
}

type executionRetainedPin struct {
	Owner  worktree.Owner `json:"owner"`
	Reason string         `json:"reason"`
}

func reconcileExecution(ctx context.Context, service *worktree.Service, id string, check func(context.Context, worktree.Owner, string, string) error) (executionReconciliation, error) {
	c, err := service.Inspect(id)
	result := executionReconciliation{ID: id, Released: []worktree.Owner{}, Retained: []executionRetainedPin{}}
	if err != nil {
		return result, err
	}
	for _, pin := range c.Pins {
		if err := check(ctx, pin, id, c.Root); err != nil {
			result.Retained = append(result.Retained, executionRetainedPin{pin, err.Error()})
			continue
		}
		if err := service.Unpin(id, pin); err != nil {
			return result, err
		}
		result.Released = append(result.Released, pin)
	}
	return result, nil
}
