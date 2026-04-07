package agent

import (
	"crude/dag"
)

// summarize asks the LLM to produce a final answer based on execution results.
func (a *Agent) summarize(sessionMessages []APIMessage, execDAG *dag.DAG, systemPrompt string, emit func(AgentEvent)) string {
	summaryText := execDAG.Summary()
	if summaryText == "" {
		summaryText = "Execution complete. No operations were performed."
	}

	summaryMsgs := make([]APIMessage, 0, len(sessionMessages)+3)
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "system", Content: systemPrompt})
	summaryMsgs = append(summaryMsgs, sessionMessages...)
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "assistant", Content: "Execution results:\n" + summaryText})
	summaryMsgs = append(summaryMsgs, APIMessage{Role: "user", Content: `Present the results of your work. Show any data you found (file listings, contents, search results) directly. Do not just say what you did — show the actual results.

CRITICAL — format your response in proper Markdown:
- Use code blocks with language tags for any code or file contents: ` + "```go\n...\n```" + `
- Use bullet lists for file listings or multiple items
- Use inline code for file paths, commands, and short references
- Use headings (##, ###) to organize sections
- Use bold for emphasis on key findings
- NEVER dump raw text without formatting — always use appropriate markdown decorations`})

	content, _, err := a.completeStream(summaryMsgs, nil, func(token string) {
		emit(AgentEvent{Type: "stream_token", Data: token})
	})
	if err != nil {
		return summaryText
	}
	if content != "" {
		return content
	}
	return summaryText
}

// directReply handles chitchat/greetings without grounding or planning.
// It asks the LLM for a brief conversational response.
func (a *Agent) directReply(sessionMessages []APIMessage, systemPrompt string, emit func(AgentEvent)) string {
	replyMsgs := make([]APIMessage, 0, len(sessionMessages)+2)
	replyMsgs = append(replyMsgs, APIMessage{
		Role:    "system",
		Content: systemPrompt + "\n\nRespond to the user's message conversationally. Be brief and friendly. Do NOT use tools or perform system operations.\n\nFormat your response in Markdown: use code blocks with language tags for any code, inline code for paths/commands, and proper formatting.",
	})
	replyMsgs = append(replyMsgs, sessionMessages...)

	content, _, err := a.completeStream(replyMsgs, nil, func(token string) {
		emit(AgentEvent{Type: "stream_token", Data: token})
	})
	if err != nil {
		logAgent("direct reply failed: %v", err)
		return ""
	}
	return content
}
