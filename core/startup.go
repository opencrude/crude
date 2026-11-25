package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"crude/core/coreprotocol"
	"crude/core/projectpath"
	"crude/types/setting"
	"crude/types/statuscenter"
	"crude/workspace"
	"crude/workspace/folderkind"
)

// StartupOwner belongs to the application. It reads and writes only profile
// metadata; neither method creates a project workspace.
type StartupOwner interface {
	StartupSnapshot(context.Context) (coreprotocol.StartupSnapshot, error)
	SaveStartup(context.Context, []setting.Edit) (setting.Onboarding, error)
	ReadStartupSettings(context.Context) (setting.App, error)
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
		location, file, missing, err := inspectLaunchPath(in)
		out.FilePath, out.FileMissing = file, missing
		if in.Explicit {
			out.ShowIntroduction = false
		}
		if err != nil {
			out.Root = in.Root
			out.PathError = err.Error()
		} else {
			out.Root, out.FolderKind = location.Path, location.Kind
			if location.Warning != "" {
				out.Issues = append(out.Issues, statuscenter.OperationIssue{Title: "Personal folder settings need attention", Summary: "Some personal folder locations could not be read.", Detail: location.Warning, Guidance: "Project folders stay available. Check the personal folder settings on this computer."})
			}
		}
		// Established project launches need no previous-workspace scan.
		out.ChooseWorkspace = out.PathError != "" || !in.Explicit && (out.ShowIntroduction || out.FolderKind != "")
		if out.ChooseWorkspace {
			infos, listErr := workspace.ListWorkspaceInfosContext(ctx)
			if listErr != nil {
				out.WorkspacesError = listErr.Error()
			} else {
				activity := c.runtime.savedWorkspaceActivity(ctx)
				for i := range infos {
					infos[i].Activity = activity[infos[i].ID]
				}
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
		location, err := folderkind.Inspect(in.Root)
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

func validateStartupEdits(edits []setting.Edit) error {
	if err := setting.ValidateEdits(setting.App{}, edits); err != nil {
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
func inspectProject(root string, explicit bool) (string, error) {
	location, err := folderkind.Inspect(root)
	if err != nil {
		return "", err
	}
	if location.Kind != "" && !explicit {
		return "", fmt.Errorf("choose a workspace to open %s", location.Path)
	}
	return location.Path, nil
}

// inspectLaunchPath treats only an explicit positional path as a possible file.
// The core owns the filesystem. A missing leaf is a new named editor buffer,
// never a request to create directories or overwrite a dangling symlink.
func inspectLaunchPath(in coreprotocol.StartupInspectRequest) (folderkind.Location, string, bool, error) {
	root, name := in.Root, ""
	missing := false
	if in.AllowFile && in.Explicit && root != "" && !os.IsPathSeparator(root[len(root)-1]) && filepath.Base(root) != "." && filepath.Base(root) != ".." {
		info, err := os.Stat(root)
		switch {
		case err == nil && info.IsDir():
		case err == nil && info.Mode().IsRegular():
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				return folderkind.Location{}, "", false, err
			}
			root, name = filepath.Dir(resolved), filepath.Base(resolved)
		case errors.Is(err, os.ErrNotExist):
			missing = true
			if _, linkErr := os.Lstat(root); !errors.Is(linkErr, os.ErrNotExist) {
				return folderkind.Location{}, "", false, fmt.Errorf("cannot open %s; check the link target", root)
			}
			root, name = filepath.Dir(root), filepath.Base(root)
			if err := projectpath.ValidateFileName(name); err != nil {
				return folderkind.Location{}, "", false, err
			}
		case err != nil:
			return folderkind.Location{}, "", false, err
		default:
			return folderkind.Location{}, "", false, fmt.Errorf("cannot open %s; choose a file or folder", root)
		}
	}
	location, err := folderkind.Inspect(root)
	if err != nil || name == "" {
		return location, "", false, err
	}
	return location, filepath.Join(location.Path, name), missing, nil
}
