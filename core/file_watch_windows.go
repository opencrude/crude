package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows"
)

// Windows cannot rename a directory while a descendant has an open directory
// handle. Watch only the root recursively, without descendant handles, and its
// parent non-recursively for root loss. Sibling descendant activity must never
// consume this project's native queue.
type coreWindowsWatcher struct {
	Events     chan fsnotify.Event
	Errors     chan error
	scopes     []*coreWindowsWatchScope
	mu         sync.Mutex
	closed     bool
	stop, done chan struct{}
}

type coreWindowsWatchScope struct {
	handle     windows.Handle
	path       string
	recursive  bool
	rootName   string // The parent observer admits this one directory name only.
	ready      chan struct{}
	overlapped windows.Overlapped
	buffer     []byte
}

func coreWatchPathKey(path string) string { return strings.ToLower(filepath.Clean(path)) }

func openCoreWatchScope(path string, recursive bool, rootName string) (*coreWindowsWatchScope, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, os.NewSyscallError("CreateFile", err)
	}
	return &coreWindowsWatchScope{handle: h, path: path, recursive: recursive, rootName: rootName, ready: make(chan struct{})}, nil
}

func newCoreWatcher(root string) (*coreWindowsWatcher, error) {
	parent, err := openCoreWatchScope(filepath.Dir(root), false, filepath.Base(root))
	if err != nil {
		return nil, err
	}
	w := &coreWindowsWatcher{Events: make(chan fsnotify.Event), Errors: make(chan error), scopes: []*coreWindowsWatchScope{parent}, stop: make(chan struct{}), done: make(chan struct{})}
	project, err := openCoreWatchScope(root, true, "")
	if err == nil {
		w.scopes = append(w.scopes, project)
	} else if !os.IsNotExist(err) {
		_ = windows.CloseHandle(parent.handle)
		return nil, err
	}
	// Missing roots remain admissible as an unreliable parent-only observer;
	// the initial traversal reports the failure and cannot restore reliability.
	var readers sync.WaitGroup
	for _, scope := range w.scopes {
		readers.Add(1)
		go func() { defer readers.Done(); w.read(scope) }()
	}
	go func() {
		readers.Wait()
		close(w.Events)
		close(w.Errors)
		close(w.done)
	}()
	for _, scope := range w.scopes {
		<-scope.ready
	}
	return w, nil
}

func addCoreWatchParent(_ *coreWindowsWatcher, root string) bool {
	return filepath.Dir(root) != root
}

func (*coreWindowsWatcher) Add(string) error { return nil }

func (w *coreWindowsWatcher) Close() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.stop)
		for _, scope := range w.scopes {
			_ = windows.CancelIoEx(scope.handle, nil)
		}
	}
	w.mu.Unlock()
	<-w.done
	w.mu.Lock()
	for _, scope := range w.scopes {
		if scope.handle != windows.InvalidHandle {
			_ = windows.CloseHandle(scope.handle)
			scope.handle = windows.InvalidHandle
		}
	}
	w.mu.Unlock()
	return nil
}

func (w *coreWindowsWatcher) read(scope *coreWindowsWatchScope) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		close(scope.ready)
		w.sendError(err)
		return
	}
	defer func() { _ = windows.CloseHandle(event) }()
	// Windows retains these pointers after ReadDirectoryChanges returns.
	// Keep them in the heap-owned scope and pinned until completion/cancel is
	// joined. A goroutine's stack can move while GetOverlappedResult waits.
	scope.buffer = make([]byte, 64*1024)
	buf := scope.buffer
	var pins runtime.Pinner
	pins.Pin(&scope.overlapped)
	pins.Pin(&buf[0])
	defer pins.Unpin()
	first := true
	for {
		scope.overlapped = windows.Overlapped{HEvent: event}
		_ = windows.ResetEvent(event)
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return
		}
		filter := uint32(windows.FILE_NOTIFY_CHANGE_DIR_NAME)
		if scope.recursive {
			filter |= windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_LAST_WRITE | windows.FILE_NOTIFY_CHANGE_ATTRIBUTES | windows.FILE_NOTIFY_CHANGE_SIZE
		}
		err = windows.ReadDirectoryChanges(scope.handle, &buf[0], uint32(len(buf)), scope.recursive, filter,
			nil, &scope.overlapped, 0)
		w.mu.Unlock()
		if first {
			close(scope.ready)
			first = false
		}
		if err != nil && err != windows.ERROR_IO_PENDING {
			w.sendError(err)
			return
		}
		var n uint32
		err = windows.GetOverlappedResult(scope.handle, &scope.overlapped, &n, true)
		if err != nil {
			if err == windows.ERROR_OPERATION_ABORTED {
				return
			}
			if err == windows.ERROR_NOTIFY_ENUM_DIR {
				if !w.sendError(fsnotify.ErrEventOverflow) {
					return
				}
				continue
			}
			w.sendError(err)
			return
		}
		if n == 0 {
			if !w.sendError(fsnotify.ErrEventOverflow) {
				return
			}
			continue
		}
		for offset := uint32(0); ; {
			if offset+12 > n {
				w.sendError(fsnotify.ErrEventOverflow)
				break
			}
			next := binary.LittleEndian.Uint32(buf[offset:])
			action := binary.LittleEndian.Uint32(buf[offset+4:])
			length := binary.LittleEndian.Uint32(buf[offset+8:])
			if length%2 != 0 || length > n-offset-12 {
				w.sendError(fsnotify.ErrEventOverflow)
				break
			}
			name := make([]uint16, length/2)
			for i := range name {
				name[i] = binary.LittleEndian.Uint16(buf[offset+12+uint32(i)*2:])
			}
			var op fsnotify.Op
			switch action {
			case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_RENAMED_NEW_NAME:
				op = fsnotify.Create
			case windows.FILE_ACTION_REMOVED:
				op = fsnotify.Remove
			case windows.FILE_ACTION_RENAMED_OLD_NAME:
				op = fsnotify.Rename
			case windows.FILE_ACTION_MODIFIED:
				op = fsnotify.Write
			}
			decoded := string(utf16.Decode(name))
			if op != 0 && (scope.rootName == "" || strings.EqualFold(decoded, scope.rootName)) {
				select {
				case w.Events <- fsnotify.Event{Name: filepath.Join(scope.path, decoded), Op: op}:
				case <-w.stop:
					return
				}
			}
			if next == 0 {
				break
			}
			if next < 12 || next > n-offset {
				w.sendError(fsnotify.ErrEventOverflow)
				break
			}
			offset += next
		}
	}
}

func (w *coreWindowsWatcher) sendError(err error) bool {
	select {
	case w.Errors <- err:
		return true
	case <-w.stop:
		return false
	}
}
