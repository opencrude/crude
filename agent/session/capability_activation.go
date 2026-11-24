package session

import (
	"context"
	"errors"
	"fmt"
	"io"

	"crude/agent/tools"
	"crude/agent/turnloop"
	captypes "crude/types/capability"
	"crude/workspace/fileio"
)

// runActivation starts admitted activation work as a background job at once.
// Activation downloads, installs or launches, so it never holds the turn:
// like async_execute_command it returns the job's handle immediately, and the
// job's command_outcome delivers the activation result to this agent.
func (s *Session) runActivation(ctx context.Context, live *turnloop.Agent, agentID, id, label string, work captypes.ActivationWork) (tools.ToolResult, error) {
	manager := live.AsyncMgr
	if manager == nil {
		return tools.ToolResult{}, errors.New("activation needs the owning agent's job manager")
	}
	media := live.MediaReadPolicy
	info, err := manager.StartTask(ctx, id, label, live.WorkDir, func(ctx context.Context, output fileio.TaskOutput) error {
		result, err := work(captypes.WithProgress(ctx, output.Report))
		settled, resultErr := capabilityMediaResult(result, err, media)
		_, _ = io.WriteString(output, settled.Content)
		if resultErr == nil {
			s.capabilitySharing.share(agentID, "activated", id)
		}
		return resultErr
	})
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.ToolResult{Content: FormatJobStart(info)}, nil
}

// FormatJobStart reports in-process work that started as a background job.
func FormatJobStart(info fileio.AsyncCmdInfo) string {
	result := commandResult(info)
	result.Retrieval = fmt.Sprintf("%s runs as background job [%s]. Its result arrives automatically as a command_outcome event; continue other work or end the turn meanwhile. read_async_output(handle=%q) shows its phases so far and kill_async_command(handle=%q) stops it. Do not start it again or poll its status.", info.Command, info.Handle, info.Handle, info.Handle)
	return tools.CommandResultContent(result)
}
