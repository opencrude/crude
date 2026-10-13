package capability

import (
	"context"
	"crude/types/control"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// Authorization is internal execution authority, never a model-supplied argument.
// Its token binds an immutable phase preview; automated access is run-local.
type Authorization struct {
	Origin string
	Token  string
	Actor  RuntimeActor
}

type authorizationKey struct{}

func AuthorizationFrom(ctx context.Context) (Authorization, bool) {
	a, ok := ctx.Value(authorizationKey{}).(Authorization)
	return a, ok && (a.Origin == "human" || a.Origin == "reviewer" || a.Origin == "yolo")
}

func authorize(ctx context.Context, origin, token string, actor RuntimeActor) context.Context {
	return context.WithValue(ctx, authorizationKey{}, Authorization{Origin: origin, Token: token, Actor: actor})
}

func (request *ApprovalRequired) CapabilityApprovalContextFor(ctx context.Context, origin string) context.Context {
	return authorize(request.CapabilityApprovalContext(ctx), origin, request.Preview.Token, request.Actor)
}

func (request *ApprovalRequired) Evidence() map[string]any {
	return map[string]any{"phase": "acquisition", "token": request.Preview.Token, "source": request.Preview.Source.ID,
		"revision": request.Preview.Candidate.Version, "digest": request.Preview.Candidate.Digest,
		"kind": request.Preview.Kind, "binding": request.Preview.Scope, "effect": EffectWrite,
		"requirements": request.Preview.Requirements, "requested_access": request.Preview.RequestedAccess,
		"source_policy": request.Preview.Source}
}

func (request *AccessApprovalRequired) CapabilityApprovalContextFor(ctx context.Context, origin string) context.Context {
	return authorize(request.CapabilityApprovalContext(ctx), origin, request.Preview.Token, request.Actor)
}

func (request *AccessApprovalRequired) Evidence() map[string]any {
	return map[string]any{"phase": "access", "token": request.Preview.Token, "connection": SafeConnection(request.Preview.Connection),
		"grants": request.Preview.Grants, "tools": request.Preview.Tools}
}

// SafeConnection keeps the executable/endpoint and names of required inputs, not
// credential values. The exact unredacted preimage remains internal only.
func SafeConnection(config ConnectionConfig) ConnectionConfig {
	encoded, _ := json.Marshal(config)
	var safe ConnectionConfig
	_ = json.Unmarshal(encoded, &safe)
	redact := func(value string) string {
		for _, values := range []map[string]string{config.Environment, config.Headers, config.SecretRefs} {
			for _, secret := range values {
				if secret != "" {
					value = strings.ReplaceAll(value, secret, "[redacted]")
				}
			}
		}
		return value
	}
	safe.URL, safe.Command, safe.Directory = redact(safe.URL), redact(safe.Command), redact(safe.Directory)
	for i := range safe.Args {
		safe.Args[i] = redact(safe.Args[i])
	}
	for key := range safe.Environment {
		safe.Environment[key] = "[redacted]"
	}
	for key := range safe.Headers {
		safe.Headers[key] = "[redacted]"
	}
	for key := range safe.SecretRefs {
		safe.SecretRefs[key] = "[redacted]"
	}
	safe.CredentialRef = ""
	return safe
}

type LaunchPreview struct {
	Connection ConnectionConfig
	Token      string
	Actor      RuntimeActor
}

type LaunchApprovalRequired struct{ Preview LaunchPreview }
type LaunchApprovedKey struct{}

func (request *LaunchApprovalRequired) Error() string {
	return "MCP local host execution requires exact authorization"
}
func (request *LaunchApprovalRequired) CapabilityApproval() (string, string) {
	encoded, _ := json.Marshal(request.Evidence())
	return request.Preview.Connection.ID, "Authorize exact local host execution (not a sandbox or durable tool grant): " + string(encoded)
}
func (request *LaunchApprovalRequired) CapabilityApprovalContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, LaunchApprovedKey{}, request.Preview)
}
func (request *LaunchApprovalRequired) CapabilityApprovalContextFor(ctx context.Context, origin string) context.Context {
	return authorize(request.CapabilityApprovalContext(ctx), origin, request.Preview.Token, request.Preview.Actor)
}
func (request *LaunchApprovalRequired) Evidence() map[string]any {
	return map[string]any{"phase": "launch", "token": request.Preview.Token, "effect": EffectExecute,
		"host": request.Preview.Connection.ExecutionHost(), "scope": "local host", "connection": SafeConnection(request.Preview.Connection)}
}

// RuntimeAuthority validates an exact durable run and attachment. Implementations
// must reject terminal, replaced, detached and stopping owners; validation never
// selects a different current run on behalf of a caller.
type RuntimeAuthority interface {
	ValidateRuntime(context.Context, RuntimeActor) error
}

// RunRevoker releases ephemeral grants and transports at exact run settlement.
// Durable runtime authority remains responsible for rejecting subsequent callers.
type RunRevoker interface{ RevokeRun(RuntimeActor) }

// ExecutionHost is explicit; omission selects the core, never another shell.
func (config ConnectionConfig) ExecutionHost() string {
	if config.Host == "" {
		return "core"
	}
	return config.Host
}

// HostRequest is internal, immutable execution authority, never model input.
// Secret references resolve on the selected host; credential bytes must not cross.
type HostRequest struct {
	ID              string
	WorkspaceID     string
	Actor           RuntimeActor
	Operation       string
	Connection      ConnectionConfig
	Authorized      bool
	Tool            Tool
	Grant           *ToolGrant
	Arguments       map[string]any
	URI             string
	Prompt          string
	PromptArguments map[string]string
}
type HostReply struct {
	ID     string
	States []ConnectionState
	Result Result
	Error  string
}
type HostDispatcher interface {
	DispatchHost(context.Context, HostRequest) (HostReply, error)
}

func ValidateCapabilityHostRequest(r HostRequest, workspace string, owner control.ShellOwner) error {
	if r.ID == "" || len(r.ID) > 256 || workspace == "" || r.WorkspaceID != workspace || r.Actor.WorkspaceID != workspace || r.Actor.ShellOwner == nil || *r.Actor.ShellOwner != owner || owner.Epoch == 0 || owner.ClientID == "" || r.Actor.RunID == "" || r.Actor.ID == "" || r.Actor.RuntimeEpoch == 0 || r.Actor.TurnGeneration == 0 {
		return errors.New("invalid exact MCP shell owner/run")
	}
	if r.Connection.Directory == "" || !filepath.IsAbs(r.Connection.Directory) {
		return errors.New("shell MCP host requires an explicit absolute shell-local directory")
	}
	if r.Connection.Host != "shell" || r.Connection.Transport != "stdio" || len(r.Connection.Environment) > 0 || len(r.Connection.Headers) > 0 || r.Connection.CredentialRef != "" {
		return errors.New("shell host must use stdio without forwarded credential values")
	}
	switch r.Operation {
	case "prepare", "connect", "inventory", "call", "readResource", "getPrompt", "disconnect":
	default:
		return errors.New("unknown MCP host operation")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > (4 << 20) {
		return errors.New("MCP host request exceeds budget")
	}
	return nil
}

// HostRequestReadOnly permits fresh discovery without retaining an effect receipt.
func HostRequestReadOnly(r HostRequest) bool {
	return r.Operation == "prepare" || r.Operation == "inventory" || r.Operation == "readResource" || r.Operation == "getPrompt" || r.Operation == "call" && r.Tool.Effect == EffectRead
}
