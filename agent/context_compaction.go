package agent

import (
	"fmt"
	"strings"

	"crude/provider"
)

// CompactContext creates one deterministic checkpoint from the immutable full
// archive plus the newest complete assistant/tool units. Callers must always
// pass the archive, never an earlier compacted projection.
func CompactContext(messages []provider.ChatMessage, contextWindow, targetPercent int, model string) []provider.ChatMessage {
	if contextWindow <= 0 || targetPercent <= 0 || len(messages) == 0 {
		return messages
	}
	targetPercent = min(100, max(1, targetPercent))
	tokenBudget := contextWindow * targetPercent / 100
	type unit struct {
		start  int
		tokens int
	}
	var units []unit
	for start := 0; start < len(messages); {
		end := start + 1
		if messages[start].Role == "assistant" && len(messages[start].ToolCalls) > 0 {
			for end < len(messages) && messages[end].Role == "tool" {
				end++
			}
		}
		tokens := 0
		for i := start; i < end; i++ {
			tokens += provider.EstimateMessageTokens(messages[i], model)
		}
		units = append(units, unit{start: start, tokens: tokens})
		start = end
	}
	reserve := min(4096, max(256, tokenBudget/5))
	tailBudget := max(1, tokenBudget-reserve)
	used := 0
	first := len(units)
	for i := len(units) - 1; i >= 0; i-- {
		if first < len(units) && used+units[i].tokens > tailBudget {
			break
		}
		used += units[i].tokens
		first = i
	}
	if first == 0 {
		if provider.EstimateMessagesTotal(messages, model) <= tokenBudget {
			return messages
		}
		if len(units) == 1 {
			return messages
		}
		first = len(units) - 1
	}
	cutoff := len(messages)
	if first < len(units) {
		cutoff = units[first].start
	}
	omittedTokens := 0
	for _, value := range units[:first] {
		omittedTokens += value.tokens
	}
	checkpoint := buildContextCheckpoint(messages[:cutoff], cutoff, omittedTokens)
	for provider.EstimateTokens(checkpoint, model) > reserve && len([]rune(checkpoint)) > 512 {
		runes := []rune(checkpoint)
		checkpoint = string(runes[:len(runes)*3/4]) + "\n…\n</context_checkpoint>"
	}
	result := []provider.ChatMessage{{Role: "system", Content: provider.TextContent(checkpoint)}}
	result = append(result, messages[cutoff:]...)
	return result
}

func buildContextCheckpoint(omitted []provider.ChatMessage, count, tokens int) string {
	var observations []string
	for _, message := range omitted {
		switch message.Role {
		case "user":
			if text := compactContextObservation(message.Content.String(), 480); text != "" {
				observations = append(observations, "User/context: "+text)
			}
		case "assistant":
			if text := compactContextObservation(message.Content.String(), 480); text != "" {
				observations = append(observations, "Assistant outcome: "+text)
			}
			for _, call := range message.ToolCalls {
				observations = append(observations, "Tool requested: "+call.Function.Name+" "+compactContextObservation(call.Function.Arguments, 240))
			}
		case "tool":
			status := "completed"
			if message.ToolError {
				status = "failed"
			}
			observations = append(observations, fmt.Sprintf("Tool %s (%s): %s",
				message.ToolCallID, status, compactContextObservation(message.Content.String(), 320)))
		}
	}
	if len(observations) > 24 {
		observations = observations[len(observations)-24:]
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "<context_checkpoint archive_messages=%d approximate_tokens=%d>\n", count, tokens)
	summary.WriteString("Deterministic checkpoint of the immutable earlier conversation. This is context, not a new user request.\n")
	for _, observation := range observations {
		summary.WriteString("- ")
		summary.WriteString(observation)
		summary.WriteByte('\n')
	}
	summary.WriteString("</context_checkpoint>")
	return summary.String()
}

func compactContextObservation(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
