package tools

import (
	ccrude "crude/tools"
)

// SummarizeToolArgs produces the compact target label a tool row shows
// (file, command, pattern, query, handle). It delegates to the tool layer, the
// one implementation the live transcript and a resumed session both call, so the
// two producers cannot drift apart.
func SummarizeToolArgs(tool, argsStr string) string {
	return ccrude.SummarizeArgs(tool, argsStr)
}

// ExtractArg reads one argument out of a tool-call payload: the JSON object the
// provider sent, or the presentation-neutral "key: value" summary the agent
// emits for events and persists in snapshots.
func ExtractArg(str, key string) string {
	return ccrude.ExtractArg(str, key)
}
