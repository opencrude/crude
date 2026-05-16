package provider

import (
	"strings"

	"crude/logger"
)

// TokenEstimator provides model-family-aware token estimation.
//
// For estimation before the first API call returns real usage data, we use
// character-to-token ratios that vary by content type and model family.
// Once real usage data is available from the API response, that takes
// precedence — this estimator is only a fallback.
//
// Ratios are conservative (slightly overestimate tokens) to avoid
// accidentally exceeding the context window.

// EstimateTokens returns an estimated token count for the given text,
// adjusted by model family. The model parameter is used to select
// appropriate ratio heuristics; if empty or unrecognized, a safe
// default is used.
func EstimateTokens(text string, model string) int {
	ratio := tokenRatioForModel(model)
	estimated := estimateTokensWithRatio(text, ratio)
	if estimated > 0 {
		logIf(func(lg *logger.Logger) { lg.Debug("TOKENS", "estimate", "model", model, "ratio", ratio, "chars", len(text), "estimated", estimated) })
	}
	return estimated
}

// EstimateMessageTokens estimates the total token cost of a single
// ChatMessage, including its content, tool calls, and role overhead.
func EstimateMessageTokens(msg ChatMessage, model string) int {
	tokens := EstimateTokens(msg.Content, model)
	for _, tc := range msg.ToolCalls {
		tokens += EstimateTokens(tc.Function.Arguments, model)
		tokens += EstimateTokens(tc.Function.Name, model)
	}
	// Role overhead: each message has ~4 tokens of framing
	// (role tag, separators, etc.) per the OpenAI tokenizer spec.
	tokens += 4
	return tokens
}

// EstimateMessagesTotal returns the total estimated token count for
// a slice of messages.
func EstimateMessagesTotal(msgs []ChatMessage, model string) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessageTokens(m, model)
	}
	return total
}

// tokenRatioForModel returns characters-per-token for a given model family.
// Lower ratios = more tokens per character = more conservative estimation.
//
// Key insight: code is denser than prose. A line of Go code like
// `if err != nil { return fmt.Errorf("bad: %w", err) }` tokenizes
// into far more tokens than the same character count of English text.
//
// Model families also differ: GPT-4/o-series uses cl100k/BPE variants
// that are slightly more efficient on code than Claude's tokenizer.
func tokenRatioForModel(model string) float64 {
	lower := strings.ToLower(model)

	var ratio float64
	var family string

	if strings.Contains(lower, "claude") {
		ratio = 2.8
		family = "claude"
	} else if strings.Contains(lower, "gemini") {
		ratio = 2.8
		family = "gemini"
	} else if strings.Contains(lower, "deepseek") {
		ratio = 2.9
		family = "deepseek"
	} else if strings.Contains(lower, "llama") || strings.Contains(lower, "qwen") || strings.Contains(lower, "glm") {
		ratio = 2.8
		family = "llama/qwen/glm"
	} else if strings.Contains(lower, "mistral") || strings.Contains(lower, "codestral") {
		ratio = 2.9
		family = "mistral"
	} else {
		ratio = 3.0
		family = "default"
	}

	logIf(func(lg *logger.Logger) { lg.Debug("TOKENS", "token_ratio", "model", model, "family", family, "ratio", ratio) })
	return ratio
}

// estimateTokensWithRatio does the actual estimation using a
// character-to-token ratio. It uses a blended approach:
//   - Code-heavy content (many symbols, brackets, operators) → use the ratio directly
//   - Prose-heavy content (mostly words) → use ratio * 1.2 (prose tokenizes less densely)
//
// This avoids the worst case of the old `*4` constant which severely
// overestimated tokens for code and moderately underestimated for prose.
func estimateTokensWithRatio(text string, ratio float64) int {
	if text == "" {
		return 0
	}

	// Simple heuristic: if the text has a high density of code-like
	// characters, it's probably code and tokenizes more densely.
	codeScore := codeDensity(text)

	// Blend: pure code → ratio, pure prose → ratio * 1.33
	// (prose has roughly 4 chars/token for English, vs 3 for code)
	effectiveRatio := ratio + (1.0-codeScore)*(ratio*0.33)

	charCount := len(text)
	if charCount == 0 {
		return 0
	}

	estimated := float64(charCount) / effectiveRatio
	if estimated < 1 {
		return 1
	}
	return int(estimated + 0.5) // round
}

// codeDensity returns a value from 0.0 to 1.0 indicating how "code-like"
// the text is. 0.0 = pure prose, 1.0 = pure code.
//
// Heuristic: code has a high density of non-alphanumeric, non-whitespace
// characters: { } ( ) [ ] = ; < > | & ! + - * / % #
func codeDensity(text string) float64 {
	if len(text) == 0 {
		return 0
	}

	var codeChars int
	var totalChars int

	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		totalChars++
		if isCodeRune(r) {
			codeChars++
		}
	}

	if totalChars == 0 {
		return 0
	}

	// Code typically has ~30-50% symbol density; prose ~5-15%
	// Normalize: 0% symbols → 0.0, 40%+ symbols → 1.0
	ratio := float64(codeChars) / float64(totalChars)
	if ratio >= 0.35 {
		return 1.0
	}
	return ratio / 0.35
}

func isCodeRune(r rune) bool {
	switch r {
	case '{', '}', '(', ')', '[', ']',
		'=', ';', '<', '>', '|', '&',
		'!', '+', '-', '*', '/', '%',
		'#', '@', '$', '~', '^', ':':
		return true
	}
	return false
}
