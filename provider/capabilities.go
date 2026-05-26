package provider

import (
	"strings"
	"sync"
)

// modelCap represents a set of capabilities for a model.
type modelCap int

const (
	CapVision modelCap = 1 << iota
	CapTools
	CapStreaming
)

// dynamicCaps holds model capabilities populated from models.dev data.
// It is mutex-protected for concurrent access.
var (
	dynamicCaps   = make(map[string]modelCap)
	dynamicCapsMu sync.RWMutex
)

// SetModelCap stores capability flags for a model ID (case-insensitive).
func SetModelCap(modelID string, vision, tools bool) {
	var caps modelCap
	if vision {
		caps |= CapVision
	}
	if tools {
		caps |= CapTools
	}
	dynamicCapsMu.Lock()
	dynamicCaps[strings.ToLower(modelID)] = caps
	dynamicCapsMu.Unlock()
}

// LoadModelCaps bulk-populates dynamicCaps from a map of model metadata
// (typically the result of FetchModelsDevAll). Existing entries are
// overwritten.
func LoadModelCaps(metas map[string]ModelMeta) {
	dynamicCapsMu.Lock()
	for id, meta := range metas {
		var caps modelCap
		if meta.SupportsVision {
			caps |= CapVision
		}
		if meta.SupportsTools {
			caps |= CapTools
		}
		dynamicCaps[strings.ToLower(id)] = caps
	}
	dynamicCapsMu.Unlock()
}

// lookupCaps checks dynamicCaps for a model ID, trying first with the
// stripped provider prefix and then the full (lowercased) ID.
// Returns the caps and whether a match was found.
func lookupCaps(modelID string) (modelCap, bool) {
	lower := strings.ToLower(modelID)
	stripped := stripProviderPrefix(lower)

	dynamicCapsMu.RLock()
	defer dynamicCapsMu.RUnlock()

	if caps, ok := dynamicCaps[stripped]; ok {
		return caps, true
	}
	if stripped != lower {
		if caps, ok := dynamicCaps[lower]; ok {
			return caps, true
		}
	}
	return 0, false
}

// SupportsVision returns true if the given model ID supports image input.
// It checks the dynamicCaps map populated from models.dev data.
// If the model is not found in dynamicCaps, it returns false (safe default).
func SupportsVision(modelID string) bool {
	if caps, ok := lookupCaps(modelID); ok {
		return caps&CapVision != 0
	}
	// Safe fallback: assume no vision for unknown models
	return false
}

// SupportsTools returns true if the model supports tool/function calling.
// It checks the dynamicCaps map populated from models.dev data.
// If the model is not found in dynamicCaps, it returns true (safe default —
// most modern models support tools).
func SupportsTools(modelID string) bool {
	if caps, ok := lookupCaps(modelID); ok {
		return caps&CapTools != 0
	}
	// Most modern models support tools; default to true
	return true
}
