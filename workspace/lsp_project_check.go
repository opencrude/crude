package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"crude/insight/facts"
	"crude/sysload"
	ttypes "crude/types"
)

// The project check gives files that are not open the same diagnostics as
// open files, for every language server that does not report them itself.
// It is generic: it borrows the existing view of the file as it is on disk,
// one file at a time, and keeps each result as the file's project result
// (retainDiskViewLocked). Such a view changes nothing else the server sees,
// so other files are not sent again. The check runs only while the user does
// not type and no agent request waits, waits as long as each file took before
// the next one, and stops for a server that fails or does not answer.
//
// When idle (the default) it also yields to the rest of the computer, after
// established background indexers: KDE Baloo pauses on battery power, the
// Windows Search indexer backs off under high system activity, and Linux
// pressure stall information measures that activity as contention for CPU,
// memory and I/O (see package sysload). A file whose server is not running
// waits until the computer has the server's memory hint free. Always turns
// these waits off, as the Windows DisableBackoff policy does; typing and
// agent requests still come first.

// Unusable attempts survive server eviction just like checked facts. Only a
// source/configuration change or an explicit check clears them. The discovery
// listing bounds this map; it is not a retry queue.
type projectCheckResult struct {
	command, key string
	outcome      string
}

type projectCheckProgress struct {
	done, total int
	wait        string // why the pass waits, as Problems says it
}

const (
	// The user is idle after this long without an edit or file open.
	projectCheckIdle = 3 * time.Second
	projectCheckPoll = 500 * time.Millisecond
	// A file that has no result in this time waits for a change or explicit check.
	projectCheckFileTimeout = 10 * time.Second
	projectCheckMinGap      = 100 * time.Millisecond
	// A server that gives no result for this many files in a row is not
	// asked again in this pass.
	projectCheckSilentLimit = 2
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

type projectCheckItem struct {
	path, command string
	rank, depth   int
}

// projectCheckAllowedLocked is the user's and the workspace's permission.
func (m *LSPManager) projectCheckAllowedLocked() bool {
	return !m.closed && !m.paused && !m.projectWatchUnreliable && m.editorActiveLocked() &&
		m.settings.Diagnostics && m.settings.ProjectCheckMode() != ttypes.ProjectCheckOff
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
	if m.checkRunning || !m.checkPending || m.discovery == nil || !m.projectCheckAllowedLocked() ||
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
		m.checkRunning, m.checkCancel, m.checkPath = false, nil, ""
		if m.check != (projectCheckProgress{}) {
			m.check = projectCheckProgress{}
			m.bumpVersionLocked()
		}
		close(done)
		m.mu.Unlock()
	}()
	m.mu.Lock()
	queue := m.projectCheckQueueLocked()
	m.check = projectCheckProgress{total: len(queue)}
	if len(queue) > 0 {
		m.bumpVersionLocked()
	}
	m.mu.Unlock()
	silent := map[string]int{}
	busy := map[string]bool{}
	for _, item := range queue {
		m.mu.Lock()
		capacity := m.projectCheckCapacityLocked(0)
		m.mu.Unlock()
		switch {
		case busy[item.command]:
			// Not this file's fault: it waits for the next pass, unremembered.
		case silent[item.command] < projectCheckSilentLimit && capacity:
			if !m.awaitProjectCheckTurn(ctx, generation, item.command) {
				return
			}
			started := time.Now()
			failed, err := m.checkClosedFile(ctx, item.path)
			if ctx.Err() != nil {
				return
			}
			// The check stops loading the computer now. The next decision
			// measures only the time after it, so Crude's own work on this
			// file is never a reason to wait.
			m.rebaseLoad()
			switch {
			case failed:
				silent[item.command] = projectCheckSilentLimit
			case errors.Is(err, errProjectCheckBusy):
				busy[item.command] = true
				m.mu.Lock()
				if generation == m.settingsGeneration {
					m.checkPending, m.checkRetryAt = true, time.Now().Add(projectCheckRetry)
				}
				m.mu.Unlock()
			case errors.Is(err, errProjectCheckUnsettled):
				silent[item.command]++
			case err == nil:
				silent[item.command] = 0
			}
			// Leave the server at least as much rest as the file needed.
			timer := time.NewTimer(max(projectCheckMinGap, time.Since(started)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		default:
			m.mu.Lock()
			if ctx.Err() == nil && generation == m.settingsGeneration {
				outcome := "The server did not answer earlier files. Restart it to check again."
				if !capacity {
					outcome = "The diagnostic store is full. Refresh after closing files to check again."
				}
				m.rememberProjectCheckLocked(item.path, item.command, "", outcome)
			}
			m.mu.Unlock()
		}
		m.mu.Lock()
		m.check.done++
		m.bumpVersionLocked()
		m.mu.Unlock()
	}
}

// awaitProjectCheckTurn waits until the check may take the next file of the
// server command. It reports false when the pass must stop.
func (m *LSPManager) awaitProjectCheckTurn(ctx context.Context, generation uint64, command string) bool {
	for {
		// The probe reads the system; never under the manager lock.
		load := m.loadSample()
		m.mu.Lock()
		if ctx.Err() != nil || generation != m.settingsGeneration || !m.projectCheckAllowedLocked() {
			m.mu.Unlock()
			return false
		}
		wait := m.projectCheckWaitLocked(command, load)
		measuring := load.Warming && wait == "" && m.settings.ProjectCheckMode() != ttypes.ProjectCheckAlways
		if measuring {
			// The first sample has no interval yet; never treat an unmeasured
			// computer as idle. The status keeps its last reason meanwhile.
			wait = m.check.wait
		}
		if m.check.wait != wait {
			m.check.wait = wait
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
func (m *LSPManager) projectCheckWaitLocked(command string, load sysload.Sample) string {
	if time.Since(m.lastEdit) < projectCheckIdle {
		return "while you type"
	}
	for path, doc := range m.documents {
		if doc.probes > 0 && path != m.checkPath {
			return "while an agent checks code"
		}
	}
	if m.serverBusyLocked(command) {
		// Its own work, for example indexing, is not the computer's load:
		// the server answers the next file after it.
		return "while the language server prepares the project"
	}
	if m.settings.ProjectCheckMode() == ttypes.ProjectCheckAlways {
		return ""
	}
	if load.PowerKnown && load.OnBattery {
		return "on battery power"
	}
	if load.Busy() {
		return "while the computer is busy"
	}
	running := false
	for _, state := range m.states {
		running = running || state.cmd == command && state.client != nil && !state.closing && !state.stopped
	}
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
func (m *LSPManager) serverBusyLocked(command string) bool {
	for _, state := range m.states {
		if state.cmd == command && state.client != nil && !state.closing && !state.stopped {
			if busy, ok := state.client.(lspBusyClient); ok && busy.Busy() {
				return true
			}
		}
	}
	return false
}

// projectCheckQueueLocked lists the files to check, nearest to the files the
// user works on first. Files of one server stay together so that few servers
// run at the same time.
func (m *LSPManager) projectCheckQueueLocked() []projectCheckItem {
	if m.discovery == nil {
		return nil
	}
	root := m.discovery.root
	anchors := make([]string, 0, len(m.documents)+1)
	for path, doc := range m.documents {
		if doc.editor {
			anchors = append(anchors, filepath.Dir(path))
		}
	}
	if m.activeFilePath != "" {
		anchors = append(anchors, filepath.Dir(m.activeFilePath))
	}
	var items []projectCheckItem
	best := map[string]int{}
	for _, language := range m.discovery.languages {
		for _, path := range language.paths {
			rel, err := filepath.Rel(root, path)
			if err != nil || vendoredPath.MatchString(filepath.ToSlash(rel)) {
				continue
			}
			command, ok := m.needsProjectCheckLocked(path)
			if !ok {
				continue
			}
			item := projectCheckItem{path: path, command: command, depth: strings.Count(filepath.ToSlash(rel), "/")}
			for _, anchor := range anchors {
				item.rank = max(item.rank, sharedDirectories(anchor, filepath.Dir(path)))
			}
			if rank, seen := best[command]; !seen || item.rank > rank {
				best[command] = item.rank
			}
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.command != b.command {
			if best[a.command] != best[b.command] {
				return best[a.command] > best[b.command]
			}
			return a.command < b.command
		}
		if a.rank != b.rank {
			return a.rank > b.rank
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		return a.path < b.path
	})
	return items
}

// needsProjectCheckLocked reports the server command that would check path,
// when no view holds the file, its server does not report closed files
// itself, and no current result exists for it.
func (m *LSPManager) needsProjectCheckLocked(path string) (string, bool) {
	if m.documents[path] != nil || m.sources == nil || m.sources.Insight == nil {
		return "", false
	}
	spec, ok := m.resolveServerLocked(path)
	if !ok || spec.DiagnosticScope == ttypes.CoverageProject {
		return "", false
	}
	for _, state := range m.states {
		if state.cmd == spec.Command && state.reportsProject {
			return "", false
		}
	}
	if result, ok := m.checkResults[path]; ok && result.command == spec.Command {
		return "", false
	}
	if fact, ok := m.sources.Insight.diagnostics.SnapshotPath(path); ok && fact.ID != "" &&
		fact.State != ttypes.InsightStale && !fact.Limited && fact.Boundary.Producer.Provider == spec.Command {
		return "", false
	}
	return spec.Command, true
}

// checkClosedFile checks one file through a view of it as it is on disk.
// failed means its server cannot answer now (stopped, failed or missing).
func (m *LSPManager) checkClosedFile(ctx context.Context, path string) (failed bool, err error) {
	m.mu.Lock()
	command, needed := m.needsProjectCheckLocked(path)
	if !needed {
		m.mu.Unlock()
		return false, nil
	}
	m.checkPath = path
	generation := m.settingsGeneration
	m.mu.Unlock()
	key := ""
	defer func() {
		// A file without an answer under load says nothing about the file.
		contended := errors.Is(err, errProjectCheckUnsettled) && ctx.Err() == nil && m.recentContention()
		m.mu.Lock()
		m.checkPath = ""
		if errors.Is(err, errProjectCheckUnsettled) && (contended || m.serverBusyLocked(command)) {
			err = errProjectCheckBusy
		} else if err != nil && ctx.Err() == nil && generation == m.settingsGeneration {
			m.rememberProjectCheckLocked(path, command, key, err.Error())
		}
		m.mu.Unlock()
	}()
	fileCtx, cancel := context.WithTimeout(ctx, projectCheckFileTimeout)
	defer cancel()
	probe, err := m.acquireProbe(fileCtx, path, func(s ttypes.LSPSettings) bool { return s.Diagnostics }, "Diagnostics")
	if err != nil {
		return false, err // the file itself: too large, unreadable or removed
	}
	defer func() {
		m.mu.Lock()
		key = probe.doc.key
		m.mu.Unlock()
		m.releasePreparedProbe(probe)
	}()
	m.mu.Lock()
	diskView := probe.doc.diskView && !probe.doc.editor
	key = probe.doc.key
	m.mu.Unlock()
	if !diskView {
		return false, nil // an editor or agent view owns the file now
	}
	err = m.runProbe(fileCtx, probe, func(ctx context.Context, client LSPClient, path string, view LSPProbeView) error {
		answer, settled := m.awaitProbeDiagnostics(ctx, client, path, view)
		if !settled {
			return errProjectCheckUnsettled
		}
		if answer.Limited || answer.State == ttypes.InsightLimited {
			return facts.ErrLimit
		}
		return nil
	})
	if err != nil && !errors.Is(err, errProjectCheckUnsettled) && ctx.Err() == nil {
		if fileCtx.Err() != nil {
			return false, errProjectCheckUnsettled
		}
		return true, err
	}
	return false, err
}

// openFilesCoverage says what a server that reports only open files covers.
func openFilesCoverage(settings ttypes.LSPSettings) string {
	if settings.Diagnostics && settings.ProjectCheckMode() != ttypes.ProjectCheckOff {
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
		changed = m.sources.Insight.diagnostics.RemoveMatching(func(b ttypes.InsightBoundary) bool {
			return isCheckedFact(b) && pathWithin(path, b.Source.Path)
		}) > 0
	}
	for candidate := range m.checkResults {
		if pathWithin(path, candidate) {
			delete(m.checkResults, candidate)
			changed = true
		}
	}
	if changed {
		m.bumpVersionLocked()
	}
}

func (m *LSPManager) invalidateCheckedFactsLocked(path string) {
	m.invalidateCheckedResultsLocked(func(candidate, _, _ string) bool { return pathWithin(path, candidate) },
		"The file changed after Crude checked it.")
}

func (m *LSPManager) rememberProjectCheckLocked(path, command, key, outcome string) {
	if m.checkResults == nil {
		m.checkResults = make(map[string]projectCheckResult)
	}
	m.checkResults[path] = projectCheckResult{command: command, key: key, outcome: outcome}
	m.bumpVersionLocked()
}

// invalidateCheckedResultsLocked treats stored problems and unusable attempts
// as one check outcome. Ordinary server stops deliberately do not call it.
func (m *LSPManager) invalidateCheckedResultsLocked(match func(path, command, key string) bool, reason string) {
	changed := false
	if m.sources != nil && m.sources.Insight != nil {
		changed = m.sources.Insight.diagnostics.InvalidateMatching(func(b ttypes.InsightBoundary) bool {
			return isCheckedFact(b) && match(b.Source.Path, b.Producer.Provider, b.Context.ID)
		}, reason)
	}
	for path, result := range m.checkResults {
		if match(path, result.command, result.key) {
			delete(m.checkResults, path)
			changed = true
		}
	}
	if m.checkPath != "" && m.checkCancel != nil {
		spec, _ := m.resolveServerLocked(m.checkPath)
		key := ""
		if doc := m.documents[m.checkPath]; doc != nil {
			key = doc.key
		}
		if match(m.checkPath, spec.Command, key) {
			m.cancelProjectCheckLocked()
			changed = true
		}
	}
	if changed {
		m.bumpVersionLocked()
		m.requestProjectCheckLocked()
	}
}

// Reserve slots, messages and bytes for editor documents. The store remains
// bounded; a background pass must yield capacity instead of failing a server.
func (m *LSPManager) projectCheckCapacityLocked(diagnostics int) bool {
	if m.sources == nil || m.sources.Insight == nil {
		return false
	}
	usage := m.sources.Insight.diagnostics.Usage()
	return usage.Documents < usage.MaxDocuments-max(1, min(64, usage.MaxDocuments/8)) &&
		usage.Diagnostics+diagnostics <= usage.MaxDiagnostics-min(1024, usage.MaxDiagnostics/8) &&
		usage.Bytes < facts.MaxRetainedBytes-(1<<20)
}

func (m *LSPManager) projectPublicationFitsLocked(p ttypes.InsightDiagnosticPublication) bool {
	store := m.sources.Insight.diagnostics
	usage := store.Usage()
	return store.CanPublishWithReserve(p, max(1, min(64, usage.MaxDocuments/8)), min(1024, usage.MaxDiagnostics/8), 1<<20)
}
