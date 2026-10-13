package app

import (
	"crude/types/control"
	"testing"
)

func TestMCPLaunchGrantAndValidation(t *testing.T) {
	grant, err := mcpGrant(CLIFlags{MCP: true})
	if err != nil || len(grant.Effects) != 1 || grant.Effects[0] != control.Read || grant.Shell != nil {
		t.Fatalf("unsafe default: %#v %v", grant, err)
	}
	for _, flags := range []CLIFlags{
		{MCPEffects: "read"}, {MCPShell: "one"}, {MCPShellEpoch: 1},
		{MCP: true, MCPEffects: "authority"}, {MCP: true, MCPEffects: "read,"}, {MCP: true, MCPEffects: "read,read"},
		{MCP: true, MCPShell: "one"}, {MCP: true, MCPShellEpoch: 1},
		{MCP: true, Core: true}, {MCP: true, CoreStdio: true}, {MCP: true, CoreStatus: true}, {MCP: true, CoreStop: true},
		{MCP: true, Server: true}, {MCP: true, Force: true}, {MCP: true, ListSessions: true}, {MCP: true, DeleteSession: "one"},
		{MCP: true, Resume: "last"}, {MCP: true, Model: "model"}, {MCP: true, ClientProfile: "one"}, {MCP: true, APIKey: "secret"},
	} {
		if err := validateMCPInvocation(flags); err == nil {
			t.Fatalf("invalid route accepted: %#v", flags)
		}
	}
	flags := CLIFlags{MCP: true, MCPEffects: "read,write", MCPShell: "exact", MCPShellEpoch: 7, SSHHost: "alias", Workspace: "/project"}
	if err := validateMCPInvocation(flags); err != nil {
		t.Fatal(err)
	}
	if err := validateConnectedInvocation(flags); err != nil {
		t.Fatal(err)
	}
	grant, err = mcpGrant(flags)
	if err != nil || grant.Shell.ClientID != "exact" || grant.Shell.Epoch != 7 || len(grant.Effects) != 2 {
		t.Fatalf("wrong grant: %#v %v", grant, err)
	}
}

func TestProtocolRoutesDoNotInitializeConsole(t *testing.T) {
	for _, args := range [][]string{{"--mcp"}, {"-mcp=true"}, {"--core"}, {"--core-stdio"}, {"--core-status"}, {"--core-stop"}, {"--mcp=invalid"}} {
		if NeedsInteractiveConsole(args) {
			t.Fatalf("noninteractive route changes console: %v", args)
		}
	}
	for _, args := range [][]string{nil, {"--mcp=false"}, {"--", "--mcp"}} {
		if !NeedsInteractiveConsole(args) {
			t.Fatalf("interactive route skipped console: %v", args)
		}
	}
}
