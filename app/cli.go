package app

import (
	"context"
	"crude/buildinfo"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"time"

	"crude/provider"
	"crude/session"
	"crude/tui/perf"
)

// CLIFlags holds parsed command-line flags.
type CLIFlags struct {
	Version       bool
	APIKey        string
	Account       string
	Model         string
	Provider      string
	SystemPrompt  string
	Resume        string
	ListSessions  bool
	Limit         int
	Offset        int
	DeleteSession string

	// Core process routes. A shell is a client of the per-user core.
	Core                 bool
	Server               bool
	CoreGrace            time.Duration
	CoreStatus           bool
	CoreStop             bool
	Force                bool
	CoreStdio            bool
	CoreHandover         string
	CoreHandoverIdentity bool
	ClientProfile        string
	SSHHost              string
	SSHUser              string
	SSHPort              int
	SSHIdentity          string
	Workspace            string
	MCP                  bool
	MCPEffects           string
	MCPShell             string
	MCPShellEpoch        uint64
}

// ParseFlags parses os.Args flags into CLIFlags.
func ParseFlags() CLIFlags {
	versionFlag := flag.Bool("version", false, "Print version and exit (no core or profile access)")
	apiKeyFlag := flag.String("key", "", "API key for the selected provider account")
	accountFlag := flag.String("account", "", "Explicit account ID for the selected provider")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Session profile instructions (extend canonical policy)")
	resumeFlag := flag.String("resume", "", "Resume session (session ID, 'last', or empty for picker)")
	listSessionsFlag := flag.Bool("list-sessions", false, "List sessions and exit")
	limitFlag := flag.Int("limit", 50, "Number of sessions to list (used with --list-sessions)")
	offsetFlag := flag.Int("offset", 0, "Offset for session listing (used with --list-sessions)")
	deleteSessionFlag := flag.String("delete-session", "", "Delete a session by ID")
	coreFlag := flag.Bool("core", false, "Run this user's core in the foreground")
	serverFlag := flag.Bool("server", false, "With --core: keep running with no attached shells until --core-stop")
	graceFlag := flag.Duration("core-grace", DefaultCoreGrace, "With --core: idle time with no shells and no active work before the core exits")
	coreStatusFlag := flag.Bool("core-status", false, "Print the running core's clients, workspaces and active work")
	coreStopFlag := flag.Bool("core-stop", false, "Stop the running core (refused while work is active unless --force)")
	forceFlag := flag.Bool("force", false, "With --core-stop: stop even though work is active")
	coreStdioFlag := flag.Bool("core-stdio", false, "Relay one core connection over stdin/stdout (remote attach)")
	coreHandoverFlag := flag.String("core-handover", "", "Continue one exact private checkpoint handover in this terminal")
	coreHandoverIdentityFlag := flag.Bool("core-handover-identity", false, "Print bounded read-only handover image identity as JSON")
	clientProfileFlag := flag.String("client-profile", "", "Private durable composer profile (exclusive while this shell is running)")
	sshHostFlag := flag.String("ssh", "", "Attach to a remote core through system SSH (host or configured alias)")
	sshUserFlag := flag.String("ssh-user", "", "SSH login user")
	sshPortFlag := flag.Int("ssh-port", 0, "SSH port (zero uses SSH configuration)")
	sshIdentityFlag := flag.String("ssh-identity", "", "Local SSH identity file")
	workspaceFlag := flag.String("workspace", "", "Workspace path on the selected core (required with --ssh)")
	mcpFlag := flag.Bool("mcp", false, "Expose Crude semantic control as an MCP stdio server (no interactive shell)")
	mcpEffectsFlag := flag.String("mcp-effects", "", "With --mcp: immutable allowed effects, comma-separated (default read)")
	mcpShellFlag := flag.String("mcp-shell", "", "With --mcp: exact registered target shell client ID (never inferred)")
	mcpShellEpochFlag := flag.Uint64("mcp-shell-epoch", 0, "With --mcp-shell: exact registered shell epoch")
	flag.Parse()

	return CLIFlags{
		Version:              *versionFlag,
		APIKey:               *apiKeyFlag,
		Account:              *accountFlag,
		Model:                *modelFlag,
		Provider:             *providerFlag,
		SystemPrompt:         *sysPromptFlag,
		Resume:               *resumeFlag,
		ListSessions:         *listSessionsFlag,
		Limit:                *limitFlag,
		Offset:               *offsetFlag,
		DeleteSession:        *deleteSessionFlag,
		Core:                 *coreFlag,
		Server:               *serverFlag,
		CoreGrace:            *graceFlag,
		CoreStatus:           *coreStatusFlag,
		CoreStop:             *coreStopFlag,
		Force:                *forceFlag,
		CoreStdio:            *coreStdioFlag,
		CoreHandover:         *coreHandoverFlag,
		CoreHandoverIdentity: *coreHandoverIdentityFlag,
		ClientProfile:        *clientProfileFlag,
		SSHHost:              *sshHostFlag,
		SSHUser:              *sshUserFlag,
		SSHPort:              *sshPortFlag,
		SSHIdentity:          *sshIdentityFlag,
		Workspace:            *workspaceFlag,
		MCP:                  *mcpFlag,
		MCPEffects:           *mcpEffectsFlag,
		MCPShell:             *mcpShellFlag,
		MCPShellEpoch:        *mcpShellEpochFlag,
	}
}

// ResolveConfig loads FileConfig and applies invocation-local CLI selectors.
func ResolveConfig(flags CLIFlags) (FileConfig, error) {
	return resolveConfigFrom(LoadFileConfig(), flags)
}

func resolveConfigFrom(fc FileConfig, flags CLIFlags) (FileConfig, error) {
	// Stage the exact invocation selection before validation. An unavailable
	// --account must not leave another saved account selected in the usable shell.
	fc.Accounts = cloneAccounts(fc.Accounts)
	if fc.Agents != nil {
		agents := *fc.Agents
		agents.Profiles = maps.Clone(agents.Profiles)
		for name, profile := range agents.Profiles {
			profile.Categories = maps.Clone(profile.Categories)
			agents.Profiles[name] = profile
		}
		fc.Agents = &agents
	}
	if flags.Model != "" || flags.Account != "" || flags.Provider != "" {
		if fc.Agents == nil {
			agents := session.DefaultAgentsConfig()
			fc.Agents = &agents
		}
		ap := fc.Agents.ActiveProfile
		if ap == "" {
			ap = "default"
			fc.Agents.ActiveProfile = ap
		}
		if fc.Agents.Profiles == nil {
			fc.Agents.Profiles = make(map[string]session.ProfileConfig)
		}
		prof := fc.Agents.Profiles[ap]
		if prof.Categories == nil {
			prof.Categories = make(map[string]session.CategoryConfig)
		}
		main := prof.Categories[session.CategoryDefault]
		if flags.Model != "" {
			main.Model = flags.Model
		}
		if flags.Account != "" {
			main.AccountID = flags.Account
		}
		prof.Categories[session.CategoryDefault] = main
		fc.Agents.Profiles[ap] = prof
	}
	if flags.APIKey != "" && flags.Account == "" {
		return fc, fmt.Errorf("--key requires an explicit --account")
	}

	profileName := "default"
	var mainCat session.CategoryConfig
	if fc.Agents != nil {
		if fc.Agents.ActiveProfile != "" {
			profileName = fc.Agents.ActiveProfile
		}
		profile := fc.Agents.Profiles[profileName]
		mainCat = profile.Categories[session.CategoryDefault]
	}

	accountID := flags.Account
	if accountID == "" && flags.Resume == "" {
		accountID = mainCat.AccountID
	}
	var selectedAccount provider.Account
	if accountID != "" {
		var exists bool
		selectedAccount, exists = fc.Accounts[accountID]
		if !exists {
			if flags.Account == "" || flags.APIKey == "" {
				return fc, fmt.Errorf("account %q not found; select or create it in Application Settings → Accounts (or use --account %s with --key)", accountID, accountID)
			}
			seed, ok := provider.DefaultTemplates()[flags.Provider]
			if !ok {
				return fc, fmt.Errorf("a new CLI account requires --provider naming a known preset")
			}
			selectedAccount = provider.Account{ID: accountID, ProviderID: flags.Provider, Label: accountID, Connection: seed}
		} else if selectedAccount.ID != accountID {
			return fc, fmt.Errorf("account %q is invalid; select an account in Application Settings → Accounts", accountID)
		} else if flags.Provider != "" && selectedAccount.ProviderID != flags.Provider {
			return fc, fmt.Errorf("account %q uses connection %q, not requested %q", accountID, selectedAccount.ProviderID, flags.Provider)
		}
		if flags.APIKey != "" {
			selectedAccount.APIKey = flags.APIKey
			selectedAccount.OAuth = nil
			if fc.Accounts == nil {
				fc.Accounts = make(map[string]provider.Account)
			}
			fc.Accounts[accountID] = selectedAccount
		}
		template := selectedAccount.Connection
		if err := template.Validate(); err != nil {
			return fc, fmt.Errorf("account %q connection is invalid: %w", accountID, err)
		}
		if template.AuthType != provider.AuthNone && selectedAccount.APIKey == "" &&
			(selectedAccount.OAuth == nil || (selectedAccount.OAuth.AccessToken == "" && selectedAccount.OAuth.RefreshToken == "")) {
			return fc, fmt.Errorf("account %q has no credentials; configure it in Application Settings → Accounts or pass --key with --account", accountID)
		}
	} else if flags.Resume == "" && (flags.Provider != "" || flags.Account != "" || flags.Model != "") {
		return fc, fmt.Errorf("select an explicit account with --account or configure one in Application Settings → Accounts")
	}

	return fc, nil
}

// HandleCLICommands runs workspace-scoped CLI-only operations (list/delete
// sessions). It returns the exit code and whether the invocation was handled.
func HandleCLICommands(flags CLIFlags, home, projectDir string) (int, bool) {
	if flags.ListSessions {
		if err := ListSessions(home, projectDir, flags.Limit, flags.Offset); err != nil {
			fmt.Fprintf(os.Stderr, "Error listing sessions: %v\n", err)
			return 1, true
		}
		return 0, true
	}
	if flags.DeleteSession != "" {
		if err := DeleteSession(home, projectDir, flags.DeleteSession); err != nil {
			fmt.Fprintf(os.Stderr, "Error deleting session: %v\n", err)
			return 1, true
		}
		fmt.Printf("Session %s deleted.\n", flags.DeleteSession)
		return 0, true
	}
	return 0, false
}

// ResolveResume resolves the --resume flag. Empty means launcher picker.
func ResolveResume(flags CLIFlags) (string, error) {
	if flags.Resume == "" {
		return "", nil
	}
	if flags.SSHHost != "" {
		return flags.Resume, nil
	}
	projectDir, err := invocationRoot(flags)
	if err != nil {
		return "", err
	}
	home, err := buildinfo.Home()
	if err != nil {
		return "", err
	}
	return ResolveResumeID(home, projectDir, flags.Resume)
}

// Main is the app-layer entry point: parse flags, resolve config, run IDE.
func Main() int {
	flags := ParseFlags()
	if flags.Version {
		fmt.Println(buildinfo.VersionString())
		return 0
	}
	if err := validateCoreRestartInvocation(flags); err != nil {
		fmt.Fprintf(os.Stderr, "crude: %v\n", err)
		return 1
	}
	if flags.CoreHandoverIdentity {
		identity, err := CoreRestartIdentityForCurrent()
		if err == nil {
			err = json.NewEncoder(os.Stdout).Encode(identity)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "crude handover identity: %v\n", err)
			return 1
		}
		return 0
	}
	if err := validateMCPInvocation(flags); err != nil {
		fmt.Fprintf(os.Stderr, "crude: %v\n", err)
		return 1
	}
	if err := validateConnectedInvocation(flags); err != nil {
		fmt.Fprintf(os.Stderr, "crude: %v\n", err)
		return 1
	}
	if flags.MCP {
		if err := RunMCP(context.Background(), flags); err != nil {
			fmt.Fprintf(os.Stderr, "crude mcp: %v\n", err)
			return 1
		}
		return 0
	}

	// Live perf sentinel (tui/perf). main.go discards slog to protect the
	// TUI surface, so the sentinel writes directly to this file when set.
	if err := perf.ConfigureSentinel(os.Getenv("CRUDE_PERF_LOG")); err != nil {
		fmt.Fprintf(os.Stderr, "crude-perf: %v\n", err)
	}

	if code, handled := handleCoreCommands(flags); handled {
		return code
	}

	home, homeErr := buildinfo.Home()
	if homeErr != nil {
		fmt.Fprintf(os.Stderr, "crude: %v\n", homeErr)
		return 1
	}
	if flags.CoreHandover != "" {
		startup, err := ReadCoreRestartStartup(home, flags.CoreHandover)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crude handover: %v\n", err)
			return 1
		}
		flags.ClientProfile, flags.Workspace, flags.Resume = startup.Profile, startup.Root, startup.SessionID
	}
	projectDir, err := invocationRoot(flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if code, handled := HandleCLICommands(flags, home, projectDir); handled {
		return code
	}
	resumeID := flags.Resume
	// Do not load credentials, configure network policy or open session storage
	// in the shell. All application state is owned by the connected core.
	application := &App{home: home, projectDir: projectDir, resumeID: resumeID,
		clientProfile: flags.ClientProfile, invocation: flags}

	if err := application.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

// handleCoreCommands runs the core process routes. It reports whether the
// invocation was one of them and the process exit code.
func handleCoreCommands(flags CLIFlags) (int, bool) {
	home, _ := buildinfo.Home()
	switch {
	case flags.Core:
		if err := RunCore(CoreOptions{Home: home, Server: flags.Server, Grace: flags.CoreGrace}); err != nil {
			if err == errCoreOwned {
				fmt.Fprintln(os.Stderr, "crude: a core is already running for this user")
				return 0, true
			}
			fmt.Fprintf(os.Stderr, "crude core: %v\n", err)
			return 1, true
		}
		return 0, true
	case flags.CoreStdio:
		if err := RunCoreStdio(home); err != nil {
			fmt.Fprintf(os.Stderr, "crude core-stdio: %v\n", err)
			return 1, true
		}
		return 0, true
	case flags.CoreStatus:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, err := CoreStatus(ctx, home)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crude: %v\n", err)
			return 1, true
		}
		mode := "default (exits when idle)"
		if status.Server {
			mode = "server"
		}
		fmt.Printf("core pid %d, %s, up since %s\n", status.PID, mode, status.StartedAt.Format(time.RFC3339))
		fmt.Printf("executable: %s\n", status.Executable)
		fmt.Printf("shells attached: %d\n", status.Clients)
		if status.Partial {
			fmt.Println("status is partial: unavailable workspace owners are not idle")
		}
		for _, w := range status.Workspaces {
			if w.ObservationError != "" {
				fmt.Printf("  %s  unavailable: %s\n", w.ID, w.ObservationError)
				continue
			}
			fmt.Printf("  %s  agents=%d sessions=%d turns=%d children=%d commands=%d compaction=%d memory=%d goals=%d loops=%d operations=%d\n",
				w.Root, w.Agents, w.Sessions, w.Active.Turns, w.Active.Children, w.Active.AsyncCommands, w.Active.Compaction, w.Active.MemoryJobs, w.Active.Goals, w.Active.Loops, w.Operations)
		}
		return 0, true
	case flags.CoreStop:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := StopCore(ctx, home, flags.Force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crude: %v\n", err)
			return 1, true
		}
		if !result.Accepted {
			fmt.Fprintf(os.Stderr, "crude: core stop refused: %s\n", result.Reason)
			for _, w := range result.Busy {
				fmt.Fprintf(os.Stderr, "  %s  turns=%d children=%d commands=%d compaction=%d memory=%d goals=%d loops=%d operations=%d\n",
					w.Root, w.Active.Turns, w.Active.Children, w.Active.AsyncCommands, w.Active.Compaction, w.Active.MemoryJobs, w.Active.Goals, w.Active.Loops, w.Operations)
			}
			if os.Getenv(expectedCoreOwnerEnv) != "" {
				return coreHandoverRefusedExit, true
			}
			return 2, true
		}
		fmt.Println("core stopping")
		return 0, true
	}
	return 0, false
}
