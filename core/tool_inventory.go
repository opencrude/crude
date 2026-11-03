package core

import (
	"context"
	"errors"
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
	for _, t := range tools {
		out.Tools = append(out.Tools, codeTool(t))
	}
	for _, e := range history {
		out.History = append(out.History, types.CodeToolHistoryEvent{Time: e.Time, Kind: e.Kind, ToolID: e.ToolID, Package: e.Package, Version: e.Version, Detail: e.Detail, By: e.By, Approval: e.Approval})
	}
	seen := make(map[string]bool)
	for _, l := range plan.Languages {
		if l.Executable == "" || (l.Source != types.ToolSourcePath && l.Source != types.ToolSourceSettings) || seen[l.Executable] {
			continue
		}
		seen[l.Executable] = true
		out.Unmanaged = append(out.Unmanaged, types.UnmanagedCodeTool{Name: l.ServerID, Command: l.Command, Executable: l.Executable, Source: l.Source})
	}
	sort.Slice(out.Unmanaged, func(i, j int) bool { return out.Unmanaged[i].Executable < out.Unmanaged[j].Executable })
	return out, nil
}
