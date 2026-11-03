package core

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"crude/toolstore"
	"crude/types"
)

func (r *Runtime) toolStore() (*toolstore.Store, error) {
	r.toolsOnce.Do(func() { r.tools, r.toolsErr = openToolStore() })
	if r.toolsErr != nil {
		return nil, r.toolsErr
	}
	if r.tools == nil {
		return nil, errors.New("the tool store is unavailable")
	}
	return r.tools, nil
}

func codeTool(t toolstore.Tool) types.CodeTool {
	return types.CodeTool{
		ID: t.ID, Package: t.Package, Version: t.Version, Dir: t.Dir,
		Executable: t.Executable, Bin: t.Bin, Command: t.Command, SHA256: t.SHA256,
		DigestOrigin: t.DigestOrigin, Source: t.Source, URL: t.URL, Archive: t.Archive,
		Size: t.Size, Files: t.Files, Licenses: append([]string(nil), t.Licenses...),
		Workspaces: append([]string(nil), t.Workspaces...), InstalledAt: t.InstalledAt,
		LastUsed: t.LastUsed, InstalledBy: t.InstalledBy, Approval: t.Approval,
	}
}

// readCodeToolInventory performs blocking I/O and must run on a worker.
func readCodeToolInventory(ctx context.Context, store *toolstore.Store, plan types.AssemblyPlan, offset, limit int) (types.CodeToolInventory, error) {
	var out types.CodeToolInventory
	tools, total, err := store.List(ctx)
	if err != nil {
		return out, err
	}
	unknown, err := store.Unrecognized(ctx)
	if err != nil {
		return out, err
	}
	history, count, err := store.History(ctx, offset, limit)
	if err != nil {
		return out, err
	}
	out.TotalBytes, out.Unrecognized, out.HistoryTotal, out.HistoryOffset = total, unknown, count, offset
	out.Project = types.CloneAssemblyPlan(plan)
	for _, t := range tools {
		value, err := inspectCodeTool(ctx, t)
		if err != nil {
			return out, err
		}
		out.Tools = append(out.Tools, value)
	}
	for _, e := range history {
		out.History = append(out.History, types.CodeToolHistoryEvent{Time: e.Time, Kind: e.Kind, ToolID: e.ToolID, Package: e.Package, Version: e.Version, Detail: e.Detail, By: e.By, Approval: e.Approval})
	}
	seen := make(map[string]bool)
	managedFiles := make([]fs.FileInfo, 0, len(tools))
	for _, tool := range tools {
		info, err := os.Stat(tool.Executable)
		if err != nil {
			return out, err
		}
		managedFiles = append(managedFiles, info)
	}
	for _, l := range plan.Languages {
		if l.Executable == "" || (l.Source != types.ToolSourcePath && l.Source != types.ToolSourceSettings) || seen[l.Executable] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		info, err := os.Stat(l.Executable)
		if errors.Is(err, fs.ErrNotExist) {
			continue // The assembly snapshot is older than this inventory read.
		}
		if err != nil {
			return out, err
		}
		managed := false
		for _, known := range managedFiles {
			if os.SameFile(info, known) {
				managed = true
				break
			}
		}
		if managed {
			continue
		}
		seen[l.Executable] = true
		out.Unmanaged = append(out.Unmanaged, types.UnmanagedCodeTool{Name: l.ServerID, Command: l.Command, Executable: l.Executable, Source: l.Source})
	}
	sort.Slice(out.Unmanaged, func(i, j int) bool { return out.Unmanaged[i].Executable < out.Unmanaged[j].Executable })
	return out, nil
}

func inspectCodeTool(ctx context.Context, tool toolstore.Tool) (types.CodeTool, error) {
	out := codeTool(tool)
	err := filepath.WalkDir(tool.Dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == tool.Dir {
			return nil
		}
		if len(out.FileEntries) >= 100000 {
			return errors.New("the tool contains too many files to review; no files were removed")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(tool.Dir, path)
		if err != nil {
			return err
		}
		file := types.CodeToolFile{Path: relative, Kind: "file", Size: info.Size()}
		switch {
		case entry.IsDir():
			file.Kind, file.Size = "folder", 0
		case entry.Type()&os.ModeSymlink != 0:
			file.Kind = "link"
			file.LinkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		out.FileEntries = append(out.FileEntries, file)
		return nil
	})
	return out, err
}
