package provider

import (
	"fmt"
	"strings"
)

type replayTool struct {
	ID, Type, Name, Arguments string
}

type streamTranscript struct {
	Content       string
	Reasoning     string
	Tools         map[int]replayTool
	ProviderItems bool
}

func (t *streamTranscript) apply(update StreamUpdate) {
	t.Content += update.ContentDelta
	t.Reasoning += update.ReasoningDelta
	if len(update.ProviderItems) > 0 {
		t.ProviderItems = true
	}
	if len(update.ToolCalls) > 0 && t.Tools == nil {
		t.Tools = make(map[int]replayTool)
	}
	for _, delta := range update.ToolCalls {
		tool := t.Tools[delta.Index]
		if delta.ID != "" {
			tool.ID = delta.ID
		}
		if delta.Type != "" {
			tool.Type = delta.Type
		}
		if delta.Name != "" {
			tool.Name = delta.Name
		}
		if delta.ReplaceArguments {
			tool.Arguments = delta.Arguments
		} else {
			tool.Arguments += delta.Arguments
		}
		t.Tools[delta.Index] = tool
	}
}

func reconcileReplay(emitted streamTranscript, replay *streamTranscript, update StreamUpdate) (StreamUpdate, bool, error) {
	if len(update.ProviderItems) > 0 {
		return StreamUpdate{}, false, fmt.Errorf("stream replay contains opaque provider items")
	}
	replay.apply(update)
	adjusted := update
	adjusted.ContentDelta = ""
	adjusted.ReasoningDelta = ""
	adjusted.ToolCalls = nil
	adjusted.ProviderItems = nil

	content, ahead, err := replaySuffix(emitted.Content, replay.Content, "content")
	if err != nil {
		return StreamUpdate{}, false, err
	}
	adjusted.ContentDelta = content
	reasoning, reasoningAhead, err := replaySuffix(emitted.Reasoning, replay.Reasoning, "reasoning")
	if err != nil {
		return StreamUpdate{}, false, err
	}
	adjusted.ReasoningDelta = reasoning
	ahead = ahead || reasoningAhead

	for index, candidate := range replay.Tools {
		prior := emitted.Tools[index]
		if prior.ID != "" && candidate.ID != "" && prior.ID != candidate.ID {
			return StreamUpdate{}, false, fmt.Errorf("stream replay diverged at tool %d id", index)
		}
		if prior.Name != "" && candidate.Name != "" && prior.Name != candidate.Name {
			return StreamUpdate{}, false, fmt.Errorf("stream replay diverged at tool %d name", index)
		}
		suffix, toolAhead, suffixErr := replaySuffix(prior.Arguments, candidate.Arguments, fmt.Sprintf("tool %d arguments", index))
		if suffixErr != nil {
			return StreamUpdate{}, false, suffixErr
		}
		ahead = ahead || toolAhead || (prior.ID == "" && candidate.ID != "") || (prior.Name == "" && candidate.Name != "")
		if suffix != "" || toolAhead || (prior.ID == "" && candidate.ID != "") || (prior.Name == "" && candidate.Name != "") {
			adjusted.ToolCalls = append(adjusted.ToolCalls, ToolCallDelta{
				Index: index, ID: candidate.ID, Type: candidate.Type, Name: candidate.Name,
				Arguments: suffix,
			})
		}
	}
	for index := range emitted.Tools {
		if _, exists := replay.Tools[index]; !exists {
			ahead = false
		}
	}

	caughtUp := replayMatches(emitted, *replay)
	if !caughtUp && !ahead {
		adjusted.Done = false
		adjusted.FinishReason = ""
		adjusted.Usage = nil
		return adjusted, false, nil
	}
	emit := adjusted.ContentDelta != "" || adjusted.ReasoningDelta != "" || len(adjusted.ToolCalls) > 0 || adjusted.Done
	return adjusted, emit, nil
}

func replaySuffix(emitted, candidate, label string) (suffix string, ahead bool, err error) {
	switch {
	case strings.HasPrefix(emitted, candidate):
		return "", false, nil
	case strings.HasPrefix(candidate, emitted):
		return candidate[len(emitted):], len(candidate) > len(emitted), nil
	default:
		return "", false, fmt.Errorf("stream replay diverged in %s", label)
	}
}

func replayMatches(left, right streamTranscript) bool {
	if left.Content != right.Content || left.Reasoning != right.Reasoning || len(left.Tools) != len(right.Tools) {
		return false
	}
	for index, a := range left.Tools {
		b, ok := right.Tools[index]
		if !ok || a != b {
			return false
		}
	}
	return true
}
