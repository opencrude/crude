package memory

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractedMemory is one candidate memory proposed by the extraction model.
type ExtractedMemory struct {
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Importance int      `json:"importance"`
	Paths      []string `json:"paths"`
}

// ExtractedSummary is the rolling session summary proposed by the
// extraction model.
type ExtractedSummary struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Outcome  string   `json:"outcome"`
	KeyFiles []string `json:"key_files"`
}

// ExtractionResult is the parsed output of one extraction call.
type ExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
	Summary  *ExtractedSummary `json:"summary"`
}

// ExtractionSystemPrompt instructs the background model. The what-NOT-to-save
// taxonomy matters as much as the positive list: derivable and transient
// content is what rots memory stores.
const ExtractionSystemPrompt = `You are the memory curator for a coding IDE agent. You read new conversation turns from one work session and maintain the project's long-term memory.

TASK 1 — extract durable memories (0 to 5; zero is common and correct).
Save ONLY information that will still be true and useful in future sessions:
- convention: project-specific commands, workflows, style rules that were actually used or stated ("tests run with X", "commits use format Y")
- decision: architectural or design decisions with their reasoning
- fact: verified project facts that are NOT obvious from reading the code (external service quirks, environment constraints, historical context)
- preference: how the user likes to work or be answered
- lesson: a mistake or failure AND its verified fix ("X fails when Y; do Z instead") — only after the fix was confirmed to work
- reference: important external resources (URLs, docs, tickets) that were actually useful

DO NOT save:
- anything derivable by reading the code, file tree, or git history
- transient state: current task progress, plans, TODO lists, what is being edited right now
- unverified guesses, hypotheses, or claims that were not confirmed
- secrets, API keys, tokens, passwords, or personal data
- generic programming knowledge the agent already has
- one-off command output, build logs, timestamps
- duplicates or trivial rewordings of EXISTING MEMORIES

Each memory: short stable title (dedup key), self-contained content (1-4 sentences), importance 0-100 (most are 30-60; 80+ is rare), paths = repo-relative files/dirs it applies to (empty if project-wide).

TASK 2 — update the rolling session summary: what this session is about, what was accomplished, current outcome (in-progress | completed | blocked), and the key files involved. Merge the PREVIOUS SUMMARY with the NEW TURNS; the result replaces it.

Respond with ONLY this JSON (no markdown fences, no prose):
{"memories":[{"kind":"...","title":"...","content":"...","importance":40,"paths":["..."]}],"summary":{"title":"...","summary":"2-4 sentences","outcome":"...","key_files":["..."]}}`

// BuildExtractionUser assembles the user message for one extraction call.
func BuildExtractionUser(previousSummary, existingTitles, transcript string) string {
	var b strings.Builder
	if previousSummary != "" {
		b.WriteString("PREVIOUS SUMMARY:\n")
		b.WriteString(previousSummary)
		b.WriteString("\n\n")
	}
	if existingTitles != "" {
		b.WriteString("EXISTING MEMORIES (titles — do not duplicate):\n")
		b.WriteString(existingTitles)
		b.WriteString("\n\n")
	}
	b.WriteString("NEW TURNS:\n")
	b.WriteString(transcript)
	return b.String()
}

// ParseExtraction parses a (possibly noisy) model response into an
// ExtractionResult. It tolerates markdown fences and leading/trailing prose
// but rejects structurally invalid output.
func ParseExtraction(raw string) (ExtractionResult, error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return ExtractionResult{}, err
	}
	var result ExtractionResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return ExtractionResult{}, fmt.Errorf("memory: parse extraction: %w", err)
	}
	valid := result.Memories[:0]
	for _, m := range result.Memories {
		m.Title = strings.TrimSpace(m.Title)
		m.Content = strings.TrimSpace(m.Content)
		if m.Title == "" || m.Content == "" {
			continue
		}
		switch m.Kind {
		case KindFact, KindDecision, KindConvention, KindPreference, KindLesson, KindReference:
		default:
			m.Kind = KindFact
		}
		if m.Importance <= 0 || m.Importance > 100 {
			m.Importance = DefaultImportance
		}
		valid = append(valid, m)
	}
	if len(valid) > 5 {
		valid = valid[:5]
	}
	result.Memories = valid
	if result.Summary != nil {
		result.Summary.Summary = strings.TrimSpace(result.Summary.Summary)
		if result.Summary.Summary == "" {
			result.Summary = nil
		}
	}
	return result, nil
}

// extractJSONObject isolates the outermost JSON object in a model response.
func extractJSONObject(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return "", fmt.Errorf("memory: response contains no JSON object")
	}
	return raw[start : end+1], nil
}
