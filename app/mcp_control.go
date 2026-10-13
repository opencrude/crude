package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
	"crude/cranemcp"
	"crude/types/control"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NeedsInteractiveConsole is a side-effect-free preflight for main, before
// flag parsing or any console mutation. Invalid route values also stay inert;
// the authoritative parser will report them on stderr.
func NeedsInteractiveConsole(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "mcp", "core", "core-stdio", "core-status", "core-stop":
			if !hasValue {
				return false
			}
			enabled, err := strconv.ParseBool(value)
			if err != nil || enabled {
				return false
			}
		}
	}
	return true
}

func mcpGrant(flags CLIFlags) (coreprotocol.ControlClientGrant, error) {
	grant := coreprotocol.ControlClientGrant{}
	effects := flags.MCPEffects
	if effects == "" {
		effects = "read"
	}
	seen := map[control.Effect]bool{}
	for _, part := range strings.Split(effects, ",") {
		effect := control.Effect(strings.TrimSpace(part))
		switch effect {
		case control.Read, control.Write, control.Delete, control.Execute, control.Communication:
		default:
			return grant, fmt.Errorf("invalid --mcp-effects value %q; choose read,write,delete,execute,communication", part)
		}
		if seen[effect] {
			return grant, fmt.Errorf("duplicate --mcp-effects value %q", part)
		}
		seen[effect] = true
		grant.Effects = append(grant.Effects, effect)
	}
	if (flags.MCPShell == "") != (flags.MCPShellEpoch == 0) {
		return grant, errors.New("--mcp-shell and positive --mcp-shell-epoch must be supplied together")
	}
	if flags.MCPShell != "" {
		grant.Shell = &control.ShellOwner{ClientID: flags.MCPShell, Epoch: flags.MCPShellEpoch}
		if err := grant.Shell.Validate(); err != nil {
			return grant, err
		}
	}
	return grant, nil
}

func validateMCPInvocation(flags CLIFlags) error {
	if !flags.MCP {
		if flags.MCPEffects != "" || flags.MCPShell != "" || flags.MCPShellEpoch != 0 {
			return errors.New("--mcp-effects/--mcp-shell/--mcp-shell-epoch require --mcp")
		}
		return nil
	}
	if flags.Core || flags.CoreStdio || flags.CoreStatus || flags.CoreStop || flags.Server || flags.Force || flags.ListSessions || flags.DeleteSession != "" || flags.Resume != "" || flags.ClientProfile != "" || flags.APIKey != "" || flags.Account != "" || flags.Model != "" || flags.Provider != "" || flags.SystemPrompt != "" {
		return errors.New("--mcp cannot be combined with interactive session selectors or core/session administration")
	}
	_, err := mcpGrant(flags)
	return err
}

// RunMCP uses the same private local/strict SSH bridge as interactive shells.
// Stdout belongs exclusively to the SDK's newline-delimited MCP protocol.
func RunMCP(ctx context.Context, flags CLIFlags) error {
	if !flags.MCP {
		return errors.New("MCP route requires --mcp")
	}
	if err := validateMCPInvocation(flags); err != nil {
		return err
	}
	if err := validateConnectedInvocation(flags); err != nil {
		return err
	}
	grant, err := mcpGrant(flags)
	if err != nil {
		return err
	}
	root, err := invocationRoot(flags)
	if err != nil {
		return err
	}
	home, err := buildinfo.Home()
	if err != nil {
		return err
	}
	id, err := cranemcp.RandomID()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := shellDialer(home, flags)(startup, "mcp-"+id)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	// Drain unsolicited workspace events: this adapter is not another replica.
	go func() {
		for range client.Events() {
		}
	}()
	var attached coreprotocol.AttachResult
	if err := client.Call(startup, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		return err
	}
	server, err := cranemcp.New(startup, client, attached.WorkspaceID, grant)
	if err != nil {
		return err
	}
	cancel()
	run, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		select {
		case <-client.Done():
			stop()
		case <-run.Done():
		}
	}()
	err = server.Run(run, &mcp.StdioTransport{})
	if client.Err() != nil && ctx.Err() == nil {
		return client.Err()
	}
	return err
}
