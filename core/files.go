package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/workspace"
)

// workspaceFilePath rejects escape and every symlink component, including the
// root's parents. Missing suffixes are allowed only for creation destinations.
func workspaceFilePath(root, path string, missing bool) (string, error) {
	if path == "" {
		path = root
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil {
			if !missing || !os.IsNotExist(e) {
				return "", e
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink paths are not allowed")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return path, nil
}

func readFileChunk(ctx context.Context, root string, in coreprotocol.FileReadRequest) (coreprotocol.FileChunk, error) {
	var out coreprotocol.FileChunk
	if in.Offset != 0 || in.Digest != "" || in.Limit < 0 || in.Limit > coreprotocol.MaxFileChunk {
		return out, errors.New("file reads are complete snapshots; continuation is not supported")
	}
	f, err := openCheckedFile(root, in.Path, coreprotocol.MaxFileRead)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	var data []byte
	buf := make([]byte, 32<<10)
	for {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		n, e := f.Read(buf)
		if n > 0 {
			if len(data)+n > coreprotocol.MaxFileRead {
				return out, errors.New("file exceeds 64 MiB")
			}
			_, _ = hash.Write(buf[:n])
			data = append(data, buf[:n]...)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, e
		}
	}
	if err = f.verify(); err != nil {
		return out, err
	}
	if int64(len(data)) != f.before.Size() {
		return out, errors.New("file changed during read")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	return coreprotocol.FileChunk{Path: f.path, NextOffset: int64(len(data)), Total: int64(len(data)), Digest: digest, Data: data, EOF: true}, nil
}

// prepareFiles captures services/root on the actor; workers never traverse mutable
// workspace session maps or carry SQL handles. The actor lifetime joins workers.
func (a *actor) prepareFiles(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	return a.prepareFilesAtRoot(request, a.ws.RootPath)
}

func (a *actor) prepareFilesAtRoot(request coreprotocol.Request, root string) (func(context.Context) coreprotocol.Response, bool) {
	ops, store, history := a.ws.FileOps, a.ws.LocalHistory, a.ws.HistoryService
	if root != a.ws.RootPath {
		ops = workspace.NewFileOpsService(root, nil, a.ws.Logger)
	}
	documents := a.ws.LiveDocuments
	bad := func(code, message string) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, code, message) }, true
	}
	work := func(fn func(context.Context) (coreprotocol.Value, error)) (func(context.Context) coreprotocol.Response, bool) {
		return func(ctx context.Context) coreprotocol.Response {
			if err := ctx.Err(); err != nil {
				return fault(request, "cancelled", err.Error())
			}
			value, err := fn(ctx)
			if err != nil {
				return fault(request, "files_failed", err.Error())
			}
			return responseValue(request, value)
		}, true
	}
	switch request.Method {
	case coreprotocol.FilesBrowse:
		var in coreprotocol.FilePathRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			path, err := workspaceFilePath(root, in.Path, false)
			if err != nil {
				return nil, err
			}
			return wired(workspace.BrowseDirectoryPath(path))
		})
	case coreprotocol.FilesCatalog:
		var in coreprotocol.FileCatalogRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			if _, err := workspaceFilePath(root, root, false); err != nil {
				return nil, err
			}
			return wired(workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: root, DirectoriesOnly: in.DirectoriesOnly, IncludeHidden: in.IncludeHidden}))
		})
	case coreprotocol.FilesRead:
		var in coreprotocol.FileReadRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) { return wired(readFileChunk(ctx, root, in)) })
	case coreprotocol.FilesSearch:
		var in coreprotocol.FileSearchRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		// Cancellation is request-owned; never call global ProjectSearch.Begin/Cancel.
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			if _, err := workspaceFilePath(root, root, false); err != nil {
				return nil, err
			}
			matches, err := fileio.SearchProject(ctx, root, in.Query, in.Options)
			return wire(coreprotocol.FileSearchResult{Matches: matches}), err
		})
	case coreprotocol.FilesCreate, coreprotocol.FilesCreateFolder, coreprotocol.FilesRename, coreprotocol.FilesDelete, coreprotocol.FilesEdit:
		if ops == nil {
			return bad("unavailable", "workspace file operations unavailable")
		}
		var in coreprotocol.FilePathRequest
		var edits coreprotocol.FileEditsRequest
		if request.Method == coreprotocol.FilesEdit {
			if err := decodeRequest(request, &edits); err != nil {
				return bad("invalid_request", err.Error())
			}
		} else if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			var result workspace.FileOperationResultMsg
			if request.Method == coreprotocol.FilesEdit {
				for i := range edits.Edits {
					path, err := workspaceFilePath(root, edits.Edits[i].Path, false)
					if err != nil {
						return nil, err
					}
					edits.Edits[i].Path = path
				}
				if documents == nil {
					return nil, errors.New("shared document authority unavailable")
				}
				result = documents.MutateFiles(ctx, workspace.FileOpApplyEdit, nil, edits.Edits, func() workspace.FileOperationResultMsg { return ops.ApplyWorkspaceEdit(edits.Edits) })
			} else {
				path, err := workspaceFilePath(root, in.Path, request.Method == coreprotocol.FilesCreate || request.Method == coreprotocol.FilesCreateFolder)
				if err != nil {
					return nil, err
				}
				if path == root {
					return nil, errors.New("workspace root mutation forbidden")
				}
				switch request.Method {
				case coreprotocol.FilesCreate:
					if documents == nil {
						return nil, errors.New("shared document authority unavailable")
					}
					result = documents.MutateFiles(ctx, workspace.FileOpCreate, []string{path}, nil, func() workspace.FileOperationResultMsg { return ops.CreateFile(path) })
				case coreprotocol.FilesCreateFolder:
					result = ops.CreateFolder(path)
				case coreprotocol.FilesDelete:
					if documents == nil {
						return nil, errors.New("shared document authority unavailable")
					}
					result = documents.MutateFiles(ctx, workspace.FileOpDelete, []string{path}, nil, func() workspace.FileOperationResultMsg { return ops.DeletePath(path) })
				case coreprotocol.FilesRename:
					target, err := workspaceFilePath(root, in.NewPath, true)
					if err != nil {
						return nil, err
					}
					if target == root {
						return nil, errors.New("workspace root mutation forbidden")
					}
					if documents == nil {
						return nil, errors.New("shared document authority unavailable")
					}
					result = documents.MutateFiles(ctx, workspace.FileOpRename, []string{path, target}, nil, func() workspace.FileOperationResultMsg { return ops.RenamePath(path, target) })
				}
			}
			out := coreprotocol.FileOperationResult{Op: result.Op, Path: result.Path, OldPath: result.OldPath, NewPath: result.NewPath, IsDir: result.IsDir, Detail: result.Detail}
			if result.Err != nil {
				out.Error = result.Err.Error()
			}
			return wire(out), nil
		})
	case coreprotocol.SavedWorkspaces:
		if err := decodeRequest(request, &coreprotocol.Empty{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			return wiredList[coreprotocol.WorkspaceInfos](workspace.ListWorkspaceInfosContext(ctx))
		})
	case coreprotocol.HistoryTimeline, coreprotocol.HistoryFile:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryListRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		if in.Limit <= 0 || in.Limit > 1000 {
			in.Limit = 1000
		}
		return work(func(context.Context) (coreprotocol.Value, error) {
			if request.Method == coreprotocol.HistoryFile {
				return wiredList[coreprotocol.SnapshotRecords](store.FileHistory(in.Path, in.Limit))
			}
			return wiredList[coreprotocol.SnapshotRecords](store.Timeline(in.Limit))
		})
	case coreprotocol.HistoryStats:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		if err := decodeRequest(request, &coreprotocol.Empty{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(context.Context) (coreprotocol.Value, error) { return wired(store.Stats()) })
	case coreprotocol.HistoryPlan, coreprotocol.HistoryPlanPaths, coreprotocol.HistoryPlanMessage, coreprotocol.HistoryPlanAgent:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryPlanRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			switch request.Method {
			case coreprotocol.HistoryPlanPaths:
				return wired(store.PlanRestorePaths(ctx, in.SnapshotID, in.Options, in.Paths))
			case coreprotocol.HistoryPlanMessage:
				if history == nil {
					return nil, errors.New("history lifecycle service unavailable")
				}
				return wired(history.PlanMessageRestore(ctx, in.SessionID, in.MessageID))
			case coreprotocol.HistoryPlanAgent:
				if history == nil {
					return nil, errors.New("history lifecycle service unavailable")
				}
				return wired(history.PlanAgentRestore(ctx, in.SessionID, in.MessageID, in.AgentID))
			default:
				return wired(store.PlanRestore(ctx, in.SnapshotID, in.Options))
			}
		})
	case coreprotocol.HistoryVerify:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryVerifyRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) {
			if in.SnapshotID == "" {
				return wire(coreprotocol.Empty{}), store.VerifyAll(ctx)
			}
			return wire(coreprotocol.Empty{}), store.Verify(ctx, in.SnapshotID)
		})
	case coreprotocol.HistoryPrune:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryPruneRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) { return store.Prune(ctx, in.Policy) })
	case coreprotocol.HistoryCapture:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryCaptureRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (coreprotocol.Value, error) { return wired(store.Snapshot(ctx, in.Options)) })
	}
	return nil, false
}

func filesMutation(method string) bool {
	switch method {
	case coreprotocol.FilesCreate, coreprotocol.FilesCreateFolder, coreprotocol.FilesRename, coreprotocol.FilesDelete, coreprotocol.FilesEdit, coreprotocol.HistoryPrune, coreprotocol.HistoryCapture, coreprotocol.HistoryPlan, coreprotocol.HistoryPlanPaths, coreprotocol.HistoryPlanMessage, coreprotocol.HistoryPlanAgent:
		return true
	}
	return false
}
