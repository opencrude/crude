package coreprotocol

import "crude/types/control"

const (
	ControlClientRegister = "control.client.register"
	ControlDescribe       = "control.describe"
	ControlResolve        = "control.resolve"
	ControlExecute        = "control.execute"
	ControlShells         = "control.shells"
)

// ControlClientGrant is an explicit application-startup grant, never an exposed
// model tool. The MCP adapter's launch configuration supplies these effects and
// exact shell binding once; it does not offer a way to widen them during a task.
type ControlClientGrant struct {
	Effects []control.Effect    `json:"effects"`
	Shell   *control.ShellOwner `json:"shell,omitempty"`
}

type ControlDescribeRequest struct {
	Query  string         `json:"query"`
	Target control.Target `json:"target"`
}

type ControlShellInfo struct {
	WorkspaceID string             `json:"workspace_id"`
	Owner       control.ShellOwner `json:"owner"`
}
