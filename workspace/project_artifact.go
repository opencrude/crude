package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// This cooperative read confines resolution to one opened root and rejects
// observed symlinks, nonregular files, replacement and metadata changes. It is
// not a hostile-same-user filesystem sandbox or a hard kernel-I/O deadline.
type projectArtifactStamp struct {
	info    os.FileInfo
	parents []os.FileInfo
}

func readProjectArtifact(ctx context.Context, root, path string, budget int64) (_ []byte, _ projectArtifactStamp, resultErr error) {
	if !validProjectArtifactPath(path) || budget < 0 || budget > projectContextMaxBytes {
		return nil, projectArtifactStamp{}, ErrProjectContextSelection
	}
	if err := ctx.Err(); err != nil {
		return nil, projectArtifactStamp{}, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, projectArtifactStamp{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	parents, err := projectArtifactParents(dir, path)
	if err != nil {
		return nil, projectArtifactStamp{}, err
	}
	before, err := dir.Lstat(path)
	if err != nil {
		return nil, projectArtifactStamp{}, err
	}
	if !before.Mode().IsRegular() || before.Size() > budget {
		return nil, projectArtifactStamp{}, ErrProjectContextSelection
	}
	file, err := openProjectArtifact(dir, path)
	if err != nil {
		return nil, projectArtifactStamp{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !sameAnalysisDisk(before, opened) {
		return nil, projectArtifactStamp{}, ErrProjectContextChanged
	}
	content, err := io.ReadAll(io.LimitReader(projectContextReader{ctx, file}, budget+1))
	if err != nil {
		return nil, projectArtifactStamp{}, err
	}
	after, err := file.Stat()
	if err != nil || !sameAnalysisDisk(opened, after) {
		return nil, projectArtifactStamp{}, ErrProjectContextChanged
	}
	current, err := dir.Lstat(path)
	if err != nil || !sameAnalysisDisk(after, current) || int64(len(content)) > budget {
		return nil, projectArtifactStamp{}, ErrProjectContextChanged
	}
	again, err := projectArtifactParents(dir, path)
	if err != nil || len(again) != len(parents) {
		return nil, projectArtifactStamp{}, ErrProjectContextChanged
	}
	for i, parent := range parents {
		if !os.SameFile(parent, again[i]) {
			return nil, projectArtifactStamp{}, ErrProjectContextChanged
		}
	}
	return content, projectArtifactStamp{after, parents}, ctx.Err()
}

func validateProjectArtifact(ctx context.Context, root, path string, stamp projectArtifactStamp) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	info, err := dir.Lstat(path)
	if err != nil || !sameAnalysisDisk(stamp.info, info) {
		return ErrProjectContextChanged
	}
	parents, err := projectArtifactParents(dir, path)
	if err != nil || len(parents) != len(stamp.parents) {
		return ErrProjectContextChanged
	}
	for i, parent := range parents {
		if !os.SameFile(parent, stamp.parents[i]) {
			return ErrProjectContextChanged
		}
	}
	return ctx.Err()
}

func projectArtifactParents(root *os.Root, path string) ([]os.FileInfo, error) {
	var parents []os.FileInfo
	current := "."
	for _, component := range strings.Split(filepath.Dir(path), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrProjectContextSelection
		}
		parents = append(parents, info)
	}
	return parents, nil
}

type projectContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r projectContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
