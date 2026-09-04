package session

import (
	"context"

	"crude/tools"
)

// ExecuteToolForTest runs one tool through the session's real tool pipeline
// (the same closure installed on agents created by newAgent). Tests use this
// to drive read_file/execute_command exactly as the model would.
func (s *Session) ExecuteToolForTest(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	if s == nil {
		return tools.ToolResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.toolFnForTest()(ctx, tool, params)
}

// toolFnForTest materializes the per-turn tool closure without running a
// provider round trip. It must stay test-only.
func (s *Session) toolFnForTest() func(ctx context.Context, tool string, params map[string]any) (tools.ToolResult, error) {
	return s.newToolFn(false)
}
