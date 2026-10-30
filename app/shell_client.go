package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"crude/clientstate"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/metrics"
	"crude/remoteconn"
	"crude/shellport/remote"
	"crude/tui"
	startuptui "crude/tui/startup"
)

// ErrClientProfileBusy is a safe refusal, never permission to displace a shell.
var ErrClientProfileBusy = errors.New("private client profile is already in use")

var ErrInvocationConfiguration = errors.New("invalid invocation configuration")

func validateConnectedInvocation(flags CLIFlags) error {
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
	if flags.Workspace != "" {
		return filepath.Abs(flags.Workspace)
	}
	return os.Getwd()
}

func invocationSelection(flags CLIFlags) coreprotocol.InvocationSelection {
	return coreprotocol.InvocationSelection{AccountID: flags.Account, Model: flags.Model,
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
	var startupID [16]byte
	if _, err := rand.Read(startupID[:]); err != nil {
		connectCancel()
		return err
	}
	clientID := fmt.Sprintf("startup-%x", startupID)
	client, err := dial(connectCtx, clientID)
	connectCancel()
	if err != nil {
		return fmt.Errorf("connect core: %w", err)
	}
	navigation := &remote.StartupClient{Client: client, Dial: dial, ClientID: clientID}
	defer func() { result = errors.Join(result, navigation.Close()) }()
	inspectCtx, inspectCancel := context.WithTimeout(ctx, 15*time.Second)
	selection, err := navigation.Inspect(inspectCtx, root)
	inspectCancel()
	if err != nil {
		return fmt.Errorf("inspect launch folder: %w", err)
	}
	selectedRoot, ws, startupIssues, err := attachStartupSelection(ctx, root, flags.CoreHandover != "", selection,
		func(ctx context.Context, initial coreprotocol.StartupSnapshot) (startuptui.Selection, error) {
			selected, err := startuptui.Run(ctx, navigation, initial)
			if err == nil && selected.Root != "" && selected.Root != initial.Root {
				resumeID = ""
			}
			return selected, err
		}, func(ctx context.Context, root string) (*remote.Workspace, error) {
			attachCtx, attachCancel := context.WithTimeout(ctx, 30*time.Second)
			defer attachCancel()
			return remote.Attach(attachCtx, remote.Options{Root: root, Logger: lg, Dial: dial,
				Namespace: namespace, ClientID: shellClientID(drafts.attachment.ClientID, namespace)})
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
		return err
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
	options := []tui.StartOption{
		tui.WithComposerDrafts(drafts.store, drafts.attachment, ws.DraftWorkspaceID()), tui.WithRemoteManager(manager, backend),
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
