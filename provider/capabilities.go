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
	capVisionKnown
	capToolsKnown
)

// dynamicCaps holds model capabilities populated from models.dev data.
// It is mutex-protected for concurrent access.
var (
	dynamicCaps   = make(map[string]modelCap)
	dynamicCapsMu sync.RWMutex
)

// SetModelCap stores capability flags for a model ID (case-insensitive).
func SetModelCap(modelID string, vision, tools bool) {
	caps := capVisionKnown | capToolsKnown
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

// MergeModelCap adds affirmative capability metadata without erasing a
// catalog value when older configs do not contain the newer capability fields.
func MergeModelCap(modelID string, vision, tools bool) {
	if !vision && !tools {
		return
	}
	lower := strings.ToLower(modelID)
	dynamicCapsMu.Lock()
	caps := dynamicCaps[lower]
	if vision {
		caps |= CapVision | capVisionKnown
	}
	if tools {
		caps |= CapTools | capToolsKnown
	}
	dynamicCaps[lower] = caps
	dynamicCapsMu.Unlock()
}

// LoadModelCaps bulk-populates dynamicCaps from a map of model metadata
// (typically the result of FetchModelsDevAll). Existing entries are
// overwritten.
func LoadModelCaps(metas map[string]ModelMeta) {
	dynamicCapsMu.Lock()
	for id, meta := range metas {
		caps := capVisionKnown | capToolsKnown
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

// VisionCapability reports both the capability value and whether it came from
// authoritative model metadata. Config merges only add affirmative flags, so a
// missing legacy model_supports_vision field must remain unknown rather than
// becoming an explicit rejection of image input.
func VisionCapability(modelID string) (supported, known bool) {
	if caps, ok := lookupCaps(modelID); ok {
		return caps&CapVision != 0, caps&capVisionKnown != 0
	}
	return false, false
}

// SupportsVision returns true only for models affirmatively known to support
// image input. Callers that can safely attempt unknown models should use
// VisionCapability and handle the known flag explicitly.
func SupportsVision(modelID string) bool {
	supported, _ := VisionCapability(modelID)
	return supported
}

// SupportsTools returns true if the model supports tool/function calling.
// It checks the dynamicCaps map populated from models.dev data.
// If the model is not found in dynamicCaps, it returns true (safe default —
// most modern models support tools).
func SupportsTools(modelID string) bool {
	if caps, ok := lookupCaps(modelID); ok {
		if caps&capToolsKnown != 0 {
			return caps&CapTools != 0
		}
	}
	// Most modern models support tools; default to true
	return true
}
