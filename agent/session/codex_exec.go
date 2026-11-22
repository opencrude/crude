package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"crude/agent/hostshell"
	"crude/agent/safety"
	"crude/agent/tools"
	"crude/types/command"
	"crude/workspace/fileio"
)

func executeCodexCommand(ctx context.Context, mgr *fileio.AsyncCommandManager, root, tool string, params map[string]any, policy safety.CommandPolicy, commandOptions ...*fileio.CommandOptions) (tools.ToolResult, error) {
	if mgr == nil {
		return tools.ToolResult{}, fmt.Errorf("command manager unavailable")
	}
	maxTokens := tools.IntArgAny(params, "max_output_tokens", 10000)
	if maxTokens < 1 {
		maxTokens = 10000
	}
	maxTokens = min(maxTokens, 1000000)
	var result command.Result
	var err error
	if tool == "write_stdin" {
		chars := getStrParam(params, "chars")
		wait := tools.IntArgAny(params, "yield_time_ms", 250)
		if chars == "" {
			wait = max(5000, min(wait, 300000))
		} else {
			wait = max(1, min(wait, 30000))
		}
		result, err = mgr.WriteInteractive(ctx, tools.IntArgAny(params, "session_id", 0), chars, time.Duration(wait)*time.Millisecond, maxTokens)
	} else {
		command := getStrParam(params, "cmd")
		if command == "" {
			return tools.ToolResult{}, fmt.Errorf("exec_command requires cmd")
		}
		if sr := safety.CheckCommandSafetyWithPolicy(command, nil, policy); sr.Risk.IsAlwaysBlocked() {
			return tools.ToolResult{}, fmt.Errorf("[SAFETY BLOCK] %s", sr.Reason)
		}
		cwd := getStrParam(params, "workdir")
		if cwd == "" {
			cwd = root
		} else if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(root, cwd)
		}
		options := fileio.InteractiveOptions{Command: command, CWD: cwd, Login: tools.BoolArgAny(params, "login", true), TTY: tools.BoolArgAny(params, "tty", false)}
		if name := getStrParam(params, "shell"); name != "" {
			if options.Shell, err = hostshell.Requested(name); err != nil {
				return tools.ToolResult{Content: "Error: " + err.Error()}, err
			}
		}
		wait := max(250, min(tools.IntArgAny(params, "yield_time_ms", 10000), 30000))
		result, err = mgr.StartInteractive(ctx, options, time.Duration(wait)*time.Millisecond, maxTokens, commandOptions...)
	}
	if err != nil && result.OutputDescriptor.Status == "" {
		return tools.ToolResult{Content: "Error: " + err.Error()}, err
	}
	result.Cancelled = result.Cancelled || errors.Is(err, context.Canceled)
	if err == nil && result.ExitCode != nil && *result.ExitCode != 0 {
		err = fmt.Errorf("command exited with code %d", *result.ExitCode)
	} else if err == nil && result.Failed() {
		err = fmt.Errorf("command failed: %s", result.Error)
	}
	return tools.ToolResult{Content: tools.CommandResultContent(result)}, err
}
