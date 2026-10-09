package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/localhistory"
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
	if in.Offset < 0 || in.Limit < 0 || in.Limit > coreprotocol.MaxFileChunk || in.Offset > 0 && in.Digest == "" {
		return out, errors.New("invalid chunk bounds or missing continuation digest")
	}
	path, err := workspaceFilePath(root, in.Path, false)
	if err != nil {
		return out, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !before.Mode().IsRegular() || before.Size() > coreprotocol.MaxFileRead {
		return out, errors.New("read requires a regular file within 64 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return out, errors.New("file identity changed")
	}
	// Bind exact content to path and stable file metadata (never access time).
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\x00", path, before.Size(), before.ModTime().UnixNano(), before.Mode())
	limit := in.Limit
	if limit == 0 {
		limit = coreprotocol.MaxFileChunk
	}
	data := make([]byte, 0, limit)
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		n, e := f.Read(buf)
		if n > 0 {
			if total+int64(n) > coreprotocol.MaxFileRead {
				return out, errors.New("file exceeds 64 MiB")
			}
			_, _ = hash.Write(buf[:n])
			start, end := max(in.Offset, total), min(in.Offset+int64(limit), total+int64(n))
			if end > start {
				data = append(data, buf[start-total:end-total]...)
			}
			total += int64(n)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, e
		}
	}
	after, err := f.Stat()
	if err != nil {
		return out, err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if _, err = workspaceFilePath(root, path, false); err != nil {
		return out, err
	}
	if !os.SameFile(before, current) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() || total != before.Size() {
		return out, errors.New("file changed during read")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if in.Offset > total || in.Digest != "" && in.Digest != digest {
		return out, errors.New("stale file continuation")
	}
	return coreprotocol.FileChunk{Path: path, Offset: in.Offset, NextOffset: in.Offset + int64(len(data)), Total: total, Digest: digest, Data: data, EOF: in.Offset+int64(len(data)) == total}, nil
}

// prepareFiles captures services/root on the actor; workers never traverse mutable
// workspace session maps or carry SQL handles. The actor lifetime joins workers.
func (a *actor) prepareFiles(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, bool) {
	root, ops, store, history := a.ws.RootPath, a.ws.FileOps, a.ws.LocalHistory, a.ws.HistoryService
	documents := a.ws.LiveDocuments
	bad := func(code, message string) (func(context.Context) coreprotocol.Response, bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, code, message) }, true
	}
	work := func(fn func(context.Context) (any, error)) (func(context.Context) coreprotocol.Response, bool) {
		return func(ctx context.Context) coreprotocol.Response {
			if err := ctx.Err(); err != nil {
				return fault(request, "cancelled", err.Error())
			}
			value, err := fn(ctx)
			if err != nil {
				return fault(request, "files_failed", err.Error())
			}
			return response(request, value)
		}, true
	}
	switch request.Method {
	case coreprotocol.FilesBrowse:
		var in coreprotocol.FilePathRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) {
			path, err := workspaceFilePath(root, in.Path, false)
			if err != nil {
				return nil, err
			}
			return workspace.BrowseDirectoryPath(path)
		})
	case coreprotocol.FilesCatalog:
		var in coreprotocol.FileCatalogRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) {
			if _, err := workspaceFilePath(root, root, false); err != nil {
				return nil, err
			}
			return workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: root, DirectoriesOnly: in.DirectoriesOnly, IncludeHidden: in.IncludeHidden})
		})
	case coreprotocol.FilesRead:
		var in coreprotocol.FileReadRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) { return readFileChunk(ctx, root, in) })
	case coreprotocol.FilesSearch:
		var in coreprotocol.FileSearchRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		// Cancellation is request-owned; never call global ProjectSearch.Begin/Cancel.
		return work(func(ctx context.Context) (any, error) {
			if _, err := workspaceFilePath(root, root, false); err != nil {
				return nil, err
			}
			matches, err := fileio.SearchProject(ctx, root, in.Query, in.Options)
			return coreprotocol.FileSearchResult{Matches: matches}, err
		})
	case coreprotocol.FilesCreate, coreprotocol.FilesCreateFolder, coreprotocol.FilesRename, coreprotocol.FilesDelete, coreprotocol.FilesEdit:
		if ops == nil {
			return bad("unavailable", "workspace file operations unavailable")
		}
		var in coreprotocol.FilePathRequest
		var edits coreprotocol.FileEditsRequest
		if request.Method == coreprotocol.FilesEdit {
			if err := decode(request.Payload, &edits); err != nil {
				return bad("invalid_request", err.Error())
			}
		} else if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) {
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
			return out, nil
		})
	case coreprotocol.SavedWorkspaces:
		if err := decode(request.Payload, &struct{}{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(context.Context) (any, error) { return workspace.ListWorkspaceInfos() })
	case coreprotocol.HistoryTimeline, coreprotocol.HistoryFile:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryListRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		if in.Limit <= 0 || in.Limit > 1000 {
			in.Limit = 1000
		}
		return work(func(context.Context) (any, error) {
			if request.Method == coreprotocol.HistoryFile {
				return store.FileHistory(in.Path, in.Limit)
			}
			return store.Timeline(in.Limit)
		})
	case coreprotocol.HistoryStats:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		if err := decode(request.Payload, &struct{}{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(context.Context) (any, error) { return store.Stats() })
	case coreprotocol.HistoryPlan, coreprotocol.HistoryPlanPaths, coreprotocol.HistoryPlanMessage, coreprotocol.HistoryPlanAgent:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryPlanRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) {
			switch request.Method {
			case coreprotocol.HistoryPlanPaths:
				return store.PlanRestorePaths(ctx, in.SnapshotID, in.Options, in.Paths)
			case coreprotocol.HistoryPlanMessage:
				if history == nil {
					return nil, errors.New("history lifecycle service unavailable")
				}
				return history.PlanMessageRestore(ctx, in.SessionID, in.MessageID)
			case coreprotocol.HistoryPlanAgent:
				if history == nil {
					return nil, errors.New("history lifecycle service unavailable")
				}
				return history.PlanAgentRestore(ctx, in.SessionID, in.MessageID, in.AgentID)
			default:
				return store.PlanRestore(ctx, in.SnapshotID, in.Options)
			}
		})
	case coreprotocol.HistoryApply:
		// Restore must exclude new turn admission for the whole worker lifetime, not
		// merely check active turns at preparation. The parent must install that lease.
		return bad("ownership_blocked", "history restore requires actor-wide turn-admission exclusion through mutation and boundary publication")
	case coreprotocol.HistoryVerify:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryVerifyRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) {
			if in.SnapshotID == "" {
				return struct{}{}, store.VerifyAll(ctx)
			}
			return struct{}{}, store.Verify(ctx, in.SnapshotID)
		})
	case coreprotocol.HistoryPrune:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryPruneRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) { return store.Prune(ctx, in.Policy) })
	case coreprotocol.HistoryCapture:
		if store == nil {
			return bad("unavailable", "local history unavailable")
		}
		var in coreprotocol.HistoryCaptureRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		return work(func(ctx context.Context) (any, error) { return store.Snapshot(ctx, in.Options) })
	}
	return nil, false
}

func filesMutation(method string) bool {
	switch method {
	case coreprotocol.FilesCreate, coreprotocol.FilesCreateFolder, coreprotocol.FilesRename, coreprotocol.FilesDelete, coreprotocol.FilesEdit, coreprotocol.HistoryApply, coreprotocol.HistoryPrune, coreprotocol.HistoryCapture, coreprotocol.HistoryPlan, coreprotocol.HistoryPlanPaths, coreprotocol.HistoryPlanMessage, coreprotocol.HistoryPlanAgent:
		return true
	}
	return false
}

// applyFiles is the owner-only publication hook. No worker calls a workspace
// method that traverses sessions. HistoryApply remains blocked until admission
// exclusion is integrated; this hook must then run before completing the receipt.
func (a *actor) applyFiles(request coreprotocol.Request, result coreprotocol.Response) error {
	if request.Method != coreprotocol.HistoryApply || result.Error != nil {
		return nil
	}
	var out localhistory.RestoreResult
	if err := json.Unmarshal(result.Payload, &out); err != nil {
		return err
	}
	return a.ws.AppendHistoryRestoreEvent(out)
}
