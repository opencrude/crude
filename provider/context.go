package provider

import (
	"strings"
	"sync"
)

var (
	dynamicContexts   = make(map[string]int)
	dynamicContextsMu sync.RWMutex
)

func SetModelContext(modelID string, ctx int) {
	dynamicContextsMu.Lock()
	dynamicContexts[strings.ToLower(modelID)] = ctx
	dynamicContextsMu.Unlock()
}

var contextHints = []struct {
	pattern string
	window  int
}{
	// OpenAI
	{"gpt-4o", 128000},
	{"gpt-4-turbo", 128000},
	{"gpt-4-32k", 32768},
	{"gpt-4", 8192},
	{"gpt-3.5-turbo", 16384},
	{"o1", 200000},
	{"o3-mini", 200000},

	// Anthropic
	{"claude-3.5-sonnet", 200000},
	{"claude-3-opus", 200000},
	{"claude-3-haiku", 200000},
	{"claude-3-sonnet", 200000},
	{"claude-sonnet-4", 200000},

	// Google
	{"gemini-2.0-flash", 1048576},
	{"gemini-2.0-pro", 1048576},
	{"gemini-1.5-flash", 1048576},
	{"gemini-1.5-pro", 1048576},
	{"gemini-2.5-pro", 1048576},

	// DeepSeek
	{"deepseek-chat", 65536},
	{"deepseek-reasoner", 65536},
	{"deepseek-v3", 65536},

	// Mistral
	{"mistral-large", 131072},
	{"mistral-small", 32768},
	{"codestral", 256000},

	// Meta / Llama
	{"llama-3.3-70b", 131072},
	{"llama-3.1-8b", 131072},
	{"llama-3.2-3b", 131072},
	{"llama-3.2-1b", 131072},

	// Qwen
	{"qwen-2.5-72b", 131072},
	{"qwen-2.5-coder-32b", 32768},
	{"qwen-max", 32768},

	// Tencent
	{"tencent/hy3", 256000},
	{"tencent/hunyuan", 131072},

	// Wafer AI — fast open-source models
	{"qwen3.5-397b-a17b", 262144},
	{"glm-5.1", 202752},

	// Zhipu / GLM
	{"glm-5", 131072},
	{"glm-4", 131072},
	{"z-ai/glm", 131072},

	// Default fallback for known families
	{"", 1048576},
}

func LookupContextWindow(modelID string) int {
	lower := strings.ToLower(modelID)

	dynamicContextsMu.RLock()
	if c, ok := dynamicContexts[lower]; ok {
		dynamicContextsMu.RUnlock()
		return c
	}
	dynamicContextsMu.RUnlock()

	for _, entry := range contextHints {
		if entry.pattern == "" {
			return entry.window
		}
		if strings.Contains(lower, entry.pattern) {
			return entry.window
		}
	}
	return 1048576
}
