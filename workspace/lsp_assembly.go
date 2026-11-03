package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"crude/fileio"
	"crude/insight/catalog"
	"crude/internal/fileuri"
	"crude/third_party/pathwalk"
	"crude/toolstore"
	ttypes "crude/types"
)

// The assembly service turns the catalog, the user's settings, installed
// tools and a bounded file listing into one assembly plan. It runs no project
// code: discovery reads directory entries only, and resolution looks for
// programs without starting them. The language-server manager owns the plan,
// publishes it with its snapshot and starts the project producers it names.

// assemblyMaxEntries bounds discovery. A larger project is reported as
// limited, not scanned further.
const assemblyMaxEntries = 100_000

// discoveredLanguage counts one language's files in the listing.
type discoveredLanguage struct {
	id, name string
	files    int
	sample   string
	depth    int
}

type assemblyDiscovery struct {
	root      string
	languages map[string]*discoveredLanguage
	seen      int
	limited   bool
	err       error
}

type assemblyToolKey struct {
	serverID, command string
}

// SetToolStore installs the shared tool store. The core owns it; nil means
// tools come only from the system path and from settings.
func (m *LSPManager) SetToolStore(store *toolstore.Store) {
	m.mu.Lock()
	m.toolStore = store
	m.mu.Unlock()
}

// Assembly returns the current assembly plan.
func (m *LSPManager) Assembly() ttypes.AssemblyPlan {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ttypes.CloneAssemblyPlan(m.assembly)
}

// RefreshAssembly lists the project again and resolves its tools. The
// workspace starts the assembly service with it; after that, settings
// changes and Start Language Servers run it again.
func (m *LSPManager) RefreshAssembly() {
	m.mu.Lock()
	m.cancelAssemblyInvalidationLocked()
	m.discovery = nil
	m.assemblyActive = true
	m.requestAssemblyLocked()
	m.mu.Unlock()
}

// requestAssemblyLocked starts a new assembly generation. A running older
// generation is cancelled; its result is dropped when it arrives.
func (m *LSPManager) requestAssemblyLocked() {
	if !m.assemblyActive || m.closed || m.workspaceRoot == "" || m.workspaceRoot == "." {
		return
	}
	m.cancelAssemblyInvalidationLocked()
	if m.assemblyCancel != nil {
		m.assemblyCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.assemblyCancel = cancel
	m.assemblyRequested++
	generation := m.assemblyRequested
	root, settings, store, discovery := m.workspaceRoot, ttypes.CloneLSPSettings(m.settings), m.toolStore, m.discovery
	if m.assembly.State == "" || discovery == nil {
		m.assembly.State, m.assembly.Message = ttypes.AssemblyScanning, "Crude is looking at the project files."
		m.bumpVersionLocked()
	}
	m.assemblyWG.Add(1)
	go func() {
		defer m.assemblyWG.Done()
		defer cancel()
		if discovery == nil || discovery.root != root {
			discovery = discoverProjectLanguages(ctx, root, settings)
		}
		plan := resolveAssembly(root, settings, discovery, func(command string, download *catalog.Download) (string, string) {
			return locateTool(ctx, command, root, store, download)
		})
		plan.Generation = generation
		m.publishAssembly(ctx, discovery, plan)
	}()
}

// publishAssembly installs a finished plan unless a newer one was requested
// or the manager closed, and starts the project producers the plan names.
func (m *LSPManager) publishAssembly(ctx context.Context, discovery *assemblyDiscovery, plan ttypes.AssemblyPlan) {
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || plan.Generation != m.assemblyRequested || discovery.root != m.workspaceRoot {
		return
	}
	m.discovery = discovery
	m.assembly = plan
	m.toolPaths = map[assemblyToolKey]string{}
	m.toolMissing = map[assemblyToolKey]string{}
	var producers []ttypes.LSPProjectProducer
	for _, language := range plan.Languages {
		switch language.Status {
		case ttypes.ToolReady:
			if language.Source == ttypes.ToolSourceStore {
				m.toolPaths[assemblyToolKey{language.ServerID, language.Command}] = language.Executable
			}
			if language.Scope != ttypes.CoverageProject || language.Root == "" {
				continue
			}
			spec, ok := m.resolveServerLocked(language.Sample)
			if ok {
				producers = append(producers, ttypes.LSPProjectProducer{Root: language.Root, Spec: spec})
			}
		case ttypes.ToolMissing:
			m.toolMissing[assemblyToolKey{language.ServerID, language.Command}] = language.Reason
		}
	}
	m.setProjectProducersLocked(producers)
	m.recomputeActiveStatusLocked()
}

// discoverProjectLanguages lists the project and counts files per language.
// Hidden folders and symbolic links are not followed. Only ignore files are read.
func discoverProjectLanguages(ctx context.Context, root string, settings ttypes.LSPSettings) *assemblyDiscovery {
	return discoverProjectLanguagesBounded(ctx, root, settings, assemblyMaxEntries)
}

func discoverProjectLanguagesBounded(ctx context.Context, root string, settings ttypes.LSPSettings, maxEntries int) *assemblyDiscovery {
	out := &assemblyDiscovery{root: root, languages: map[string]*discoveredLanguage{}}
	root, err := filepath.Abs(root)
	if err != nil {
		out.err = err
		return out
	}
	ignores := &assemblyIgnores{ctx: ctx, root: root, patterns: make(map[string][]gitignore.Pattern)}
	listing, err := pathwalk.Scan(ctx, pathwalk.Request{Root: root, MaxEntries: maxEntries, MaxWorkers: pathwalk.DefaultWorkers,
		SkipDir: func(path, name string) bool {
			return defaultBrowserSkipDir(path, name) || ignores.ignored(path, true)
		}})
	out.limited = listing.Truncated
	if err == pathwalk.ErrEntryLimit {
		out.limited, err = true, nil
	}
	out.err = assemblyListingError(ctx, listing, err)
	for _, entry := range listing.Entries {
		if entry.IsDir || entry.IsSymlink || ignores.ignored(entry.Path, false) {
			continue
		}
		out.seen++
		id, name, ok := settings.LanguageForPath(entry.Path)
		if !ok {
			continue
		}
		found := out.languages[id]
		if found == nil {
			found = &discoveredLanguage{id: id, name: name, sample: entry.Path, depth: entry.Depth}
			out.languages[id] = found
		}
		found.files++
		if entry.Depth < found.depth || entry.Depth == found.depth && entry.Path < found.sample {
			found.sample, found.depth = entry.Path, entry.Depth
		}
	}
	if ignores.err != nil {
		out.err = errors.Join(out.err, ignores.err)
	}
	return out
}

func assemblyListingError(ctx context.Context, listing pathwalk.Catalog, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if listing.Errors > 0 {
		return errors.Join(err, fmt.Errorf("could not list %d project paths", listing.Errors))
	}
	return err
}

// assemblyIgnores caches each directory's inherited rules. The walk callback
// can run concurrently; bounded rule reads and cache updates share one lock.
type assemblyIgnores struct {
	mu       sync.Mutex
	ctx      context.Context
	root     string
	patterns map[string][]gitignore.Pattern
	bytes    int64
	err      error
}

const assemblyIgnoreBytes = 8 << 20

func (m *assemblyIgnores) ignored(path string, dir bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		m.err = err
		return true
	}
	patterns := m.rules(filepath.Dir(path))
	if m.err != nil {
		return true
	}
	return gitignore.NewMatcher(patterns).Match(strings.Split(filepath.ToSlash(rel), "/"), dir)
}

func (m *assemblyIgnores) rules(dir string) []gitignore.Pattern {
	if err := m.ctx.Err(); err != nil {
		m.err = err
		return nil
	}
	if patterns, ok := m.patterns[dir]; ok {
		return patterns
	}
	var patterns []gitignore.Pattern
	if dir != m.root {
		patterns = append(patterns, m.rules(filepath.Dir(dir))...)
	}
	if m.err != nil {
		return nil
	}
	remaining := int64(assemblyIgnoreBytes) - m.bytes
	data, err := fileio.ReadRegularNoFollow(m.ctx, filepath.Join(dir, ".gitignore"), remaining)
	// Git does not follow worktree ignore symlinks. Other non-regular entries
	// likewise supply no rules; never open a FIFO waiting for a writer.
	if os.IsNotExist(err) || errors.Is(err, fileio.ErrNotRegular) {
		m.patterns[dir] = patterns
		return patterns
	}
	if err != nil {
		m.err = fmt.Errorf("cannot read ignore rules in %s: %w", dir, err)
		return nil
	}
	m.bytes += int64(len(data))
	rel, err := filepath.Rel(m.root, dir)
	if err != nil {
		m.err = err
		return nil
	}
	var domain []string
	if rel != "." {
		domain = strings.Split(filepath.ToSlash(rel), "/")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), assemblyIgnoreBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			patterns = append(patterns, gitignore.ParsePattern(line, domain))
		}
	}
	if err := scanner.Err(); err != nil {
		m.err = err
		return nil
	}
	m.patterns[dir] = patterns
	return patterns
}

// resolveAssembly decides, per discovered language, which tool serves it and
// where it comes from. locate finds a program without running it.
func resolveAssembly(root string, settings ttypes.LSPSettings, discovery *assemblyDiscovery, locate func(string, *catalog.Download) (string, string)) ttypes.AssemblyPlan {
	plan := ttypes.AssemblyPlan{State: ttypes.AssemblyReady, FilesSeen: discovery.seen, Limited: discovery.limited}
	if discovery.err != nil {
		plan.State = ttypes.AssemblyFailed
		plan.Message = "Crude could not list the project files. Check the folder permissions and refresh."
		return plan
	}
	c := catalog.Current()
	enabled := settings
	enabled.Enabled = true
	for _, found := range discovery.languages {
		language := ttypes.AssemblyLanguage{ID: found.id, Name: found.name, Files: found.files, Sample: found.sample}
		catalogLanguage, builtin := c.Language(found.id)
		spec, ok := enabled.ResolveServer(found.sample)
		switch {
		case !ok && builtin && len(catalogLanguage.Servers) == 0:
			language.Status = ttypes.ToolNoServer
			language.Reason = fmt.Sprintf("No language server is known for %s. Editing works without one.", found.name)
		case !ok:
			language.Status = ttypes.ToolOff
			language.Reason = fmt.Sprintf("The language server for %s is off. Turn it on in Language servers settings.", found.name)
		default:
			language.ServerID, language.Command = spec.ServerID, spec.Command
			installReason := ""
			var download *catalog.Download
			if server, known := c.Server(spec.ServerID); known && server.Package != "" {
				language.Package = server.Package
				if pkg, ok := c.Package(server.Package); ok {
					var candidate catalog.Download
					candidate, installReason = pkg.DownloadFor(spec.Command, catalog.CurrentTargets())
					language.Installable = installReason == ""
					if language.Installable {
						download = &candidate
					}
				}
			}
			language.Executable, language.Source = locate(spec.Command, download)
			if spec.ServerID == "" {
				language.Source = ttypes.ToolSourceSettings
			}
			if language.Executable == "" {
				language.Status = ttypes.ToolMissing
				language.Reason = fmt.Sprintf("%s needs %s, which is not installed.", found.name, filepath.Base(spec.Command))
				if language.Installable {
					language.Reason += " Crude can install it."
				} else {
					language.Reason += " " + strings.TrimSpace(installReason+" Install it yourself, then check again.")
				}
				break
			}
			language.Status = ttypes.ToolReady
			language.Scope = ttypes.CoverageOpenFiles
			language.Reason = "Checks open files only."
			if spec.DiagnosticScope == ttypes.CoverageProject {
				language.Scope = ttypes.CoverageProject
				language.Root, _ = fileuri.ToPath(resolveLSPRoot(found.sample, root, spec.RootMarkers, spec.WorkspaceRootMarkers))
				language.Reason = "Checks the whole project, including files that are not open."
			}
		}
		plan.Languages = append(plan.Languages, language)
	}
	sort.Slice(plan.Languages, func(i, j int) bool {
		a, b := plan.Languages[i], plan.Languages[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.ID < b.ID
	})
	plan.Message = assemblyMessage(plan)
	return plan
}

func assemblyMessage(plan ttypes.AssemblyPlan) string {
	ready, missing := 0, 0
	for _, language := range plan.Languages {
		switch language.Status {
		case ttypes.ToolReady:
			ready++
		case ttypes.ToolMissing:
			missing++
		}
	}
	var message string
	switch {
	case len(plan.Languages) == 0:
		message = "Crude found no source files that a language server can check."
	case missing > 0:
		message = fmt.Sprintf("%d of %d languages have a language server. Install the missing servers to check those files.", ready, ready+missing)
	default:
		message = "Every language in this project with a known language server has one installed."
	}
	if plan.Limited {
		message += " The project is large, so Crude looked at only part of it."
	}
	return message
}

// locateTool finds an installed program without running it: an absolute
// path from settings, a tool in the tool store, then the system path.
// Path entries inside the workspace are skipped, so a project cannot supply
// its own server program.
func locateTool(ctx context.Context, command, workspaceRoot string, store *toolstore.Store, download *catalog.Download) (string, string) {
	if command == "" {
		return "", ""
	}
	if filepath.IsAbs(command) {
		if executableFile(command) {
			return command, ttypes.ToolSourceSettings
		}
		return "", ""
	}
	if store != nil && download != nil {
		if tools, _, err := store.List(ctx); err == nil {
			var newest toolstore.Tool
			for _, tool := range tools {
				if managedToolMatches(tool, *download) && tool.InstalledAt.After(newest.InstalledAt) {
					newest = tool
				}
			}
			if newest.Executable != "" && executableFile(newest.Executable) {
				return newest.Executable, ttypes.ToolSourceStore
			}
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) || (workspaceRoot != "" && pathWithin(workspaceRoot, dir)) {
			continue
		}
		for _, name := range executableNames(command) {
			candidate := filepath.Join(dir, name)
			if executableFile(candidate) {
				return candidate, ttypes.ToolSourcePath
			}
		}
	}
	return "", ""
}

func managedToolMatches(tool toolstore.Tool, download catalog.Download) bool {
	return tool.Package == download.Package && tool.Version == download.Version &&
		tool.Source == download.Source && tool.URL == download.URL &&
		tool.Archive == download.Archive && tool.Bin == download.Bin &&
		tool.Command == download.Command
}

func executableNames(command string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(command) != "" {
		return []string{command}
	}
	var names []string
	for _, ext := range filepath.SplitList(os.Getenv("PATHEXT")) {
		if ext != "" {
			names = append(names, command+strings.ToLower(ext))
		}
	}
	if len(names) == 0 {
		names = []string{command + ".exe"}
	}
	return names
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

// assemblyStatusLocked returns the plan's reason for a file whose server is
// not installed, so the status names the missing program.
func (m *LSPManager) assemblyStatusLocked(path string) (string, bool) {
	spec, ok := m.settings.ResolveServer(path)
	if !ok {
		return "", false
	}
	reason, missing := m.toolMissing[assemblyToolKey{spec.ServerID, spec.Command}]
	return reason, missing
}

// joinAssembly waits for a running assembly to finish after Close cancelled it.
func (m *LSPManager) joinAssembly(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		m.assemblyWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
