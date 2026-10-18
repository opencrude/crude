package filetree

import (
	"crude/textsearch"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"crude/third_party/pathwalk"
)

// PathMatch is a ranked path result. NamePositions are rune offsets in Entry.Name
// and are consumed by the shared tree row painter for highlighting.
type PathMatch struct {
	Entry         pathwalk.Entry
	Score         int
	NamePositions []int
}

// RankPathEntries performs Crude's filename/path fuzzy ranking without reading
// file contents. It is deliberately independent of the tree's loaded-node
// state, so unloaded descendants are searchable once the path catalog exists.
func RankPathEntries(entries []pathwalk.Entry, query string) []PathMatch {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	result := make([]PathMatch, 0, len(entries))
	for _, entry := range entries {
		score, _, ok := fuzzyPathScore(entry.Relative, query)
		if !ok {
			continue
		}
		nameScore, namePositions, nameOK := fuzzySequenceScore(entry.Name, query)
		if nameOK {
			score += nameScore + 1000
		}
		result = append(result, PathMatch{
			Entry: entry, Score: score, NamePositions: namePositions,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].Entry.Relative != result[j].Entry.Relative {
			return result[i].Entry.Relative < result[j].Entry.Relative
		}
		return result[i].Entry.Path < result[j].Entry.Path
	})
	return result
}

// NewPickerSearchModel builds a tree-shaped projection from ranked results and
// their necessary ancestors. It performs no filesystem I/O. The input should
// contain only paths that matched the current query; ancestor directories are
// synthesized so the result still looks like the normal file tree.
func NewPickerSearchModel(rootPath string, directoriesOnly bool, matches []PathMatch) *FileTreeModel {
	return NewSearchModel(rootPath, ModelOptions{DirectoriesOnly: directoriesOnly}, matches)
}

// NewSearchModel builds a no-I/O tree projection from ranked path matches.
// Callers may enable the normal tree services when the projection belongs to a
// persistent workspace tree; picker callers use the service-free defaults.
func NewSearchModel(rootPath string, options ModelOptions, matches []PathMatch) *FileTreeModel {
	options.InitialLoad = false
	options.NoLocalIO = true
	m := NewFileTreeModelWithOptions(rootPath, options)
	root := m.Root
	if root == nil {
		return m
	}
	root.Loading = false
	root.IsExpanded = true
	m.filterEd.SetValue("")
	m.filterMatchPos = make(map[string][]int)
	m.nodes = map[string]*TreeNode{root.Path: root}

	for _, match := range matches {
		path := filepath.Clean(match.Entry.Path)
		rel, err := filepath.Rel(root.Path, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		parent := root
		parts := strings.Split(rel, string(filepath.Separator))
		for i, part := range parts {
			if part == "" || part == "." {
				continue
			}
			childPath := filepath.Join(parent.Path, part)
			child := m.nodes[childPath]
			if child == nil {
				child = &TreeNode{
					Name: part, Path: childPath, IsDir: i < len(parts)-1,
					Depth: parent.Depth + 1, Parent: parent,
					Hidden: strings.HasPrefix(part, "."),
				}
				parent.Children = append(parent.Children, child)
				m.nodes[childPath] = child
			}
			if i < len(parts)-1 {
				child.IsDir = true
				child.IsExpanded = true
			}
			if i == len(parts)-1 {
				child.IsDir = match.Entry.IsDir
				child.IsSymlink = match.Entry.IsSymlink
				if !child.IsDir {
					child.Children = nil
				}
				m.filterMatchPos[child.Path] = append([]int(nil), match.NamePositions...)
			}
			parent = child
		}
	}

	var sortTree func(*TreeNode)
	sortTree = func(node *TreeNode) {
		if node == nil {
			return
		}
		m.sortChildren(node.Children)
		for _, child := range node.Children {
			sortTree(child)
		}
	}
	sortTree(root)
	m.rebuildVisibleNodes()
	// Construction must establish an actionable ranked selection. The default
	// root cursor makes Enter merely collapse the result tree after filtering.
	for _, match := range matches {
		if index := m.visibleIndexLocked(filepath.Clean(match.Entry.Path)); index >= 0 {
			m.setCursorIndexLocked(index)
			break
		}
	}
	return m
}

// NewSearchModelFromBase preserves complete Git and diagnostic metadata, even
// for unloaded search matches, without a resolver or filesystem work.
func NewSearchModelFromBase(base *FileTreeModel, matches []PathMatch) *FileTreeModel {
	if base == nil {
		return NewSearchModel(".", ModelOptions{}, matches)
	}
	m := NewSearchModel(base.RootPath(), ModelOptions{}, matches)
	m.showHidden = base.ShowHidden()
	for path, node := range m.nodes {
		source := base.NodeByPath(path)
		if source != nil {
			node.ModTime = source.ModTime
		}
	}
	m.RefreshStatusFrom(base)
	m.rebuildVisibleNodes()
	return m
}

// RefreshStatusFrom updates only presentation metadata on an existing search
// projection. Its selection, disclosure and scroll remain privately owned.
func (m *FileTreeModel) RefreshStatusFrom(base *FileTreeModel) {
	if m == nil || base == nil || m == base {
		return
	}
	base.mu.RLock()
	statuses := maps.Clone(base.remoteGitStatuses)
	if base.gitResolver != nil {
		statuses = make(map[string]GitStatus)
		for path, status := range base.gitResolver.AllStatuses() {
			statuses[filepath.Join(base.gitResolver.RepoPath(), filepath.FromSlash(path))] = status
		}
	}
	diagnostics := maps.Clone(base.diagnosticResolver)
	base.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.remoteGitStatuses = statuses
	for path, node := range m.nodes {
		node.GitStatus = statuses[path]
	}
	m.diagnosticResolver = diagnostics
	m.recomputeAggregateStatuses()
	m.recomputeAggregateDiagnostics()
}

func fuzzyPathScore(value, query string) (int, []int, bool) {
	value = filepath.ToSlash(value)
	return fuzzySequenceScore(value, query)
}

func fuzzySequenceScore(value, query string) (int, []int, bool) {
	positions, score, ok := textsearch.Fuzzy(value, strings.TrimSpace(query), false)
	return score, positions, ok
}
