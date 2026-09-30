package provider

import (
	"strings"
	"sync"

	"crude/logger"
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
	// Safe default fallback for known families
	{"", 256000},
}

func LookupContextWindow(modelID string) int {
	lower := strings.ToLower(modelID)

	dynamicContextsMu.RLock()
	if c, ok := dynamicContexts[lower]; ok {
		dynamicContextsMu.RUnlock()
		logIf(func(lg *logger.Logger) {
			lg.Debug("PROVIDER", "ctx_window", "model", modelID, "window", c, "source", "dynamic")
		})
		return c
	}
	dynamicContextsMu.RUnlock()
	if catalog := activeModelCatalog.Load(); catalog != nil {
		if meta, ok := catalog.Lookup(modelID); ok && meta.ContextWindow > 0 {
			return meta.ContextWindow
		}
	}

	for _, entry := range contextHints {
		if entry.pattern == "" {
			logIf(func(lg *logger.Logger) {
				lg.Debug("PROVIDER", "ctx_window", "model", modelID, "window", entry.window, "source", "default")
			})
			return entry.window
		}
		if strings.Contains(lower, entry.pattern) {
			logIf(func(lg *logger.Logger) {
				lg.Debug("PROVIDER", "ctx_window", "model", modelID, "window", entry.window, "source", "hint", "pattern", entry.pattern)
			})
			return entry.window
		}
	}
	logIf(func(lg *logger.Logger) {
		lg.Debug("PROVIDER", "ctx_window", "model", modelID, "window", 1048576, "source", "hardcoded_default")
	})
	return 1048576
}
