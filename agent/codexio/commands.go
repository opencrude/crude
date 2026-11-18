// Copyright 2025 OpenAI
// SPDX-License-Identifier: Apache-2.0
// Tool descriptions and parameter guides copied from Codex shell_spec.rs at
// aa380897f67b91e1a47d530d7286d497b6726d3f; Go representation added for Crude.
package codexio

import "encoding/json"

type FunctionTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

func ExecCommandTool() FunctionTool {
	return FunctionTool{Name: "exec_command", Description: "Runs a command in a PTY, returning output or a session ID for ongoing interaction.", Parameters: json.RawMessage(`{
"type":"object","additionalProperties":false,"required":["cmd"],"properties":{
"cmd":{"type":"string","description":"Shell command to execute."},
"workdir":{"type":"string","description":"Working directory for the command. Defaults to the turn cwd."},
"tty":{"type":"boolean","description":"True allocates a PTY for the command; false or omitted uses plain pipes."},
"yield_time_ms":{"type":"number","description":"Wait before yielding output. Defaults to 10000 ms; effective range is 250-30000 ms."},
"max_output_tokens":{"type":"number","description":"Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy."},
"shell":{"type":"string","description":"Shell binary to launch. Defaults to the user's default shell."},
"login":{"type":"boolean","description":"True runs the shell with -l/-i semantics; false disables them. Defaults to true."},
"sandbox_permissions":{"type":"string","enum":["use_default","require_escalated"],"description":"Per-command sandbox override. Defaults to ` + "`use_default`" + `; use ` + "`require_escalated`" + ` for unsandboxed execution."},
"justification":{"type":"string","description":"User-facing approval question for ` + "`require_escalated`" + `; omit otherwise."},
"prefix_rule":{"type":"array","items":{"type":"string"},"description":"Reusable approval prefix for ` + "`cmd`" + `, only with ` + "`sandbox_permissions: \\\"require_escalated\\\"`" + `; for example [\"git\", \"pull\"]."}
}}`)}
}

func WriteStdinTool() FunctionTool {
	return FunctionTool{Name: "write_stdin", Description: "Writes characters to an existing unified exec session and returns recent output.", Parameters: json.RawMessage(`{
"type":"object","additionalProperties":false,"required":["session_id"],"properties":{
"session_id":{"type":"number","description":"Identifier of the running unified exec session."},
"chars":{"type":"string","description":"Bytes to write to stdin. Defaults to empty, which polls without writing."},
"yield_time_ms":{"type":"number","description":"Wait before yielding output. Non-empty writes default to 250 ms and cap at 30000 ms; empty polls wait 5000-300000 ms by default."},
"max_output_tokens":{"type":"number","description":"Output token budget. Defaults to 10000 tokens; larger requests may be capped by policy."}
}}`)}
}
