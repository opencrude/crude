package agent

import (
	"fmt"
	"strings"

	"crude/provider"
)

const ollamaUncachedActiveWindow = 32768
const activeWireSafetyPct = 0.70

func activeWireContextWindow(protocol string, providerWindow int) int {
	if protocol == "ollama" && (providerWindow == 0 || providerWindow > ollamaUncachedActiveWindow) {
		return ollamaUncachedActiveWindow
	}
	return providerWindow
}

// compactWireEpoch creates a deterministic checkpoint plus the newest complete
// assistant/tool units. The immutable archive ledger remains untouched and is
// returned to session persistence; only the next provider epoch uses this
// bounded projection.
func compactWireEpoch(messages []provider.ChatMessage, contextWindow int, model string) []provider.ChatMessage {
	if contextWindow <= 0 || len(messages) <= 2 {
		return messages
	}
	tokenBudget := int(float64(contextWindow) * 0.75)
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
	reserve := min(4096, max(128, tokenBudget/4))
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
		total := 0
		for _, value := range units {
			total += value.tokens
		}
		if total <= tokenBudget {
			return messages
		}
		if len(units) == 1 {
			first = len(units)
		} else {
			first = len(units) - 1
		}
	}
	cutoff := len(messages)
	if first < len(units) {
		cutoff = units[first].start
	}
	omittedTokens := 0
	for _, value := range units[:first] {
		omittedTokens += value.tokens
	}
	checkpoint := buildWireCheckpoint(messages[:cutoff], cutoff, omittedTokens)
	for provider.EstimateTokens(checkpoint, model) > reserve && len([]rune(checkpoint)) > 512 {
		runes := []rune(checkpoint)
		checkpoint = string(runes[:len(runes)*3/4]) + "\n…\n</wire_checkpoint>"
	}
	result := []provider.ChatMessage{provider.NewWireHiddenMessage(checkpoint)}
	result = append(result, messages[cutoff:]...)
	return result
}

func buildWireCheckpoint(omitted []provider.ChatMessage, count, tokens int) string {
	const maxObservationRunes = 480
	observations := make([]string, 0, len(omitted))
	for _, message := range omitted {
		switch message.Role {
		case "user":
			if text := compactWireObservation(message.Content.String(), maxObservationRunes); text != "" {
				observations = append(observations, "User/context: "+text)
			}
		case "assistant":
			if text := compactWireObservation(message.Content.String(), maxObservationRunes); text != "" {
				observations = append(observations, "Assistant outcome: "+text)
			}
			for _, call := range message.ToolCalls {
				observations = append(observations, "Tool requested: "+call.Function.Name+" "+compactWireObservation(call.Function.Arguments, 240))
			}
		case "tool":
			status := "completed"
			if message.ToolError {
				status = "failed"
			}
			observations = append(observations, fmt.Sprintf("Tool %s (%s): %s",
				message.ToolCallID, status, compactWireObservation(message.Content.String(), 320)))
		}
	}
	if len(observations) > 24 {
		observations = observations[len(observations)-24:]
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "<wire_checkpoint epoch_archive_messages=%d approximate_tokens=%d>\n", count, tokens)
	summary.WriteString("Older provider-visible messages remain immutable in the session archive. This deterministic checkpoint preserves recent outcomes; use repository/session tools to re-read details when needed.\n")
	for _, observation := range observations {
		summary.WriteString("- ")
		summary.WriteString(observation)
		summary.WriteByte('\n')
	}
	summary.WriteString("</wire_checkpoint>")
	return summary.String()
}

func compactWireObservation(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
