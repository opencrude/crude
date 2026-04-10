package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type InteractionLogger struct {
	file    *os.File
	encoder *json.Encoder
	mu      sync.Mutex
	path    string
}

func NewInteractionLogger(logDir string) (*InteractionLogger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}

	timestamp := time.Now().Format("2006-01-02-15-04-05")
	filename := filepath.Join(logDir, "crude-"+timestamp+".jsonl")

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	return &InteractionLogger{
		file:    file,
		encoder: json.NewEncoder(file),
		path:    filename,
	}, nil
}

func (l *InteractionLogger) Log(entry LogEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.encoder.Encode(entry)
}

func (l *InteractionLogger) LogSessionStart(version string) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategorySession,
		Event:    EventSessionStart,
		Data:     map[string]any{"version": version},
	})
}

func (l *InteractionLogger) LogSessionEnd(totalDurationMs int64) {
	dur := totalDurationMs
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategorySession,
		Event:    EventSessionEnd,
		Data:     map[string]any{},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogPhaseStart(phase string, data map[string]any) {
	evt := ""
	switch phase {
	case "grounding":
		evt = EventPhaseGroundingStart
	case "planning":
		evt = EventPhasePlanningStart
	case "executing":
		evt = EventPhaseExecutionStart
	case "summarizing", "summary":
		evt = EventPhaseSummarizeStart
	case "replying", "chitchat":
		evt = EventPhaseChitchatStart
	}
	if data == nil {
		data = map[string]any{}
	}
	data["phase"] = phase
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryPhase,
		Event:    evt,
		Data:     data,
	})
}

func (l *InteractionLogger) LogPhaseEnd(phase string, durationMs int64) {
	evt := ""
	switch phase {
	case "grounding":
		evt = EventPhaseGroundingEnd
	case "planning":
		evt = EventPhasePlanningEnd
	case "executing":
		evt = EventPhaseExecutionEnd
	case "summarizing", "summary":
		evt = EventPhaseSummarizeEnd
	case "replying", "chitchat":
		evt = EventPhaseChitchatEnd
	}
	dur := durationMs
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryPhase,
		Event:    evt,
		Data:     map[string]any{"phase": phase},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogToolCall(tool string, params map[string]any) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryTool,
		Event:    EventToolCall,
		Data: map[string]any{
			"tool":   tool,
			"params": params,
		},
	})
}

func (l *InteractionLogger) LogToolResult(tool string, result string, durationMs int64, err error) {
	dur := durationMs
	entry := LogEntry{
		Level:    LevelDebug,
		Category: CategoryTool,
		Event:    EventToolResult,
		Data: map[string]any{
			"tool":   tool,
			"result": truncate(result, 500),
		},
		Duration: &dur,
	}
	if err != nil {
		entry.Level = LevelError
		entry.Data["error"] = err.Error()
	}
	l.Log(entry)
}

func (l *InteractionLogger) LogLLMRequest(provider, model string, msgCount, toolCount int) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryLLM,
		Event:    EventLLMRequest,
		Data: map[string]any{
			"provider":   provider,
			"model":      model,
			"msg_count":  msgCount,
			"tool_count": toolCount,
		},
	})
}

func (l *InteractionLogger) LogLLMResponse(provider, model, finishReason string, usage *struct{ PromptTokens, CompletionTokens int }, durationMs int64) {
	dur := durationMs
	data := map[string]any{
		"provider":      provider,
		"model":         model,
		"finish_reason": finishReason,
	}
	if usage != nil {
		data["prompt_tokens"] = usage.PromptTokens
		data["completion_tokens"] = usage.CompletionTokens
		data["total_tokens"] = usage.PromptTokens + usage.CompletionTokens
	}
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryLLM,
		Event:    EventLLMResponse,
		Data:     data,
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogLLMStreamStart(provider, model string, msgCount, toolCount int) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryLLM,
		Event:    EventLLMStreamStart,
		Data: map[string]any{
			"provider":   provider,
			"model":      model,
			"msg_count":  msgCount,
			"tool_count": toolCount,
		},
	})
}

func (l *InteractionLogger) LogLLMStreamEnd(finishReason string, contentLen int, durationMs int64) {
	dur := durationMs
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryLLM,
		Event:    EventLLMStreamEnd,
		Data: map[string]any{
			"finish_reason": finishReason,
			"content_len":   contentLen,
		},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogLLMError(provider, model string, err error, durationMs int64) {
	dur := durationMs
	l.Log(LogEntry{
		Level:    LevelError,
		Category: CategoryLLM,
		Event:    EventLLMError,
		Data: map[string]any{
			"provider": provider,
			"model":    model,
			"error":    err.Error(),
		},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogDAGNodeStart(nodeID, tool, operation string, params map[string]any) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryDAG,
		Event:    EventDAGNodeStart,
		Data: map[string]any{
			"node_id":   nodeID,
			"tool":      tool,
			"operation": operation,
			"params":    params,
		},
	})
}

func (l *InteractionLogger) LogDAGNodeComplete(nodeID, tool string, status string, durationMs int64) {
	dur := durationMs
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryDAG,
		Event:    EventDAGNodeComplete,
		Data: map[string]any{
			"node_id": nodeID,
			"tool":    tool,
			"status":  status,
		},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogDAGNodeError(nodeID, tool string, err error, durationMs int64) {
	dur := durationMs
	l.Log(LogEntry{
		Level:    LevelError,
		Category: CategoryDAG,
		Event:    EventDAGNodeError,
		Data: map[string]any{
			"node_id": nodeID,
			"tool":    tool,
			"error":   err.Error(),
		},
		Duration: &dur,
	})
}

func (l *InteractionLogger) LogDAGBlocking(nodeID, question string) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryDAG,
		Event:    EventDAGBlocking,
		Data: map[string]any{
			"node_id":  nodeID,
			"question": question,
		},
	})
}

func (l *InteractionLogger) LogDAGResume(nodeID string) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryDAG,
		Event:    EventDAGResume,
		Data: map[string]any{
			"node_id": nodeID,
		},
	})
}

func (l *InteractionLogger) LogSafetyConfirmation(nodeID, tool string, destructive bool) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategorySafety,
		Event:    EventSafetyConfirmationRequested,
		Data: map[string]any{
			"node_id":     nodeID,
			"tool":        tool,
			"destructive": destructive,
		},
	})
}

func (l *InteractionLogger) LogSafetyConfirmationResult(nodeID string, granted bool) {
	evt := EventSafetyConfirmationDenied
	if granted {
		evt = EventSafetyConfirmationGranted
	}
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategorySafety,
		Event:    evt,
		Data: map[string]any{
			"node_id": nodeID,
		},
	})
}

func (l *InteractionLogger) LogSafetyRollback(nodeID, tool string) {
	l.Log(LogEntry{
		Level:    LevelWarn,
		Category: CategorySafety,
		Event:    EventSafetyRollbackTriggered,
		Data: map[string]any{
			"node_id": nodeID,
			"tool":    tool,
		},
	})
}

func (l *InteractionLogger) LogStateGoalCreated(goalID, goalType, description string, priority string) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryState,
		Event:    EventStateGoalCreated,
		Data: map[string]any{
			"goal_id":     goalID,
			"goal_type":   goalType,
			"description": description,
			"priority":    priority,
		},
	})
}

func (l *InteractionLogger) LogStateGoalComplete(goalID string) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryState,
		Event:    EventStateGoalComplete,
		Data: map[string]any{
			"goal_id": goalID,
		},
	})
}

func (l *InteractionLogger) LogStateGoalFailed(goalID, reason string) {
	l.Log(LogEntry{
		Level:    LevelError,
		Category: CategoryState,
		Event:    EventStateGoalFailed,
		Data: map[string]any{
			"goal_id": goalID,
			"reason":  reason,
		},
	})
}

func (l *InteractionLogger) LogStateBlocked(goalID, reason string) {
	l.Log(LogEntry{
		Level:    LevelInfo,
		Category: CategoryState,
		Event:    EventStateBlocked,
		Data: map[string]any{
			"goal_id": goalID,
			"reason":  reason,
		},
	})
}

func (l *InteractionLogger) LogTUIEventSent(eventType string, data any) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryTUI,
		Event:    EventTUIEventSent,
		Data: map[string]any{
			"event_type": eventType,
			"data":       data,
		},
	})
}

func (l *InteractionLogger) LogTUIUserInput(input string) {
	l.Log(LogEntry{
		Level:    LevelDebug,
		Category: CategoryTUI,
		Event:    EventTUIUserInput,
		Data: map[string]any{
			"input": truncate(input, 200),
		},
	})
}

func (l *InteractionLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *InteractionLogger) Path() string {
	return l.path
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…[truncated]"
}
