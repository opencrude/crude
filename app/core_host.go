package app

import (
	"bufio"
	"context"
	"crude/buildinfo"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/history"
	"crude/metrics"
	"crude/provider"
	"crude/session"
	"crude/workspace"
)

// DefaultCoreGrace is how long a core with zero clients and no active work
// stays available for a returning shell before it shuts down.
const DefaultCoreGrace = 60 * time.Second

// CoreOptions configures one per-user core process.
type CoreOptions struct {
	Home string
	// Server keeps the core running with zero clients until an explicit stop.
	Server bool
	Grace  time.Duration
}

// DataDir is the per-user data directory that owns the core endpoint.
func DataDir(home string) string { return filepath.Join(home, buildinfo.DataDir()) }

// CoreLogPath is where a detached core writes its diagnostics.
func CoreLogPath(home string) string { return filepath.Join(DataDir(home), "core.log") }

// errCoreOwned reports that another live core already serves this data directory.
var errCoreOwned = errors.New("another core already serves this data directory")

// RunCore serves the private local endpoint until shutdown. It is the single
// owner of application configuration, accounts, catalogs, prompt history and
// every retained workspace for this user; shells are clients of it.
func RunCore(opts CoreOptions) error {
	if err := initializeCoreSignals(); err != nil {
		return err
	}
	if opts.Home == "" {
		opts.Home, _ = buildinfo.Home()
	}
	if opts.Grace <= 0 {
		opts.Grace = DefaultCoreGrace
	}
	dataDir := DataDir(opts.Home)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	metrics.Start("core")
	// The core owns Crude's data; the Performance surface reads its disk usage.
	metrics.SetDiskSource(workspace.NewDataUsage(dataDir).Usage)
	lg, err := openCoreLogger(CoreLogPath(opts.Home))
	if err != nil {
		return err
	}
	defer lg.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	lifetime, err := lockCoreLifetime(opts.Home)
	if err != nil {
		return err
	}
	defer func() { _ = lifetime.Close() }()
	listener, err := listenCore(ctx, dataDir)
	if err != nil {
		if errors.Is(err, errCoreOwned) {
			lg.Info("CORE", "already_running")
		}
		return err
	}
	defer func() { _ = listener.Close() }()
	if err := clearSettledCoreOwner(opts.Home); err != nil {
		return err
	}
	owner, err := installCoreOwner(opts.Home)
	if err != nil {
		return err
	}
	defer func() {
		if err := removeCoreOwner(opts.Home, owner.Generation); err != nil {
			lg.Warn("CORE", "owner_release_failed", "error", err.Error())
		}
	}()
	lg.Info("CORE", "listening", "pid", os.Getpid(), "server", opts.Server, "grace", opts.Grace.String())

	// Application state is resolved by the core, never by an attaching shell.
	prepared := PrepareStartup(CLIFlags{})
	sessionsRoot, _ := session.SessionsRoot()
	application, err := New(Opts{FC: prepared.Config, Home: opts.Home, SessionsRoot: sessionsRoot, StartupIssues: prepared.Issues})
	if err != nil {
		return err
	}
	catalog, catalogErr := provider.OpenModelCatalog(filepath.Join(dataDir, "model-catalog.db"), provider.DefaultModelSources())
	if catalogErr != nil {
		lg.Warn("CATALOG", "cache_unavailable", "error", catalogErr.Error())
	} else {
		provider.UseModelCatalog(catalog)
		defer func() { _ = catalog.Close() }()
	}

	var issuesOnce sync.Once
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws, err := application.newWorkspace(root, lg)
		if err != nil {
			return nil, err
		}
		// Startup issues describe the shared configuration; the first opened
		// workspace surfaces them once rather than every attachment repeating them.
		issuesOnce.Do(func() {
			for _, issue := range application.startupIssues {
				ws.QueueStartupIssue(issue)
			}
		})
		return ws, nil
	}, filepath.Join(dataDir, "core-receipts.db"))
	if err != nil {
		return err
	}
	if err := runtime.SetStartupOwner(application); err != nil {
		_ = runtime.Close()
		return err
	}
	prompts, err := history.LoadFile(history.DefaultPath())
	if err != nil {
		lg.Warn("CORE", "history_load_failed", "error", err.Error())
		prompts = history.New()
	}
	if err := runtime.SetPromptHistory(prompts, history.DefaultPath()); err != nil {
		lg.Warn("CORE", "history_install_failed", "error", err.Error())
	}
	runtime.SetLifetimePolicy(opts.Server, opts.Grace, cancel)
	runtime.SetBuildID(BuildID())
	runtime.SetOwnerGeneration(owner.Generation)
	remoteHost, remoteErr := newCoreRemote(ctx, runtime, filepath.Join(dataDir, "remote-access"), application.committedDefaults().Network, lg)
	if remoteHost == nil {
		_ = runtime.Close()
		return remoteErr
	}
	application.remote = remoteHost
	defer remoteHost.Close()
	if remoteErr != nil {
		application.startupIssues = append(application.startupIssues, remoteListenerIssue(remoteErr))
	}

	var background sync.WaitGroup
	// One latest-wins worker republishes committed application defaults on each
	// workspace actor. A commit never waits for it, so an actor can never block
	// on itself, and successive commits cannot be applied out of order.
	defaultsChanged := make(chan struct{}, 1)
	fileChanged := make(chan struct{}, 1)
	stopConfigWatch, watchErr := watchFileConfig(func(FileConfig) {
		select {
		case fileChanged <- struct{}{}:
		default:
		}
	})
	if watchErr != nil {
		lg.Warn("CONFIG", "watch_failed", "error", watchErr.Error())
	} else {
		defer stopConfigWatch()
	}
	application.defaultsChanged = func() {
		select {
		case defaultsChanged <- struct{}{}:
		default:
		}
	}
	background.Add(1)
	go func() {
		defer background.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-fileChanged:
				if err := application.reloadFileConfiguration(); err != nil {
					lg.Warn("CONFIG", "reload_rejected", "error", err.Error())
					for _, ws := range runtime.Workspaces() {
						ws.QueueStartupIssue(remoteListenerIssue(err))
					}
					continue
				}
				if err := runtime.ApplyAppDefaults(ctx, application.committedDefaults()); err != nil {
					lg.Warn("CONFIG", "defaults_publish_failed", "error", err.Error())
				}
			case <-defaultsChanged:
				settings := application.committedDefaults()
				if err := runtime.ApplyAppDefaults(ctx, settings); err != nil {
					lg.Warn("CORE", "defaults_publish_failed", "error", err.Error())
				}
			}
		}
	}()
	if catalog != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case generation := <-catalog.Updates():
					for _, ws := range runtime.Workspaces() {
						ws.ModelCatalogUpdated(generation)
					}
				}
			}
		}()
		catalog.Start()
	}
	if !opts.Server {
		background.Add(1)
		go func() {
			defer background.Done()
			superviseIdleCore(ctx, runtime, opts.Grace, cancel, lg)
		}()
	}

	serveErr := core.Serve(ctx, runtime, listener)
	cancel()
	background.Wait()
	_ = remoteHost.Close()
	lg.Info("CORE", "stopping")
	closeErr := runtime.Close()
	if prompts.Len() > 0 {
		if err := prompts.Save(history.DefaultPath()); err != nil {
			lg.Warn("CORE", "history_save_failed", "error", err.Error())
		}
	}
	if serveErr != nil && !errors.Is(serveErr, context.Canceled) && !errors.Is(serveErr, net.ErrClosed) {
		return errors.Join(serveErr, closeErr)
	}
	return closeErr
}

// superviseIdleCore stops a non-server core only after zero clients and zero
// active work have persisted for the whole grace period. Draining refuses new
// connections atomically, so a racing attach can never reach a closing core.
func superviseIdleCore(ctx context.Context, runtime *core.Runtime, grace time.Duration, stop func(), lg *coreLogger) {
	interval := min(time.Second, max(grace/4, 10*time.Millisecond))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inspect, cancel := context.WithTimeout(ctx, 5*time.Second)
			idle, err := runtime.Idle(inspect, grace)
			cancel()
			if err != nil {
				lg.Warn("CORE", "idle_inspection_failed", "error", err.Error())
				continue
			}
			if idle && runtime.BeginDrain() {
				lg.Info("CORE", "idle_shutdown", "grace", grace.String())
				stop()
				return
			}
		}
	}
}

// listenCore claims the endpoint. A socket left by a crashed core is removed
// only by corewire's lock-guarded reclaim; a live owner is never displaced.
func listenCore(ctx context.Context, dataDir string) (net.Listener, error) {
	listener, err := corewire.ListenLocal(ctx, dataDir)
	if errors.Is(err, corewire.ErrOwned) {
		return nil, errCoreOwned
	}
	if err == nil {
		return listener, nil
	}
	reclaimed, reclaimErr := corewire.ReclaimStale(ctx, dataDir)
	if errors.Is(reclaimErr, corewire.ErrOwned) {
		return nil, errCoreOwned
	}
	if reclaimErr != nil || !reclaimed {
		return nil, errors.Join(err, reclaimErr)
	}
	listener, err = corewire.ListenLocal(ctx, dataDir)
	if errors.Is(err, corewire.ErrOwned) {
		return nil, errCoreOwned
	}
	return listener, err
}

// ConnectCore dials this user's core, starting a detached one when none is
// listening. Concurrent callers converge on the single owner of the endpoint.
func ConnectCore(ctx context.Context, home, clientID string) (*coreclient.Client, error) {
	dataDir := DataDir(home)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	client, err := dialCore(ctx, dataDir, clientID)
	if err == nil {
		// Image identity is not protocol identity: same-cohort clients may use
		// different native executable images without replacing their owner.
		return client, nil
	}
	if isCoreMismatch(err) {
		if err := handoverCore(ctx, home); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, coreclient.ErrNoCarrier) {
		return nil, fmt.Errorf("connect to existing core: %w", err)
	}
	start, cancelStart := context.WithTimeout(ctx, 15*time.Second)
	startErr := prepareCoreStart(start, home)
	cancelStart()
	if startErr != nil && !errors.Is(startErr, errCoreOwned) {
		return nil, fmt.Errorf("confirm core absence: %w", startErr)
	}
	if startErr == nil {
		if err := spawnCore(home); err != nil {
			return nil, fmt.Errorf("start core: %w", err)
		}
	}
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastErr error
	for delay := 20 * time.Millisecond; ; delay = min(delay*2, 250*time.Millisecond) {
		client, err := dialCore(wait, dataDir, clientID)
		if err == nil {
			return client, nil
		}
		if !errors.Is(err, coreclient.ErrNoCarrier) {
			return nil, fmt.Errorf("connect to starting core: %w", err)
		}
		lastErr = err
		select {
		case <-wait.Done():
			return nil, fmt.Errorf("core did not become available (see %s): %w", CoreLogPath(home), lastErr)
		case <-time.After(delay):
		}
	}
}

func dialCore(ctx context.Context, dataDir, clientID string) (*coreclient.Client, error) {
	return coreclient.Dial(ctx, localCarrier(dataDir), clientID)
}

func coreAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

func isCoreMismatch(err error) bool {
	var fault *coreprotocol.Fault
	return errors.As(err, &fault) && (fault.Code == "schema_mismatch" || fault.Code == "build_mismatch")
}

// localCarrier dials the per-user endpoint as a stream-mux carrier. The
// client dials again to resume its session after a lost carrier.
func localCarrier(dataDir string) coreclient.Dialer {
	return func(ctx context.Context) (corewire.Conn, error) {
		conn, err := corewire.DialLocal(ctx, dataDir)
		if coreAbsent(err) {
			// No core is running: sessions live in the core, so none survives.
			return nil, fmt.Errorf("%w: %v", coreclient.ErrNoCarrier, err)
		}
		if err != nil {
			return nil, err
		}
		return corewire.NewMux(conn, corewire.CarrierLocal, true), nil
	}
}

// spawnCore starts `crude --core` detached from the invoking terminal so it
// outlives the shell that started it. A losing concurrent start exits on its own.
func spawnCore(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(DataDir(home), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(CoreLogPath(home), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	image, err := stageCoreImage(home, exe, BuildID())
	if err != nil {
		return fmt.Errorf("stage core executable: %w", err)
	}
	cmd := exec.Command(image, "--core")
	cmd.Env = coreProfileEnv(home, "")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, logFile, logFile
	cmd.Dir = home
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap without waiting on the core's lifetime.
	go func() { _ = cmd.Wait() }()
	return nil
}

// RunCoreStdio terminates one SSH mux and relays its streams to the local core.
// The relay narrows local admission to authenticated SSH authority, never host
// administration. Empty stdin is not a session and must not start a core.
func RunCoreStdio(home string) error {
	input := bufio.NewReader(os.Stdin)
	if _, err := input.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	dataDir := DataDir(home)
	ctx := context.Background()
	conn, err := corewire.DialLocal(ctx, dataDir)
	if err != nil {
		if !coreAbsent(err) {
			return err
		}
		start, cancel := context.WithTimeout(ctx, 15*time.Second)
		startErr := prepareCoreStart(start, home)
		cancel()
		if startErr != nil && !errors.Is(startErr, errCoreOwned) {
			return startErr
		}
		if startErr == nil {
			if err := spawnCore(home); err != nil {
				return err
			}
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			conn, err = corewire.DialLocal(ctx, dataDir)
			if err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			return err
		}
	}
	defer func() { _ = conn.Close() }()
	return coreclient.RelaySSH(ctx, corewire.NewMux(stdioRelay{reader: input}, corewire.CarrierSSH, false), corewire.NewMux(conn, corewire.CarrierLocal, true))
}

// stdioRelay belongs only to the fixed SSH subprocess; closing it releases its
// pipes, not any shell or the retained core it is attached to.
type stdioRelay struct{ reader io.Reader }

func (s stdioRelay) Read(p []byte) (int, error) {
	if s.reader != nil {
		return s.reader.Read(p)
	}
	return os.Stdin.Read(p)
}
func (stdioRelay) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdioRelay) Close() error                { return errors.Join(os.Stdin.Close(), os.Stdout.Close()) }

// CoreStatus queries the running core without starting one.
func CoreStatus(ctx context.Context, home string) (coreprotocol.CoreStatus, error) {
	var status coreprotocol.CoreStatus
	client, err := dialCore(ctx, DataDir(home), core.NewClientID())
	if err != nil {
		return status, fmt.Errorf("core status unavailable: %w", err)
	}
	defer func() { _ = client.Close() }()
	err = client.Call(ctx, "", coreprotocol.CoreStatusMethod, &coreprotocol.Empty{}, &status)
	return status, err
}

// StopCore asks the running core to shut down. Active work refuses the stop
// unless force is set; the result names that work either way.
func StopCore(ctx context.Context, home string, force bool) (coreprotocol.ShutdownResult, error) {
	var result coreprotocol.ShutdownResult
	expected := os.Getenv(expectedCoreOwnerEnv)
	if expected != "" {
		owner, err := readCoreOwner(home)
		if err != nil {
			return result, err
		}
		if force || owner.Generation != expected {
			return result, errors.New("matching helper owner precondition failed")
		}
		exe, err := os.Executable()
		if err != nil {
			return result, err
		}
		if err := verifyCoreImage(exe, owner.Digest); err != nil {
			return result, err
		}
	}
	conn, err := corewire.DialLocal(ctx, DataDir(home))
	if err != nil {
		return result, fmt.Errorf("core stop unavailable: %w", err)
	}
	carrier := corewire.NewMux(conn, corewire.CarrierLocal, true)
	defer func() { _ = carrier.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = carrier.Close() })
	defer stop()
	stream, err := carrier.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		return result, err
	}
	body, err := coreprotocol.EncodeLifecycleStop(force, expected)
	if err != nil {
		return result, err
	}
	if err := corewire.WriteMessage(stream, body); err != nil {
		return result, fmt.Errorf("core stop outcome unknown; inspect the original owner before retrying: %w", err)
	}
	body, err = corewire.NewReader(stream, coreprotocol.LifecycleMaxMessage).Next()
	if err != nil {
		return result, fmt.Errorf("core stop outcome unknown; inspect the original owner before retrying: %w", err)
	}
	result, err = coreprotocol.DecodeLifecycleResult(body)
	_ = stream.CloseWrite()
	return result, err
}

// newWorkspace constructs a retained workspace exactly as an interactive start
// does: the initial badge is the main agent's model, and every workspace shares
// one account manager and the application configuration callbacks.
func (a *App) newWorkspace(root string, lg workspace.Logger) (*workspace.Workspace, error) {
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	a.configMu.Lock()
	fc := a.fc
	a.configMu.Unlock()
	initialModel := FileConfigToSession(fc).GetActiveDefaultCategory().Model
	ws := workspace.NewWorkspace(root, initialModel, session.ResolveUsername(), a.initialSettingsFrom(fc), lg)
	// Construction currently provides these services eagerly. Do not admit a
	// partially initialized workspace if a constructor ever fails silently.
	if ws.Git == nil || ws.Documents == nil || ws.LiveDocuments == nil || ws.Terminals == nil {
		ws.Close()
		return nil, errors.New("workspace construction failed: Git, document and terminal services are required")
	}
	a.configureWorkspace(ws)
	return ws, nil
}

// coreLogger is the core process's diagnostic log; shells never write it.
type coreLogger struct {
	mu   sync.Mutex
	file *os.File
}

func openCoreLogger(path string) (*coreLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &coreLogger{file: file}, nil
}

func (l *coreLogger) write(level, tag, msg string, pairs ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%s %s [%s] %s", time.Now().Format("2006-01-02T15:04:05.000"), level, tag, msg)
	for i := 0; i+1 < len(pairs); i += 2 {
		line += fmt.Sprintf(" %v=%v", pairs[i], pairs[i+1])
	}
	_, _ = fmt.Fprintln(l.file, line)
}

func (l *coreLogger) Info(tag, msg string, pairs ...any)  { l.write("INFO", tag, msg, pairs...) }
func (l *coreLogger) Warn(tag, msg string, pairs ...any)  { l.write("WARN", tag, msg, pairs...) }
func (l *coreLogger) Error(tag, msg string, pairs ...any) { l.write("ERROR", tag, msg, pairs...) }
func (l *coreLogger) Debug(tag, msg string, pairs ...any) {}
func (l *coreLogger) Close() error                        { return l.file.Close() }

var _ workspace.Logger = (*coreLogger)(nil)
