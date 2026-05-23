package virtlist

import (
	"strings"

	"crude/tui/rendering"
)

func EstimatePlainTextHeight(text string, width int) int {
	if width <= 0 {
		lines := strings.Split(text, "\n")
		if len(lines) < 1 {
			return 1
		}
		return len(lines)
	}
	lines := strings.Split(text, "\n")
	total := 0
	for _, line := range lines {
		wrapped := WrapText(line, width)
		total += len(wrapped)
	}
	if total < 1 {
		return 1
	}
	return total
}

func EstimateMarkdownHeight(renderedContent string) int {
	if renderedContent == "" {
		return 1
	}
	return strings.Count(renderedContent, "\n") + 1
}

func StreamingHeightEstimate(completedLines []string, lastLineText string, contentWidth int) int {
	h := len(completedLines) + len(WrapText(lastLineText, contentWidth))
	if h < 1 {
		return 1
	}
	return h
}

func WrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	paragraphs := strings.Split(text, "\n")
	var result []string
	for _, para := range paragraphs {
		if para == "" {
			result = append(result, "")
			continue
		}
		if rendering.StringWidth(para) <= width {
			result = append(result, para)
			continue
		}
		words := strings.Fields(para)
		var cur string
		for _, word := range words {
			wordWidth := rendering.StringWidth(word)
			if cur == "" {
				cur = word
				continue
			}
			if rendering.StringWidth(cur)+1+wordWidth <= width {
				cur += " " + word
			} else {
				result = append(result, cur)
				cur = word
			}
		}
		if cur != "" {
			result = append(result, cur)
		}
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}
