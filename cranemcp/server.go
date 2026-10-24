// Package cranemcp exposes the core's semantic control API over MCP. It owns
// transport adaptation only: command discovery and authorization stay in core.
package cranemcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/tools"
	"crude/types/control"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client interface {
	Owner() coreprotocol.Owner
	Call(context.Context, string, string, coreprotocol.Value, coreprotocol.Target) error
	CallWith(context.Context, string, string, coreprotocol.Value, coreprotocol.Target, coreclient.CallOptions) (uint64, error)
}

// New installs one immutable launch grant before offering tools. The client must
// already be attached to workspace; shell is explicit and never inferred.
func New(ctx context.Context, client Client, workspace string, grant coreprotocol.ControlClientGrant) (*mcp.Server, error) {
	if workspace == "" {
		return nil, errors.New("MCP requires an attached workspace")
	}
	if grant.Shell != nil {
		owner := *grant.Shell
		grant.Shell = &owner
	}
	if err := client.Call(ctx, workspace, coreprotocol.ControlClientRegister, &grant, nil); err != nil {
		return nil, err
	}
	a := &adapter{client: client, workspace: workspace, shell: grant.Shell}
	s := mcp.NewServer(&mcp.Implementation{Name: "crude", Version: "1"}, nil)
	for _, definition := range tools.ControlTools {
		var schema any
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			return nil, err
		}
		s.AddTool(&mcp.Tool{Name: definition.Function.Name, Description: definition.Function.Description, InputSchema: schema}, a.handle)
	}
	s.AddTool(&mcp.Tool{Name: "crude_shells", Description: "List up to 128 registered shell identities in this workspace. Informational only; select --mcp-shell and --mcp-shell-epoch at launch. This tool cannot change the launch binding.", InputSchema: map[string]any{"type": "object", "additionalProperties": false}}, a.handle)
	s.AddTool(&mcp.Tool{Name: "crude_receipt", Description: "Observe the original control request outcome by request_id; never re-execute an uncertain effect. Pending or uncertain is not success.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"request_id": map[string]any{"type": "string"}}, "required": []string{"request_id"}, "additionalProperties": false}}, a.handle)
	return s, nil
}

type adapter struct {
	client    Client
	workspace string
	shell     *control.ShellOwner
}
type target struct {
	SessionID   string `json:"session_id,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
}
type invocation struct {
	Command       string                `json:"command"`
	Arguments     json.RawMessage       `json:"arguments"`
	Target        target                `json:"target"`
	Preconditions control.Preconditions `json:"preconditions"`
}

func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}
func result(value any, summary string, failed bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: summary}}, IsError: failed}
}
func (a *adapter) handle(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	value, summary, err := a.invoke(ctx, call)
	if err != nil {
		return result(value, err.Error(), true), nil
	}
	failed := false
	if r, ok := value.(control.Result); ok {
		failed = r.State == control.Failed || r.State == control.Uncertain
	}
	return result(value, summary, failed), nil
}
func (a *adapter) invoke(ctx context.Context, call *mcp.CallToolRequest) (any, string, error) {
	bound := control.Target{WorkspaceID: a.workspace, Shell: a.shell}
	switch call.Params.Name {
	case "crude_commands":
		var input struct {
			Query string `json:"query"`
		}
		if err := decode(call.Params.Arguments, &input); err != nil {
			return nil, "", err
		}
		var values coreprotocol.ControlDescriptors
		err := a.client.Call(ctx, a.workspace, coreprotocol.ControlDescribe, &coreprotocol.ControlDescribeRequest{Query: input.Query, Target: bound}, &values)
		return values, fmt.Sprintf("Crude command descriptors: %d; discovery grants no authority.", len(values)), err
	case "crude_shells":
		if err := decode(call.Params.Arguments, &struct{}{}); err != nil {
			return nil, "", err
		}
		var values coreprotocol.ControlShellInfos
		err := a.client.Call(ctx, a.workspace, coreprotocol.ControlShells, &coreprotocol.Empty{}, &values)
		return values, fmt.Sprintf("Registered Crude shells: %d; launch binding unchanged.", len(values)), err
	case "crude_receipt":
		var input coreprotocol.ReceiptRequest
		if err := decode(call.Params.Arguments, &input); err != nil {
			return nil, "", err
		}
		if input.RequestID == "" {
			return nil, "", errors.New("request_id is required")
		}
		var receipt coreprotocol.Receipt
		err := a.client.Call(ctx, a.workspace, coreprotocol.GetReceipt, &input, &receipt)
		return receipt.View(), "Crude request receipt: " + receipt.State, err
	case "crude_control":
		var input invocation
		if err := decode(call.Params.Arguments, &input); err != nil {
			return nil, "", err
		}
		if input.Command == "" || len(input.Arguments) == 0 || input.Arguments[0] != '{' {
			return nil, "", errors.New("command and object arguments are required")
		}
		id, err := RandomID()
		if err != nil {
			return nil, "", err
		}
		bound.SessionID = input.Target.SessionID
		bound.ResourceID = input.Target.ResourceID
		bound.ExecutionID = input.Target.ExecutionID
		request := control.Request{ID: id, CommandID: input.Command, Target: bound, Preconditions: input.Preconditions, Input: input.Arguments}
		// Resolve generic controls through the actual shell owner before execute;
		// core repeats authorization/fences at the effect boundary.
		var descriptor control.Descriptor
		if err := a.request(ctx, coreprotocol.ControlResolve, request, &descriptor); err != nil {
			return nil, "", err
		}
		var outcome control.Result
		err = a.request(ctx, coreprotocol.ControlExecute, request, &outcome)
		var uncertain *coreclient.UncertainError
		if errors.As(err, &uncertain) {
			observe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			var receipt coreprotocol.Receipt
			if lookup := a.client.Call(observe, a.workspace, coreprotocol.GetReceipt, &coreprotocol.ReceiptRequest{RequestID: id}, &receipt); lookup == nil && receipt.State == "completed" && receipt.Response.Error == nil && coreprotocol.Decode(receipt.Response.Payload, &outcome) == nil && outcome.RequestID == id {
				err = nil
			} else {
				outcome = control.Result{RequestID: id, OperationID: id, State: control.Uncertain, Fault: &control.Fault{Code: control.UnknownOutcome, Message: "Outcome unknown; inspect crude_receipt with this request_id. Do not replay the effect."}}
				return outcome, "Crude control outcome uncertain: " + id, nil
			}
		}
		if err != nil {
			return nil, "", err
		}
		return outcome, fmt.Sprintf("Crude control %s: %s (request %s).", input.Command, outcome.State, outcome.RequestID), nil
	default:
		return nil, "", errors.New("unknown Crude tool")
	}
}
func (a *adapter) request(ctx context.Context, method string, input control.Request, output coreprotocol.Target) error {
	_, err := a.client.CallWith(ctx, a.workspace, method, &input, output, coreclient.CallOptions{ID: input.ID})
	return err
}

// RandomID provides a stable, unguessable process/request identity without
// depending on an SDK-private JSON-RPC request identifier.
func RandomID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
