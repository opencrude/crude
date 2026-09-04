package tools

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"crude/logger"
	"crude/provider"
)

// MediaReadPolicy carries the model capability snapshot read_file needs to
// decide between attaching a binary media file and refusing it explicitly.
// A nil policy keeps the legacy behavior: every binary is refused.
type MediaReadPolicy struct {
	// Model is the active model ID used for capability lookup and limits.
	Model string
	// Standard is the resolved wire protocol ID (openai, openai-responses,
	// anthropic, ollama, codex) — attachment encoding differs per protocol.
	Standard string
	// Modalities is the exact per-turn settings snapshot. Affirmative
	// overrides win even if the asynchronous model catalog reloads later.
	Modalities []string
}

// mediaRefusal renders the explicit, actionable refusal for a binary the
// active model cannot consume.
func (p *MediaReadPolicy) mediaRefusal(path, modality, mime string, size int64) string {
	modalityLabel := modality
	if modalityLabel == "" {
		modalityLabel = "unrecognized binary"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Error: binary file detected: %s (%d bytes).", path, size)
	fmt.Fprintf(&b, " This file is a %s attachment (MIME %s) and the model %q does not advertise %s input.",
		modalityLabel, mime, p.Model, modalityLabel)
	fmt.Fprintf(&b, " Enable the %s capability for this model in settings, or use execute_command to convert or extract text from it.", modalityLabel)
	return b.String()
}

// tryMediaAttachment classifies path and, when the model supports the file's
// modality, reads it (bounded by the per-model size cap) and appends a
// ContentPart to the policy's attachment list. Returns:
//   - handled: true when the file was recognised as media (attached or
//     explicitly refused) — read_file must not fall through to generic
//     binary handling.
//   - result: the user-visible tool result text.
func (p *MediaReadPolicy) tryMediaAttachment(path string, lg *logger.Logger) (handled bool, result string, attachments []provider.ContentPart) {
	modality, mime, ok := provider.MediaModalityFromPath(path)
	if !ok {
		return false, "", nil
	}
	if p == nil {
		return true, fmt.Sprintf("Error: binary file detected: %s — cannot read binary files, use execute_command instead", path), nil
	}
	supported := slices.Contains(p.Modalities, modality)
	if !supported {
		supported, _ = provider.ModalityCapability(p.Model, modality)
	}
	if !supported {
		if lg != nil {
			lg.Warn("TOOL", "media_refused", "path", path, "modality", modality, "model", p.Model)
		}
		size := fileSizeOf(path)
		return true, p.mediaRefusal(path, modality, mime, size), nil
	}
	if !provider.ProtocolSupportsMedia(p.Standard, modality, mime) {
		return true, fmt.Sprintf(
			"Error: %s input is enabled for model %q, but protocol %q cannot carry %s attachments with MIME %q. Convert or extract the file before retrying.",
			modality, p.Model, p.Standard, modality, mime,
		), nil
	}
	data, err := readMediaBounded(path, provider.MaxImageFileSizeFor(p.Model))
	if err != nil {
		return true, fmt.Sprintf("Error: reading %s: %v", path, err), nil
	}
	part := provider.ContentPart{
		Type:     mediaPartType(modality),
		MimeType: mime,
		Filename: baseNameOf(path),
	}
	if modality == provider.ModalityImage {
		part.ImageURL = &provider.ImageURL{
			URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		}
	} else {
		part.Data = data
	}
	if lg != nil {
		lg.Info("TOOL", "media_attached", "path", path, "modality", modality, "bytes", len(data))
	}
	return true, fmt.Sprintf("%s read as %s attachment (%s, %d bytes) — attached to this tool result.",
		path, modality, mime, len(data)), []provider.ContentPart{part}
}

// mediaPartType maps a modality to the neutral ContentPart type used across
// protocols; encoders translate these to wire shapes.
func mediaPartType(modality string) string {
	switch modality {
	case provider.ModalityPDF:
		return "document"
	case provider.ModalityVideo:
		return "video"
	case provider.ModalityAudio:
		return "audio"
	default:
		return "image_url"
	}
}

func baseNameOf(path string) string {
	if index := strings.LastIndexAny(path, "/\\"); index >= 0 && index+1 < len(path) {
		return path[index+1:]
	}
	return path
}

// mediaPolicyFor resolves the effective policy, treating a nil options value
// as the legacy no-media policy.
func mediaPolicyFor(opts *ToolOptions) *MediaReadPolicy {
	if opts == nil || opts.Media == nil {
		return nil
	}
	return opts.Media
}
