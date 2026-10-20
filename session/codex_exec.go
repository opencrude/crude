package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"crude/fileio"
	"crude/safety"
	"crude/tools"
)

func executeCodexCommand(ctx context.Context, mgr *fileio.AsyncCommandManager, root, tool string, params map[string]any, policy safety.CommandPolicy) (tools.ToolResult, error) {
	if mgr == nil {
		return tools.ToolResult{}, fmt.Errorf("command manager unavailable")
	}
	maxTokens := tools.GetIntArgAny(params, "max_output_tokens", 10000)
	if maxTokens < 1 {
		maxTokens = 10000
	}
	maxTokens = min(maxTokens, 1000000)
	var result fileio.InteractiveResult
	var err error
	if tool == "write_stdin" {
		chars := getStrParam(params, "chars")
		wait := tools.GetIntArgAny(params, "yield_time_ms", 250)
		if chars == "" {
			wait = max(5000, min(wait, 300000))
		} else {
			wait = max(1, min(wait, 30000))
		}
		result, err = mgr.WriteInteractive(ctx, tools.GetIntArgAny(params, "session_id", 0), chars, time.Duration(wait)*time.Millisecond, maxTokens)
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
		wait := max(250, min(tools.GetIntArgAny(params, "yield_time_ms", 10000), 30000))
		result, err = mgr.StartInteractive(ctx, fileio.InteractiveOptions{Command: command, CWD: cwd, Shell: getStrParam(params, "shell"), Login: tools.GetBoolArgAny(params, "login", true), TTY: tools.GetBoolArgAny(params, "tty", false)}, time.Duration(wait)*time.Millisecond, maxTokens)
	}
	if err != nil && result.OutputDescriptor.Status == "" {
		return tools.ToolResult{Content: "Error: " + err.Error()}, err
	}
	result.Cancelled = errors.Is(err, context.Canceled)
	if err == nil && result.ExitCode != nil && *result.ExitCode != 0 {
		err = fmt.Errorf("command exited with code %d", *result.ExitCode)
	}
	return tools.ToolResult{Content: tools.CommandResultContent(result)}, err
}
