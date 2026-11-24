package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"crude/insight/facts"
	"crude/internal/sysload"
	"crude/types/insight"
	"crude/types/setting"
)

// The fallback covers the eligible files of a launch that cannot report its
// whole project. It borrows a bounded set of current disk views, lets the
// server rest between sets, and yields to editor and agent work. An unanswered
// file does not prevent a different file from producing diagnostics.
//
// When idle (the default) it also yields to the rest of the computer, after
// established background indexers: KDE Baloo pauses on battery power, the
// Windows Search indexer backs off under high system activity, and Linux
// pressure stall information measures that activity as contention for CPU,
// memory and I/O (see package sysload). A file whose server is not running
// waits until the computer has the server's memory hint free. Always turns
// these waits off, as the Windows DisableBackoff policy does; typing and
// agent requests still come first.

// A receipt is either an accepted clean disk view (empty outcome) or an
// unsuccessful attempt. Neither needs a diagnostic payload or document slot.
// Receipts survive eviction just like checked facts. Only a
// source/configuration change or an explicit check clears them. The discovery
// listing bounds this map; it is not a retry queue.
type projectCheckResult struct {
	command, key string
	outcome      string
}

const (
	// The user is idle after this long without an edit or file open.
	projectCheckIdle = 3 * time.Second
	projectCheckPoll = 500 * time.Millisecond
	// A file that has no result in this time waits for a change or explicit check.
	projectCheckFileTimeout = 10 * time.Second
	projectCheckMinGap      = 100 * time.Millisecond
	// Bound borrowed disk views across the whole workspace, not per language.
	projectCheckBatch = 4
	// A file without an answer while the computer or its server was busy is
	// not the file's fault; the check tries again after this long.
	projectCheckRetry = time.Minute
)

// errProjectCheckBusy is a file that got no answer while the computer or its
// server was busy. It is not remembered; the server's other files wait for
// the next pass.
var errProjectCheckBusy = errors.New("the language server was busy")

var errProjectCheckUnsettled = errors.New("the language server did not report this file in time")

// vendoredPath follows the directory conventions of GitHub Linguist's
// vendor.yml (MIT): code that a project copies from elsewhere. Its problems
// show when the file is open or its server reports it, but Crude does not
// spend idle time on it.
var vendoredPath = regexp.MustCompile(`(^|/)((3rd|[Tt]hird)[-_]?[Pp]arty|vendors?|node_modules|bower_components|[Dd]ependencies|deps|extern(al)?|Godeps/_workspace|Carthage|Pods|\.yarn)/|\.min\.(js|css)$`)

// projectCheckAllowedLocked is the user's and the workspace's permission.
func (m *LSPManager) projectCheckAllowedLocked() bool {
	return !m.closed && !m.paused && !m.projectWatchUnreliable && m.editorActiveLocked() &&
		m.settings.Diagnostics && m.settings.ProjectCheckMode() != setting.ProjectCheckOff
}

// cancelProjectCheckLocked stops a running pass; another one starts when
// the check is allowed again.
func (m *LSPManager) cancelProjectCheckLocked() {
	if m.checkCancel != nil {
		m.checkCancel()
	}
	m.checkPending = true
}

// requestProjectCheckLocked asks for another pass. The worker starts it; a
// project without open files or servers still needs the worker for that.
func (m *LSPManager) requestProjectCheckLocked() {
	m.checkPending, m.checkRetryAt = true, time.Time{}
	if !m.closed {
		m.signalLocked()
	}
}

// tickProjectCheckLocked runs on the language-server worker. It settles each
// server's own project report and starts a check pass when one is due.
func (m *LSPManager) tickProjectCheckLocked(now time.Time) {
	for _, state := range m.states {
		m.settleProjectReportLocked(state, now)
	}
	if m.checkRunning || !m.checkPending || m.discovery == nil || m.inventory == nil || !m.projectCheckAllowedLocked() ||
		now.Sub(m.lastEdit) < projectCheckIdle || now.Before(m.checkRetryAt) {
		return
	}
	m.checkPending = false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.checkRunning, m.checkCancel, m.checkDone = true, cancel, done
	go m.runProjectCheck(ctx, cancel, m.settingsGeneration, done)
}

func (m *LSPManager) runProjectCheck(ctx context.Context, cancel context.CancelFunc, generation uint64, done chan struct{}) {
	defer func() {
		cancel()
		m.mu.Lock()
		m.checkRunning, m.checkCancel, m.checkPaths, m.checkTarget, m.checkWait = false, nil, nil, "", ""
		m.bumpVersionLocked()
		close(done)
		m.mu.Unlock()
	}()
	m.mu.Lock()
	inventory := m.inventory
	var anchors []string
	for path, doc := range m.documents {
		if doc.editor {
			anchors = append(anchors, filepath.Dir(path))
		}
	}
	m.checkPaths = make(map[string]bool, projectCheckBatch)
	m.mu.Unlock()
	// Use spare process slots to negotiate all roots before a slow document
	// checker can delay native reporting for an unrelated language. Do not evict
	// another process for discovery, or re-launch servers with current results.
	for _, g := range inventory.groups {
		m.mu.Lock()
		spare := !m.forceFallback && !m.nativeGroupLocked(g) && m.states[g.key] == nil && m.liveCountLocked() < m.settings.MaxServers
		m.mu.Unlock()
		if !spare {
			continue
		}
		for _, path := range g.paths {
			if ctx.Err() != nil {
				return
			}
			m.mu.Lock()
			needed := m.checkNeededLocked(path, g.key)
			m.mu.Unlock()
			if !needed {
				continue
			}
			if !m.awaitProjectCheckTurn(ctx, generation, g.key) {
				return
			}
			_ = m.prepareAnalysisServer(ctx, g)
			break
		}
	}
	for _, group := range inventory.groups {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		native := m.nativeGroupLocked(group)
		m.mu.Unlock()
		if native {
			continue
		}
		paths := orderedFiles(group, anchors)
		m.runAnalysisGroup(ctx, generation, group, paths)
	}
}

// Negotiate before opening a disk view. The existing worker and governor own
// the process; an unknown server may announce whole-project coverage at startup.
func (m *LSPManager) prepareAnalysisServer(ctx context.Context, g *analysisGroup) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer func() { m.mu.Lock(); m.checkLaunch = false; m.mu.Unlock() }()
	for {
		m.mu.Lock()
		if ctx.Err() != nil || !m.projectCheckAllowedLocked() {
			m.mu.Unlock()
			return context.Canceled
		}
		m.checkLaunch, m.checkTarget = true, g.key
		state := m.states[g.key]
		m.signalLocked()
		if state == nil {
			state = &lspClientState{rootURI: g.rootURI, cmd: g.spec.Command, args: g.spec.Args, spec: g.spec, status: LSPIdle, lastUsed: time.Now()}
			m.states[g.key] = state
		}
		switch {
		case state.stopped || state.status == LSPFailed || state.teardownErr != nil:
			m.mu.Unlock()
			return errors.New("language server stopped; restart it to check again")
		case state.waiting:
			m.mu.Unlock()
			return errProjectCheckBusy
		case state.client != nil && state.status == LSPConnected && !state.closing:
			m.mu.Unlock()
			return nil
		}
		changed := m.stateChanged
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return errProjectCheckBusy
		case <-changed:
		}
	}
}

func (m *LSPManager) checkNeededLocked(path, key string) bool {
	if m.documents[path] != nil || m.sources == nil || m.sources.Insight == nil {
		return false
	}
	if r, ok := m.checkResults[path]; ok && r.key == key {
		return false
	}
	store := m.sources.Insight.diagnostics
	return !store.CurrentDocument(checkedFactPrefix+path, key) && !store.CurrentDocument(projectFactPrefix+path, key)
}

func (m *LSPManager) runAnalysisGroup(ctx context.Context, generation uint64, g *analysisGroup, paths []string) {
	m.mu.Lock()
	m.checkTarget = g.key
	m.checkLaunch = false
	m.bumpVersionLocked()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.checkTarget, m.checkWait, m.checkLaunch = "", "", false
		if !m.closed {
			m.signalLocked()
		}
		m.mu.Unlock()
	}()
	var serverErr error
	for len(paths) > 0 && ctx.Err() == nil {
		m.mu.Lock()
		if m.nativeGroupLocked(g) || !m.projectCheckAllowedLocked() {
			m.mu.Unlock()
			return
		}
		batch := make([]string, 0, projectCheckBatch)
		slots := m.projectCheckSlotsLocked()
		// Keep each lock hold bounded even when thousands of files already have answers.
		for n := 0; n < 64 && len(paths) > 0 && len(batch) < max(1, slots); n++ {
			path := paths[0]
			paths = paths[1:]
			if !m.checkNeededLocked(path, g.key) {
				continue
			}
			switch {
			case serverErr != nil:
				m.rememberProjectCheckLocked(path, g.spec.Command, g.key, serverErr.Error())
			case slots == 0:
				m.rememberProjectCheckLocked(path, g.spec.Command, g.key, "The diagnostic store is full. Refresh after closing files to check again.")
			default:
				batch = append(batch, path)
			}
		}
		m.mu.Unlock()
		if len(batch) == 0 {
			continue
		}
		if !m.awaitProjectCheckTurn(ctx, generation, g.key) {
			return
		}
		serverErr = m.prepareAnalysisServer(ctx, g)
		if serverErr != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(serverErr, errProjectCheckBusy) {
				m.deferProjectCheck(ctx, generation)
				return
			}
			paths = append(batch, paths...)
			continue
		}
		m.mu.Lock()
		native := m.nativeGroupLocked(g)
		m.mu.Unlock()
		if native {
			return
		}
		start := time.Now()
		var wg sync.WaitGroup
		errs := make([]error, len(batch))
		for i, path := range batch {
			wg.Go(func() { errs[i] = m.checkClosedFile(ctx, path, g) })
		}
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		m.rebaseLoad()
		for _, err := range errs {
			if errors.Is(err, errProjectCheckBusy) {
				m.deferProjectCheck(ctx, generation)
				return
			}
		}
		// Leave measured idle time between working sets. Waiting for an answer
		// never allocates more views or raises server concurrency.
		if len(paths) > 0 {
			timer := time.NewTimer(max(projectCheckMinGap, time.Since(start)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func (m *LSPManager) deferProjectCheck(ctx context.Context, generation uint64) {
	m.mu.Lock()
	if ctx.Err() == nil && generation == m.settingsGeneration {
		m.checkPending, m.checkRetryAt = true, time.Now().Add(projectCheckRetry)
	}
	m.mu.Unlock()
}

// awaitProjectCheckTurn waits until the check may take the next file of the
// server command. It reports false when the pass must stop.
func (m *LSPManager) awaitProjectCheckTurn(ctx context.Context, generation uint64, key string) bool {
	for {
		// The probe reads the system; never under the manager lock.
		load := m.loadSample()
		m.mu.Lock()
		if ctx.Err() != nil || generation != m.settingsGeneration || !m.projectCheckAllowedLocked() {
			m.mu.Unlock()
			return false
		}
		wait := m.projectCheckWaitLocked(key, load)
		measuring := load.Warming && wait == "" && m.settings.ProjectCheckMode() != setting.ProjectCheckAlways
		if measuring {
			// The first sample has no interval yet; never treat an unmeasured
			// computer as idle. The status keeps its last reason meanwhile.
			wait = m.checkWait
		}
		if m.checkWait != wait {
			m.checkWait = wait
			m.bumpVersionLocked()
		}
		m.mu.Unlock()
		if wait == "" && !measuring {
			return true
		}
		timer := time.NewTimer(projectCheckPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

// projectCheckWaitLocked says why the next file must wait, empty when it may
// start. The user's own work in Crude always comes first.
func (m *LSPManager) projectCheckWaitLocked(key string, load sysload.Sample) string {
	if time.Since(m.lastEdit) < projectCheckIdle {
		return "while you type"
	}
	for path, doc := range m.documents {
		if doc.probes > 0 && (!m.checkPaths[path] || doc.probes > 1) {
			return "while an agent checks code"
		}
	}
	if m.serverBusyLocked(key) {
		// Its own work, for example indexing, is not the computer's load:
		// the server answers the next file after it.
		return "while the language server prepares the project"
	}
	if m.settings.ProjectCheckMode() == setting.ProjectCheckAlways {
		return ""
	}
	if load.PowerKnown && load.OnBattery {
		return "on battery power"
	}
	if load.Busy() {
		return "while the computer is busy"
	}
	state := m.states[key]
	running := state != nil && state.client != nil && !state.closing && !state.stopped
	if !running && load.MemoryKnown && load.AvailableMemory < uint64(m.settings.ServerMemoryMB)<<20 {
		return "for free memory"
	}
	return ""
}

// loadSample observes the computer for the project check.
func (m *LSPManager) loadSample() sysload.Sample {
	if m.loadProbe != nil {
		return m.loadProbe()
	}
	return m.load.Sample()
}

// rebaseLoad starts the next load measurement when the check stops working.
func (m *LSPManager) rebaseLoad() {
	if m.loadProbe == nil {
		m.load.Rebase()
	}
}

// recentContention reports contention over the last ten seconds, the time of
// one file at most. Unknown is not contention.
func (m *LSPManager) recentContention() bool {
	if m.contention != nil {
		return m.contention()
	}
	contended, _ := sysload.RecentContention()
	return contended
}

// serverBusyLocked reports whether a running server of command reports its
// own work in progress.
func (m *LSPManager) serverBusyLocked(key string) bool {
	state := m.states[key]
	if state == nil || state.closing || state.stopped {
		return false
	}
	client, ok := state.client.(lspBusyClient)
	return ok && client.Busy()
}

// checkClosedFile checks one file through a view of it as it is on disk.
// Release and freshness checks share the manager lock.
func (m *LSPManager) checkClosedFile(ctx context.Context, path string, g *analysisGroup) (err error) {
	m.mu.Lock()
	command := g.spec.Command
	if ctx.Err() != nil || !m.projectCheckAllowedLocked() || m.nativeGroupLocked(g) || !m.checkNeededLocked(path, g.key) {
		m.mu.Unlock()
		return nil
	}
	m.checkPaths[path] = true
	generation := m.settingsGeneration
	m.mu.Unlock()
	key := g.key
	defer func() {
		// A file without an answer under load says nothing about the file.
		contended := errors.Is(err, errProjectCheckUnsettled) && ctx.Err() == nil && m.recentContention()
		m.mu.Lock()
		delete(m.checkPaths, path)
		if errors.Is(err, errProjectCheckUnsettled) && (contended || m.serverBusyLocked(key)) {
			err = errProjectCheckBusy
		} else if err != nil && ctx.Err() == nil && generation == m.settingsGeneration && m.checkNeededLocked(path, key) {
			m.rememberProjectCheckLocked(path, command, key, err.Error())
		}
		m.mu.Unlock()
	}()
	fileCtx, cancel := context.WithTimeout(ctx, projectCheckFileTimeout)
	defer cancel()
	probe, err := m.acquireProbe(fileCtx, path, func(s setting.LSP) bool { return s.Diagnostics && s.ProjectCheckMode() != setting.ProjectCheckOff }, "Diagnostics")
	if err != nil {
		return err // the file itself: too large, unreadable or removed
	}
	defer probe.source.release()
	m.mu.Lock()
	diskView := probe.doc.diskView && !probe.doc.editor
	if !diskView {
		m.releaseProbeLocked(path, probe.doc)
	}
	m.mu.Unlock()
	if !diskView {
		return nil
	}
	check := func(ctx context.Context, client LSPClient, path string, view insight.LSPProbeView) error {
		answer, settled := m.awaitProbeDiagnostics(ctx, client, path, view)
		if !settled {
			return errProjectCheckUnsettled
		}
		if answer.Limited || answer.State == insight.Limited {
			return facts.ErrLimit
		}
		return nil
	}
	// A change elsewhere in the project (a build that writes its output, a
	// server that writes its cache) makes Crude send the file again, and the
	// wait for the old send ends early. That is not silence: wait for the
	// answer to the new send within the same time. Only the end of that time
	// says that the server did not answer.
	for {
		m.mu.Lock()
		seen := m.contextGeneration
		m.mu.Unlock()
		err = m.runProbe(fileCtx, probe, check)
		m.mu.Lock()
		interrupted := errors.Is(err, errProbeStateChanged) || (err == nil || errors.Is(err, errProjectCheckUnsettled)) && m.contextGeneration != seen
		if !interrupted || fileCtx.Err() != nil {
			// Releasing the last disk view converts its answer to project coverage.
			// Do that in the same critical section as the freshness check: a sibling
			// file's watch event must not invalidate the answer in between.
			if probe.doc.key != "" {
				key = probe.doc.key
			}
			m.releaseProbeLocked(path, probe.doc)
			m.mu.Unlock()
			break
		}
		m.mu.Unlock()
	}
	if err != nil && ctx.Err() == nil && fileCtx.Err() != nil {
		return errProjectCheckUnsettled
	}
	return err
}

// openFilesCoverage says what a server that reports only open files covers.
func openFilesCoverage(settings setting.LSP) string {
	if settings.Diagnostics && settings.ProjectCheckMode() != setting.ProjectCheckOff {
		return "Checks open files. Crude checks closed files with it while you do not type."
	}
	return "Checks open files only."
}

// sharedDirectories counts the leading directories two paths share.
func sharedDirectories(a, b string) int {
	as := strings.Split(filepath.ToSlash(a), "/")
	bs := strings.Split(filepath.ToSlash(b), "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// invalidateCheckedFactsLocked marks Crude's own results for path (a file or
// a directory) out of date after the file changed; the next pass checks
// them again. A server that reports closed files maintains its own results.
// lspFileDeleted is the LSP FileChangeType of a deleted file or directory.
const lspFileDeleted = 3

// forgetCheckedFactsLocked drops Crude's results for a deleted file or for
// every file below a deleted directory. A file that is gone has no problems;
// an out-of-date row for it would stay in Problems forever, because nothing
// checks a file that does not exist.
func (m *LSPManager) forgetCheckedFactsLocked(path string) {
	changed := false
	if m.sources != nil && m.sources.Insight != nil {
		changed = m.sources.Insight.diagnostics.RemoveMatching(func(b insight.Boundary) bool {
			return isCheckedFact(b) && pathWithin(path, b.Source.Path)
		}) > 0
	}
	for candidate := range m.checkResults {
		if pathWithin(path, candidate) {
			m.dropCheckResultLocked(candidate)
			changed = true
		}
	}
	if changed {
		m.bumpVersionLocked()
	}
}

func (m *LSPManager) invalidateCheckedFactsLocked(path string) {
	// A native reporter may not observe an external edit. Never keep its old
	// result current while waiting for a fresh report (or explicit refresh).
	if m.sources != nil && m.sources.Insight != nil && m.sources.Insight.diagnostics.InvalidateMatching(func(b insight.Boundary) bool {
		return strings.HasPrefix(b.Source.DocumentID, projectFactPrefix) && pathWithin(path, b.Source.Path)
	}, "The file changed after the server checked it.") {
		m.bumpVersionLocked()
	}
	m.invalidateCheckedResultsLocked(func(candidate, _, _ string) bool { return pathWithin(path, candidate) },
		"The file changed after Crude checked it.")
}

// UncheckedFile is a closed file that the project check could not check, and
// why. It is shown to agents so that they can find the cause.
type UncheckedFile struct {
	Path, Command, Outcome string
}

// UncheckedFiles lists the closed files that the project check remembers as
// not checked, sorted by path.
func (m *LSPManager) UncheckedFiles() []UncheckedFile {
	m.mu.Lock()
	var out []UncheckedFile
	for path, result := range m.checkResults {
		if result.outcome == "" {
			continue
		}
		out = append(out, UncheckedFile{Path: path, Command: result.command, Outcome: result.outcome})
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type checkCounts struct{ clean, unchecked int }

func (m *LSPManager) dropCheckResultLocked(path string) {
	r, ok := m.checkResults[path]
	if !ok {
		return
	}
	delete(m.checkResults, path)
	c := m.checkCounts[r.key]
	if r.outcome == "" {
		c.clean--
	} else {
		c.unchecked--
	}
	if c == (checkCounts{}) {
		delete(m.checkCounts, r.key)
	} else {
		m.checkCounts[r.key] = c
	}
}

func (m *LSPManager) rememberProjectCheckLocked(path, command, key, outcome string) {
	if m.checkResults == nil {
		m.checkResults = make(map[string]projectCheckResult)
	}
	m.dropCheckResultLocked(path)
	if m.checkCounts == nil {
		m.checkCounts = make(map[string]checkCounts)
	}
	c := m.checkCounts[key]
	if outcome == "" {
		c.clean++
	} else {
		c.unchecked++
	}
	m.checkCounts[key] = c
	m.checkResults[path] = projectCheckResult{command: command, key: key, outcome: outcome}
	m.bumpVersionLocked()
}

// invalidateCheckedResultsLocked treats stored problems and unusable attempts
// as one check outcome. Ordinary server stops deliberately do not call it.
func (m *LSPManager) invalidateCheckedResultsLocked(match func(path, command, key string) bool, reason string) {
	changed := false
	if m.sources != nil && m.sources.Insight != nil {
		changed = m.sources.Insight.diagnostics.InvalidateMatching(func(b insight.Boundary) bool {
			return isCheckedFact(b) && match(b.Source.Path, b.Producer.Provider, b.Context.ID)
		}, reason)
	}
	for path, result := range m.checkResults {
		if match(path, result.command, result.key) {
			m.dropCheckResultLocked(path)
			changed = true
		}
	}
	for path := range m.checkPaths {
		key := m.inventory.owners[path]
		spec, _ := m.resolveServerLocked(path)
		if match(path, spec.Command, key) {
			m.cancelProjectCheckLocked()
			changed = true
			break
		}
	}
	if changed {
		m.bumpVersionLocked()
		m.requestProjectCheckLocked()
	}
}

// Reserve slots, messages and bytes for editor documents. The store remains
// bounded; a background pass must yield capacity instead of failing a server.
func (m *LSPManager) projectCheckSlotsLocked() int {
	if m.sources == nil || m.sources.Insight == nil {
		return 0
	}
	usage := m.sources.Insight.diagnostics.Usage()
	if usage.Diagnostics > usage.MaxDiagnostics-min(1024, usage.MaxDiagnostics/8) || usage.Bytes >= facts.MaxRetainedBytes-(1<<20) {
		return 0
	}
	return min(projectCheckBatch, max(0, usage.MaxDocuments-max(1, min(64, usage.MaxDocuments/8))-usage.Documents))
}

func (m *LSPManager) projectPublicationFitsLocked(p insight.DiagnosticPublication) bool {
	store := m.sources.Insight.diagnostics
	usage := store.Usage()
	return store.CanPublishWithReserve(p, max(1, min(64, usage.MaxDocuments/8)), min(1024, usage.MaxDiagnostics/8), 1<<20)
}
