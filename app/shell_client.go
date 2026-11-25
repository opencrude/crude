package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"crude/assets"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/core/remoteconn"
	"crude/core/shellport/remote"
	"crude/internal/clientstate"
	"crude/internal/metrics"
	"crude/internal/perfdata"
	"crude/tui"
	"crude/tui/desktopnotify"
	startuptui "crude/tui/startup"
	"crude/types/statuscenter"
	"crude/workspace"
)

// ErrClientProfileBusy is a safe refusal, never permission to displace a shell.
var ErrClientProfileBusy = errors.New("private client profile is already in use")

var ErrInvocationConfiguration = errors.New("invalid invocation configuration")

func validateConnectedInvocation(flags CLIFlags) error {
	if flags.extraPaths || flags.Path != "" && (flags.Workspace != "" || flags.Core || flags.CoreStatus || flags.CoreStop || flags.CoreStdio || flags.Server || flags.MCP || flags.ListSessions || flags.DeleteSession != "") {
		return fmt.Errorf("%w: give one file or folder path for the editor, without --workspace or administration flags", ErrInvocationConfiguration)
	}
	if flags.SSHHost == "" && (flags.SSHUser != "" || flags.SSHPort != 0 || flags.SSHIdentity != "") {
		return fmt.Errorf("%w: --ssh-user/--ssh-port/--ssh-identity require --ssh", ErrInvocationConfiguration)
	}
	if flags.SSHHost != "" {
		if flags.Workspace == "" || strings.ContainsAny(flags.Workspace, "\x00\r\n") {
			return fmt.Errorf("%w: --ssh requires an explicit --workspace remote path", ErrInvocationConfiguration)
		}
		if flags.Core || flags.CoreStdio || flags.CoreStatus || flags.CoreStop || flags.Server || flags.ListSessions || flags.DeleteSession != "" {
			return fmt.Errorf("%w: SSH attach cannot be combined with local core/session administration", ErrInvocationConfiguration)
		}
	}
	if flags.APIKey != "" && flags.Account == "" {
		return fmt.Errorf("%w: --key requires --account", ErrInvocationConfiguration)
	}
	return nil
}

func invocationRoot(flags CLIFlags) (string, error) {
	if err := validateConnectedInvocation(flags); err != nil {
		return "", err
	}
	if flags.SSHHost != "" {
		return flags.Workspace, nil
	}
	if flags.Path != "" {
		return filepath.Abs(flags.Path)
	}
	if flags.Workspace != "" {
		return filepath.Abs(flags.Workspace)
	}
	return os.Getwd()
}

func invocationSelection(flags CLIFlags) workspace.InvocationSelection {
	return workspace.InvocationSelection{AccountID: flags.Account, Model: flags.Model,
		ProviderID: flags.Provider, APIKey: flags.APIKey, Instructions: flags.SystemPrompt}
}

func shellDialer(home string, flags CLIFlags) remote.Dialer {
	if flags.CoreHandover != "" {
		return newCoreRestartDialer(home, flags.CoreHandover)
	}
	if flags.SSHHost == "" {
		return func(ctx context.Context, clientID string) (*coreclient.Client, error) {
			return ConnectCore(ctx, home, clientID)
		}
	}
	options := corewire.SSHOptions{Host: flags.SSHHost, User: flags.SSHUser, Port: flags.SSHPort, IdentityFile: flags.SSHIdentity}
	return remoteSSHDialer(home, options)
}

func draftNamespace(flags CLIFlags, workspaceID string) string {
	if flags.SSHHost == "" {
		return workspaceID
	}
	return fmt.Sprintf("ssh-%x:%s", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", flags.SSHHost, flags.SSHUser, flags.SSHPort))), workspaceID)
}

func shellClientID(profile, namespace string) string {
	return fmt.Sprintf("shell-%x", sha256.Sum256([]byte(profile+"\x00"+namespace)))
}

func defaultClientProfile() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	identity := ""
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		identity = "tmux:" + os.Getenv("TMUX") + ":" + pane
	} else {
		for _, name := range []string{"TERM_SESSION_ID", "WT_SESSION", "SSH_TTY"} {
			if value := os.Getenv(name); value != "" {
				identity = name + ":" + value
				break
			}
		}
	}
	if identity == "" {
		// tty observes the actual input terminal, not TERM or a process ID.
		cmd := exec.Command("tty")
		cmd.Stdin = os.Stdin
		if output, err := cmd.Output(); err == nil {
			identity = strings.TrimSpace(string(output))
		}
	}
	if identity == "" {
		return "", errors.New("cannot identify a stable terminal profile; specify --client-profile NAME")
	}
	return fmt.Sprintf("terminal-%x", sha256.Sum256([]byte(host+":"+identity))), nil
}

type shellDraftProfile struct {
	store      *clientstate.Store
	attachment clientstate.Attachment
	lock       *flock.Flock
}

func openShellDraftProfile(home, profile string) (*shellDraftProfile, error) {
	if profile == "" || strings.TrimSpace(profile) != profile || len(profile) > clientstate.MaxIDBytes || strings.ContainsAny(profile, "\x00\r\n\t") {
		return nil, clientstate.ErrInvalid
	}
	dir := filepath.Join(DataDir(home), "client-profile-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(profile)))))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("%w: %q; choose another --client-profile", ErrClientProfileBusy, profile)
	}
	path := filepath.Join(DataDir(home), "client-state.db")
	store, err := clientstate.Open(path)
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attachment, err := store.Acquire(ctx, profile, clientstate.AcquireOptions{})
	if errors.Is(err, clientstate.ErrAttached) {
		// This OS lock excludes another running owner. Only now observe the
		// authoritative crash-left epoch and request its exact CAS takeover.
		state, inspectErr := store.Profile(ctx, profile)
		err = inspectErr
		if err == nil {
			attachment, err = store.Acquire(ctx, profile, clientstate.AcquireOptions{Takeover: true, ExpectedEpoch: state.Epoch})
		}
	}
	if err != nil {
		_ = store.Close()
		_ = lock.Unlock()
		return nil, err
	}
	return &shellDraftProfile{store: store, attachment: attachment, lock: lock}, nil
}

func (p *shellDraftProfile) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := p.store.Release(ctx, p.attachment.ClientID, p.attachment.Epoch)
	return errors.Join(err, p.store.Close(), p.lock.Unlock())
}

type coreRestartExit struct {
	host *coreRestartHost
	id   string
}

func runConnectedShell(home, root, resumeID, profile string, flags CLIFlags) error {
	var next coreRestartExit
	if err := runConnectedShellAttachment(home, root, resumeID, profile, flags, &next); err != nil {
		return err
	}
	if next.host == nil || next.id == "" {
		return nil
	}
	// The attachment function has returned through all close/flush barriers.
	// This foreground application process survives the core it is replacing.
	return next.host.RunReady(context.Background(), next.id)
}

func runConnectedShellAttachment(home, root, resumeID, profile string, flags CLIFlags, next *coreRestartExit) (result error) {
	if err := validateConnectedInvocation(flags); err != nil {
		return err
	}
	if profile == "" {
		var err error
		profile, err = defaultClientProfile()
		if err != nil {
			return err
		}
	}
	drafts, err := openShellDraftProfile(home, profile)
	if err != nil {
		return fmt.Errorf("open private composer profile: %w", err)
	}
	defer func() { result = errors.Join(result, drafts.Close()) }()
	lg := &appLogger{}
	ctx := context.Background()
	namespace := draftNamespace(flags, "")
	dial := shellDialer(home, flags)
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	navigation, err := remote.ConnectStartup(connectCtx, remote.Options{Root: root, Logger: lg, Dial: dial, Namespace: namespace, ClientID: shellClientID(drafts.attachment.ClientID, namespace)})
	connectCancel()
	if err != nil {
		return fmt.Errorf("connect core: %w", err)
	}
	defer func() { result = errors.Join(result, navigation.Close()) }()
	inspectCtx, inspectCancel := context.WithTimeout(ctx, 15*time.Second)
	explicit := flags.Workspace != "" || flags.Path != ""
	selection, err := navigation.InspectLaunch(inspectCtx, coreprotocol.StartupInspectRequest{Root: root, Explicit: explicit, AllowFile: flags.Path != ""})
	inspectCancel()
	if err != nil {
		return fmt.Errorf("inspect launch folder: %w", err)
	}
	selectedRoot, ws, startupIssues, err := attachStartupSelection(ctx, root, flags.CoreHandover != "", explicit, selection,
		func(ctx context.Context, initial coreprotocol.StartupSnapshot) (startuptui.Selection, error) {
			selected, err := startuptui.Run(ctx, navigation, initial)
			if err == nil && selected.Root != "" && selected.Root != initial.Root {
				resumeID = ""
			}
			return selected, err
		}, func(ctx context.Context, root string, entry coreprotocol.EntryIntent) (*remote.Workspace, error) {
			attachCtx, attachCancel := context.WithTimeout(ctx, 30*time.Second)
			defer attachCancel()
			if !entry.FileOnly {
				selection.FilePath = ""
			}
			return navigation.AttachWorkspace(attachCtx, root, entry)
		})
	if err != nil {
		return err
	}
	if ws == nil {
		return nil
	}
	root = selectedRoot
	defer func() { result = errors.Join(result, ws.Close()) }()
	if err := navigation.Close(); err != nil {
		// The detached navigation session owns no workspace resources. Its
		// close receipt can be lost even when the new attachment is ready.
		// Keep that attachment and report the unconfirmed cleanup separately.
		startupIssues = append(startupIssues, statuscenter.OperationIssue{
			Title:  "The core did not confirm that the startup connection closed.",
			Detail: "Your workspace is open. The core will release the old connection when it expires.",
		})
	}
	ws.AddStartupIssues(startupIssues)
	ws.ConfigureInvocation(invocationSelection(flags))
	manager, err := newRemoteBackendManager(home, profile, lg)
	if err != nil {
		return fmt.Errorf("open client connection store: %w", err)
	}
	manager.ssh = func(c remoteconn.Connection) remote.Dialer {
		return remoteSSHDialer(home, corewire.SSHOptions{Host: c.Host, User: c.User, Port: c.Port, IdentityFile: c.IdentityFile})
	}
	backend, err := rememberInitialBackend(ctx, manager, flags)
	if err != nil {
		return fmt.Errorf("remember connected backend: %w", err)
	}
	if err = manager.RecordUse(ctx, backend, root); err != nil {
		return fmt.Errorf("remember connected workspace: %w", err)
	}
	metrics.Start("shell")
	defer perfdata.RecordArchive(perfdata.ArchivePath(home), "shell", func(err error) {
		lg.Warn("PERFORMANCE", "history_write_failed", "error", err.Error())
	})()
	// Desktop notifications are this shell's effect on this computer's
	// desktop. The core elects one shell per workspace to show them.
	notifier := desktopnotify.Start(desktopnotify.Identity{AppID: "Crude", Name: "Crude", IconPNG: assets.IconPNG, Dir: filepath.Join(DataDir(home), "notifications")})
	defer notifier.Close()
	options := []tui.StartOption{
		tui.WithWorkspaceEntry(selection.FilePath, selection.FileMissing), tui.WithComposerDrafts(drafts.store, drafts.attachment, ws.DraftWorkspaceID()), tui.WithRemoteManager(manager, backend),
		tui.WithDesktopNotifications(notifier),
	}
	var restart *coreRestartHost
	if flags.SSHHost == "" {
		if flags.CoreHandover != "" {
			restart, err = newCoreRestartHostFromManifest(home, root, profile, ws.ControlIdentity(), ws, flags.CoreHandover)
		} else {
			restart, err = newCoreRestartHost(home, root, profile, ws.ControlIdentity(), ws)
		}
		if err != nil {
			if flags.CoreHandover != "" {
				return fmt.Errorf("prepare local checkpoint handover: %w", err)
			}
			// Optional automation storage must not prevent an otherwise usable
			// IDE from opening. Its control surface remains explicitly unavailable.
			lg.Warn("CORE", "checkpoint_restart_unavailable", "error", err.Error())
			restart = nil
		}
		if restart != nil {
			options = append(options, tui.WithCoreRestart(restart))
		}
	}
	terminal, err := tui.Start(ws, providerPresets(), nil, lg, options...)
	if err != nil {
		return fmt.Errorf("start terminal UI: %w", err)
	}
	if resumeID == "last" || resumeID == "true" {
		metas := ws.ListSessions()
		if len(metas) == 0 {
			terminal.ShowOperationIssue(resumeStartupIssue(resumeID, errors.New("no sessions found to resume")))
			resumeID = ""
		} else {
			resumeID = metas[0].ID
		}
	}
	if resumeID != "" {
		terminal.ResumeStartupSession(resumeID)
	}
	terminal.Wait() // Includes the draft flush barrier before profile release.
	if id := terminal.CoreRestartOperationID(); id != "" {
		next.host, next.id = restart, id
	}
	return nil
}

// CLI SSH is already authenticated and attached when it reaches the connection
// catalog. It records that exact authority, not an untested editor draft.
func rememberInitialBackend(ctx context.Context, manager *remoteBackendManager, flags CLIFlags) (string, error) {
	if flags.SSHHost == "" {
		return remoteconn.Local, nil
	}
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%s", flags.SSHHost, flags.SSHUser, flags.SSHPort, flags.SSHIdentity)
	id := fmt.Sprintf("ssh-%x", sha256.Sum256([]byte(identity)))
	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range snapshot.Connections {
		if c.Kind == "ssh" && c.Host == flags.SSHHost && c.User == flags.SSHUser && c.Port == flags.SSHPort && c.IdentityFile == flags.SSHIdentity {
			return c.ID, nil
		}
	}
	return id, manager.store.Save(ctx, remoteconn.Connection{ID: id, Name: flags.SSHHost, Kind: "ssh", Host: flags.SSHHost, User: flags.SSHUser, Port: flags.SSHPort, IdentityFile: flags.SSHIdentity, Carrier: "auto"})
}
