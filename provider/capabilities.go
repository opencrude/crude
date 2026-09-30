package provider

import (
	"slices"
	"strings"
	"sync"
)

// modelCap represents a set of capabilities for a model.
type modelCap int

const (
	CapVision modelCap = 1 << iota
	CapTools
	CapStreaming
	CapPDF
	CapVideo
	CapAudio
	capVisionKnown
	capToolsKnown
	capPDFKnown
	capVideoKnown
	capAudioKnown
)

// dynamicCaps holds explicit registrations for standalone callers. An installed
// application catalog is authoritative for discovered, not user, capabilities.
var (
	dynamicCaps   = make(map[string]modelCap)
	dynamicCapsMu sync.RWMutex
)

// modalityCapBits maps a canonical modality to its capability + known bits.
var modalityCapBits = map[string]struct {
	cap   modelCap
	known modelCap
}{
	ModalityImage: {CapVision, capVisionKnown},
	ModalityPDF:   {CapPDF, capPDFKnown},
	ModalityVideo: {CapVideo, capVideoKnown},
	ModalityAudio: {CapAudio, capAudioKnown},
}

// SetModelModalities stores an authoritative per-modality capability snapshot
// for a model ID (case-insensitive). Listed modalities are supported; unlisted
// modalities are explicitly unsupported. Settings forms publish all four
// toggles together, so disabling a capability must remove an earlier value.
func SetModelModalities(modelID string, modalities []string) {
	lower := strings.ToLower(modelID)
	dynamicCapsMu.Lock()
	caps := dynamicCaps[lower]
	for modality, bits := range modalityCapBits {
		caps &^= bits.cap | bits.known
		caps |= bits.known
		if slices.Contains(modalities, modality) {
			caps |= bits.cap
		}
	}
	dynamicCaps[lower] = caps
	dynamicCapsMu.Unlock()
}

// MergeModelModalities records affirmative settings overrides without
// turning omitted fields into known-false values. Category inheritance uses
// omission for "no override", so catalog capabilities must survive.
func MergeModelModalities(modelID string, modalities []string) {
	lower := strings.ToLower(modelID)
	dynamicCapsMu.Lock()
	caps := dynamicCaps[lower]
	for modality, bits := range modalityCapBits {
		if slices.Contains(modalities, modality) {
			caps |= bits.cap | bits.known
		}
	}
	dynamicCaps[lower] = caps
	dynamicCapsMu.Unlock()
}

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
// (for standalone callers and fixtures). Existing entries are
// overwritten.
func LoadModelCaps(metas map[string]ModelMeta) {
	dynamicCapsMu.Lock()
	for id, meta := range metas {
		caps := capVisionKnown | capToolsKnown | capPDFKnown | capVideoKnown | capAudioKnown
		for _, modality := range meta.Modalities {
			if bits, ok := modalityCapBits[modality]; ok {
				caps |= bits.cap
			}
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
	if catalog := activeModelCatalog.Load(); catalog != nil {
		if meta, ok := catalog.Lookup(modelID); ok {
			var caps modelCap
			for modality, supported := range meta.ModalitySupport {
				bits := modalityCapBits[modality]
				caps |= bits.known
				if supported {
					caps |= bits.cap
				}
			}
			if meta.ToolsKnown {
				caps |= capToolsKnown
			}
			if meta.SupportsTools {
				caps |= CapTools
			}
			return caps, true
		}
	}
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

// ModalityCapability reports whether a model affirmatively supports one
// input modality (provider.ModalityImage/PDF/Video/Audio) and whether that
// answer came from authoritative metadata. Unknown models return
// (false, false) so callers can decide whether to attempt anyway.
func ModalityCapability(modelID, modality string) (supported, known bool) {
	bits, ok := modalityCapBits[modality]
	if !ok {
		return false, true
	}
	caps, found := lookupCaps(modelID)
	if !found {
		return false, false
	}
	return caps&bits.cap != 0, caps&bits.known != 0
}

// SupportsModality returns true only for models affirmatively known to
// accept the given modality.
func SupportsModality(modelID, modality string) bool {
	supported, _ := ModalityCapability(modelID, modality)
	return supported
}
