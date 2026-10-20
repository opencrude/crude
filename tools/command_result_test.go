package tools

import "testing"

func TestCommandResultDecodesOnlyToolEnvelope(t *testing.T) {
	for _, name := range []string{"execute_command", "async_execute_command", "check_async_command", "read_async_output", "exec_command", "write_stdin"} {
		result, ok := ParseCommandResult(name, `{"output":"literal {\"output\":\"not an envelope\"}","exit_code":7,"output_descriptor":{"handle":"job","status":"failed","total_bytes":42}}`)
		if !ok || !result.Failed() || result.Output != `literal {"output":"not an envelope"}` || result.ExitCode == nil || *result.ExitCode != 7 || result.OutputDescriptor.Handle != "job" {
			t.Fatalf("%s failed to decode typed result: %+v", name, result)
		}
	}
	for _, tc := range []struct{ name, content string }{
		{"read_file", `{"output":"source"}`},
		{"execute_command", `{"output":"command wrote JSON"}`},
		{"execute_command", `{"output_descriptor":{"status":"completed"}}`},
		{"exec_command", `{"output":42}`},
		{"exec_command", `{"output":"unterminated`},
	} {
		if _, ok := ParseCommandResult(tc.name, tc.content); ok {
			t.Fatalf("interpreted ordinary/malformed result: %s", tc.content)
		}
	}
}
