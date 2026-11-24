package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"crude/insight/catalog"
	"crude/internal/fileuri"
	"crude/types/insight"
	"crude/types/setting"
)

// Assembly assigns each eligible file to the same root and launch as the editor.
// The listing is immutable until the next assembly. No root lookup or catalog
// resolution runs in the progress reader, on the editor lock, or per frame.
type analysisGroup struct {
	key, rootURI string
	project      bool
	spec         setting.LSPServerSpec
	paths        []string
}

type analysisInventory struct {
	groups []*analysisGroup
	owners map[string]string
	// Counts are derived from current facts, not submissions or silence.
	counts  map[string]int
	version uint64
}

func planAnalysis(ctx context.Context, discovery *assemblyDiscovery, settings setting.LSP, plan insight.AssemblyPlan) *analysisInventory {
	inventory := &analysisInventory{owners: make(map[string]string)}
	groups := map[string]*analysisGroup{}
	settings.Enabled = true
	for _, l := range plan.Languages {
		lang := discovery.languages[l.ID]
		if l.Status != insight.ToolReady || lang == nil || len(lang.paths) == 0 {
			continue
		}
		spec, ok := settings.ResolveServer(lang.paths[0])
		if !ok {
			continue
		}
		if l.Source == insight.ToolSourceStore {
			spec.Command = l.Executable
		}
		dirs := map[string]*analysisGroup{}
		roots := map[string]*analysisGroup{}
		for _, path := range lang.paths {
			if ctx.Err() != nil {
				return nil
			}
			rel, err := filepath.Rel(discovery.root, path)
			if err != nil || vendoredPath.MatchString(filepath.ToSlash(rel)) {
				continue
			}
			dir := filepath.Dir(path)
			group := dirs[dir]
			if group == nil {
				uri := resolveLSPRoot(path, discovery.root, spec.RootMarkers, spec.WorkspaceRootMarkers)
				group = roots[uri]
				if group == nil {
					key := lspServerKey(uri, spec)
					group = groups[key]
					if group == nil {
						group = &analysisGroup{key: key, rootURI: uri, spec: spec, project: declaresProject(spec, uri)}
						groups[key] = group
						inventory.groups = append(inventory.groups, group)
					}
					roots[uri] = group
				}
				dirs[dir] = group
			}
			group.paths = append(group.paths, path)
			inventory.owners[path] = group.key
		}
	}
	sort.Slice(inventory.groups, func(i, j int) bool {
		a, b := inventory.groups[i], inventory.groups[j]
		if a.spec.Command != b.spec.Command {
			return a.spec.Command < b.spec.Command
		}
		return a.key < b.key
	})
	for _, g := range inventory.groups {
		slices.Sort(g.paths)
	}
	return inventory
}

func (m *LSPManager) analysisProgressLocked() []insight.ServerAnalysis {
	inv := m.inventory
	if inv == nil || m.discovery == nil || !m.projectCheckAllowedLocked() || m.sources == nil || m.sources.Insight == nil {
		return nil
	}
	store := m.sources.Insight.diagnostics
	if inv.counts == nil || store.Version() != inv.version {
		inv.version, inv.counts = store.CountCurrent(inv.owners)
	}
	var rows []insight.ServerAnalysis
	for _, g := range inv.groups {
		if m.nativeGroupLocked(g) {
			continue
		}
		counts := m.checkCounts[g.key]
		checked := inv.counts[g.key] + counts.clean
		// A queued, uninitialized server has not chosen its diagnostic model.
		// Do not label it as a fallback reporter before negotiation.
		state := m.states[g.key]
		if !m.forceFallback && counts.unchecked == 0 && m.checkWait == "" && (state == nil || state.status != LSPConnected) {
			continue
		}
		if checked == len(g.paths) || !m.checkRunning && !m.checkPending && counts.unchecked == 0 {
			continue
		}
		root, _ := fileuri.ToPath(g.rootURI)
		rows = append(rows, insight.ServerAnalysis{ID: g.key, Server: filepath.Base(g.spec.Command), Root: root, Total: len(g.paths), Checked: checked, Unchecked: counts.unchecked, Active: m.checkTarget == g.key})
	}
	return rows
}

func (m *LSPManager) nativeGroupLocked(g *analysisGroup) bool {
	return !m.forceFallback && (g.project || m.reportsProjectLocked(g.key, m.states[g.key]))
}

// declaresProject evaluates a catalog push declaration for this root. Some
// servers operate on single documents unless the root has a project marker.
// Assembly calls it off the state lock. A trailing slash requires a directory.
func declaresProject(spec setting.LSPServerSpec, uri string) bool {
	if spec.DiagnosticScope != insight.CoverageProject {
		return false
	}
	server, _ := catalog.Current().Server(spec.ServerID)
	if len(server.DiagnosticRootMarkers) == 0 {
		return true
	}
	root, err := fileuri.ToPath(uri)
	if err != nil {
		return false
	}
	for _, marker := range server.DiagnosticRootMarkers {
		name, dir := strings.CutSuffix(marker, "/")
		info, err := os.Stat(filepath.Join(root, name))
		if err == nil && (dir && info.IsDir() || !dir && info.Mode().IsRegular()) {
			return true
		}
	}
	return false
}

// Pick nearby files first, keeping files of one launch together to avoid
// evicting a server only to restart it for its next file.
func orderedFiles(g *analysisGroup, anchors []string) []string {
	type ranked struct {
		path        string
		rank, depth int
	}
	files := make([]ranked, 0, len(g.paths))
	for _, path := range g.paths {
		f := ranked{path: path, depth: strings.Count(filepath.ToSlash(path), "/")}
		for _, a := range anchors {
			f.rank = max(f.rank, sharedDirectories(a, filepath.Dir(path)))
		}
		files = append(files, f)
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].rank != files[j].rank {
			return files[i].rank > files[j].rank
		}
		return files[i].depth < files[j].depth
	})
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths
}
