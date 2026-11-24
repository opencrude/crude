package session

import (
	"context"
	"errors"
	"io"

	"crude/agent/tools"
	"crude/agent/turnloop"
	captypes "crude/types/capability"
	"crude/types/command"
	"crude/workspace/fileio"
)

func (s *Session) runActivation(ctx context.Context, live *turnloop.Agent, agentID, id, label string, work captypes.ActivationWork) (tools.ToolResult, error) {
	manager := live.AsyncMgr
	if manager == nil {
		return tools.ToolResult{}, errors.New("activation needs the owning agent's job manager")
	}
	captypes.ReportProgress(ctx, label, 0, 0)
	media := live.MediaReadPolicy
	changes := make(chan struct{}, 1)
	var inline tools.ToolResult
	var resultErr error
	task, err := manager.StartTask(ctx, id, label, live.WorkDir, func(ctx context.Context, output fileio.TaskOutput) error {
		result, err := work(captypes.WithProgress(ctx, func(p command.Progress) {
			output.Report(p)
			select {
			case changes <- struct{}{}:
			default:
			}
		}))
		inline, resultErr = capabilityMediaResult(result, err, media)
		_, _ = io.WriteString(output, inline.Content)
		if resultErr == nil {
			s.capabilitySharing.share(agentID, "activated", id)
		}
		return resultErr
	})
	if err != nil {
		return tools.ToolResult{}, err
	}
	observe, stop := context.WithCancel(ctx)
	defer stop()
	return awaitJobWithPromotion(ctx, manager, task, s.logger, agentID, s.humanInputWake(observe, agentID), changes, captypes.ProgressObserver(ctx), func() (tools.ToolResult, error) {
		// The job's done channel synchronizes its native result. Promoted
		// callers never read it; the normal settlement journal owns delivery.
		if resultErr == nil && task.Status() != fileio.AsyncCompleted {
			return commandOutcome(manager, task)
		}
		return inline, resultErr
	})
}
