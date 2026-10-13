package provider

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// ValidateImageBytes checks the media signature against its declared MIME.
// In particular, encoded text is never accepted as an image file. This does not
// promise that a remote provider supports every feature of a matching codec.
func ValidateImageBytes(mime string, data []byte) error {
	if len(data) == 0 || !strings.HasPrefix(mime, "image/") || http.DetectContentType(data) != mime {
		return fmt.Errorf("invalid image bytes for %q", mime)
	}
	return nil
}

// MediaModalityFromPath maps a file path to the canonical input modality it
// represents (provider.ModalityImage/PDF/Video/Audio) and its MIME type.
// The second return value reports whether the path is a recognised media
// attachment candidate at all — plain documents and unknown binaries are not.
func MediaModalityFromPath(path string) (modality, mime string, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if m, known := ImageMimeFromExt(ext); known {
		return ModalityImage, m, true
	}
	switch ext {
	case ".pdf":
		return ModalityPDF, "application/pdf", true
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v":
		return ModalityVideo, videoMimeFromExt(ext), true
	case ".wav", ".mp3", ".m4a", ".ogg", ".flac", ".aac":
		return ModalityAudio, audioMimeFromExt(ext), true
	}
	// Extension unknown: sniff the content before deciding.
	return sniffedMediaModality(path)
}

// videoMimeFromExt maps a video extension to its MIME type.
func videoMimeFromExt(ext string) string {
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	default:
		return "application/octet-stream"
	}
}

// audioMimeFromExt maps an audio extension to its MIME type.
func audioMimeFromExt(ext string) string {
	switch ext {
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	default:
		return "application/octet-stream"
	}
}

// sniffedMediaModality classifies a file whose extension did not identify a
// media type. It reads a bounded head and uses HTTP content sniffing, so a
// renamed screenshot still attaches and a random binary does not.
func sniffedMediaModality(path string) (modality, mime string, ok bool) {
	head := make([]byte, 512)
	n, readErr := readFileHead(path, head)
	if readErr != nil || n == 0 {
		return "", "", false
	}
	data := head[:n]
	sniffed := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(sniffed, "image/"):
		return ModalityImage, sniffed, true
	case strings.HasPrefix(sniffed, "video/"):
		return ModalityVideo, sniffed, true
	case strings.HasPrefix(sniffed, "audio/"):
		return ModalityAudio, sniffed, true
	case sniffed == "application/pdf":
		return ModalityPDF, sniffed, true
	}
	return "", "", false
}

// IsMediaPath reports whether a path resolves to a recognised media
// attachment without reading the file.
func IsMediaPath(path string) bool {
	_, _, ok := MediaModalityFromPath(path)
	return ok
}

// ProtocolSupportsModality is the transport contract independent of model
// capability. OpenAI-compatible Chat Completions supports OpenRouter's four
// documented multipart shapes; Responses/Codex support image and input_file;
// Anthropic supports image and PDF document blocks; Ollama supports images.
func ProtocolSupportsModality(protocolID, modality string) bool {
	switch protocolID {
	case "openai":
		return modality == ModalityImage || modality == ModalityPDF ||
			modality == ModalityAudio || modality == ModalityVideo
	case "openai-responses", "codex", "anthropic":
		return modality == ModalityImage || modality == ModalityPDF
	case "ollama":
		return modality == ModalityImage
	default:
		return false
	}
}

// ProtocolSupportsMedia refines modality support by MIME type so read_file
// fails before a provider request instead of sending a shape the protocol is
// documented to reject.
func ProtocolSupportsMedia(protocolID, modality, mime string) bool {
	if !ProtocolSupportsModality(protocolID, modality) {
		return false
	}
	switch modality {
	case ModalityImage:
		switch mime {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
			return true
		}
		return protocolID == "ollama"
	case ModalityPDF:
		return mime == "application/pdf"
	case ModalityAudio:
		return mime == "audio/wav" || mime == "audio/mpeg"
	case ModalityVideo:
		switch mime {
		case "video/mp4", "video/quicktime", "video/webm":
			return true
		}
	}
	return false
}
