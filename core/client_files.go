package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"crude/coreprotocol"
)

// externalReadPath is deliberately distinct from project authority. Only an
// explicit absolute same-user read request may use it; no symlink is followed.
func externalReadPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("external read requires an absolute path")
	}
	path = filepath.Clean(path)
	return workspaceFilePath(filepath.VolumeName(path)+string(filepath.Separator), path, false)
}

func browseClientDirectory(ctx context.Context, root, path string, external bool) ([]coreprotocol.ClientDirectoryEntry, error) {
	var err error
	if external {
		path, err = externalReadPath(path)
	} else {
		path, err = workspaceFilePath(root, path, false)
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]coreprotocol.ClientDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, coreprotocol.ClientDirectoryEntry{
			Name: entry.Name(), Path: filepath.Join(path, entry.Name()),
			IsDir: info.IsDir(), IsSymlink: info.Mode()&os.ModeSymlink != 0, ModTime: info.ModTime(),
		})
	}
	// Reject a directory replaced by a link during enumeration.
	if external {
		_, err = externalReadPath(path)
	} else {
		_, err = workspaceFilePath(root, path, false)
	}
	return out, err
}

// prepareClientFiles is a typed worker hook; the actor captures only its root.
// External methods rely on the transport's authenticated same-user authority,
// never on a project path silently escalating out of the workspace.
func (a *actor) prepareClientFiles(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	switch request.Method {
	case coreprotocol.ClientFilesBrowse, coreprotocol.ClientFilesBrowseExternal, coreprotocol.ClientFilesReadExternal:
	default:
		return nil, false
	}
	root := a.ws.RootPath
	return func(ctx context.Context) coreprotocol.Response {
		if err := ctx.Err(); err != nil {
			return fault(request, "cancelled", err.Error())
		}
		var value any
		var err error
		if request.Method == coreprotocol.ClientFilesReadExternal {
			var in coreprotocol.FileReadRequest
			if err := decode(request.Payload, &in); err != nil {
				return fault(request, "invalid_request", err.Error())
			}
			in.Path, err = externalReadPath(in.Path)
			if err == nil {
				value, err = readFileChunk(ctx, filepath.VolumeName(in.Path)+string(filepath.Separator), in)
			}
		} else {
			var in coreprotocol.FilePathRequest
			if err := decode(request.Payload, &in); err != nil {
				return fault(request, "invalid_request", err.Error())
			}
			value, err = browseClientDirectory(ctx, root, in.Path, request.Method == coreprotocol.ClientFilesBrowseExternal)
		}
		if err != nil {
			return fault(request, "files_failed", err.Error())
		}
		return response(request, value)
	}, true
}
