package tools

import "encoding/json"

// CommandResult decodes only the documented native command envelope. Output
// that happens to contain JSON is not an envelope for any other tool family.
func CommandResult(name, content string) (output string, exit *int, session int, ok bool) {
	if name != "exec_command" && name != "write_stdin" {
		return
	}
	var result struct {
		Output  *string `json:"output"`
		Exit    *int    `json:"exit_code"`
		Session int     `json:"session_id"`
	}
	if json.Unmarshal([]byte(content), &result) != nil || result.Output == nil {
		return
	}
	return *result.Output, result.Exit, result.Session, true
}
