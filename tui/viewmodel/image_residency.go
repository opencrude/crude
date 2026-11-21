package viewmodel

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/imagepreview"
	"crude/tui/window"
	"crude/workspace/fileio"
)

const (
	// Keep a few recent photos ready to revisit, within the same byte envelope
	// as one maximum-sized input. Selected pane images do not compete with
	// this allowance, and a single large dormant raster can be dropped at once.
	warmPaneImageBytes = 64 << 20
	paneImageLoading   = "Loading image…"
	paneImageError     = "Could not load image. Reopen it."
)

// An image snapshot is a private, process-local copy of the accepted file
// bytes, not a filename on the core. Only the store writes and removes it.
type paneImageSnapshot struct {
	store  *paneImageStore
	path   string
	size   int
	digest [sha256.Size]byte
	taken  atomic.Bool
}

// Exactly one tab or rejected-result cleanup can claim a prepared snapshot.
// Duplicate or reordered open results must not unlink a claimed source.
func (s *paneImageSnapshot) take() bool {
	return s != nil && s.taken.CompareAndSwap(false, true)
}

// The store tracks all prepared snapshots, even if the UI never receives the
// command result. It holds no pixels. The mutex protects the inventory only.
type paneImageStore struct {
	mu     sync.Mutex
	paths  map[string]struct{}
	closed bool
}

func (s *paneImageStore) save(ctx context.Context, data []byte) (*paneImageSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "crude-pane-image-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	snapshot := &paneImageSnapshot{store: s, path: name, size: len(data), digest: sha256.Sum256(data)}
	s.mu.Lock()
	if !s.closed && ctx.Err() == nil {
		s.paths[name] = struct{}{}
		s.mu.Unlock()
		return snapshot, nil
	}
	s.mu.Unlock()
	_ = os.Remove(name)
	return nil, context.Canceled
}

func (s *paneImageSnapshot) read(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Permit the exact snapshot to be unlinked on close/reset while a cancelled
	// worker still owns its read handle, including on Windows.
	f, err := fileio.OpenReadSharedDelete(s.path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() != int64(s.size)) {
		err = errors.New("image snapshot changed")
	}
	var data []byte
	if err == nil {
		data = make([]byte, s.size)
		_, err = io.ReadFull(f, data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sha256.Sum256(data) != s.digest {
		return nil, errors.New("image snapshot changed")
	}
	return data, nil
}

func (s *paneImageSnapshot) discard() {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return // Try again when the owning store is retired.
	}
	s.store.mu.Lock()
	delete(s.store.paths, s.path)
	s.store.mu.Unlock()
}

func (s *paneImageStore) close() {
	s.mu.Lock()
	paths := s.paths
	s.paths, s.closed = nil, true
	s.mu.Unlock()
	for path := range paths {
		_ = os.Remove(path)
	}
}

type paneImageViewState struct {
	tab      window.TabID
	snapshot *paneImageSnapshot
	decoded  imagepreview.Decoded
	warm     *list.Element
	failed   bool
}

// Update owns the selection, cache and single load slot. The lane is shared by
// initial snapshot reads and reloads, including across a workspace boundary.
type imageResidency struct {
	store         *paneImageStore
	lane          chan struct{}
	flight        *paneImageLoad
	visible, next []*mainPaneView
	warm          list.List // most recently hidden view first
	warmBytes     int64
	openTabs      int
}

type paneImageLoad struct {
	view       *mainPaneView
	snapshot   *paneImageSnapshot
	win        *window.Window
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
}

type paneImageLoadedMsg struct {
	owner   *paneImageLoad
	decoded imagepreview.Decoded
	err     error
}

func (r *imageResidency) snapshotOwner() (*paneImageStore, chan struct{}) {
	if r.store == nil {
		r.store = &paneImageStore{paths: make(map[string]struct{})}
	}
	if r.lane == nil {
		r.lane = make(chan struct{}, 1)
	}
	return r.store, r.lane
}

func claimPaneImageWork(ctx context.Context, lane chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case lane <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-lane
			return err
		}
		return nil
	}
}

func (m *App) syncImageResidency() tea.Cmd {
	r := &m.imageResidency
	if (r.openTabs == 0 && len(r.visible) == 0) || m.Win == nil || m.mainPanes == nil || m.asyncIOStopped.Load() {
		return nil
	}
	r.next = m.selectedImageViews(r.next[:0])
	if !slices.Equal(r.visible, r.next) {
		// Pin the new selections first, before adding the hidden views to the
		// LRU. A warm tab should never be evicted just as it comes into view.
		for _, v := range r.next {
			if !slices.Contains(r.visible, v) && v.imageState.decoded.Pixels != nil {
				r.removeWarm(v)
				m.presentPaneImage(v)
			}
		}
		for _, v := range r.visible {
			if v.imageState.decoded.Pixels != nil && !slices.Contains(r.next, v) {
				// No hidden screen retains a native placement or placeholders.
				v.image.SetImageResidency(nil, nil, paneImageLoading)
				r.cache(v)
			}
		}
		r.visible, r.next = r.next, r.visible
	}
	clear(r.next)
	r.next = r.next[:0]
	if r.flight != nil {
		if !slices.Contains(r.visible, r.flight.view) {
			m.cancelImageLoad()
		}
		return nil // Keep the slot until the actual reader/decoder returns.
	}
	for _, v := range r.visible {
		if v.imageState.decoded.Pixels == nil && !v.imageState.failed {
			return m.loadPaneImage(v)
		}
	}
	return nil
}

func (m *App) selectedImageViews(dst []*mainPaneView) []*mainPaneView {
	add := func(group window.GroupID) {
		if tab := m.Win.GroupActiveTab(group); tab != nil {
			if view := m.mainPanes.views[tab.ID]; view != nil && view.imageState.snapshot != nil {
				dst = append(dst, view)
			}
		}
	}
	root, active := m.Win.PaneRoot(), m.Win.ActiveGroup()
	bounds := m.mainPaneBounds()
	// The focused pane wins the next slot. Other selected docked panes need
	// pixels too, unless the shared layout suppresses them in compact mode.
	if m.Win.IsFloating(active) || !bounds.Empty() {
		add(active)
	}
	if !bounds.Empty() && !compactPaneLayout(root, bounds) {
		var walk func(*window.PaneNode)
		walk = func(n *window.PaneNode) {
			if n.Split == nil {
				if n.Group != active {
					add(n.Group)
				}
				return
			}
			walk(n.First)
			walk(n.Second)
		}
		walk(root)
	}
	for _, group := range m.Win.FloatingGroupIDs() {
		if group != active {
			add(group)
		}
	}
	return dst
}

func (r *imageResidency) removeWarm(v *mainPaneView) {
	state := &v.imageState
	if state.warm != nil {
		r.warm.Remove(state.warm)
		r.warmBytes -= state.decoded.Bytes
		state.warm = nil
	}
}

func (r *imageResidency) cache(v *mainPaneView) {
	state := &v.imageState
	if state.decoded.Bytes > warmPaneImageBytes {
		state.decoded = imagepreview.Decoded{}
		return
	}
	for r.warmBytes+state.decoded.Bytes > warmPaneImageBytes {
		old := r.warm.Back().Value.(*mainPaneView)
		r.removeWarm(old)
		old.imageState.decoded = imagepreview.Decoded{}
	}
	state.warm = r.warm.PushFront(v)
	r.warmBytes += state.decoded.Bytes
}

func (m *App) loadPaneImage(v *mainPaneView) tea.Cmd {
	r := &m.imageResidency
	ctx, cancel := context.WithCancel(context.Background())
	load := &paneImageLoad{view: v, snapshot: v.imageState.snapshot, win: m.Win,
		generation: m.workspaceSwitchGen.Load(), ctx: ctx, cancel: cancel}
	r.flight = load
	lane, protocol := r.lane, m.terminalImages.protocol()
	command := m.clientIOCommand(ctx, func(owner context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(owner, 2*time.Minute)
		defer cancel()
		msg := paneImageLoadedMsg{owner: load}
		if err := claimPaneImageWork(ctx, lane); err != nil {
			msg.err = err
			return msg
		}
		defer func() { <-lane }()
		data, err := load.snapshot.read(ctx)
		if err == nil {
			msg.decoded, err = imagepreview.Prepare(ctx, data, protocol)
		}
		msg.err = err
		return msg
	})
	return func() tea.Msg {
		if msg := command(); msg != nil {
			return msg
		}
		return paneImageLoadedMsg{owner: load, err: context.Canceled}
	}
}

func (m *App) applyPaneImageLoaded(msg paneImageLoadedMsg) {
	load, r := msg.owner, &m.imageResidency
	if load == nil || r.flight != load {
		return
	}
	r.flight = nil
	defer load.cancel()
	if load.ctx.Err() != nil || m.asyncIOStopped.Load() || load.win != m.Win ||
		load.generation != m.workspaceSwitchGen.Load() || m.mainPanes == nil {
		return
	}
	v := load.view
	if m.mainPanes.views[v.imageState.tab] != v || v.imageState.snapshot != load.snapshot ||
		!slices.Contains(r.visible, v) || errors.Is(msg.err, context.Canceled) {
		return
	}
	if msg.err != nil {
		m.ws.Logger().Debug("UI", "image_snapshot_load", "error", msg.err.Error())
		v.imageState.failed = true
	}
	v.imageState.decoded = msg.decoded
	m.presentPaneImage(v)
}

func (m *App) presentPaneImage(v *mainPaneView) {
	state := &v.imageState
	status := ""
	if state.failed {
		status = paneImageError
	}
	m.configureImageGraphics(v.image)
	v.image.SetImageResidency(state.decoded.Pixels, state.decoded.Resource, status)
	m.forceFrame()
}

func (m *App) discardPaneImageSnapshot(s *paneImageSnapshot) {
	m.runDetachedAsyncCmd(func() tea.Msg {
		s.discard()
		return nil
	})
}

func (m *App) releaseImageTab(id window.TabID) {
	if m.mainPanes == nil {
		return
	}
	v := m.mainPanes.views[id]
	if v == nil || v.imageState.snapshot == nil {
		return
	}
	r, state := &m.imageResidency, &v.imageState
	if r.flight != nil && r.flight.view == v {
		m.cancelImageLoad()
	}
	r.removeWarm(v)
	r.openTabs--
	v.image.SetImageResidency(nil, nil, "")
	m.discardPaneImageSnapshot(state.snapshot)
	*state = paneImageViewState{}
	delete(m.mainPanes.views, id)
}

func (m *App) cancelImageLoad() {
	if load := m.imageResidency.flight; load != nil {
		load.cancel()
	}
}

func (m *App) detachImageResidency() *paneImageStore {
	r := &m.imageResidency
	m.cancelImageLoad()
	for _, v := range r.visible {
		v.image.SetImageResidency(nil, nil, "")
		v.imageState.decoded = imagepreview.Decoded{}
	}
	for next := r.warm.Front(); next != nil; next = next.Next() {
		v := next.Value.(*mainPaneView)
		v.imageState.decoded = imagepreview.Decoded{}
		v.imageState.warm = nil
	}
	r.warm.Init()
	r.warmBytes, r.openTabs = 0, 0
	r.visible, r.next = nil, nil
	store := r.store
	r.store = nil
	return store
}

func (m *App) resetImageResidency() {
	if store := m.detachImageResidency(); store != nil {
		m.runDetachedAsyncCmd(func() tea.Msg {
			store.close()
			return nil
		})
	}
}

// Stop runs after both the client I/O and detached command bodies have joined.
func (m *App) stopImageResidency() {
	if store := m.detachImageResidency(); store != nil {
		store.close()
	}
	m.imageResidency.flight = nil
	clear(m.imagePlacements)
	m.imagePlacements = nil
}
