package tools

import (
	"encoding/json"

	"crude/types/command"
)

func IsCommandTool(name string) bool {
	switch name {
	case "execute_command", "async_execute_command", "check_async_command", "read_async_output", "exec_command", "write_stdin":
		return true
	}
	return false
}

func CommandResultContent(result command.Result) string {
	body, err := json.Marshal(result)
	if err != nil {
		return "Error: encode command result: " + err.Error()
	}
	return string(body)
}

// ParseCommandResult decodes only command tool envelopes, never arbitrary JSON
// printed by a command. Native exec's documented output/exit/session fields may
// stand alone; the other tools require the explicit process descriptor.
func ParseCommandResult(name, content string) (command.Result, bool) {
	if !IsCommandTool(name) {
		return command.Result{}, false
	}
	var envelope struct {
		command.Result
		Output *string `json:"output"`
	}
	if json.Unmarshal([]byte(content), &envelope) != nil || envelope.Output == nil {
		return command.Result{}, false
	}
	result := envelope.Result
	result.Output = *envelope.Output
	if result.OutputDescriptor.Status == "" {
		if name != "exec_command" && name != "write_stdin" {
			return command.Result{}, false
		}
		if result.ExitCode != nil {
			result.OutputDescriptor.Status = command.Completed
			if *result.ExitCode != 0 {
				result.OutputDescriptor.Status = command.Failed
			}
		} else if result.SessionID > 0 {
			result.OutputDescriptor.Status = command.Running
		}
	}
	return result, true
}
