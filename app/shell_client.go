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
	"crude/shellport/remote"
	"crude/tui"
	startuptui "crude/tui/startup"
	"crude/types"
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
	if flags.SSHHost == "" {
		return func(ctx context.Context, clientID string) (*coreclient.Client, error) {
			return ConnectCore(ctx, home, clientID)
		}
	}
	return func(ctx context.Context, clientID string) (*coreclient.Client, error) {
		// The negotiation context does not own the established ssh process. Dial
		// handles handshake cancellation by closing/joining its transport.
		conn, err := corewire.DialSSH(context.Background(), corewire.SSHOptions{
			Host: flags.SSHHost, User: flags.SSHUser, Port: flags.SSHPort, IdentityFile: flags.SSHIdentity})
		if err != nil {
			return nil, fmt.Errorf("start SSH connection: %w", err)
		}
		client, err := coreclient.Dial(ctx, conn, clientID)
		if err != nil {
			return nil, fmt.Errorf("SSH core negotiation failed: %w", err)
		}
		return client, nil
	}
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

func runConnectedShell(home, root, resumeID, profile string, flags CLIFlags) (result error) {
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
	var ws *remote.Workspace
	startupIssues := append([]types.OperationIssue(nil), selection.Issues...)
	for {
		if selection.ShowIntroduction || selection.FolderKind != "" || selection.PathError != "" {
			selected, selectErr := startuptui.Run(ctx, navigation, selection)
			if selectErr != nil {
				return fmt.Errorf("select project: %w", selectErr)
			}
			if selected.Root == "" {
				return nil
			}
			startupIssues = selected.Issues
			if selected.Root != selection.Root {
				resumeID = ""
			}
			root = selected.Root
		} else {
			root = selection.Root
		}
		attachCtx, attachCancel := context.WithTimeout(ctx, 30*time.Second)
		ws, err = remote.Attach(attachCtx, remote.Options{Root: root, Logger: lg, Dial: dial,
			Namespace: namespace, ClientID: shellClientID(drafts.attachment.ClientID, namespace)})
		attachCancel()
		if err == nil {
			break
		}
		// Keep navigation usable when the destination disappears or initialization
		// fails after selection. No old attachment or private draft is discarded.
		selection.Root, selection.PathError = root, err.Error()
		selection.ShowIntroduction = false
		selection.Issues = startupIssues
	}
	defer func() { result = errors.Join(result, ws.Close()) }()
	if err := navigation.Close(); err != nil {
		return err
	}
	ws.AddStartupIssues(startupIssues)
	ws.ConfigureInvocation(invocationSelection(flags))
	terminal, err := tui.Start(ws, providerPresets(), nil, lg,
		tui.WithComposerDrafts(drafts.store, drafts.attachment, ws.DraftWorkspaceID()))
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
	return nil
}
