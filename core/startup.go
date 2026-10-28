package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"crude/coreprotocol"
	"crude/projectpath"
	"crude/types"
	"crude/workspace"
)

// StartupOwner belongs to the application. It reads and writes only profile
// metadata; neither method creates a project workspace.
type StartupOwner interface {
	StartupSnapshot(context.Context) (coreprotocol.StartupSnapshot, error)
	SaveStartup(context.Context, []types.SettingEdit) (types.OnboardingSettings, error)
	ReadStartupSettings(context.Context) (types.AppSettings, error)
}

func (r *Runtime) SetStartupOwner(owner StartupOwner) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || len(r.clients) != 0 {
		return errors.New("startup owner must be installed before clients connect")
	}
	r.startup = owner
	return nil
}

func startupMethod(method string) bool {
	switch method {
	case coreprotocol.StartupInspect, coreprotocol.StartupBrowse, coreprotocol.StartupCatalog, coreprotocol.StartupCreateFolder, coreprotocol.StartupSave, coreprotocol.StartupOnboarding:
		return true
	}
	return false
}

// dispatchStartup releases the wire reader before filesystem and configuration
// work. Connection lifetime and request bounds own these workers.
func (c *Connection) dispatchStartup(ctx context.Context, request coreprotocol.Request) <-chan coreprotocol.Response {
	result := make(chan coreprotocol.Response, 1)
	c.runtime.mu.Lock()
	if c.runtime.closed {
		c.runtime.mu.Unlock()
		result <- fault(request, "closed", "core is closed")
		return result
	}
	c.runtime.factoryWG.Add(1)
	c.runtime.mu.Unlock()
	go func() {
		defer c.runtime.factoryWG.Done()
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(c.ctx, cancel)
		defer cancel()
		defer stop()
		if request.Method != coreprotocol.StartupCreateFolder {
			result <- c.startupCall(ctx, request)
			return
		}
		prior, fresh, err := c.runtime.receipts.begin(ctx, request)
		if err != nil {
			result <- fault(request, "receipt_failed", err.Error())
			return
		}
		if !fresh {
			if prior.State == "completed" {
				result <- prior.Response
			} else {
				result <- fault(request, "outcome_"+prior.State, "Folder creation may have completed; inspect the original receipt, not a repeated request.")
			}
			return
		}
		out := c.startupCall(ctx, request)
		if err := c.runtime.receipts.complete(context.WithoutCancel(ctx), request, out); err != nil {
			out = fault(request, "outcome_uncertain", "Folder creation may have completed; inspect the original receipt: "+err.Error())
		}
		result <- out
	}()
	return result
}

func (c *Connection) startupCall(ctx context.Context, request coreprotocol.Request) coreprotocol.Response {
	if err := ctx.Err(); err != nil {
		return fault(request, "cancelled", err.Error())
	}
	bad := func(err error) coreprotocol.Response { return fault(request, "startup_failed", err.Error()) }
	switch request.Method {
	case coreprotocol.StartupOnboarding:
		if err := decodeRequest(request, &coreprotocol.Empty{}); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		if c.runtime.startup == nil {
			return fault(request, "settings_unavailable", "application startup settings are unavailable")
		}
		settings, err := c.runtime.startup.ReadStartupSettings(ctx)
		if err != nil {
			return bad(err)
		}
		out := coreprotocol.OnboardingResult{Application: publicApp(settings), Accounts: make(map[string]coreprotocol.AccountView)}
		for id, account := range settings.Accounts {
			out.Accounts[id] = publicAccount(account)
		}
		return response(request, out)
	case coreprotocol.StartupInspect:
		var in coreprotocol.StartupInspectRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		var out coreprotocol.StartupSnapshot
		if owner := c.runtime.startup; owner != nil {
			var err error
			out, err = owner.StartupSnapshot(ctx)
			if err != nil {
				return bad(err)
			}
		}
		location, err := projectpath.Inspect(in.Root)
		if err != nil {
			out.Root = in.Root
			out.PathError = err.Error()
		} else {
			out.Root, out.FolderKind = location.Path, location.Kind
			if location.Warning != "" {
				out.Issues = append(out.Issues, types.OperationIssue{Title: "Personal folder settings need attention", Summary: "Some personal folder locations could not be read.", Detail: location.Warning, Guidance: "Project folders stay available. Check the personal folder settings on this computer."})
			}
		}
		// Established project launches need no previous-workspace scan.
		if out.ShowIntroduction || out.FolderKind != "" || out.PathError != "" {
			infos, listErr := workspace.ListWorkspaceInfosContext(ctx)
			if listErr != nil {
				out.WorkspacesError = listErr.Error()
			} else {
				out.Workspaces = infos
			}
		}
		return response(request, out)
	case coreprotocol.StartupBrowse:
		var in coreprotocol.StartupBrowseRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		directory, err := projectpath.Browse(ctx, in.Path, in.Offset)
		if err != nil {
			return bad(err)
		}
		out := coreprotocol.StartupBrowseResult{Path: directory.Path, NextOffset: directory.NextOffset, Truncated: directory.Truncated}
		for _, entry := range directory.Entries {
			out.Entries = append(out.Entries, coreprotocol.ClientDirectoryEntry{Name: entry.Name, Path: entry.Path, IsDir: entry.IsDir, IsSymlink: entry.IsSymlink})
		}
		return response(request, out)
	case coreprotocol.StartupCatalog:
		var in coreprotocol.StartupCatalogRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		location, err := projectpath.Inspect(in.Root)
		if err != nil {
			return bad(err)
		}
		catalog, err := startupCatalog(ctx, location.Path, in.IncludeHidden)
		if err != nil {
			return bad(err)
		}
		return response(request, catalog)
	case coreprotocol.StartupCreateFolder:
		var in coreprotocol.StartupCreateFolderRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		path, err := projectpath.CreateFolder(ctx, in.Parent, in.Name)
		if err != nil {
			if errors.Is(err, projectpath.ErrParentMovedAfterCreate) {
				return fault(request, "outcome_uncertain", err.Error())
			}
			return bad(err)
		}
		return response(request, coreprotocol.StartupCreateFolderResult{Path: path})
	case coreprotocol.StartupSave:
		var in coreprotocol.StartupSaveRequest
		if err := decodeRequest(request, &in); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		if err := validateStartupEdits(in.Edits); err != nil {
			return fault(request, "invalid_request", err.Error())
		}
		owner := c.runtime.startup
		if owner == nil {
			return fault(request, "settings_unavailable", "application startup settings are unavailable")
		}
		out, err := owner.SaveStartup(ctx, in.Edits)
		if err != nil {
			return bad(err)
		}
		return response(request, out)
	}
	return fault(request, "unknown_method", "unknown startup operation")
}

func validateStartupEdits(edits []types.SettingEdit) error {
	if err := types.ValidateSettingEdits(types.AppSettings{}, edits); err != nil {
		return err
	}
	for _, edit := range edits {
		if len(edit.Path) != 2 || edit.Path[0] != "onboarding" {
			return fmt.Errorf("startup can change only introduction and reminder choices")
		}
	}
	return nil
}

// inspectProject is the sole admission check, shared by every attachment route.
func inspectProject(root string) (string, error) {
	location, err := projectpath.Inspect(root)
	if err != nil {
		return "", err
	}
	if location.Kind != "" {
		return "", fmt.Errorf("%s is a %s folder. Select a project folder. Crude does not start project services here", filepath.Clean(location.Path), location.Kind)
	}
	return location.Path, nil
}
