package filetree

import (
	"path/filepath"

	"crude/tui/ui"
)

// NewPlaceholderFileTreeModel creates a non-loading model for a workspace
// whose root listing is being prepared asynchronously. It performs no
// filesystem I/O, so a project switch can return control to the event loop
// before a large or remote root directory has been read.
func NewPlaceholderFileTreeModel(rootPath string) *FileTreeModel {
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		absPath = rootPath
	}
	root := &TreeNode{
		Name:       filepath.Base(absPath),
		Path:       absPath,
		IsDir:      true,
		Depth:      -1,
		IsExpanded: true,
		Loading:    true,
	}
	return &FileTreeModel{
		rootPath:           absPath,
		Root:               root,
		nodes:              map[string]*TreeNode{absPath: root},
		selectedPath:       absPath,
		sortBy:             SortByType,
		iconResolver:       NewIconResolver(),
		gitResolver:        &GitStatusResolver{repoPath: absPath, statuses: make(map[string]GitStatus)},
		diagnosticResolver: make(map[string]DiagCounts),
		Hover:              ui.NewHover(),
		filterEd:           ui.NewTextEdit(),
	}
}
