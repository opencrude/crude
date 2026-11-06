package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const maxCommandArgumentsBytes = 1 << 20

// CommandSupport reports whether the initialized static server capability
// advertises workspace/executeCommand. It does not consult dynamic registrations.
func (c *Client) CommandSupport() bool {
	if !c.initialized.Load() || c.capabilities.ExecuteCommandProvider == nil {
		return false
	}
	return len(c.capabilities.ExecuteCommandProvider.Commands) != 0
}

// CommandSupported reports whether this exact command was advertised by the
// initialized server. An unlisted command is never sent.
func (c *Client) CommandSupported(command string) bool {
	if command == "" || !c.initialized.Load() || c.capabilities.ExecuteCommandProvider == nil {
		return false
	}
	for _, advertised := range c.capabilities.ExecuteCommandProvider.Commands {
		if advertised == command {
			return true
		}
	}
	return false
}

// ExecuteCommand sends one explicit command that the initialized server
// advertised. Arguments stay opaque JSON so provider numeric spelling and
// nested data are not rebuilt. The transport does not interpret the result or
// apply server-initiated workspace edits.
func (c *Client) ExecuteCommand(ctx context.Context, command string, arguments json.RawMessage) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if !c.CommandSupported(command) {
		return nil, errors.New("command is not advertised by the initialized language server")
	}
	if len(arguments) > maxCommandArgumentsBytes || !utf8.Valid(arguments) ||
		!validCommandArguments(arguments) {
		return nil, errors.New("invalid or oversized command arguments")
	}
	params := struct {
		Command   string          `json:"command"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}{Command: command, Arguments: bytes.Clone(arguments)}
	var result json.RawMessage
	if err := c.conn.Call(ctx, MethodWorkspaceExecuteCommand, params, &result); err != nil {
		return nil, fmt.Errorf("workspace/executeCommand failed: %w", err)
	}
	if len(result) > maxCommandArgumentsBytes || !utf8.Valid(result) {
		return nil, errors.New("invalid or oversized command result")
	}
	return bytes.Clone(result), nil
}

func validCommandArguments(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if !json.Valid(raw) {
		return false
	}
	var items []json.RawMessage
	return json.Unmarshal(raw, &items) == nil
}
