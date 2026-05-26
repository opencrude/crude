package provider

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// modelLimits holds per-model file size limits, populated dynamically.
type modelLimits struct {
	MaxImageFileSize     int64
	MaxTextFileSize      int64
	MaxInlineContentSize int64
}

var (
	dynamicLimits   = make(map[string]modelLimits)
	dynamicLimitsMu sync.RWMutex
)

// providerFamilyLimits returns default file size limits based on the provider
// family extracted from the model ID prefix (e.g., "openai/gpt-4o" → openai).
// These represent known API provider constraints, not model-specific quirks.
func providerFamilyLimits(modelID string) modelLimits {
	lower := strings.ToLower(modelID)
	provider := lower
	if idx := strings.Index(lower, "/"); idx > 0 {
		provider = lower[:idx]
	}

	switch provider {
	case "anthropic":
		return modelLimits{
			MaxImageFileSize:     32 * 1024 * 1024, // 32 MB — Claude supports larger images
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 32 * 1024 * 1024, // 32 MB
		}
	case "openai":
		return modelLimits{
			MaxImageFileSize:     20 * 1024 * 1024, // 20 MB
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 20 * 1024 * 1024, // 20 MB
		}
	case "google":
		return modelLimits{
			MaxImageFileSize:     20 * 1024 * 1024, // 20 MB
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 20 * 1024 * 1024, // 20 MB
		}
	case "groq":
		return modelLimits{
			MaxImageFileSize:     20 * 1024 * 1024, // 20 MB
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 20 * 1024 * 1024, // 20 MB
		}
	case "deepseek":
		return modelLimits{
			MaxImageFileSize:     20 * 1024 * 1024, // 20 MB
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 20 * 1024 * 1024, // 20 MB
		}
	default:
		return modelLimits{
			MaxImageFileSize:     20 * 1024 * 1024, // 20 MB (conservative default)
			MaxTextFileSize:      2 * 1024 * 1024,  // 2 MB
			MaxInlineContentSize: 20 * 1024 * 1024, // 20 MB
		}
	}
}

// lookupLimits checks dynamicLimits for a model ID, trying first with the
// stripped provider prefix and then the full (lowercased) ID.
// Returns the limits and whether a match was found.
func lookupLimits(modelID string) (modelLimits, bool) {
	lower := strings.ToLower(modelID)
	stripped := stripProviderPrefix(lower)

	dynamicLimitsMu.RLock()
	defer dynamicLimitsMu.RUnlock()

	if lim, ok := dynamicLimits[stripped]; ok {
		return lim, true
	}
	if stripped != lower {
		if lim, ok := dynamicLimits[lower]; ok {
			return lim, true
		}
	}
	return modelLimits{}, false
}

// MaxImageFileSizeFor returns the maximum allowed image file size for the
// given model ID. It first checks dynamicLimits, then falls back to
// provider-family defaults derived from the model ID prefix.
func MaxImageFileSizeFor(modelID string) int64 {
	if lim, ok := lookupLimits(modelID); ok {
		return lim.MaxImageFileSize
	}
	return providerFamilyLimits(modelID).MaxImageFileSize
}

// MaxTextFileSizeFor returns the maximum allowed text file size for the
// given model ID. It first checks dynamicLimits, then falls back to
// provider-family defaults derived from the model ID prefix.
func MaxTextFileSizeFor(modelID string) int64 {
	if lim, ok := lookupLimits(modelID); ok {
		return lim.MaxTextFileSize
	}
	return providerFamilyLimits(modelID).MaxTextFileSize
}

// MaxInlineContentSizeFor returns the maximum allowed size for inline/base64
// content when FilePath is absent, for the given model ID. It first checks
// dynamicLimits, then falls back to provider-family defaults.
func MaxInlineContentSizeFor(modelID string) int64 {
	if lim, ok := lookupLimits(modelID); ok {
		return lim.MaxInlineContentSize
	}
	return providerFamilyLimits(modelID).MaxInlineContentSize
}

// SetModelLimits stores per-model file size limits (case-insensitive).
func SetModelLimits(modelID string, limits modelLimits) {
	dynamicLimitsMu.Lock()
	dynamicLimits[strings.ToLower(modelID)] = limits
	dynamicLimitsMu.Unlock()
}

// FileSizeExceeded returns a non-empty warning string if the file at path
// exceeds maxSize. Returns empty string if the file is within limits or
// cannot be stat'd (the caller's own read-error handling covers that case).
func FileSizeExceeded(path string, maxSize int64) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "" // can't stat — let caller handle read error
	}
	if fi.Size() > maxSize {
		return fmt.Sprintf("file too large: %s (%d bytes, max %d)", path, fi.Size(), maxSize)
	}
	return ""
}
