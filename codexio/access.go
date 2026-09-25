package codexio

import "path/filepath"

// Access is a filesystem authorization request. It is not a patch-language
// restriction: the complete tool remains available independently of grants.
type Access struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
}

func (p *Patch) Accesses(root string) []Access {
	var result []Access
	resolve := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		return filepath.Clean(path)
	}
	for _, op := range p.Operations {
		kind := "Update"
		switch op.Kind {
		case AddFile:
			kind = "Create"
		case DeleteFile:
			kind = "Delete"
		}
		result = append(result, Access{Operation: kind, Path: resolve(op.Path)})
		if op.MoveTo != "" {
			result = append(result, Access{Operation: "Update", Path: resolve(op.MoveTo)})
		}
	}
	return result
}
